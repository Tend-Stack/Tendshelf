import XCTest
@preconcurrency import TendshelfShared
@testable import Tendshelf

final class BookTimelineTests: XCTestCase {
    private func file(_ i: Int, offset: Int64, dur: Int64, playable: Bool = true) -> AudioFile {
        AudioFile(id: "f\(i)", idx: Int32(i), offsetMs: offset, durationMs: dur, mime: "audio/mpeg", needsCompat: false, playable: playable, size: 0)
    }

    private var three: [AudioFile] {
        [file(0, offset: 0, dur: 1000), file(1, offset: 1000, dur: 2000), file(2, offset: 3000, dur: 500)]
    }

    func testOffsetsMapToFilesAndBack() {
        let t = BookTimeline(files: three)
        XCTAssertEqual(t.target(bookMs: 0)?.fileIndex, 0)
        XCTAssertEqual(t.target(bookMs: 999)?.inFileMs, 999)
        XCTAssertEqual(t.target(bookMs: 1000)?.fileIndex, 1)
        XCTAssertEqual(t.target(bookMs: 1000)?.inFileMs, 0)
        XCTAssertEqual(t.target(bookMs: 3200)?.fileIndex, 2)
        XCTAssertEqual(t.target(bookMs: 3200)?.inFileMs, 200)
        XCTAssertEqual(t.bookMs(fileIndex: 1, itemMs: 250), 1250)
        XCTAssertEqual(t.next(after: 0), 1)
        XCTAssertNil(t.next(after: 2))
    }

    func testMissingFirstFileStartsAtTheSecond() {
        let f = [file(0, offset: 0, dur: 1000, playable: false), file(1, offset: 1000, dur: 2000)]
        let t = BookTimeline(files: f)
        XCTAssertEqual(t.playable, [1])
        XCTAssertEqual(t.target(bookMs: 0)?.fileIndex, 1)
        XCTAssertEqual(t.target(bookMs: 0)?.inFileMs, 0)
        XCTAssertEqual(t.target(bookMs: 500)?.fileIndex, 1)
    }

    func testMissingMiddleFileIsSteppedOver() {
        var f = three
        f[1] = file(1, offset: 1000, dur: 2000, playable: false)
        let t = BookTimeline(files: f)
        XCTAssertEqual(t.playable, [0, 2])
        XCTAssertEqual(t.target(bookMs: 1500)?.fileIndex, 2)
        XCTAssertEqual(t.target(bookMs: 1500)?.inFileMs, 0)
        XCTAssertEqual(t.next(after: 0), 2)
    }

    func testMissingLastFileEndsTheBook() {
        var f = three
        f[2] = file(2, offset: 3000, dur: 500, playable: false)
        let t = BookTimeline(files: f)
        XCTAssertNil(t.target(bookMs: 3100))
        XCTAssertNil(t.target(bookMs: 3000))
        XCTAssertNotNil(t.target(bookMs: 2999))
    }

    func testPastTheEndIsNil() {
        let t = BookTimeline(files: three)
        XCTAssertNil(t.target(bookMs: 3500))
        XCTAssertNil(t.target(bookMs: 99_999))
        XCTAssertEqual(t.target(bookMs: -5)?.fileIndex, 0)
    }

    func testNoPlayableFileIsNil() {
        let t = BookTimeline(files: [file(0, offset: 0, dur: 100, playable: false)])
        XCTAssertNil(t.target(bookMs: 0))
        XCTAssertTrue(t.playable.isEmpty)
    }
}
