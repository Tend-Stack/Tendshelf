import XCTest
@preconcurrency import TendshelfShared
@testable import Tendshelf

@MainActor
final class ResumeTests: XCTestCase {
    private var now: Int64 = 1_000_000
    private var book = "bk-1"
    private var position: Int64 = 100_000
    private var settings: PlayerSettings? = PlayerSettings(
        speed: 1, skipBackS: 30, skipForwardS: 30, autoRewind: true, nextInSeries: false,
        rewindSteps: SmartRewindKt.DEFAULT_REWIND, skipQuiet: false, autoDownload: false, night: NightSettings.companion.OFF)
    private var seeks: [Int64] = []
    private var rewound: [Int] = []
    private var checked: [String] = []

    private func make() -> Resume {
        let r = Resume(now: { [unowned self] in now }, settings: { [unowned self] in settings }, book: { [unowned self] in book }, position: { [unowned self] in position })
        r.seekTo = { [unowned self] ms in seeks.append(ms) }
        r.onRewound = { [unowned self] s in rewound.append(s) }
        r.checkRemote = { [unowned self] b in checked.append(b) }
        return r
    }

    /// A load, its first sound (the start), then a pause of [away] ms and the next sound.
    private func pause(_ r: Resume, away: Int64) {
        r.bookLoaded(writtenAt: nil, atGiven: true)
        r.sound(.playing)
        r.sound(.paused)
        now += away
        r.sound(.playing)
    }

    func testAShortPauseDoesNothing() async {
        let r = make()
        pause(r, away: 5_000)
        XCTAssertEqual(seeks, [])
        XCTAssertEqual(checked, [])
    }

    func testAPauseOfTwoMinutesStepsBackAndChecksTheServer() async {
        let r = make()
        pause(r, away: 120_000)
        XCTAssertEqual(seeks, [95_000], "5 s for a minute or more")
        XCTAssertEqual(rewound, [5])
        XCTAssertEqual(checked, ["bk-1"])
    }

    func testTheFirstSoundAfterALoadIsTheStartAndAsksNothing() async {
        let r = make()
        r.bookLoaded(writtenAt: now - 7_200_000, atGiven: false) // saved two hours ago
        r.sound(.playing)
        XCTAssertEqual(seeks, [90_000], "10 s for an hour or more")
        XCTAssertEqual(checked, [], "the start is not a resume")
    }

    func testBufferingIsNotAPause() async {
        let r = make()
        r.bookLoaded(writtenAt: nil, atGiven: true)
        r.sound(.playing)
        r.sound(.waiting)
        now += 300_000
        r.sound(.playing)
        XCTAssertEqual(seeks, [])
    }

    func testAutoRewindOffStillChecks() async {
        settings = PlayerSettings(
            speed: 1, skipBackS: 30, skipForwardS: 30, autoRewind: false, nextInSeries: false,
            rewindSteps: SmartRewindKt.DEFAULT_REWIND, skipQuiet: false, autoDownload: false, night: NightSettings.companion.OFF)
        let r = make()
        pause(r, away: 120_000)
        XCTAssertEqual(seeks, [])
        XCTAssertEqual(checked, ["bk-1"])
    }

    func testAStationNeverRewindsOrAsks() async {
        book = ""
        let r = make()
        pause(r, away: 600_000)
        XCTAssertEqual(seeks, [])
        XCTAssertEqual(rewound, [])
        XCTAssertEqual(checked, [])
    }

    func testRewindNeverGoesBelowZero() async {
        position = 1_000
        let r = make()
        pause(r, away: 120_000)
        XCTAssertEqual(seeks, [0])
    }
}

@MainActor
final class AskStateTests: XCTestCase {
    private func make(_ test: XCTestCase) -> (WriterRig, AskState, () -> [Int64], () -> Int) {
        let r = WriterRig(test)
        var seeks: [Int64] = []
        var changes = 0
        let ask = AskState(writer: r.writer, seekTo: { seeks.append($0) }, changed: { changes += 1 })
        return (r, ask, { seeks }, { changes })
    }

    func testShowHoldsTheBooksSaves() async {
        let (r, ask, _, changes) = make(self)
        ask.show(Ask(bookId: "bk-1", positionMs: 900_000, deviceLabel: "Kitchen"))
        XCTAssertTrue(ask.isOpen)
        XCTAssertEqual(changes(), 1)
        r.writer.saveNow("tick")
        await settle(80)
        XCTAssertEqual(r.api.batches.count, 0, "held")
        XCTAssertEqual(r.store.count(), 1)
    }

    func testStayHereForcesTheHeldPlace() async {
        let (r, ask, seeks, _) = make(self)
        ask.show(Ask(bookId: "bk-1", positionMs: 900_000, deviceLabel: "Kitchen"))
        r.writer.saveNow("tick")
        ask.answer(keepHere: true)
        XCTAssertFalse(ask.isOpen)
        XCTAssertEqual(seeks(), [])
        await eventually("sent") { r.api.batches.count == 1 }
        XCTAssertEqual(r.api.batches.first?.first?.force, true)
    }

    func testContinueThereDropsTheHeldPlaceAndSeeks() async {
        let (r, ask, seeks, _) = make(self)
        ask.show(Ask(bookId: "bk-1", positionMs: 900_000, deviceLabel: "Kitchen"))
        r.writer.saveNow("tick")
        ask.answer(keepHere: false)
        XCTAssertEqual(seeks(), [900_000])
        XCTAssertEqual(r.store.count(), 0)
    }

    func testDismissPlainSendsTheHeldPlaceWithoutForce() async {
        let (r, ask, _, _) = make(self)
        ask.show(Ask(bookId: "bk-1", positionMs: 900_000, deviceLabel: "Kitchen"))
        r.writer.saveNow("tick")
        ask.dismissPlain()
        await eventually("sent") { r.api.batches.count == 1 }
        XCTAssertEqual(r.api.batches.first?.first?.force, false)
    }

    func testOnlyOneQuestionAtATime() async {
        let (_, ask, _, changes) = make(self)
        ask.show(Ask(bookId: "bk-1", positionMs: 1, deviceLabel: "A"))
        ask.show(Ask(bookId: "bk-1", positionMs: 2, deviceLabel: "B"))
        XCTAssertEqual(ask.open?.deviceLabel, "A")
        XCTAssertEqual(changes(), 1)
    }

    func testRestartedWriterReleasesHeldRows() async {
        let (r, ask, _, _) = make(self)
        ask.show(Ask(bookId: "bk-1", positionMs: 1, deviceLabel: "A"))
        r.writer.saveNow("tick")
        XCTAssertEqual(r.store.held().count, 1)
        r.writer.outbox.releaseAllHeld()
        XCTAssertEqual(r.store.held().count, 0)
    }
}
