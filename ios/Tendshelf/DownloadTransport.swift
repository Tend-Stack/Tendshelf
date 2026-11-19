import Foundation
import OSLog

/// What the engine hears from the transport. Everything arrives on the main actor.
@MainActor
protocol DownloadEvents: AnyObject {
    /// Bytes written so far of one file; [expected] is the length the server announced, 0 when it did not.
    func progressed(key: String, written: Int64, expected: Int64)
    /// A 2xx answer was received whole and waits at [staged] (a file of the engine's own `incoming` folder).
    func arrived(key: String, status: Int, length: Int64, mime: String?, staged: URL, bytes: Int64)
    /// The file did not arrive: [status] is the HTTP status, or 0 for no answer; [resumeData] lets the same download go on.
    func failed(key: String, status: Int, retryAfterS: Int, resumeData: Data?)
}

/// What moves the bytes: a background `URLSession` in the app, a fake in the tests. A key is `<bookId>|<cacheKey>`.
@MainActor
protocol DownloadTransport: AnyObject {
    var events: DownloadEvents? { get set }
    /// Starts one file. [resumeData] (memory only: it embeds the request, and with it the bearer) continues an earlier try.
    func start(key: String, request: URLRequest, resumeData: Data?)
    func cancel(key: String) async
    func cancelAll() async
    /// The keys of the tasks the system still holds.
    func runningKeys() async -> Set<String>
}

/// The real transport (decision 0048, I3b): one background `URLSession`, so downloads go on with the app suspended or ended by
/// the system. The device token travels only as the `Authorization` header of each request, never in an address or in
/// `taskDescription` (that is the key). No cookies, no cache; a redirect is refused, so a 3xx fails the file.
@MainActor
final class BackgroundTransport: NSObject, DownloadTransport, URLSessionDownloadDelegate {
    nonisolated static let identifier = "app.tendshelf.ios.downloads"
    /// The system's "all events of the background session were delivered" handler, kept by the app delegate until then.
    nonisolated(unsafe) static var pendingCompletion: (() -> Void)?
    private nonisolated static let log = Logger(subsystem: "app.tendshelf.ios", category: "downloads")

    weak var events: DownloadEvents?
    private let incoming: URL
    private var session: URLSession!
    private let queue = OperationQueue()
    private nonisolated let throttle = Throttle()

    init(incoming: URL, identifier: String = BackgroundTransport.identifier) {
        self.incoming = incoming
        super.init()
        queue.maxConcurrentOperationCount = 1
        queue.name = "app.tendshelf.ios.downloads.delegate"
        let configuration = URLSessionConfiguration.background(withIdentifier: identifier)
        configuration.sessionSendsLaunchEvents = true
        configuration.isDiscretionary = false
        configuration.httpShouldSetCookies = false
        configuration.httpCookieStorage = nil
        configuration.urlCache = nil
        session = URLSession(configuration: configuration, delegate: self, delegateQueue: queue)
    }

    // MARK: DownloadTransport

    func start(key: String, request: URLRequest, resumeData: Data?) {
        let task = resumeData.map { session.downloadTask(withResumeData: $0) } ?? session.downloadTask(with: request)
        task.taskDescription = key
        task.priority = Self.priority(for: request)
        task.resume()
    }

    /// A person's download runs before an automatic one (decision 0048, I3c): a request that forbids an expensive network is
    /// the automatic kind (invariant 4), so its priority is low and a person's is high. Background tasks cannot be held.
    nonisolated static func priority(for request: URLRequest) -> Float {
        request.allowsExpensiveNetworkAccess ? URLSessionTask.highPriority : URLSessionTask.lowPriority
    }

    func cancel(key: String) async {
        for task in await session.allTasks where task.taskDescription == key { task.cancel() }
    }

    func cancelAll() async {
        for task in await session.allTasks { task.cancel() }
    }

    func runningKeys() async -> Set<String> {
        Set(await session.allTasks.compactMap { $0.state == .completed ? nil : $0.taskDescription })
    }

    // MARK: URLSessionDownloadDelegate (a serial background queue)

    nonisolated func urlSession(
        _ session: URLSession, downloadTask: URLSessionDownloadTask, didWriteData bytesWritten: Int64,
        totalBytesWritten: Int64, totalBytesExpectedToWrite: Int64
    ) {
        guard let key = downloadTask.taskDescription, throttle.allow(key) else { return }
        let expected = max(totalBytesExpectedToWrite, 0)
        Task { @MainActor [weak self] in self?.events?.progressed(key: key, written: totalBytesWritten, expected: expected) }
    }

    nonisolated func urlSession(_ session: URLSession, downloadTask: URLSessionDownloadTask, didFinishDownloadingTo location: URL) {
        guard let key = downloadTask.taskDescription else { return }
        let response = downloadTask.response as? HTTPURLResponse
        let status = response?.statusCode ?? 0
        guard (200..<300).contains(status) else {
            let wait = Int(response?.value(forHTTPHeaderField: "Retry-After") ?? "") ?? 0
            Task { @MainActor [weak self] in self?.events?.failed(key: key, status: status, retryAfterS: wait, resumeData: nil) }
            return
        }
        // The system deletes [location] when this method returns, so the move happens here, before anything else.
        let staged = incoming.appendingPathComponent(UUID().uuidString)
        do {
            try FileManager.default.createDirectory(at: incoming, withIntermediateDirectories: true)
            try FileManager.default.moveItem(at: location, to: staged)
        } catch {
            Self.log.warning("download: could not keep a finished file")
            Task { @MainActor [weak self] in self?.events?.failed(key: key, status: 0, retryAfterS: 0, resumeData: nil) }
            return
        }
        let bytes = ((try? FileManager.default.attributesOfItem(atPath: staged.path))?[.size] as? NSNumber)?.int64Value ?? 0
        let announced = response?.expectedContentLength ?? -1
        let length = announced > 0 ? announced : 0
        let mime = response?.value(forHTTPHeaderField: "Content-Type")
        Task { @MainActor [weak self] in
            self?.events?.arrived(key: key, status: status, length: length, mime: mime, staged: staged, bytes: bytes)
        }
    }

    nonisolated func urlSession(_ session: URLSession, task: URLSessionTask, didCompleteWithError error: Error?) {
        guard let error, let key = task.taskDescription else { return }
        let ns = error as NSError
        // A task the app cancelled (remove, sign-out) is no failure.
        if ns.domain == NSURLErrorDomain && ns.code == NSURLErrorCancelled { return }
        let resume = ns.userInfo[NSURLSessionDownloadTaskResumeData] as? Data
        Task { @MainActor [weak self] in self?.events?.failed(key: key, status: 0, retryAfterS: 0, resumeData: resume) }
    }

    /// A redirect is never followed (the bearer must not leave the pinned origin): the 3xx answer fails the file.
    nonisolated func urlSession(
        _ session: URLSession, task: URLSessionTask, willPerformHTTPRedirection response: HTTPURLResponse,
        newRequest request: URLRequest, completionHandler: @escaping (URLRequest?) -> Void
    ) {
        completionHandler(nil)
    }

    nonisolated func urlSessionDidFinishEvents(forBackgroundURLSession session: URLSession) {
        DispatchQueue.main.async {
            MainActor.assumeIsolated {
                let done = BackgroundTransport.pendingCompletion
                BackgroundTransport.pendingCompletion = nil
                done?()
            }
        }
    }
}

/// At most four progress events a second for each key.
nonisolated private final class Throttle: @unchecked Sendable {
    private let lock = NSLock()
    private var last: [String: TimeInterval] = [:]

    func allow(_ key: String) -> Bool {
        lock.lock()
        defer { lock.unlock() }
        let now = ProcessInfo.processInfo.systemUptime
        if let before = last[key], now - before < 0.25 { return false }
        last[key] = now
        return true
    }
}
