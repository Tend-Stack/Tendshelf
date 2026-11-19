import MediaPlayer
import UIKit
@preconcurrency import TendshelfShared

/// What the lock screen and headphones ask of the player. `Playback` is the one that answers.
@MainActor
protocol RemoteHandler: AnyObject {
    func remotePlay()
    func remotePause()
    func remoteToggle()
    func remoteSkip(forward: Bool)
    func remoteSeek(seconds: Double)
    func remoteRate(_ rate: Double)
    func remoteChapter(next: Bool)
}

/// The lock screen's "Now Playing" card and the remote commands, by hand (not `MPNowPlayingSession`): the time shown is book
/// time, not item time. Port of the metadata of `BookPlayer.kt` and the buttons of `PlaybackService.kt`.
@MainActor
final class NowPlaying {
    private weak var handler: RemoteHandler?
    private let center = MPNowPlayingInfoCenter.default()
    private let commands = MPRemoteCommandCenter.shared()
    private var artKey: Data?
    private var artImage: UIImage?

    init(handler: RemoteHandler) {
        self.handler = handler
        commands.playCommand.addTarget { [weak self] _ in
            Task { @MainActor in self?.handler?.remotePlay() }
            return .success
        }
        commands.pauseCommand.addTarget { [weak self] _ in
            Task { @MainActor in self?.handler?.remotePause() }
            return .success
        }
        commands.togglePlayPauseCommand.addTarget { [weak self] _ in
            Task { @MainActor in self?.handler?.remoteToggle() }
            return .success
        }
        commands.skipBackwardCommand.addTarget { [weak self] _ in
            Task { @MainActor in self?.handler?.remoteSkip(forward: false) }
            return .success
        }
        commands.skipForwardCommand.addTarget { [weak self] _ in
            Task { @MainActor in self?.handler?.remoteSkip(forward: true) }
            return .success
        }
        commands.changePlaybackPositionCommand.addTarget { [weak self] event in
            guard let event = event as? MPChangePlaybackPositionCommandEvent else { return .commandFailed }
            let seconds = event.positionTime
            Task { @MainActor in self?.handler?.remoteSeek(seconds: seconds) }
            return .success
        }
        commands.changePlaybackRateCommand.addTarget { [weak self] event in
            guard let event = event as? MPChangePlaybackRateCommandEvent else { return .commandFailed }
            let rate = Double(event.playbackRate)
            Task { @MainActor in self?.handler?.remoteRate(rate) }
            return .success
        }
        commands.nextTrackCommand.addTarget { [weak self] _ in
            Task { @MainActor in self?.handler?.remoteChapter(next: true) }
            return .success
        }
        commands.previousTrackCommand.addTarget { [weak self] _ in
            Task { @MainActor in self?.handler?.remoteChapter(next: false) }
            return .success
        }
        commands.changePlaybackRateCommand.supportedPlaybackRates = LibraryTreeKt.SPEED_STEPS.map { NSNumber(value: $0.doubleValue) }
        setEnabled(loaded: false, live: false, skipBackS: 30, skipForwardS: 30)
    }

    // MARK: Info

    /// The info dictionary for [s]: titles from the shared `nowLines`, time in book seconds, rate 0 when not playing.
    static func info(_ s: PlayerSnapshot, lines: NowLines, live: Bool, art: UIImage?) -> [String: Any] {
        var info: [String: Any] = [
            MPMediaItemPropertyTitle: lines.title,
            MPMediaItemPropertyArtist: lines.artist,
            MPMediaItemPropertyAlbumTitle: lines.album,
            MPNowPlayingInfoPropertyPlaybackRate: s.playing ? s.speed : 0.0,
            MPNowPlayingInfoPropertyDefaultPlaybackRate: s.speed,
            MPNowPlayingInfoPropertyIsLiveStream: live,
            MPNowPlayingInfoPropertyMediaType: MPNowPlayingInfoMediaType.audio.rawValue,
        ]
        if !live {
            info[MPMediaItemPropertyPlaybackDuration] = Double(s.durationMs) / 1000
            info[MPNowPlayingInfoPropertyElapsedPlaybackTime] = Double(s.positionMs) / 1000
        }
        if let art {
            info[MPMediaItemPropertyArtwork] = MPMediaItemArtwork(boundsSize: art.size) { _ in art }
        }
        return info
    }

    /// Pushes the card for [player]'s current state. Called on load, play and pause, seek, speed, chapter and live-title
    /// changes; not on every tick (the system counts the time itself from the rate).
    func update(_ player: AudioPlayer) {
        guard !player.bookId.isEmpty else {
            clear()
            return
        }
        let s = player.snapshot()
        let lines = NowLinesKt.nowLines(
            live: player.live, title: player.title, author: player.author, episode: player.episodeTitle,
            chapters: player.chapters, positionMs: s.positionMs, liveNow: player.liveNow, liveWord: player.liveWord)
        if player.art != artKey {
            artKey = player.art
            artImage = player.art.flatMap { UIImage(data: $0) }
        }
        center.nowPlayingInfo = Self.info(s, lines: lines, live: player.live, art: artImage)
        setEnabled(
            loaded: true, live: player.live,
            skipBackS: Int(player.settings?.skipBackS ?? 30), skipForwardS: Int(player.settings?.skipForwardS ?? 30))
    }

    /// Empties the card and disables every command (stop, sign-out).
    func clear() {
        center.nowPlayingInfo = nil
        artKey = nil
        artImage = nil
        setEnabled(loaded: false, live: false, skipBackS: 30, skipForwardS: 30)
    }

    /// Nothing loaded, or a station: only play, pause and toggle. Otherwise the buttons the shared `mediaButtonsFor` names.
    private func setEnabled(loaded: Bool, live: Bool, skipBackS: Int, skipForwardS: Int) {
        let buttons: [MediaButton] = loaded ? RadioKt.mediaButtonsFor(live: live, showSpeed: true) : []
        let full = loaded && !live
        commands.playCommand.isEnabled = true
        commands.pauseCommand.isEnabled = true
        commands.togglePlayPauseCommand.isEnabled = true
        commands.skipBackwardCommand.isEnabled = buttons.contains(.back)
        commands.skipForwardCommand.isEnabled = buttons.contains(.forward)
        commands.skipBackwardCommand.preferredIntervals = [NSNumber(value: skipBackS)]
        commands.skipForwardCommand.preferredIntervals = [NSNumber(value: skipForwardS)]
        commands.changePlaybackPositionCommand.isEnabled = full
        commands.changePlaybackRateCommand.isEnabled = buttons.contains(.speed)
        commands.nextTrackCommand.isEnabled = full
        commands.previousTrackCommand.isEnabled = full
    }
}
