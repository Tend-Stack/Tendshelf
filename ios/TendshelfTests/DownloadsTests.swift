import XCTest
@preconcurrency import TendshelfShared
@testable import Tendshelf

/// A transport that records what the engine asked of it; the test plays the system's part by calling the engine's events.
@MainActor
final class FakeTransport: DownloadTransport {
    struct Started {
        let key: String
        let request: URLRequest
        let resume: Data?
    }

    weak var events: DownloadEvents?
    var started: [Started] = []
    var cancelled: [String] = []
    var cancelledAll = 0
    var held: Set<String> = []

    func start(key: String, request: URLRequest, resumeData: Data?) { started.append(Started(key: key, request: request, resume: resumeData)) }
    func cancel(key: String) async { cancelled.append(key) }
    func cancelAll() async { cancelledAll += 1 }
    func runningKeys() async -> Set<String> { held }
}

/// A server with scripted manifests and episode documents.
@MainActor
final class FakeDownloadApi: PlayerAPI {
    var manifest = ""
    var episode = ""
    var error: ApiError?
    var calls: [String] = []
    var peek: String?
    var peekError: ApiError?
    var removeStatus = 204
    var removed: [String] = []
    var shows: [ShowCard] = []
    var showsError: ApiError?
    var episodesOf: [String: [EpisodeCard]] = [:]
    /// Episode documents by id, for an automatic fetch of several (falls back to `episode`).
    var episodeDocs: [String: String] = [:]
    var manifests: [String: String] = [:]

    func manifestText(token: String, bookId: String) async throws -> String {
        calls.append("manifest \(token)")
        if let error { throw error }
        if let text = manifests[bookId] { return text }
        return manifest
    }

    func episodeText(token: String, id: String) async throws -> String {
        calls.append("episode \(token)")
        if let error { throw error }
        if let text = episodeDocs[id] { return text }
        return episode
    }

    func cover(token: String, bookId: String) async -> Data? { Data([0xff, 0xd8, 0xff]) }
    func chaptersText(token: String, path: String) async -> String? { nil }
    func podcastArt(token: String, artPath: String) async -> Data? { nil }
    func settings(token: String) async throws -> PlayerSettings { throw ApiError(status: 0) }
    func manifest(token: String, bookId: String) async throws -> BookManifest { throw ApiError(status: 0) }
    func episode(token: String, id: String) async throws -> EpisodeDoc { throw ApiError(status: 0) }
    func chapters(token: String, path: String, durationMs: Int64) async -> [Chapter] { [] }
    func radioStation(token: String, id: String) async throws -> StationCard { throw ApiError(status: 0) }
    func radioArt(token: String, id: String, artPath: String) async -> Data? { nil }
    func radioNow(token: String, id: String) async -> String? { nil }
    func liveAddress(token: String, id: String, origin: Origin) async throws -> URL { throw ApiError(status: 0) }
    func progress(token: String, bookId: String) async throws -> RemoteProgress? { nil }
    func queueNext(token: String, ended: String) async throws -> String? { nil }
    func queuePeek(token: String, ended: String) async throws -> String? {
        calls.append("peek \(ended)")
        if let peekError { throw peekError }
        return peek
    }

    func queueRemove(token: String, bookId: String) async throws -> Int {
        removed.append(bookId)
        return removeStatus
    }

    func podcastShows(token: String) async throws -> [ShowCard] {
        if let showsError { throw showsError }
        return shows
    }

    func showEpisodes(token: String, showId: String) async throws -> [EpisodeCard] { episodesOf[showId] ?? [] }
    func putSpeed(token: String, bookId: String, speed: Double) async throws {}
    func batch(token: String, saves: [Save], deviceId: String) async throws -> [BatchItemResult] { [] }
}

/// The engine over a temporary folder, a fake transport and a fake server; all of it removed at teardown.
@MainActor
final class DownloadsRig {
    static let book = "bk_01TESTBOOK0001"
    static let episode = "pe_01TESTEPISODE1"
    static let origin = Origin.companion.parse(input: "https://books.example")!

    let root: URL
    let paths: OfflinePaths
    let defaults: UserDefaults
    let store: SqliteOfflineStore
    let transport = FakeTransport()
    let api = FakeDownloadApi()
    var connection: Connection? = Connection(origin: DownloadsRig.origin, token: "tok-1", deviceId: "dev-1")
    var freeBytes: Int64 = 10 * 1024 * 1024 * 1024
    var notices: [String] = []
    var unmetered = true
    var clock: Int64 = 1_000
    var changes: [String] = []
    let downloads: Downloads
    private let suite: String

    init(_ test: XCTestCase, pendingWipe: Bool = false, retryScale: Double = 0.001) {
        let id = UUID().uuidString
        root = FileManager.default.temporaryDirectory.appendingPathComponent("tendshelf-dl-\(id)", isDirectory: true)
        paths = OfflinePaths(root: root.appendingPathComponent("offline", isDirectory: true))
        suite = "tendshelf.tests.dl.\(id)"
        defaults = UserDefaults(suiteName: suite)!
        if pendingWipe { defaults.set(true, forKey: Downloads.wipePendingKey) }
        try? paths.prepare()
        if pendingWipe { try? Data("x".utf8).write(to: paths.root.appendingPathComponent("stray")) }
        store = SqliteOfflineStore(path: paths.database)
        // The closures read the rig's fields, so a test can change them after init.
        var rig: DownloadsRig?
        downloads = Downloads(
            store: store, paths: paths, transport: transport, api: { _ in rig!.api }, connection: { rig!.connection },
            userId: { "usr-1" }, free: { _ in rig!.freeBytes }, notice: { rig!.notices.append($0) }, now: { rig!.clock },
            defaults: defaults, retryScale: retryScale, unmetered: { rig!.unmetered },
            text: TextStore(paths: paths, protocolClasses: [StubProtocol.self]))
        rig = self
        downloads.onChange = { [unowned self] in changes.append($0) }
        let (root, suite) = (self.root, self.suite)
        test.addTeardownBlock {
            try? FileManager.default.removeItem(at: root)
            UserDefaults(suiteName: suite)!.removePersistentDomain(forName: suite)
        }
        api.manifest = Self.manifestJson(download: true, files: [("f_01FILEONE", 100), ("f_01FILETWO", 200)])
        api.episode = Self.episodeJson(bytes: 0)
    }

    static func manifestJson(download: Bool, files: [(String, Int)]) -> String {
        let list = files.enumerated().map {
            "{\"id\":\"\($1.0)\",\"idx\":\($0),\"offsetMs\":0,\"durationMs\":4500,\"mime\":\"audio/mpeg\",\"needsCompat\":false,\"size\":\($1.1)}"
        }.joined(separator: ",")
        return "{\"book\":{\"id\":\"\(book)\",\"title\":\"Parts\",\"durationMs\":9000,\"cover\":\"x\",\"authors\":[{\"name\":\"Ann\"}]},"
            + "\"files\":[\(list)],\"chapters\":[],\"permissions\":{\"download\":\(download)}}"
    }

    static func episodeJson(bytes: Int) -> String {
        "{\"id\":\"\(episode)\",\"showId\":\"ps_01SHOW\",\"title\":\"Ep\",\"show\":\"Show\",\"artUrl\":\"\",\"durationMs\":1000,"
            + "\"audioUrl\":\"/x\",\"mime\":\"audio/mpeg\",\"bytes\":\(bytes),\"chaptersUrl\":\"\"}"
    }

    static func key(_ cache: String) -> String { Downloads.key(book, cache) }

    /// A staged file of [size] bytes, as the transport leaves it in `incoming`.
    func staged(_ size: Int) -> URL {
        try? FileManager.default.createDirectory(at: paths.incoming, withIntermediateDirectories: true)
        let url = paths.incoming.appendingPathComponent(UUID().uuidString)
        try? Data(count: size).write(to: url)
        return url
    }

    func arrive(_ cache: String, size: Int, length: Int64? = nil, book: String = DownloadsRig.book) {
        downloads.arrived(
            key: Downloads.key(book, cache), status: 200, length: length ?? Int64(size), mime: "audio/mpeg", staged: staged(size),
            bytes: Int64(size))
    }
}

@MainActor
final class DownloadsTests: XCTestCase {
    private func state(_ r: DownloadsRig, _ id: String = DownloadsRig.book) -> String? {
        r.store.get(id: id)?.state.name
    }

    func testTwoFilesMakeTwoAuthorizedRequestsAndNoTokenIsKept() async {
        let r = DownloadsRig(self)
        await r.downloads.add(DownloadsRig.book)
        XCTAssertEqual(r.transport.started.count, 2)
        for s in r.transport.started {
            XCTAssertEqual(s.request.httpMethod, "GET")
            XCTAssertEqual(s.request.value(forHTTPHeaderField: "Authorization"), "Bearer tok-1")
            XCTAssertFalse(s.request.url!.absoluteString.contains("tok-1"))
            XCTAssertFalse(s.key.contains("tok-1"))
            XCTAssertTrue(s.request.url!.absoluteString.hasPrefix("https://books.example/api/v1/stream/"))
            XCTAssertTrue(s.request.allowsExpensiveNetworkAccess)
            XCTAssertEqual(s.request.timeoutInterval, 60)
            XCTAssertNil(s.resume)
        }
        XCTAssertEqual(state(r), "QUEUED")
        for f in r.store.files(DownloadsRig.book) {
            XCTAssertFalse([f.url, f.cacheKey, f.mime, f.name].contains { $0.contains("tok-1") })
        }
        XCTAssertFalse(String(describing: r.defaults.dictionaryRepresentation()).contains("tok-1"))
        XCTAssertNotNil(r.downloads.coverData(DownloadsRig.book))
    }

    func testRefusalsTellThePersonAndAddNothing() async {
        let r = DownloadsRig(self)
        r.api.manifest = DownloadsRig.manifestJson(download: false, files: [("f_01FILEONE", 100)])
        await r.downloads.add(DownloadsRig.book)
        XCTAssertEqual(r.notices, [words(Words.Key.downloadNotAllowed)])

        r.api.manifest = DownloadsRig.manifestJson(download: true, files: [("f_01FILEONE", 100)])
        r.freeBytes = 0
        await r.downloads.add(DownloadsRig.book)
        XCTAssertEqual(r.notices.last, words(Words.Key.downloadNoRoom))

        r.freeBytes = 10 * 1024 * 1024 * 1024
        r.api.manifest = DownloadsRig.manifestJson(download: true, files: [])
        await r.downloads.add(DownloadsRig.book)
        XCTAssertEqual(r.notices.last, words(Words.Key.downloadNothing))

        r.api.error = ApiError(status: 403)
        await r.downloads.add(DownloadsRig.book)
        XCTAssertEqual(r.notices.last, words(Words.Key.downloadNotAllowed))
        XCTAssertEqual(r.notices.count, 4)
        XCTAssertTrue(r.store.all().isEmpty)
        XCTAssertTrue(r.transport.started.isEmpty)
    }

    func testBothFilesWholeIsReadyAndNamedByTheirId() async {
        let r = DownloadsRig(self)
        await r.downloads.add(DownloadsRig.book)
        r.arrive("f_01FILEONE", size: 100)
        XCTAssertEqual(state(r), "QUEUED", "one file is not a book")
        r.arrive("f_01FILETWO", size: 200)
        XCTAssertEqual(state(r), "READY")
        for name in ["f_01FILEONE.mp3", "f_01FILETWO.mp3"] {
            XCTAssertTrue(FileManager.default.fileExists(atPath: r.paths.audioFile(DownloadsRig.book, name)!.path), name)
        }
        XCTAssertTrue(r.downloads.hasAudio(DownloadsRig.book))
        XCTAssertTrue(r.downloads.playable(DownloadsRig.book))
        XCTAssertEqual(r.downloads.view(DownloadsRig.book).state, "ready")
        XCTAssertEqual(r.downloads.view(DownloadsRig.book).pct, 100)
        XCTAssertNotNil(r.downloads.localFile(DownloadsRig.book, "f_01FILEONE"))
        XCTAssertEqual((try? FileManager.default.contentsOfDirectory(atPath: r.paths.incoming.path))?.count ?? 0, 0)
    }

    func testAShortFileIsNeverWhole() async {
        let r = DownloadsRig(self)
        await r.downloads.add(DownloadsRig.book)
        r.arrive("f_01FILEONE", size: 60, length: 100)
        r.arrive("f_01FILETWO", size: 200)
        XCTAssertEqual(state(r), "FAILED")
        XCTAssertFalse(r.downloads.playable(DownloadsRig.book))
    }

    func testABusyServerIsRetriedAfterTheDelayThenFinishes() async {
        let r = DownloadsRig(self)
        await r.downloads.add(DownloadsRig.book)
        r.downloads.failed(key: DownloadsRig.key("f_01FILEONE"), status: 503, retryAfterS: 0, resumeData: nil)
        XCTAssertEqual(r.store.files(DownloadsRig.book)[0].failures, 1)
        await eventually("second try") { r.transport.started.count == 3 }
        XCTAssertEqual(r.transport.started.last?.key, DownloadsRig.key("f_01FILEONE"))
        r.arrive("f_01FILEONE", size: 100)
        r.arrive("f_01FILETWO", size: 200)
        XCTAssertEqual(state(r), "READY")
    }

    func testAnUnauthorizedFileFails() async {
        let r = DownloadsRig(self)
        await r.downloads.add(DownloadsRig.book)
        r.downloads.failed(key: DownloadsRig.key("f_01FILEONE"), status: 401, retryAfterS: 0, resumeData: nil)
        XCTAssertEqual(state(r), "FAILED")
        XCTAssertEqual(r.transport.started.count, 2, "no retry for 401")
    }

    func testResumeDataIsUsedOnceAndNotKept() async {
        let r = DownloadsRig(self)
        await r.downloads.add(DownloadsRig.book)
        let key = DownloadsRig.key("f_01FILEONE")
        r.downloads.failed(key: key, status: 0, retryAfterS: 0, resumeData: Data([1, 2]))
        XCTAssertEqual(r.transport.started.last?.resume, Data([1, 2]))
        r.downloads.failed(key: key, status: 0, retryAfterS: 0, resumeData: nil)
        await eventually("plain retry") { r.transport.started.count == 4 }
        XCTAssertNil(r.transport.started.last?.resume)
    }

    func testRemoveCancelsAndDeletesEverything() async {
        let r = DownloadsRig(self)
        await r.downloads.add(DownloadsRig.book)
        r.arrive("f_01FILEONE", size: 100)
        await r.downloads.remove(DownloadsRig.book, byPerson: true)
        XCTAssertEqual(Set(r.transport.cancelled), [DownloadsRig.key("f_01FILEONE"), DownloadsRig.key("f_01FILETWO")])
        XCTAssertTrue(r.store.all().isEmpty)
        XCTAssertTrue(r.store.files(DownloadsRig.book).isEmpty)
        XCTAssertFalse(FileManager.default.fileExists(atPath: r.paths.audio(DownloadsRig.book)!.path))
        XCTAssertFalse(FileManager.default.fileExists(atPath: r.paths.cover(DownloadsRig.book)!.path))
        // A file that arrives for a removed book is deleted, not kept.
        r.arrive("f_01FILETWO", size: 200)
        XCTAssertEqual((try? FileManager.default.contentsOfDirectory(atPath: r.paths.incoming.path))?.count ?? 0, 0)
    }

    func testReconcileRestartsALostTask() async {
        let r = DownloadsRig(self)
        await r.downloads.add(DownloadsRig.book)
        r.transport.held = [DownloadsRig.key("f_01FILEONE")]
        await r.downloads.reconcile()
        XCTAssertEqual(r.transport.started.count, 3)
        XCTAssertEqual(r.transport.started.last?.key, DownloadsRig.key("f_01FILETWO"))
        XCTAssertNil(r.transport.started.last?.resume)
    }

    func testPercentNeverGoesBack() async {
        let r = DownloadsRig(self)
        await r.downloads.add(DownloadsRig.book)
        r.downloads.progressed(key: DownloadsRig.key("f_01FILEONE"), written: 90, expected: 100)
        let first = r.downloads.view(DownloadsRig.book).pct
        XCTAssertGreaterThan(first, 0)
        r.downloads.progressed(key: DownloadsRig.key("f_01FILEONE"), written: 5, expected: 100)
        XCTAssertGreaterThanOrEqual(r.downloads.view(DownloadsRig.book).pct, first)
        XCTAssertEqual(state(r), "DOWNLOADING")
    }

    func testAnEpisodeWithNoLengthIsWholeByItsBytesAndItsSizeIsSet() async {
        let r = DownloadsRig(self)
        await r.downloads.add(DownloadsRig.episode)
        XCTAssertEqual(r.transport.started.count, 1)
        XCTAssertTrue(r.transport.started[0].request.url!.absoluteString.contains("/api/v1/podcasts/audio/"))
        r.downloads.arrived(
            key: Downloads.key(DownloadsRig.episode, DownloadsRig.episode), status: 200, length: 0, mime: "audio/mpeg",
            staged: r.staged(300), bytes: 300)
        let row = r.store.get(id: DownloadsRig.episode)
        XCTAssertEqual(row?.state.name, "READY")
        XCTAssertEqual(row?.sizeBytes, 300)
        XCTAssertEqual(row?.kind, OfflineKt.KIND_EPISODE)
    }

    func testWipeCancelsEverythingAndDeletesTheFolderAndTheDefaults() async {
        let r = DownloadsRig(self)
        await r.downloads.add(DownloadsRig.book)
        r.defaults.set("x", forKey: "offline.something")
        await r.downloads.wipe().value
        XCTAssertEqual(r.transport.cancelledAll, 1)
        XCTAssertFalse(FileManager.default.fileExists(atPath: r.paths.root.path))
        XCTAssertNil(r.defaults.object(forKey: "offline.something"))
        XCTAssertFalse(r.defaults.bool(forKey: Downloads.wipePendingKey))
        XCTAssertTrue(r.store.all().isEmpty)
        // The next download makes the folder and the store anew.
        await r.downloads.add(DownloadsRig.book)
        XCTAssertEqual(r.store.all().count, 1)
    }

    func testAWipeLeftPendingFinishesAtLaunch() async {
        let r = DownloadsRig(self, pendingWipe: true)
        await eventually("wipe") { !FileManager.default.fileExists(atPath: r.paths.root.path) }
        XCTAssertGreaterThanOrEqual(r.transport.cancelledAll, 1)
        await eventually("flag") { !r.defaults.bool(forKey: Downloads.wipePendingKey) }
    }

    func testHasOtherPerson() async {
        let r = DownloadsRig(self)
        await r.downloads.add(DownloadsRig.book)
        XCTAssertFalse(r.downloads.hasOtherPerson("usr-1"))
        XCTAssertTrue(r.downloads.hasOtherPerson("usr-2"))
    }
}
