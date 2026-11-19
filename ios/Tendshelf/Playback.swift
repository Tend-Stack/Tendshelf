import Foundation
import OSLog
@preconcurrency import TendshelfShared

/// The playback side of the app: owns the player, the audio session and the lock-screen card, answers the bridge and the
/// remote commands. `answer` and `sleep` do nothing until I2c.
@MainActor
final class Playback: PlaybackControl, RemoteHandler {
    private static let log = Logger(subsystem: "app.tendshelf.ios", category: "playback")

    let audio: AudioPlayer
    private let session = AudioSession()
    private var nowPlaying: NowPlaying!
    private let store: TokenStore
    private let client: ServerClient
    var onChange: (() -> Void)?
    /// What the lock screen card last showed, so it is pushed on changes and not on every tick.
    private var shown = ""

    init(store: TokenStore, client: ServerClient) {
        self.store = store
        self.client = client
        audio = AudioPlayer(connection: { store.current() }, makeApi: { PlayerApi(origin: $0, client: client) })
        nowPlaying = NowPlaying(handler: self)
        audio.willPlay = { [weak self] live in self?.session.prepare(live: live) }
        audio.onChange = { [weak self] in self?.changed() }
        audio.onChapterChanged = { [weak self] in self?.refreshCard(force: true) }
        session.isPlaying = { [weak self] in self?.audio.playRequested ?? false }
        session.play = { [weak self] in self?.audio.play() }
        session.pause = { [weak self] in self?.audio.pause() }
        session.reset = { [weak self] in self?.mediaServicesReset() }
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
    func answer(keepHere: Bool) {}
    func sleep(choice: String, minutes: Int) {}
    func snapshot() -> PlayerSnapshot { audio.snapshot() }

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
