import XCTest
@preconcurrency import TendshelfShared
@testable import Tendshelf

/// A player the writer, the resume rules and the sleep timer read from.
@MainActor
final class FakeSource: SaveSource, SleepPlayer {
    var bookId = "bk-1"
    var positionMs: Int64 = 60_000
    var rate = 1.0
    var ended = false
    var hasPlace = true
    var playing = false
    var live = false
    var duration: Int64 = 600_000
    var chapters: [Chapter] = []
    var volumes: [Float] = []
    var pauses = 0
    var seeks: [Int64] = []

    func setVolume(_ value: Float) { volumes.append(value) }
    func pause() {
        pauses += 1
        playing = false
    }
    func seek(_ bookMs: Int64) {
        seeks.append(bookMs)
        positionMs = bookMs
    }
}

/// A server with scripted answers; records what was sent.
@MainActor
final class FakePlayerApi: PlayerAPI {
    var batches: [[Save]] = []
    var batchError: Error?
    var applied = true
    var progressAnswer: RemoteProgress?
    var queueAnswer: String?
    var queueError: Error?
    var queueCalls: [String] = []
    var onQueue: (() -> Void)?
    var tokens: [String] = []
    /// Results given to the next batches, in order; when empty every row is `applied`.
    var scripted: [[BatchItemResult]] = []

    func batch(token: String, saves: [Save], deviceId: String) async throws -> [BatchItemResult] {
        tokens.append(token)
        batches.append(saves)
        if let batchError { throw batchError }
        if !scripted.isEmpty { return scripted.removeFirst() }
        return saves.map { BatchItemResult(bookId: $0.bookId, applied: applied, errorCode: nil, progress: nil) }
    }

    func progress(token: String, bookId: String) async throws -> RemoteProgress? { progressAnswer }

    func queueNext(token: String, ended: String) async throws -> String? {
        queueCalls.append(ended)
        onQueue?()
        if let queueError { throw queueError }
        return queueAnswer
    }

    func settings(token: String) async throws -> PlayerSettings { throw ApiError(status: 0) }
    func manifest(token: String, bookId: String) async throws -> BookManifest { throw ApiError(status: 0) }
    func cover(token: String, bookId: String) async -> Data? { nil }
    func episode(token: String, id: String) async throws -> EpisodeDoc { throw ApiError(status: 0) }
    func chapters(token: String, path: String, durationMs: Int64) async -> [Chapter] { [] }
    func podcastArt(token: String, artPath: String) async -> Data? { nil }
    func radioStation(token: String, id: String) async throws -> StationCard { throw ApiError(status: 0) }
    func radioArt(token: String, id: String, artPath: String) async -> Data? { nil }
    func radioNow(token: String, id: String) async -> String? { nil }
    func liveAddress(token: String, id: String, origin: Origin) async throws -> URL { throw ApiError(status: 0) }
    func putSpeed(token: String, bookId: String, speed: Double) async throws {}
    func manifestText(token: String, bookId: String) async throws -> String { throw ApiError(status: 0) }
    func episodeText(token: String, id: String) async throws -> String { throw ApiError(status: 0) }
    func chaptersText(token: String, path: String) async -> String? { nil }
}

@MainActor
final class FakeBackground: BackgroundWork {
    var begun = 0
    var ended = 0
    func begin(_ name: String) -> Int {
        begun += 1
        return begun
    }
    func end(_ id: Int) { ended += 1 }
}

/// What the writer's closures read: a class, so the rig can change it after init.
@MainActor
final class Env {
    var conn: Connection? = Connection(origin: Origin.companion.parse(input: "https://books.example")!, token: "tok-1", deviceId: "dev-1")
    var mono: Int64 = 1_000_000
    var wall: Int64 = 1_800_000_000_000
}

/// A writer over a temporary file and its own defaults suite, both removed at teardown.
@MainActor
final class WriterRig {
    let path: URL
    let defaults: UserDefaults
    let box = ProgressWriter.DeviceBox("")
    let store: SqliteOutbox
    let api = FakePlayerApi()
    let source = FakeSource()
    let background = FakeBackground()
    let env = Env()
    let writer: ProgressWriter

    var conn: Connection? {
        get { env.conn }
        set { env.conn = newValue }
    }

    var mono: Int64 {
        get { env.mono }
        set { env.mono = newValue }
    }

    init(_ test: XCTestCase) {
        let id = UUID().uuidString
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent("tendshelf-outbox-\(id)")
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        path = dir.appendingPathComponent("outbox.sqlite")
        let suite = "tendshelf.tests.\(id)"
        defaults = UserDefaults(suiteName: suite)!
        let box = self.box
        store = SqliteOutbox(path: path) { box.value }
        let env = self.env
        let api = self.api
        writer = ProgressWriter(
            store: store, deviceBox: box, defaults: defaults, connection: { env.conn }, makeApi: { _ in api },
            background: background, now: { env.wall }, mono: { env.mono })
        writer.settleMs = 20
        writer.attach(source: source)
        let store = self.store
        test.addTeardownBlock { @MainActor in
            // Close before the directory goes: SQLite objects to a file that is unlinked while open.
            store.close()
            UserDefaults(suiteName: suite)!.removePersistentDomain(forName: suite)
            try? FileManager.default.removeItem(at: dir)
        }
    }
}

/// A flush's retry timers and background loops are `Task`s: wait for them.
@MainActor
func settle(_ ms: UInt64 = 50) async {
    try? await Task.sleep(nanoseconds: ms * 1_000_000)
}
