import XCTest
@preconcurrency import TendshelfShared
@testable import Tendshelf

@MainActor
final class NextBookTests: XCTestCase {
    private var started: [String] = []

    private func make(_ r: WriterRig) -> NextBook {
        let env = r.env
        let api = r.api
        let next = NextBook(
            writer: r.writer, connection: { env.conn }, makeApi: { _ in api }, background: r.background,
            start: { [unowned self] id in started.append(id) })
        next.lingerSeconds = 0
        return next
    }

    func testFinishedIsSentThenTheQueueIsAskedAndTheNextBookStarts() async {
        let r = WriterRig(self)
        r.api.queueAnswer = "bk-2"
        let next = make(r)
        let job = next.ended(bookId: "bk-1", positionMs: 600_000, speed: 1, askQueue: true) { true }
        await job?.value
        XCTAssertEqual(r.api.batches.last?.last?.finished, true)
        XCTAssertEqual(r.api.queueCalls, ["bk-1"])
        XCTAssertEqual(started, ["bk-2"])
        XCTAssertEqual(r.background.begun, r.background.ended, "the background task is ended")
    }

    func testAnUnsentFinishNeverAsks() async {
        let r = WriterRig(self)
        r.api.batchError = ApiError(status: 0)
        r.api.queueAnswer = "bk-2"
        let next = make(r)
        await next.ended(bookId: "bk-1", positionMs: 600_000, speed: 1, askQueue: true) { true }?.value
        XCTAssertEqual(r.api.queueCalls, [])
        XCTAssertEqual(started, [])
        XCTAssertEqual(r.store.count(), 1, "the finished row waits for the next flush")
    }

    func testOneAskPerEnd() async {
        let r = WriterRig(self)
        r.api.queueAnswer = nil
        let next = make(r)
        let first = next.ended(bookId: "bk-1", positionMs: 600_000, speed: 1, askQueue: true) { true }
        let second = next.ended(bookId: "bk-1", positionMs: 600_000, speed: 1, askQueue: true) { true }
        XCTAssertNotNil(first)
        XCTAssertNil(second)
        await first?.value
        XCTAssertEqual(r.api.queueCalls.count, 1)
        next.reset()
        await next.ended(bookId: "bk-1", positionMs: 600_000, speed: 1, askQueue: true) { true }?.value
        XCTAssertEqual(r.api.queueCalls.count, 2, "a replay ends again")
    }

    func testTheAnswerStartsOnlyWhileStillEnded() async {
        let r = WriterRig(self)
        r.api.queueAnswer = "bk-2"
        let next = make(r)
        await next.ended(bookId: "bk-1", positionMs: 600_000, speed: 1, askQueue: true) { false }?.value
        XCTAssertEqual(r.api.queueCalls, ["bk-1"], "asked")
        XCTAssertEqual(started, [], "but the person already pressed play on something else")
    }

    func testASleepTimerEndsTheBookWithoutAskingOrStarting() async {
        let r = WriterRig(self)
        r.api.queueAnswer = "bk-2"
        let next = make(r)
        await next.ended(bookId: "bk-1", positionMs: 600_000, speed: 1, askQueue: false) { true }?.value
        XCTAssertEqual(r.api.batches.last?.last?.finished, true, "finished is still sent")
        XCTAssertEqual(r.api.queueCalls, [])
        XCTAssertEqual(started, [])
    }

    func testAFailedAskIsNotRepeatedAndStartsNothing() async {
        let r = WriterRig(self)
        r.api.queueError = ApiError(status: 0)
        let next = make(r)
        await next.ended(bookId: "bk-1", positionMs: 600_000, speed: 1, askQueue: true) { true }?.value
        XCTAssertEqual(r.api.queueCalls.count, 1)
        XCTAssertEqual(started, [])
    }

    func testSignedOutWhileWaitingStartsNothing() async {
        let r = WriterRig(self)
        r.api.queueAnswer = "bk-2"
        let next = make(r)
        let env = r.env
        r.api.onQueue = { env.conn = nil }
        let job = next.ended(bookId: "bk-1", positionMs: 600_000, speed: 1, askQueue: true) { true }
        await job?.value
        XCTAssertEqual(started, [])
    }
}
