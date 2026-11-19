import AVFoundation
import XCTest
@preconcurrency import TendshelfShared
@testable import Tendshelf

/// An `OfflineSource` over fixed answers and files in a temporary folder.
@MainActor
final class FakeOffline: OfflineSource {
    var rows: [String: OfflineBook] = [:]
    var manifests: [String: String] = [:]
    var files: [String: URL] = [:] // "<book>|<cacheKey>"
    var covers: [String: Data] = [:]
    var chapters: [String: String] = [:]
    var settings = StoredJsonKt.defaultPlayerSettings()
    var kept: [PlayerSettings] = []
    var places: [(String, Int64)] = []
    var played: [String] = []
    var playingNow = ""

    func playableRow(_ id: String) -> OfflineBook? { rows[id] }
    func manifestJson(_ id: String) -> String? { manifests[id] }
    func localFile(_ bookId: String, _ cacheKey: String) -> URL? { files["\(bookId)|\(cacheKey)"] }
    func coverData(_ id: String) -> Data? { covers[id] }
    func chaptersText(_ id: String) -> String? { chapters[id] }
    func keptSettings() -> PlayerSettings { settings }
    func keep(_ s: PlayerSettings) { kept.append(s) }
    func notePlace(_ id: String, ms: Int64, at: Int64) { places.append((id, ms)) }
    func markPlayed(_ id: String) { played.append(id) }

    static func row(_ id: String, kind: String = OfflineKt.KIND_BOOK, placeMs: Int64 = 0, placeAt: Int64 = 0) -> OfflineBook {
        OfflineBook(
            bookId: id, userId: "usr-1", title: "T", author: "A", durationMs: 4_000, sizeBytes: 1, state: .ready, placeMs: placeMs,
            placeAt: placeAt, kind: kind, showId: "ps_01SHOW", publishedAt: 1, played: false)
    }
}

/// A server that must never be asked: every call is counted and fails.
@MainActor
final class DeadApi: PlayerAPI {
    var calls = 0
    private func dead<T>() throws -> T {
        calls += 1
        throw ApiError(status: 0)
    }

    func settings(token: String) async throws -> PlayerSettings { try dead() }
    func manifest(token: String, bookId: String) async throws -> BookManifest { try dead() }
    func manifestText(token: String, bookId: String) async throws -> String { try dead() }
    func episodeText(token: String, id: String) async throws -> String { try dead() }
    func chaptersText(token: String, path: String) async -> String? { calls += 1; return nil }
    func cover(token: String, bookId: String) async -> Data? { calls += 1; return nil }
    func episode(token: String, id: String) async throws -> EpisodeDoc { try dead() }
    func chapters(token: String, path: String, durationMs: Int64) async -> [Chapter] { calls += 1; return [] }
    func podcastArt(token: String, artPath: String) async -> Data? { calls += 1; return nil }
    func radioStation(token: String, id: String) async throws -> StationCard { try dead() }
    func radioArt(token: String, id: String, artPath: String) async -> Data? { calls += 1; return nil }
    func radioNow(token: String, id: String) async -> String? { calls += 1; return nil }
    func liveAddress(token: String, id: String, origin: Origin) async throws -> URL { try dead() }
    func progress(token: String, bookId: String) async throws -> RemoteProgress? { try dead() }
    func queueNext(token: String, ended: String) async throws -> String? { try dead() }
    func queuePeek(token: String, ended: String) async throws -> String? { try dead() }
    func queueRemove(token: String, bookId: String) async throws -> Int { try dead() }
    func podcastShows(token: String) async throws -> [ShowCard] { try dead() }
    func showEpisodes(token: String, showId: String) async throws -> [EpisodeCard] { try dead() }
    func putSpeed(token: String, bookId: String, speed: Double) async throws { calls += 1 }
    func batch(token: String, saves: [Save], deviceId: String) async throws -> [BatchItemResult] { try dead() }
}

/// I3c: a downloaded book or episode starts from the phone with no request (invariant 5).
@MainActor
final class OfflinePlaybackTests: XCTestCase {
    private let origin = Origin.companion.parse(input: "https://books.example")!
    private var folder: URL!

    override func setUp() async throws {
        folder = FileManager.default.temporaryDirectory.appendingPathComponent("tendshelf-play-\(UUID().uuidString)", isDirectory: true)
        try FileManager.default.createDirectory(at: folder, withIntermediateDirectories: true)
        // A network that fails every request, and counts them.
        MediaStub.reset { _ in MediaScript(status: 503, headers: [:], pieces: []) }
    }

    override func tearDown() async throws {
        try? FileManager.default.removeItem(at: folder)
        MediaStub.reset(nil)
    }

    private func wav(_ name: String, seconds: Double = 4) throws -> URL {
        let url = folder.appendingPathComponent(name)
        try makeWav(seconds: seconds).write(to: url)
        return url
    }

    private func manifest(files: [String], progress: String = "") -> String {
        let list = files.enumerated().map {
            "{\"id\":\"\($1)\",\"idx\":\($0),\"offsetMs\":\($0 * 4000),\"durationMs\":4000,\"mime\":\"audio/wav\",\"needsCompat\":false,\"size\":100}"
        }.joined(separator: ",")
        return "{\"book\":{\"id\":\"bk_01PARTS\",\"title\":\"Parts\",\"durationMs\":\(files.count * 4000),\"cover\":\"x\",\"authors\":[{\"name\":\"Ann\"}]},"
            + "\"files\":[\(list)],\"chapters\":[],\"permissions\":{\"download\":true}\(progress)}"
    }

    private func make(_ offline: FakeOffline, _ api: DeadApi) -> AudioPlayer {
        let conn = Connection(origin: origin, token: "tok-1", deviceId: "dev-1")
        let p = AudioPlayer(connection: { conn }, makeApi: { _ in api }, loader: MediaLoader(connection: { conn }, configuration: stubConfiguration()))
        p.offline = offline
        return p
    }

    func testAReadyBookReachesPlayingFromAFileWithZeroRequests() async throws {
        let offline = FakeOffline()
        offline.rows["bk_01PARTS"] = FakeOffline.row("bk_01PARTS")
        offline.manifests["bk_01PARTS"] = manifest(files: ["f_01ONE"])
        offline.files["bk_01PARTS|f_01ONE"] = try wav("one.wav")
        let api = DeadApi()
        let p = make(offline, api)
        p.start("bk_01PARTS", atMs: nil)
        let playing = await waitFor(15) { p.playing }
        XCTAssertTrue(playing, "error \(p.error)")
        XCTAssertEqual(api.calls, 0)
        XCTAssertEqual(MediaStub.seen.count, 0, "no request of any kind")
        XCTAssertEqual(offline.playingNow, "bk_01PARTS")
        XCTAssertEqual(p.items().first?.scheme, "file")
        p.stopAndClear()
        XCTAssertEqual(offline.playingNow, "")
    }

    func testANewerPlaceBesideTheRowWins() async throws {
        let offline = FakeOffline()
        offline.rows["bk_01PARTS"] = FakeOffline.row("bk_01PARTS", placeMs: 2_000, placeAt: 9_000)
        offline.manifests["bk_01PARTS"] = manifest(
            files: ["f_01ONE"],
            progress: ",\"progress\":{\"positionMs\":500,\"finished\":false,\"clientUpdatedAt\":100,\"deviceId\":\"x\",\"deviceLabel\":\"\"}")
        offline.files["bk_01PARTS|f_01ONE"] = try wav("one.wav")
        let p = make(offline, DeadApi())
        var written: [Int64?] = []
        p.onBookStarted = { _, at, _ in written.append(at) }
        p.start("bk_01PARTS", atMs: nil)
        XCTAssertEqual(written.count, 1)
        XCTAssertEqual(written.first ?? nil, 9_000, "the row's place is the newest write")
        let playing = await waitFor(15) { p.playing && p.positionMs >= 1_900 }
        XCTAssertTrue(playing, "position \(p.positionMs) error \(p.error)")
    }

    func testAHalfDownloadedBookPlaysItsLocalFileAndStreamsTheRest() async throws {
        let offline = FakeOffline()
        // Not ready: the network path runs (settings, manifest), and the file that is on the phone is used first.
        offline.manifests["bk_01PARTS"] = manifest(files: ["f_01ONE", "f_01TWO"])
        offline.files["bk_01PARTS|f_01ONE"] = try wav("one.wav")
        let conn = Connection(origin: origin, token: "tok-1", deviceId: "dev-1")
        let api = HalfApi(manifest: manifest(files: ["f_01ONE", "f_01TWO"]))
        let streaming = AudioPlayer(connection: { conn }, makeApi: { _ in api }, loader: MediaLoader(connection: { conn }, configuration: stubConfiguration()))
        streaming.offline = offline
        streaming.start("bk_01PARTS", atMs: nil)
        let queued = await waitFor(15) { streaming.items().count == 2 }
        XCTAssertTrue(queued, "error \(streaming.error)")
        XCTAssertEqual(streaming.items().first?.scheme, "file", "file 1 is on the phone")
        XCTAssertEqual(streaming.items().last?.scheme, MediaLoader.scheme, "file 2 is streamed")
        XCTAssertEqual(offline.kept.count, 1, "the settings the server gave are kept for an offline start")
        streaming.stopAndClear()
    }

    func testADownloadedEpisodePlaysItsFileAndTakesItsStoredChapters() async throws {
        let offline = FakeOffline()
        let id = "pe_01TESTEPISODE1"
        offline.rows[id] = FakeOffline.row(id, kind: OfflineKt.KIND_EPISODE)
        offline.manifests[id] =
            "{\"id\":\"\(id)\",\"showId\":\"ps_01SHOW\",\"title\":\"Ep\",\"show\":\"Show\",\"artUrl\":\"\",\"durationMs\":4000,"
            + "\"audioUrl\":\"/x\",\"mime\":\"audio/wav\",\"bytes\":100,\"chaptersUrl\":\"/c\"}"
        offline.files["\(id)|\(id)"] = try wav("ep.wav")
        offline.chapters[id] = "{\"chapters\":[{\"startTime\":0,\"title\":\"One\"},{\"startTime\":2,\"title\":\"Two\"}]}"
        let api = DeadApi()
        let p = make(offline, api)
        p.start(id, atMs: nil)
        let playing = await waitFor(15) { p.playing }
        XCTAssertTrue(playing, "error \(p.error)")
        XCTAssertEqual(api.calls, 0)
        XCTAssertEqual(MediaStub.seen.count, 0)
        XCTAssertEqual(p.items().first?.scheme, "file")
        XCTAssertEqual(offline.playingNow, id)
        p.stopAndClear()
    }
}

/// Settings and a manifest only; every file would be streamed.
@MainActor
final class HalfApi: PlayerAPI {
    let text: String
    private let dead = DeadApi()
    init(manifest: String) { text = manifest }

    func settings(token: String) async throws -> PlayerSettings { StoredJsonKt.defaultPlayerSettings() }
    func manifest(token: String, bookId: String) async throws -> BookManifest {
        PlayerParseKt.parseManifest(body: text, paths: ApiPaths(server: "https://books.example"))!
    }
    func manifestText(token: String, bookId: String) async throws -> String { text }
    func cover(token: String, bookId: String) async -> Data? { nil }
    func episodeText(token: String, id: String) async throws -> String { try await dead.episodeText(token: token, id: id) }
    func chaptersText(token: String, path: String) async -> String? { nil }
    func episode(token: String, id: String) async throws -> EpisodeDoc { try await dead.episode(token: token, id: id) }
    func chapters(token: String, path: String, durationMs: Int64) async -> [Chapter] { [] }
    func podcastArt(token: String, artPath: String) async -> Data? { nil }
    func radioStation(token: String, id: String) async throws -> StationCard { try await dead.radioStation(token: token, id: id) }
    func radioArt(token: String, id: String, artPath: String) async -> Data? { nil }
    func radioNow(token: String, id: String) async -> String? { nil }
    func liveAddress(token: String, id: String, origin: Origin) async throws -> URL { try await dead.liveAddress(token: token, id: id, origin: origin) }
    func progress(token: String, bookId: String) async throws -> RemoteProgress? { nil }
    func queueNext(token: String, ended: String) async throws -> String? { nil }
    func queuePeek(token: String, ended: String) async throws -> String? { nil }
    func queueRemove(token: String, bookId: String) async throws -> Int { 204 }
    func podcastShows(token: String) async throws -> [ShowCard] { [] }
    func showEpisodes(token: String, showId: String) async throws -> [EpisodeCard] { [] }
    func putSpeed(token: String, bookId: String, speed: Double) async throws {}
    func batch(token: String, saves: [Save], deviceId: String) async throws -> [BatchItemResult] { [] }
}

extension AudioPlayer {
    /// The addresses of the queued items (tests only).
    func items() -> [URL] { player.items().compactMap { ($0.asset as? AVURLAsset)?.url } }
}
