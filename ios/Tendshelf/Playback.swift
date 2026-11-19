import Foundation
import OSLog
@preconcurrency import TendshelfShared

/// The playback side of the app: owns the player, the audio session and the lock-screen card, answers the bridge and the
/// remote commands, saves the place, rewinds after a pause, asks "Continue there?", runs the sleep timer and starts the next
/// book (I2c; wiring as `PlaybackService.kt:199-277`).
@MainActor
final class Playback: PlaybackControl, RemoteHandler {
    private static let log = Logger(subsystem: "app.tendshelf.ios", category: "playback")

    let audio: AudioPlayer
    private let session = AudioSession()
    private var nowPlaying: NowPlaying!
    private let store: TokenStore
    private let client: ServerClient
    let writer: ProgressWriter
    private(set) var ask: AskState!
    private var resume: Resume!
    private(set) var sleepTimer: SleepTimer!
    private var nextBook: NextBook!
    private var autoNext: AutoNext?
    private weak var offline: OfflineSource?
    private var rewoundS = 0
    private var rewoundSeq = 0
    private var lastChapter = -2
    private var wasPlaying = false
    var onChange: (() -> Void)?
    /// What the lock screen card last showed, so it is pushed on changes and not on every tick.
    private var shown = ""

    init(
        store: TokenStore, client: ServerClient, outboxPath: URL = SqliteOutbox.defaultPath(),
        defaults: UserDefaults = .standard, background: BackgroundWork? = nil
    ) {
        self.store = store
        self.client = client
        let makeApi: (Origin) -> PlayerAPI = { PlayerApi(origin: $0, client: client) }
        audio = AudioPlayer(connection: { store.current() }, makeApi: makeApi)
        let box = ProgressWriter.DeviceBox("")
        let work = background ?? SystemBackground()
        writer = ProgressWriter(
            store: SqliteOutbox(path: outboxPath) { box.value }, deviceBox: box, defaults: defaults,
            connection: { store.current() }, makeApi: makeApi, background: work)
        nowPlaying = NowPlaying(handler: self)
        audio.willPlay = { [weak self] live in self?.session.prepare(live: live) }
        audio.onChange = { [weak self] in self?.changed() }
        audio.onChapterChanged = { [weak self] in self?.refreshCard(force: true) }
        session.isPlaying = { [weak self] in self?.audio.playRequested ?? false }
        session.play = { [weak self] in self?.audio.play() }
        session.pause = { [weak self] in self?.audio.pause() }
        session.reset = { [weak self] in self?.mediaServicesReset() }
        wireProgress(makeApi: makeApi, background: work, defaults: defaults)
    }

    /// The phone's own downloads (I3c): downloaded items start with no request, places are kept beside their rows, an ended
    /// episode is marked played, and the next book is made ready on Wi-Fi and remembered for an offline hand-off.
    func attach(_ downloads: Downloads) {
        offline = downloads
        audio.offline = downloads
        writer.offline = downloads
        autoNext = AutoNext(downloads)
        nextBook.offlineNext = { [weak downloads] in downloads?.offlineNext($0) }
        nextBook.autoDrop = { [weak downloads] in downloads?.autoDrop($0) }
    }

    /// Saving, rewinding, the place question, the sleep timer and the next book. None of it needs a page.
    private func wireProgress(makeApi: @escaping (Origin) -> PlayerAPI, background: BackgroundWork, defaults: UserDefaults) {
        let w = writer
        ask = AskState(writer: w, seekTo: { [weak self] ms in self?.audio.seek(ms) }, changed: { [weak self] in self?.changed() })
        resume = Resume(
            now: { Int64(Date().timeIntervalSince1970 * 1000) },
            settings: { [weak self] in self?.audio.settings },
            // A station has no place to rewind to or ask about (invariant 6).
            book: { [weak self] in
                guard let self, !RadioKt.isStationId(id: audio.bookId) else { return "" }
                return audio.bookId
            },
            position: { [weak self] in self?.audio.positionMs ?? 0 })
        resume.seekTo = { [weak self] ms in self?.audio.seek(ms) }
        resume.onRewound = { [weak self] s in
            self?.rewoundS = s
            self?.rewoundSeq += 1
            self?.changed()
        }
        resume.checkRemote = { [weak self] book in self?.checkRemote(book, makeApi) }
        sleepTimer = SleepTimer(
            player: audio, defaults: defaults, night: { [weak self] in self?.audio.settings?.night ?? NightSettings.companion.OFF },
            shake: MotionShake(), publish: { [weak self] in self?.changed() })
        nextBook = NextBook(
            writer: w, connection: { [store] in store.current() }, makeApi: makeApi, background: background,
            start: { [weak self] id in self?.audio.start(id, atMs: nil) })

        audio.pendingFor = { [weak self] id in self?.writer.pending(id) }
        audio.onBookChanging = { [weak self] in self?.writer.saveNow("book") }
        audio.onBookStarted = { [weak self] id, writtenAt, given in
            guard let self else { return }
            ask.dismissPlain()
            if !RadioKt.isStationId(id: id) { resume.bookLoaded(writtenAt: writtenAt, atGiven: given) }
            nextBook.reset()
            autoNext?.reset()
            sleepTimer.bookChanged()
            lastChapter = -2
        }
        audio.onSound = { [weak self] sound in
            guard let self else { return }
            let playing = sound == .playing
            if playing != wasPlaying {
                wasPlaying = playing
                writer.playingChanged(playing)
            }
            resume.sound(sound)
            if playing { sleepTimer.playbackStarted() }
        }
        audio.onSeek = { [weak self] in self?.writer.sought() }
        audio.onTick = { [weak self] in self?.tick() }
        audio.onEnded = { [weak self] in self?.ended() }
        audio.onLeftEnded = { [weak self] in self?.nextBook.reset() }
        w.onSignedOut = { [weak self] in self?.audio.fail("signedOut") }
        w.onLost = { [weak self] lost, conn in
            guard let self, !ask.isOpen else { return }
            // The place of this device's own save, or the one that was lost for another book.
            let local = lost.save.bookId == audio.bookId ? audio.positionMs : lost.save.positionMs
            if let q = ConflictKt.conflict(bookId: lost.save.bookId, localMs: local, remote: lost.remote, ownDeviceId: conn.deviceId) {
                ask.show(q)
            }
        }
        w.start(source: audio)
    }

    /// Every tick while sound plays: a chapter change saves, the cadence saves, nothing else.
    private func tick() {
        let book = audio.bookId
        guard !book.isEmpty, !RadioKt.isStationId(id: book) else { return }
        let chapter = audio.chapterIdx
        if lastChapter != -2, chapter != lastChapter { writer.saveNow("chapter") }
        lastChapter = chapter
        writer.onTick()
        // Near the end: the next item is made ready on Wi-Fi (decision 0035).
        autoNext?.tick(book: book, positionMs: audio.positionMs, durationMs: audio.duration, playing: audio.playing)
    }

    /// A running sleep timer wins over the queue: the item is finished, nothing new starts. After a book or an episode the one
    /// queue is asked.
    private func ended() {
        let askQueue = !sleepTimer.endedBook()
        if PodcastKt.isEpisodeId(id: audio.bookId) { offline?.markPlayed(audio.bookId) }
        let position = audio.duration > 0 ? audio.duration : audio.positionMs
        nextBook.ended(bookId: audio.bookId, positionMs: position, speed: audio.rate, askQueue: askQueue) { [weak self] in
            self?.audio.ended ?? false
        }
    }

    /// A place elsewhere than this device's: books only (an episode's state names no device, so its own save cannot be told
    /// from another's).
    private func checkRemote(_ book: String, _ makeApi: @escaping (Origin) -> PlayerAPI) {
        guard !PodcastKt.isEpisodeId(id: book), let conn = store.current() else { return }
        Task { @MainActor [weak self] in
            guard let remote = try? await makeApi(conn.origin).progress(token: conn.token, bookId: book) else { return }
            guard let self, !ask.isOpen, audio.bookId == book else { return }
            if let q = ConflictKt.conflict(bookId: book, localMs: audio.positionMs, remote: remote, ownDeviceId: conn.deviceId) {
                ask.show(q)
            }
        }
    }

    private func changed() {
        refreshCard(force: false)
        onChange?()
    }

    /// Pushes the lock-screen card when what it shows changed (or [force]).
    private func refreshCard(force: Bool) {
        let s = audio.snapshot()
        let key = "\(s.bookId)|\(s.playing)|\(s.speed)|\(s.chapterIdx)|\(s.error)|\(s.ended)|\(audio.liveNow)|\(s.durationMs)|\(audio.chapters.count)"
        if !force, key == shown { return }
        shown = key
        nowPlaying.update(audio)
    }

    private func mediaServicesReset() {
        Self.log.warning("media services were reset")
        audio.stopAndClear()
        nowPlaying.clear()
        audio.fail("stream")
    }

    /// Everything stops and is forgotten.
    func stopAndClear() {
        audio.stopAndClear()
        nowPlaying.clear()
        shown = ""
        session.deactivate()
    }

    // MARK: PlaybackControl

    func start(_ id: String, atMs: Int64?) { audio.start(id, atMs: atMs) }
    func play() { audio.play() }
    func pause() { audio.pause() }

    func seek(_ ms: Int64) {
        audio.seek(ms)
        refreshCard(force: true)
    }

    func setSpeed(_ speed: Double) { audio.setSpeed(speed) }
    func answer(keepHere: Bool) { ask.answer(keepHere: keepHere) }
    func sleep(choice: String, minutes: Int) { sleepTimer.command(choice, minutes: minutes) }

    func snapshot() -> PlayerSnapshot {
        let s = audio.snapshot()
        return PlayerSnapshot(
            bookId: s.bookId, positionMs: s.positionMs, durationMs: s.durationMs, playing: s.playing, buffering: s.buffering,
            ended: s.ended, speed: s.speed, chapterIdx: s.chapterIdx, error: s.error, ask: ask.open,
            rewoundS: Int32(rewoundS), rewoundSeq: Int32(rewoundSeq), sleep: sleepTimer.published)
    }

    /// Sign-out or a change of server (called before the token goes, with the connection that is going): playback stops, the
    /// last place is written, sent once with that connection, and every row goes.
    func signOut(_ connection: Connection?) {
        ask.discard()
        sleepTimer.release()
        sleepTimer.off()
        resume.clear()
        stopAndClear()
        Task { await writer.flushAndClear(connection: connection) }
    }

    // MARK: RemoteHandler

    func remotePlay() { audio.play() }
    func remotePause() { audio.pause() }

    func remoteToggle() {
        if audio.playRequested { audio.pause() } else { audio.play() }
    }

    func remoteSkip(forward: Bool) {
        if forward { audio.skipForward() } else { audio.skipBack() }
        refreshCard(force: true)
    }

    func remoteSeek(seconds: Double) { seek(Int64(max(0, seconds) * 1000)) }

    /// A new speed from the lock screen: the player takes it, and a book's server-side speed follows (best effort, as
    /// `PlaybackService.kt:596-608`). An episode's speed is set on its show's page and a station has one speed.
    func remoteRate(_ rate: Double) {
        guard !audio.live else { return }
        audio.setSpeed(rate)
        let id = audio.bookId
        guard !id.isEmpty, !PodcastKt.isEpisodeId(id: id), let conn = store.current() else { return }
        let api = PlayerApi(origin: conn.origin, client: client)
        Task {
            do { try await api.putSpeed(token: conn.token, bookId: id, speed: rate) } catch {
                Self.log.warning("speed not saved")
            }
        }
    }

    func remoteChapter(next: Bool) {
        if next { audio.nextChapter() } else { audio.previousChapter() }
        refreshCard(force: true)
    }
}
