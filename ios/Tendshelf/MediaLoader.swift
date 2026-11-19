import AVFoundation
import UniformTypeIdentifiers
@preconcurrency import TendshelfShared

/// One thing AVFoundation asked the loader for. The app wraps an `AVAssetResourceLoadingRequest`; the tests use a fake.
/// Every method is called on the loader's queue.
nonisolated protocol MediaRequest: AnyObject {
    /// First byte wanted.
    var offset: Int64 { get }
    /// Bytes wanted (0 for an information-only request).
    var length: Int { get }
    /// True when everything from [offset] to the end is wanted.
    var toEnd: Bool { get }
    var wantsInfo: Bool { get }
    func setInfo(type: String, length: Int64, ranges: Bool)
    func respond(_ data: Data)
    func finish()
    func fail(_ error: Error)
}

/// Why a loading request failed, for the places that only see an `Error`.
nonisolated enum MediaLoaderError: Error {
    case unknownHandle
    case otherServer
    case status(Int)
}

/// Gives AVFoundation the audio without the token in any URL (invariant 1). AVPlayer sees `tsmedia://m/<n>` handles that
/// native code registered; each loading request becomes one GET to the registered address with the bearer token in the
/// `Authorization` header, a `Range`, no cookies, and only while the current connection is on the handle's origin.
/// Handles are opaque counters, so file ids stay out of AVFoundation's URL cache keys.
nonisolated final class MediaLoader: NSObject, AVAssetResourceLoaderDelegate, URLSessionDataDelegate, @unchecked Sendable {
    static let scheme = "tsmedia"

    private nonisolated struct Entry {
        let url: String
        let origin: Origin
        let mime: String
    }

    private nonisolated final class Job {
        let request: MediaRequest
        let handle: Int
        let entry: Entry
        var task: URLSessionDataTask?
        var delivered: Int64 = 0
        var skip: Int64 = 0
        var errorCount = 0
        var infoDone = false
        var finished = false
        init(request: MediaRequest, handle: Int, entry: Entry) {
            self.request = request
            self.handle = handle
            self.entry = entry
        }
        var infoOnly: Bool { request.wantsInfo && request.length == 0 && !request.toEnd }
    }

    /// The one serial queue of the loader: the delegate callbacks, the session delegate and every table.
    let queue = DispatchQueue(label: "app.tendshelf.media")
    private let connection: @Sendable () -> Connection?
    private let retryScale: Double
    private var session: URLSession!
    private var entries: [Int: Entry] = [:]
    private var failures: [Int: (status: Int, retryAfterS: Int, otherServer: Bool)] = [:]
    private var jobs: [ObjectIdentifier: Job] = [:]
    private var tasks: [Int: Job] = [:]
    private var counter = 0

    init(
        connection: @escaping @Sendable () -> Connection?,
        configuration: URLSessionConfiguration = ServerClient.ephemeralConfiguration(),
        retryScale: Double = 1
    ) {
        self.connection = connection
        self.retryScale = retryScale
        super.init()
        let operations = OperationQueue()
        operations.maxConcurrentOperationCount = 1
        operations.underlyingQueue = queue
        session = URLSession(configuration: configuration, delegate: self, delegateQueue: operations)
    }

    // MARK: Handles

    /// A handle for [url] that AVFoundation may load while the connection is on [origin]. Call off the loader's queue.
    func register(url: String, origin: Origin, mime: String) -> URL {
        queue.sync {
            counter += 1
            entries[counter] = Entry(url: url, origin: origin, mime: mime)
            return URL(string: "\(Self.scheme)://m/\(counter)")!
        }
    }

    /// Drops every handle and cancels every task: sign-out, a new item, stop.
    func forgetAll() {
        queue.sync {
            entries.removeAll()
            failures.removeAll()
            for job in jobs.values {
                job.finished = true
                job.task?.cancel()
            }
            jobs.removeAll()
            tasks.removeAll()
        }
    }

    /// The last failure the loader saw for [handle], for the engine's retry and message rules.
    func lastFailure(_ handle: URL) -> (status: Int, retryAfterS: Int, otherServer: Bool)? {
        guard let n = Self.number(of: handle) else { return nil }
        return queue.sync { failures[n] }
    }

    static func number(of url: URL) -> Int? {
        guard url.scheme == scheme, url.host == "m" else { return nil }
        return Int(url.path.dropFirst())
    }

    // MARK: AVAssetResourceLoaderDelegate

    func resourceLoader(_ resourceLoader: AVAssetResourceLoader, shouldWaitForLoadingOfRequestedResource loadingRequest: AVAssetResourceLoadingRequest) -> Bool {
        guard let url = loadingRequest.request.url, Self.number(of: url) != nil else { return false }
        begin(AVMediaRequest(loadingRequest), url: url)
        return true
    }

    func resourceLoader(_ resourceLoader: AVAssetResourceLoader, didCancel loadingRequest: AVAssetResourceLoadingRequest) {
        queue.async { [self] in
            guard let job = jobs.values.first(where: { ($0.request as? AVMediaRequest)?.raw === loadingRequest }) else { return }
            drop(job)
            job.task?.cancel()
        }
    }

    /// Starts [request] for the handle in [url]. Used by the delegate and by the tests.
    func begin(_ request: MediaRequest, url: URL) {
        queue.async { [self] in
            guard let n = Self.number(of: url), let entry = entries[n] else {
                request.fail(MediaLoaderError.unknownHandle)
                return
            }
            // The registered address must still be on the handle's origin, and the connection must be on it too.
            guard entry.origin.matches(url: entry.url), let conn = connection(), conn.origin == entry.origin else {
                failures[n] = (0, 0, true)
                request.fail(MediaLoaderError.otherServer)
                return
            }
            let job = Job(request: request, handle: n, entry: entry)
            jobs[ObjectIdentifier(request)] = job
            send(job, conn)
        }
    }

    // MARK: Requests

    private func send(_ job: Job, _ conn: Connection) {
        guard let url = URL(string: job.entry.url), url.path.hasPrefix("/api/v1/") else {
            failures[job.handle] = (0, 0, true)
            finish(job, error: MediaLoaderError.otherServer)
            return
        }
        var request = URLRequest(url: url, cachePolicy: .reloadIgnoringLocalCacheData, timeoutInterval: 30)
        request.httpShouldHandleCookies = false
        request.setValue("Bearer \(conn.token)", forHTTPHeaderField: "Authorization")
        let start = job.request.offset + job.delivered
        if job.infoOnly {
            request.setValue("bytes=0-1", forHTTPHeaderField: "Range")
        } else if job.request.toEnd {
            request.setValue("bytes=\(start)-", forHTTPHeaderField: "Range")
        } else {
            let end = job.request.offset + Int64(job.request.length) - 1
            if start > end {
                finish(job, error: nil)
                return
            }
            request.setValue("bytes=\(start)-\(end)", forHTTPHeaderField: "Range")
        }
        let task = session.dataTask(with: request)
        job.task = task
        tasks[task.taskIdentifier] = job
        task.resume()
    }

    private func drop(_ job: Job) {
        job.finished = true
        jobs[ObjectIdentifier(job.request)] = nil
        if let id = job.task?.taskIdentifier { tasks[id] = nil }
    }

    private func finish(_ job: Job, error: Error?) {
        guard !job.finished else { return }
        drop(job)
        if let error { job.request.fail(error) } else { job.request.finish() }
    }

    /// A failed answer: retry a busy or broken one the way Android does, or fail the request and remember why.
    private func failed(_ job: Job, status: Int, retryAfterS: Int) {
        job.task?.cancel()
        if let id = job.task?.taskIdentifier { tasks[id] = nil }
        job.task = nil
        job.errorCount += 1
        if let wait = StreamRetryKt.streamRetryDelayMs(status: Int32(status), errorCount: Int32(job.errorCount)) {
            let delay = Double(wait.int64Value) / 1000 * retryScale
            queue.asyncAfter(deadline: .now() + delay) { [self] in
                guard !job.finished else { return }
                guard let conn = connection(), conn.origin == job.entry.origin else {
                    failures[job.handle] = (status, retryAfterS, true)
                    finish(job, error: MediaLoaderError.otherServer)
                    return
                }
                send(job, conn)
            }
            return
        }
        failures[job.handle] = (status, retryAfterS, false)
        finish(job, error: MediaLoaderError.status(status))
    }

    // MARK: URLSessionDataDelegate

    func urlSession(
        _ session: URLSession, task: URLSessionTask, willPerformHTTPRedirection response: HTTPURLResponse,
        newRequest request: URLRequest, completionHandler: @escaping (URLRequest?) -> Void
    ) {
        completionHandler(nil)
    }

    func urlSession(
        _ session: URLSession, dataTask: URLSessionDataTask, didReceive response: URLResponse,
        completionHandler: @escaping (URLSession.ResponseDisposition) -> Void
    ) {
        guard let job = tasks[dataTask.taskIdentifier], !job.finished, let http = response as? HTTPURLResponse else {
            completionHandler(.cancel)
            return
        }
        let status = http.statusCode
        guard (200..<300).contains(status) else {
            let wait = http.value(forHTTPHeaderField: "Retry-After").flatMap { Int($0.trimmingCharacters(in: .whitespaces)) } ?? 0
            completionHandler(.cancel)
            failed(job, status: status, retryAfterS: wait)
            return
        }
        // A server that ignored the Range answers 200 with the whole file: drop what came before the wanted offset.
        if status == 200 {
            job.skip = job.request.offset + job.delivered
        }
        if job.request.wantsInfo, !job.infoDone {
            job.infoDone = true
            let total = Self.total(http) ?? 0
            job.request.setInfo(type: Self.type(http, fallback: job.entry.mime), length: total, ranges: status == 206)
        }
        if job.infoOnly {
            completionHandler(.cancel)
            finish(job, error: nil)
            return
        }
        completionHandler(.allow)
    }

    func urlSession(_ session: URLSession, dataTask: URLSessionDataTask, didReceive data: Data) {
        guard let job = tasks[dataTask.taskIdentifier], !job.finished else { return }
        var piece = data
        if job.skip > 0 {
            let cut = min(Int64(piece.count), job.skip)
            piece = piece.dropFirst(Int(cut))
            job.skip -= cut
        }
        // A request for a few bytes (AVFoundation probes with two) gets only those.
        if !job.request.toEnd {
            let left = Int64(job.request.length) - job.delivered
            if Int64(piece.count) > left { piece = piece.prefix(Int(max(0, left))) }
        }
        if !piece.isEmpty {
            job.request.respond(Data(piece))
            job.delivered += Int64(piece.count)
        }
        if !job.request.toEnd, job.delivered >= Int64(job.request.length) {
            dataTask.cancel()
            finish(job, error: nil)
        }
    }

    func urlSession(_ session: URLSession, task: URLSessionTask, didCompleteWithError error: Error?) {
        guard let job = tasks[task.taskIdentifier], !job.finished, job.task === task else { return }
        tasks[task.taskIdentifier] = nil
        if let error {
            if (error as? URLError)?.code == .cancelled { return }
            failed(job, status: 0, retryAfterS: 0)
            return
        }
        finish(job, error: nil)
    }

    // MARK: Answers

    /// The content type to tell AVFoundation: the answer's, else the manifest's, else plain audio; an `.m4b` is an MPEG-4 audio file.
    static func type(_ http: HTTPURLResponse, fallback: String) -> String {
        let fromAnswer = http.value(forHTTPHeaderField: "Content-Type")?
            .split(separator: ";").first.map { $0.trimmingCharacters(in: .whitespaces).lowercased() }
        for word in [fromAnswer, fallback.lowercased()].compactMap({ $0 }) where !word.isEmpty {
            if word == "audio/x-m4b" || word == "audio/m4b" { return "com.apple.m4a-audio" }
            if word == "application/octet-stream" { continue }
            if let t = UTType(mimeType: word) { return t.identifier }
        }
        return UTType.audio.identifier
    }

    /// The whole length of the file: the total of `Content-Range`, else `Content-Length`.
    static func total(_ http: HTTPURLResponse) -> Int64? {
        if let range = http.value(forHTTPHeaderField: "Content-Range"), let slash = range.lastIndex(of: "/"),
           let total = Int64(range[range.index(after: slash)...].trimmingCharacters(in: .whitespaces)) {
            return total
        }
        if http.statusCode == 206 { return nil }
        return http.value(forHTTPHeaderField: "Content-Length").flatMap { Int64($0) }
    }
}

/// The adapter over a real loading request.
nonisolated final class AVMediaRequest: MediaRequest, @unchecked Sendable {
    let raw: AVAssetResourceLoadingRequest
    init(_ raw: AVAssetResourceLoadingRequest) { self.raw = raw }

    var offset: Int64 { raw.dataRequest?.requestedOffset ?? 0 }
    var length: Int { Int(raw.dataRequest?.requestedLength ?? 0) }
    var toEnd: Bool { raw.dataRequest?.requestsAllDataToEndOfResource ?? false }
    var wantsInfo: Bool { raw.contentInformationRequest != nil }

    func setInfo(type: String, length: Int64, ranges: Bool) {
        guard let info = raw.contentInformationRequest else { return }
        info.contentType = type
        info.contentLength = length
        info.isByteRangeAccessSupported = ranges
    }

    func respond(_ data: Data) { raw.dataRequest?.respond(with: data) }
    func finish() { raw.finishLoading() }
    func fail(_ error: Error) { raw.finishLoading(with: error) }
}
