import XCTest
@preconcurrency import TendshelfShared
@testable import Tendshelf

/// The shared `Outbox` driven over the real SQLite store on a temporary file.
@MainActor
final class SqliteOutboxTests: XCTestCase {
    private var path: URL!
    private var device = ProgressWriter.DeviceBox("dev-1")
    private var clock: Int64 = 1_000
    private var stores: [SqliteOutbox] = []

    override func setUp() {
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent("tendshelf-sqlite-\(UUID().uuidString)")
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        path = dir.appendingPathComponent("outbox.sqlite")
        device = ProgressWriter.DeviceBox("dev-1")
        clock = 1_000
        stores = []
    }

    override func tearDown() {
        stores.forEach { $0.close() }
        try? FileManager.default.removeItem(at: path.deletingLastPathComponent())
    }

    private func open() -> (SqliteOutbox, Outbox) {
        let box = device
        let store = SqliteOutbox(path: path) { box.value }
        stores.append(store)
        let outbox = Outbox(store: store, now: { [unowned self] in
            clock += 10
            return KotlinLong(value: clock)
        })
        return (store, outbox)
    }

    func testPlainSavesMergeIntoTheLastRow() {
        let (store, outbox) = open()
        _ = outbox.tick(bookId: "b1", positionMs: 1_000, speed: 1, listenedMs: 500, hold: false)
        _ = outbox.tick(bookId: "b1", positionMs: 5_000, speed: 1, listenedMs: 700, hold: false)
        XCTAssertEqual(store.count(), 1)
        let row = outbox.newest(bookId: "b1")
        XCTAssertEqual(row?.positionMs, 5_000)
        XCTAssertEqual(row?.listenedMs, 1_200)
        XCTAssertFalse(store.lastWriteFailed)
    }

    func testFinishNeverMerges() {
        let (store, outbox) = open()
        _ = outbox.tick(bookId: "b1", positionMs: 1_000, speed: 1, listenedMs: 0, hold: false)
        _ = outbox.finish(bookId: "b1", positionMs: 9_000, speed: 1, listenedMs: 0)
        _ = outbox.tick(bookId: "b1", positionMs: 9_100, speed: 1, listenedMs: 0, hold: false)
        XCTAssertEqual(store.count(), 3)
        XCTAssertEqual(outbox.nextBatch(n: 100).map(\.finished), [false, true, false])
    }

    func testHeldRowsAreNeverInTheBatch() {
        let (_, outbox) = open()
        _ = outbox.tick(bookId: "b1", positionMs: 1_000, speed: 1, listenedMs: 0, hold: true)
        XCTAssertEqual(outbox.nextBatch(n: 100).count, 0)
        _ = outbox.release(bookId: "b1", force: false)
        XCTAssertEqual(outbox.nextBatch(n: 100).count, 1)
    }

    func testReleaseForceMakesTheRowWin() {
        let (_, outbox) = open()
        _ = outbox.tick(bookId: "b1", positionMs: 1_000, speed: 1, listenedMs: 0, hold: true)
        let before = outbox.newest(bookId: "b1")!
        let after = outbox.release(bookId: "b1", force: true)!
        XCTAssertTrue(after.force)
        XCTAssertFalse(after.held)
        XCTAssertGreaterThan(after.clientUpdatedAt, before.clientUpdatedAt)
        XCTAssertTrue(outbox.newest(bookId: "b1")!.force, "the change is on disk")
    }

    func testDropHeldDeletesTheHeldRow() {
        let (store, outbox) = open()
        _ = outbox.tick(bookId: "b1", positionMs: 1_000, speed: 1, listenedMs: 0, hold: true)
        outbox.dropHeld(bookId: "b1")
        XCTAssertEqual(store.count(), 0)
    }

    func testTrimKeepsFinishedAndForcedRows() {
        let (store, _) = open()
        func save(_ book: String, finished: Bool = false, force: Bool = false) {
            _ = store.add(s: Save(seq: 0, bookId: book, positionMs: 1, clientUpdatedAt: 1, finished: finished, force: force, speed: 1, listenedMs: 0, held: false))
        }
        save("a")
        save("b", finished: true)
        save("c", force: true)
        save("d")
        save("e")
        store.trim(max: 3)
        XCTAssertEqual(store.count(), 3)
        let books = store.firstBatch(n: 10).map(\.bookId)
        XCTAssertTrue(books.contains("b") && books.contains("c"), "finished and forced rows stay: \(books)")
        XCTAssertFalse(books.contains("a"), "the oldest plain row goes")
    }

    func testRowsOfAnotherDeviceAreInvisibleButCountedAndCleared() {
        let (store, outbox) = open()
        _ = outbox.tick(bookId: "b1", positionMs: 1_000, speed: 1, listenedMs: 0, hold: false)
        device.value = "dev-2"
        XCTAssertEqual(outbox.nextBatch(n: 100).count, 0)
        XCTAssertNil(outbox.newest(bookId: "b1"))
        XCTAssertEqual(store.count(), 1, "count sees every device")
        outbox.clear()
        XCTAssertEqual(store.count(), 0, "clear empties every device")
    }

    func testSurvivesReopening() {
        var (store, outbox) = open()
        _ = outbox.tick(bookId: "b1", positionMs: 4_000, speed: 1.25, listenedMs: 900, hold: false)
        store.close()
        (store, outbox) = open()
        let row = outbox.nextBatch(n: 100).first
        XCTAssertEqual(row?.bookId, "b1")
        XCTAssertEqual(row?.positionMs, 4_000)
        XCTAssertEqual(row?.speed, 1.25)
        XCTAssertEqual(row?.listenedMs, 900)
    }

    func testSettledDeletesAppliedRows() {
        let (store, outbox) = open()
        _ = outbox.tick(bookId: "b1", positionMs: 4_000, speed: 1, listenedMs: 0, hold: false)
        let batch = outbox.nextBatch(n: 100)
        let applied = outbox.settled(sent: batch, results: [BatchItemResult(bookId: "b1", applied: true, errorCode: nil, progress: nil)])
        XCTAssertEqual(applied.deleteSeqs.count, 1)
        XCTAssertEqual(store.count(), 0)
    }

    func testAnUnopenableFileFailsSoftly() {
        let store = SqliteOutbox(path: URL(fileURLWithPath: "/nonexistent-dir/x/outbox.sqlite")) { "dev-1" }
        XCTAssertTrue(store.lastWriteFailed)
        XCTAssertEqual(store.firstBatch(n: 10).count, 0)
        _ = store.add(s: Save(seq: 0, bookId: "b", positionMs: 1, clientUpdatedAt: 1, finished: false, force: false, speed: 1, listenedMs: 0, held: false))
        XCTAssertEqual(store.count(), 0)
    }
}
