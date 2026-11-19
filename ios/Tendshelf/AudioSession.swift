import AVFoundation
import Foundation
import OSLog

/// The app's audio session: category `.playback` (sound continues with the screen locked and the ringer switch off), spoken
/// audio for books and episodes and the default mode for a station, long-form policy. It reacts to calls and Siri
/// (interruptions), to headphones coming out (pause) and to the media services restarting (stop, `stream` message).
@MainActor
final class AudioSession {
    private static let log = Logger(subsystem: "app.tendshelf.ios", category: "session")

    var isPlaying: () -> Bool = { false }
    var play: () -> Void = {}
    var pause: () -> Void = {}
    /// The media services were reset: everything stops and the page is told the stream failed.
    var reset: () -> Void = {}

    private let session = AVAudioSession.sharedInstance()
    private var wasPlaying = false
    private var tokens: [NSObjectProtocol] = []

    init() {
        let center = NotificationCenter.default
        tokens.append(center.addObserver(forName: AVAudioSession.interruptionNotification, object: nil, queue: .main) { [weak self] n in
            let kind = (n.userInfo?[AVAudioSessionInterruptionTypeKey] as? UInt).flatMap(AVAudioSession.InterruptionType.init(rawValue:))
            let options = (n.userInfo?[AVAudioSessionInterruptionOptionKey] as? UInt).map(AVAudioSession.InterruptionOptions.init(rawValue:))
            MainActor.assumeIsolated { self?.interruption(kind, options) }
        })
        tokens.append(center.addObserver(forName: AVAudioSession.routeChangeNotification, object: nil, queue: .main) { [weak self] n in
            let reason = (n.userInfo?[AVAudioSessionRouteChangeReasonKey] as? UInt).flatMap(AVAudioSession.RouteChangeReason.init(rawValue:))
            MainActor.assumeIsolated { self?.routeChanged(reason) }
        })
        tokens.append(center.addObserver(forName: AVAudioSession.mediaServicesWereResetNotification, object: nil, queue: .main) { [weak self] _ in
            MainActor.assumeIsolated { self?.reset() }
        })
    }

    /// Sets the category for the item about to play and activates the session (first play, and every play after a stop).
    func prepare(live: Bool) {
        do {
            try session.setCategory(.playback, mode: live ? .default : .spokenAudio, policy: .longFormAudio, options: [])
        } catch {
            // The long-form policy needs a device that routes to AirPlay; a plain playback category is the fallback.
            try? session.setCategory(.playback, mode: live ? .default : .spokenAudio, options: [])
        }
        do {
            try session.setActive(true)
        } catch {
            Self.log.warning("could not activate the audio session")
        }
    }

    /// Lets other apps' audio come back: only when playback is stopped and cleared.
    func deactivate() {
        try? session.setActive(false, options: .notifyOthersOnDeactivation)
    }

    private func interruption(_ kind: AVAudioSession.InterruptionType?, _ options: AVAudioSession.InterruptionOptions?) {
        switch kind {
        case .began:
            wasPlaying = isPlaying()
        case .ended:
            if wasPlaying, options?.contains(.shouldResume) == true { play() }
            wasPlaying = false
        default:
            break
        }
    }

    private func routeChanged(_ reason: AVAudioSession.RouteChangeReason?) {
        if reason == .oldDeviceUnavailable { pause() }
    }
}
