import XCTest
@preconcurrency import TendshelfShared
@testable import Tendshelf

@MainActor
final class SqliteOfflineStoreTests: XCTestCase {
    private var path: URL!

    override func setUp() async throws {
        path = FileManager.default.temporaryDirectory.appendingPathComponent("offline-\(UUID().uuidString).sqlite")
    }

    override func tearDown() async throws {
        for suffix in ["", "-wal", "-shm"] { try? FileManager.default.removeItem(atPath: path.path + suffix) }
    }

    private func book(_ id: String, user: String = "usr-1", state: OfflineState = .queued, kind: String = OfflineKt.KIND_BOOK) -> OfflineBook {
        OfflineBook(
            bookId: id, userId: user, title: "T \(id)", author: "A", durationMs: 9000, sizeBytes: 300, state: state, placeMs: 0,
            placeAt: 0, kind: kind, showId: "", publishedAt: 0, played: false)
    }

    private func file(_ id: String, _ key: String) -> OfflineFile {
        OfflineFile(bookId: id, cacheKey: key, url: "https://x/api/v1/stream/\(key)?download=1", planned: 100, mime: "audio/mpeg", auto: false)
    }

    func testPutGetAllOrderedByWhenAdded() {
        let s = SqliteOfflineStore(path: path)
        s.put(book("b_2"), "{\"m\":2}", at: 20)
        s.put(book("b_1"), "{\"m\":1}", at: 10)
        XCTAssertEqual(s.all().map(\.bookId), ["b_1", "b_2"])
        XCTAssertEqual(s.get(id: "b_2")?.title, "T b_2")
        XCTAssertEqual(s.manifest(id: "b_1"), "{\"m\":1}")
        XCTAssertNil(s.get(id: "nope"))
    }

    func testReplaceDropsTheOldFileRows() {
        let s = SqliteOfflineStore(path: path)
        s.put(book("b_1"), "{}", at: 1)
        s.putFiles("b_1", [file("b_1", "f_1"), file("b_1", "f_2")])
        XCTAssertEqual(s.files("b_1").count, 2)
        s.put(book("b_1"), "{}", at: 2)
        XCTAssertTrue(s.files("b_1").isEmpty)
    }

    func testRemoveCascadesToFiles() {
        let s = SqliteOfflineStore(path: path)
        s.put(book("b_1"), "{}", at: 1)
        s.putFiles("b_1", [file("b_1", "f_1")])
        s.remove(id: "b_1")
        XCTAssertTrue(s.allFiles().isEmpty)
    }

    func testPlaceStateSizePlayedAndFileSteps() {
        let s = SqliteOfflineStore(path: path)
        s.put(book("b_1"), "{}", at: 1)
        s.notePlace(id: "b_1", ms: 5000, at: 77)
        s.setState(id: "b_1", s: .ready)
        s.setSize(id: "b_1", bytes: 999)
        s.setPlayed(id: "b_1", played: true)
        let row = s.get(id: "b_1")
        XCTAssertEqual(row?.placeMs, 5000)
        XCTAssertEqual(row?.placeAt, 77)
        XCTAssertEqual(row?.state.name, "READY")
        XCTAssertEqual(row?.sizeBytes, 999)
        XCTAssertEqual(row?.played, true)

        s.putFiles("b_1", [file("b_1", "f_1")])
        s.setStep("b_1", "f_1", .running, failures: 2)
        s.setBytes("b_1", "f_1", bytes: 40)
        var f = s.files("b_1")[0]
        XCTAssertEqual(f.step.name, "RUNNING")
        XCTAssertEqual(f.failures, 2)
        XCTAssertEqual(f.bytes, 40)
        s.setArrived("b_1", "f_1", length: 100, bytes: 100, name: "f_1.mp3")
        f = s.files("b_1")[0]
        XCTAssertEqual(f.name, "f_1.mp3")
        XCTAssertEqual(f.length, 100)
    }

    func testTextAndCheckedExtras() {
        let s = SqliteOfflineStore(path: path)
        s.put(book("b_1"), "{}", at: 1)
        XCTAssertEqual(s.text("b_1")?.state, "none")
        s.setText("b_1", state: "ready", revision: "r1", edition: "e1")
        XCTAssertEqual(s.text("b_1")?.edition, "e1")
        XCTAssertEqual(s.withText("ready"), ["b_1"])
        s.touchChecked("b_1", at: 5)
        XCTAssertEqual(s.checkedAt("b_1"), 5)
        s.setManifest("b_1", "{\"new\":1}", at: 9)
        XCTAssertEqual(s.manifest(id: "b_1"), "{\"new\":1}")
        XCTAssertEqual(s.checkedAt("b_1"), 9)
    }

    func testClearEmptiesEveryPerson() {
        let s = SqliteOfflineStore(path: path)
        s.put(book("b_1", user: "usr-1"), "{}", at: 1)
        s.put(book("b_2", user: "usr-2"), "{}", at: 2)
        s.clear()
        XCTAssertTrue(s.all().isEmpty)
        XCTAssertTrue(s.allFiles().isEmpty)
    }

    func testReopenKeepsRowsAndClosedStoreIsEmptyAndSafe() {
        var s: SqliteOfflineStore? = SqliteOfflineStore(path: path)
        s?.put(book("b_1"), "{}", at: 1)
        s?.putFiles("b_1", [file("b_1", "f_1")])
        s = nil
        let again = SqliteOfflineStore(path: path)
        XCTAssertEqual(again.all().map(\.bookId), ["b_1"])
        XCTAssertEqual(again.files("b_1").count, 1)
        again.close()
        XCTAssertTrue(again.all().isEmpty)
        again.setState(id: "b_1", s: .ready)
        XCTAssertTrue(again.lastWriteFailed)
        again.reopen()
        XCTAssertEqual(again.get(id: "b_1")?.state.name, "QUEUED")
    }
}
