import XCTest
@preconcurrency import TendshelfShared
@testable import Tendshelf

@MainActor
final class FakeShake: ShakeSource {
    var starts = 0
    var stops = 0
    private var handler: ((Float) -> Void)?
    func start(_ handler: @escaping (Float) -> Void) {
        starts += 1
        self.handler = handler
    }
    func stop() {
        stops += 1
        handler = nil
    }
    func send(_ size: Float) { handler?(size) }
    var active: Bool { handler != nil }
}

@MainActor
final class SleepTimerTests: XCTestCase {
    private let source = FakeSource()
    private let shake = FakeShake()
    private var mono: Int64 = 1_000_000
    private var wall = Date(timeIntervalSince1970: 1_800_000_000)
    private var night = NightSettings.companion.OFF
    private var defaults: UserDefaults!
    private var publishes = 0
    private var calendar: Calendar = {
        var c = Calendar(identifier: .gregorian)
        c.timeZone = TimeZone(identifier: "UTC")!
        return c
    }()

    override func setUp() {
        let suite = "tendshelf.tests.sleep.\(UUID().uuidString)"
        defaults = UserDefaults(suiteName: suite)!
        addTeardownBlock { UserDefaults(suiteName: suite)!.removePersistentDomain(forName: suite) }
        source.playing = true
        source.chapters = [Chapter(idx: 0, startMs: 0, endMs: 300_000, title: "One"), Chapter(idx: 1, startMs: 300_000, endMs: 600_000, title: "Two")]
    }

    private func make() -> SleepTimer {
        SleepTimer(
            player: source, defaults: defaults, night: { [unowned self] in night }, mono: { [unowned self] in mono },
            wall: { [unowned self] in wall }, calendar: calendar, shake: shake, autoTick: false, publish: { [unowned self] in publishes += 1 })
    }

    private func advance(_ timer: SleepTimer, _ ms: Int64) {
        mono += ms
        timer.step()
    }

    private func at(hour: Int, minute: Int = 0) {
        wall = calendar.date(from: DateComponents(year: 2027, month: 1, day: 15, hour: hour, minute: minute))!
    }

    func testMinutesFadeThenFinishAndPause() async {
        let t = make()
        t.command("1", minutes: 1)
        XCTAssertTrue(t.isOn)
        advance(t, 30_000)
        XCTAssertEqual(source.volumes.last, 1, "no fade yet")
        advance(t, 25_000)
        XCTAssertEqual(source.volumes.last ?? 1, 0.5, accuracy: 0.01, "5 s left of a 10 s fade")
        XCTAssertTrue(t.published.fading)
        advance(t, 6_000)
        XCTAssertFalse(t.isOn)
        XCTAssertEqual(source.pauses, 1)
        XCTAssertEqual(source.volumes.last, 1, "the volume comes back for the next start")
        XCTAssertEqual(source.seeks, [], "minutes rest where they are")
    }

    func testNothingCountsWhilePaused() async {
        let t = make()
        t.command("1", minutes: 1)
        source.playing = false
        advance(t, 120_000)
        XCTAssertTrue(t.isOn)
        XCTAssertEqual(source.pauses, 0)
    }

    func testChapterTimerRestsAtTheChapterEnd() async {
        let t = make()
        source.positionMs = 290_000
        t.command("chapter", minutes: 0)
        XCTAssertEqual(t.published.mode, .chapter)
        source.positionMs = 300_000
        advance(t, 250)
        XCTAssertFalse(t.isOn)
        XCTAssertEqual(source.pauses, 1)
        XCTAssertEqual(source.seeks, [300_000])
    }

    func testTheLastChapterRestsOneSecondBeforeTheBooksEnd() async {
        let t = make()
        source.positionMs = 590_000
        t.command("chapter", minutes: 0)
        source.positionMs = 599_500
        advance(t, 250)
        XCTAssertEqual(source.seeks, [599_000], "so the queue is not asked")
    }

    func testEndOfEpisodeEndsOneSecondBeforeTheEnd() async {
        let t = make()
        source.chapters = []
        source.positionMs = 100_000
        t.command("episode", minutes: 0)
        source.positionMs = 599_100
        advance(t, 250)
        XCTAssertFalse(t.isOn)
        XCTAssertEqual(source.seeks, [599_000])
    }

    func testAStationCannotHaveAChapterTimer() async {
        let t = make()
        source.live = true
        t.command("chapter", minutes: 0)
        XCTAssertFalse(t.isOn)
    }

    func testEndedBookUnderATimerStopsTheQueue() async {
        let t = make()
        XCTAssertFalse(t.endedBook(), "no timer: the queue is asked")
        t.command("30", minutes: 30)
        XCTAssertTrue(t.endedBook(), "a running timer wins")
        XCTAssertFalse(t.isOn)
        XCTAssertFalse(t.endedBook(), "once")
    }

    func testPlusAddsTenMinutesAndEndsTheFade() async {
        let t = make()
        t.command("1", minutes: 1)
        advance(t, 55_000)
        XCTAssertTrue(shake.active, "the sensor is on during the fade")
        shake.send(20)
        mono += 300
        shake.send(20)
        XCTAssertFalse(t.published.fading)
        XCTAssertGreaterThan(t.published.leftMs, 600_000)
        XCTAssertFalse(shake.active, "and off again")
        XCTAssertEqual(source.volumes.last, 1)
    }

    func testTheSensorIsOnlyOnDuringTheFade() async {
        let t = make()
        t.command("2", minutes: 2)
        advance(t, 10_000)
        XCTAssertEqual(shake.starts, 0)
        advance(t, 105_000)
        XCTAssertEqual(shake.starts, 1)
        t.off()
        XCTAssertFalse(shake.active)
    }

    func testPublishesAtMostOncePerSecond() async {
        let t = make()
        t.command("30", minutes: 30)
        let base = publishes
        advance(t, 250)
        advance(t, 250)
        advance(t, 250)
        XCTAssertEqual(publishes, base, "inside a second")
        advance(t, 800)
        XCTAssertEqual(publishes, base + 1)
    }

    func testNightSleepStartsInsideTheWindowOnly() async {
        night = NightSettings(enabled: true, fromMinutes: 22 * 60, toMinutes: 6 * 60, minutes: 20)
        let t = make()
        at(hour: 14)
        t.playbackStarted()
        XCTAssertFalse(t.isOn)
        at(hour: 23)
        t.playbackStarted()
        XCTAssertTrue(t.isOn)
        XCTAssertEqual(t.published.leftMs, 20 * 60_000)
    }

    func testNightSleepDoesNotReplaceARunningTimer() async {
        night = NightSettings(enabled: true, fromMinutes: 22 * 60, toMinutes: 6 * 60, minutes: 20)
        let t = make()
        at(hour: 23)
        t.command("45", minutes: 45)
        t.playbackStarted()
        XCTAssertEqual(t.published.leftMs, 45 * 60_000)
    }

    func testSwitchingOffInsideTheWindowKeepsItOffUntilTheWindowEnds() async {
        night = NightSettings(enabled: true, fromMinutes: 22 * 60, toMinutes: 6 * 60, minutes: 20)
        let t = make()
        at(hour: 23)
        t.playbackStarted()
        t.command("off", minutes: 0)
        XCTAssertFalse(t.isOn)
        t.playbackStarted()
        XCTAssertFalse(t.isOn, "off until the window ends")
        wall = calendar.date(from: DateComponents(year: 2027, month: 1, day: 16, hour: 5, minute: 59))!
        t.playbackStarted()
        XCTAssertFalse(t.isOn)
        // The next evening it starts again.
        wall = calendar.date(from: DateComponents(year: 2027, month: 1, day: 16, hour: 23))!
        t.playbackStarted()
        XCTAssertTrue(t.isOn)
    }

    func testBookChangedKeepsMinutesAndFollowsChapters() async {
        let t = make()
        t.command("30", minutes: 30)
        t.bookChanged()
        XCTAssertTrue(t.isOn)
        t.off()
        source.positionMs = 10_000
        t.command("chapter", minutes: 0)
        source.chapters = [Chapter(idx: 0, startMs: 0, endMs: 50_000, title: "New")]
        source.positionMs = 0
        t.bookChanged()
        source.positionMs = 50_000
        advance(t, 250)
        XCTAssertFalse(t.isOn, "the timer ended at the new book's chapter")
    }
}
