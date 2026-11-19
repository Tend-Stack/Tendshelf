import XCTest
@preconcurrency import TendshelfShared
@testable import Tendshelf

@MainActor
final class ProgressWriterTests: XCTestCase {
    func testThePlaceIsOnDiskBeforeAnythingIsSent() async {
        let r = WriterRig(self)
        r.writer.saveNow("tick")
        XCTAssertEqual(r.store.count(), 1, "written first")
        XCTAssertEqual(r.api.batches.count, 0, "nothing has gone out in the same turn")
        await eventually("sent") { r.api.batches.count == 1 }
        await eventually("settled") { r.store.count() == 0 }
        XCTAssertEqual(r.api.tokens, ["tok-1"])
        XCTAssertEqual(r.api.batches.first?.first?.positionMs, 60_000)
        XCTAssertEqual(r.background.begun, r.background.ended, "every background task is ended")
    }

    func testA401KeepsTheRowsAndFailsSignedOut() async {
        let r = WriterRig(self)
        r.api.batchError = ApiError(status: 401)
        var signedOut = 0
        r.writer.onSignedOut = { signedOut += 1 }
        r.writer.saveNow("tick")
        await eventually("401") { signedOut == 1 }
        XCTAssertEqual(r.store.count(), 1)
    }

    func testA429WaitsForRetryAfter() async {
        let r = WriterRig(self)
        r.api.batchError = ApiError(status: 429, retryAfterS: 1)
        r.writer.saveNow("tick")
        await eventually("first try") { r.api.batches.count == 1 }
        r.api.batchError = nil
        await settle(300)
        XCTAssertEqual(r.api.batches.count, 1, "no second try inside the wait")
        await eventually("retry") { r.api.batches.count == 2 }
        await eventually("settled") { r.store.count() == 0 }
    }

    func testOtherFailuresKeepRowsAndBackOff() async {
        let r = WriterRig(self)
        r.api.batchError = ApiError(status: 0)
        r.writer.saveNow("tick")
        await eventually("try") { r.api.batches.count == 1 }
        await settle(100)
        XCTAssertEqual(r.api.batches.count, 1, "the backoff is 5 s")
        XCTAssertEqual(r.store.count(), 1)
    }

    func testAStationSavesNothing() async {
        let r = WriterRig(self)
        r.source.bookId = "rs_abc"
        r.writer.saveNow("tick")
        r.writer.playingChanged(false)
        XCTAssertEqual(r.store.count(), 0)
        await settle()
        XCTAssertEqual(r.api.batches.count, 0)
    }

    func testNothingIsSavedWithoutAPlaceOrAfterTheEnd() async {
        let r = WriterRig(self)
        r.source.hasPlace = false
        r.writer.saveNow("tick")
        r.source.hasPlace = true
        r.source.ended = true
        r.writer.saveNow("tick")
        r.conn = nil
        r.source.ended = false
        r.writer.saveNow("tick")
        XCTAssertEqual(r.store.count(), 0)
    }

    func testAnotherDeviceIdClearsTheOldRows() async {
        let r = WriterRig(self)
        r.api.batchError = ApiError(status: 0)
        r.writer.saveNow("tick")
        XCTAssertEqual(r.store.count(), 1)
        XCTAssertEqual(r.defaults.string(forKey: "outboxDevice"), "dev-1")
        r.conn = Connection(origin: r.conn!.origin, token: "tok-2", deviceId: "dev-2")
        r.source.positionMs = 90_000
        r.writer.saveNow("tick")
        XCTAssertEqual(r.store.count(), 1, "the old device's row is gone, the new one is in")
        XCTAssertEqual(r.defaults.string(forKey: "outboxDevice"), "dev-2")
        XCTAssertEqual(r.writer.pending("bk-1")?.positionMs, 90_000)
    }

    func testListenedTimeIsDrainedIntoTheSave() async {
        let r = WriterRig(self)
        r.source.playing = true
        r.writer.playingChanged(true)
        r.mono += 5_000
        r.source.playing = false
        r.writer.playingChanged(false)
        XCTAssertEqual(r.writer.pending("bk-1")?.listenedMs, 5_000)
    }

    func testTheCadenceSavesEveryTenSecondsOnceTheBookMoved() async {
        let r = WriterRig(self)
        r.source.playing = true
        r.writer.playingChanged(true)
        r.mono += 4_000
        r.writer.onTick()
        XCTAssertEqual(r.store.count(), 0, "not due")
        r.mono += 7_000
        r.writer.onTick()
        XCTAssertEqual(r.store.count(), 1)
    }

    func testASeekWhilePausedSavesOnceItSettles() async {
        let r = WriterRig(self)
        r.writer.sought()
        r.source.positionMs = 70_000
        r.writer.sought()
        XCTAssertEqual(r.store.count(), 0)
        await eventually("settled save") { r.store.count() + r.api.batches.count >= 1 }
        XCTAssertEqual(r.api.batches.count + r.store.count(), 1, "a run of seeks saves once")
    }

    func testFinishAndFlushSendsTheFinishedRow() async {
        let r = WriterRig(self)
        let sent = await r.writer.finishAndFlush(bookId: "bk-1", positionMs: 600_000, speed: 1, listened: 1_000)
        XCTAssertTrue(sent)
        XCTAssertEqual(r.api.batches.last?.last?.finished, true)
        XCTAssertEqual(r.store.count(), 0)
    }

    func testAnUnsentFinishIsNotReportedAsSent() async {
        let r = WriterRig(self)
        r.api.batchError = ApiError(status: 0)
        let sent = await r.writer.finishAndFlush(bookId: "bk-1", positionMs: 600_000, speed: 1, listened: 0)
        XCTAssertFalse(sent)
        XCTAssertEqual(r.store.count(), 1, "the row stays for the next flush")
    }

    func testALostRowIsReportedWithTheConnection() async {
        let r = WriterRig(self)
        let remote = RemoteProgress(positionMs: 900_000, finished: false, clientUpdatedAt: 5, deviceId: "dev-9", deviceLabel: "Kitchen")
        r.api.scripted = [[BatchItemResult(bookId: "bk-1", applied: false, errorCode: nil, progress: remote)]]
        var lost: [String] = []
        r.writer.onLost = { l, c in lost.append("\(l.remote.deviceLabel)|\(c.deviceId)") }
        r.writer.saveNow("tick")
        await eventually("lost") { !lost.isEmpty }
        XCTAssertEqual(lost, ["Kitchen|dev-1"])
    }

    func testHeldRowsAreNotSent() async {
        let r = WriterRig(self)
        r.writer.heldBook = "bk-1"
        r.writer.saveNow("tick")
        await settle(100)
        XCTAssertEqual(r.api.batches.count, 0)
        r.writer.release(bookId: "bk-1", force: true)
        await eventually("released") { r.api.batches.count == 1 }
        XCTAssertEqual(r.api.batches.first?.first?.force, true)
    }
}
