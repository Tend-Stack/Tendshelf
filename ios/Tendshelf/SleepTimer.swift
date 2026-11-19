import CoreMotion
import Foundation
import OSLog
@preconcurrency import TendshelfShared

/// What the sleep timer reads from and tells the player.
@MainActor
protocol SleepPlayer: AnyObject {
    var positionMs: Int64 { get }
    var duration: Int64 { get }
    var rate: Double { get }
    var playing: Bool { get }
    var live: Bool { get }
    var chapters: [Chapter] { get }
    func setVolume(_ value: Float)
    func pause()
    func seek(_ bookMs: Int64)
}

extension AudioPlayer: SleepPlayer {}

/// Where a shake comes from: device motion on the phone, a fake in the tests. Sizes are m/s^2 with gravity removed.
@MainActor
protocol ShakeSource: AnyObject {
    func start(_ handler: @escaping (Float) -> Void)
    func stop()
}

/// `CMMotionManager` device motion: `userAcceleration` is in g and already without gravity.
@MainActor
final class MotionShake: ShakeSource {
    private let manager = CMMotionManager()

    func start(_ handler: @escaping (Float) -> Void) {
        guard manager.isDeviceMotionAvailable, !manager.isDeviceMotionActive else { return }
        manager.deviceMotionUpdateInterval = 1.0 / 30
        manager.startDeviceMotionUpdates(to: .main) { motion, _ in
            guard let a = motion?.userAcceleration else { return }
            let size = Float((a.x * a.x + a.y * a.y + a.z * a.z).squareRoot() * 9.81)
            MainActor.assumeIsolated { handler(size) }
        }
    }

    func stop() {
        if manager.isDeviceMotionActive { manager.stopDeviceMotionUpdates() }
    }
}

/// The sleep timer (port of `SleepTimer.kt`), here so a frozen page or a dark screen changes nothing. The rules are the shared
/// `SleepClock`'s, the same as the web's. A 250 ms tick runs only while a timer is on (the audio session keeps the app alive
/// while sound plays, and when nothing plays the clock does not count). Device motion is read only during the fade, and a
/// shake is "+10 min".
@MainActor
final class SleepTimer {
    private static let log = Logger(subsystem: "app.tendshelf.ios", category: "sleep")
    private static let stepMs: Double = 0.25
    static let nightOffKey = "nightOffUntilMs"

    private let player: SleepPlayer
    private let defaults: UserDefaults
    private let night: () -> NightSettings
    private let mono: () -> Int64
    private let wall: () -> Date
    private let calendar: Calendar
    private let shakeSource: ShakeSource?
    private let publish: () -> Void
    private let autoTick: Bool

    private let clock = SleepClock()
    private let shake = ShakeDetector(peak: 12, gapMs: 150, windowMs: 1000)
    private var lastAt: Int64 = 0
    private var shown = SleepInfo.companion.OFF
    private var shownAt: Int64 = 0
    private var listening = false
    private var timer: Timer?
    /// The "end of episode" choice: the timer ends at the end of the whole item, not of a chapter.
    private var wholeEpisode = false

    init(
        player: SleepPlayer, defaults: UserDefaults = .standard, night: @escaping () -> NightSettings,
        mono: @escaping () -> Int64 = { Int64(ProcessInfo.processInfo.systemUptime * 1000) },
        wall: @escaping () -> Date = Date.init, calendar: Calendar = .current, shake: ShakeSource? = nil,
        autoTick: Bool = true, publish: @escaping () -> Void
    ) {
        self.player = player
        self.defaults = defaults
        self.night = night
        self.mono = mono
        self.wall = wall
        self.calendar = calendar
        shakeSource = shake
        self.autoTick = autoTick
        self.publish = publish
    }

    var isOn: Bool { clock.isOn }

    /// What the page is shown: at most once a second, and at once when the mode or the fade changes.
    var published: SleepInfo { shown }

    private var speed: Double { max(0.1, player.rate) }

    private var info: SleepInfo { clock.info(positionMs: player.positionMs, speed: speed) }

    /// The page's choice: `off`, `plus`, `chapter`, `episode` or minutes as digits.
    func command(_ asked: String, minutes: Int) {
        if asked == "off" { nightSwitchedOff() }
        wholeEpisode = asked == "episode"
        let choice = wholeEpisode ? "chapter" : asked
        switch choice {
        case "off":
            off()
        case "plus":
            plus()
        default:
            var end: Int64 = 0
            if choice == "chapter" {
                guard let e = chapterEnd() else { return }
                end = e
            }
            clock.set(choice: choice, minutes: Int32(minutes), chapterEndMs: end)
            begin()
        }
    }

    private func nowMinutes() -> Int {
        let c = calendar.dateComponents([.hour, .minute], from: wall())
        return (c.hour ?? 0) * 60 + (c.minute ?? 0)
    }

    private func nowMs() -> Int64 { Int64(wall().timeIntervalSince1970 * 1000) }

    /// Night sleep (the page's rule): called when sound starts, from any start (page, lock screen, next book). Inside the
    /// person's night hours with no timer running it starts the timer. Books, episodes and stations alike, as on the page.
    func playbackStarted() {
        let settings = night()
        guard settings.enabled else { return }
        let offUntil = Int64(defaults.object(forKey: Self.nightOffKey) as? Int64 ?? 0)
        guard NightSleepKt.shouldStartNightSleep(
            night: settings, nowMs: nowMs(), nowMinutes: Int32(nowMinutes()), sleepOn: clock.isOn, offUntilMs: offUntil)
        else { return }
        Self.log.info("night sleep starts the timer for \(settings.minutes) min")
        clock.set(choice: String(settings.minutes), minutes: settings.minutes, chapterEndMs: 0)
        wholeEpisode = false
        begin()
    }

    /// The person turned the timer off inside the night window: it stays off until the window ends. Outside it nothing is kept.
    private func nightSwitchedOff() {
        let settings = night()
        guard settings.enabled else { return }
        let minutes = nowMinutes()
        guard NightSleepKt.inNightWindow(nowMinutes: Int32(minutes), from: settings.fromMinutes, to: settings.toMinutes) else { return }
        let until = NightSleepKt.nightWindowEndMs(nowMs: nowMs(), nowMinutes: Int32(minutes), to: settings.toMinutes)
        defaults.set(until, forKey: Self.nightOffKey)
    }

    private func begin() {
        stopSensor()
        player.setVolume(1)
        lastAt = mono()
        timer?.invalidate()
        if autoTick {
            timer = Timer.scheduledTimer(withTimeInterval: Self.stepMs, repeats: true) { [weak self] _ in
                MainActor.assumeIsolated { self?.step() }
            }
        }
        publishNow()
    }

    func off() {
        wholeEpisode = false
        clock.off()
        settle()
    }

    private func plus() {
        guard clock.isOn else { return }
        clock.plus(positionMs: player.positionMs, speed: speed)
        stopSensor()
        player.setVolume(1)
        publishNow()
    }

    /// A new book: minutes go on; a chapter timer follows the new book's chapters.
    func bookChanged() {
        wholeEpisode = false // the end of one episode is not the end of the next
        guard clock.isOn else { return }
        // A station has no end of a chapter: a timer set for one ends with the book it was set for. Minutes go on.
        if player.live, info.mode == .chapter {
            off()
            return
        }
        if let end = chapterEnd() { clock.followChapter(chapterEndMs: end) }
    }

    /// The book ended. True when a timer was on: it is cleared, and the caller must not ask the queue (invariant 7).
    func endedBook() -> Bool {
        guard clock.isOn else { return false }
        Self.log.info("the book ended under the timer, queue not asked")
        off()
        return true
    }

    /// One tick: public so the tests drive it with their own clock.
    func step() {
        let now = mono()
        let delta = now - lastAt
        lastAt = now
        let position = player.positionMs
        let result = clock.step(deltaMs: delta, playing: player.playing, positionMs: position, speed: speed, chapterEndNow: chapterEnd() ?? position)
        if let run = result as? SleepStepRun {
            player.setVolume(run.volume)
            if clock.info(positionMs: position, speed: speed).fading { startSensor() } else { stopSensor() }
            publishIfDue(now)
        } else if let finish = result as? SleepStepFinish {
            Self.log.info("finished")
            player.pause() // a live station lets go of its stream here
            if let rest = finish.restAtMs?.int64Value, !player.live {
                // The last chapter ends at the end of the book; resting 1 s short keeps the queue from popping.
                player.seek(SleepRulesKt.sleepRestAt(restAtMs: rest, durationMs: player.duration))
            }
            settle()
        }
    }

    private func settle() {
        timer?.invalidate()
        timer = nil
        stopSensor()
        player.setVolume(1)
        publishNow()
    }

    private func chapterEnd() -> Int64? {
        SleepRulesKt.sleepChapterEnd(
            live: player.live, wholeEpisode: wholeEpisode, chapters: player.chapters, positionMs: player.positionMs,
            durationMs: player.duration)?.int64Value
    }

    private func publishNow() {
        shown = info
        shownAt = mono()
        publish()
    }

    private func publishIfDue(_ now: Int64) {
        let i = info
        if i.fading != shown.fading || i.mode != shown.mode || now - shownAt >= 1000 { publishNow() }
    }

    private func startSensor() {
        guard !listening, let shakeSource else { return }
        listening = true
        shakeSource.start { [weak self] size in
            guard let self else { return }
            if shake.sample(size: size, nowMs: mono()) { plus() }
        }
    }

    private func stopSensor() {
        guard listening else { return }
        shakeSource?.stop()
        listening = false
    }

    /// A jolt as if the sensor had read [size] (tests).
    func jolt(_ size: Float) {
        if shake.sample(size: size, nowMs: mono()) { plus() }
    }

    func release() {
        timer?.invalidate()
        timer = nil
        stopSensor()
    }
}
