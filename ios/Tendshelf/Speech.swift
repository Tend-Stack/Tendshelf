import AVFoundation
import OSLog
@preconcurrency import TendshelfShared

/// The part of `AVSpeechSynthesizer` the app uses, so tests can stand in for the engine.
protocol SpeechEngine: AnyObject {
    var delegate: AVSpeechSynthesizerDelegate? { get set }
    func speak(_ utterance: AVSpeechUtterance)
    func stopNow()
}

extension AVSpeechSynthesizer: SpeechEngine {
    func stopNow() { _ = stopSpeaking(at: .immediate) }
}

/// What the bridge asks of read aloud (`Speech` in the app, a fake in the tests).
@MainActor
protocol SpeechControl: AnyObject {
    /// The `voices` message: voices on this iPhone only, never a Personal or novelty voice.
    func voicesJson() -> String
    /// Speaks one sentence; [reply] gets `encodeTts` JSON for it (start, end, error, stopped).
    func say(n: Int, rate: Double, voiceId: String, text: String, reply: @escaping (String) -> Void)
    /// Stops now. Not silent: the page is told `stopped` for the sentence being spoken.
    func stop(silent: Bool)
    /// The page is gone: stop and forget where to answer.
    func drop()
}

/// Read aloud (decision 0032, 0048 I4a): the page sends a sentence, `AVSpeechSynthesizer` speaks it with a voice that is on the
/// iPhone, the page hears `start`, `end` or `stopped`. Invariant 1: the text goes only to the synthesizer; the log holds a length.
///
/// A voice identifier such as `com.apple.voice.compact.en-US.Samantha` has dots, which the bridge's id charset
/// (`validId`: letters, digits, `_` and `-`) refuses, so the page's id is the identifier with each dot written `_`; the
/// engine is always given the real identifier, found again from the voices on the phone at that moment.
@MainActor
final class Speech: NSObject, AVSpeechSynthesizerDelegate, SpeechControl {
    private static let log = Logger(subsystem: "app.tendshelf.ios", category: "speech")

    private let engine: SpeechEngine
    private let voices: () -> [AVSpeechSynthesisVoice]
    private let defaultVoiceId: () -> String?
    private let activate: () -> Void
    private var reply: ((String) -> Void)?
    private var numbers: [ObjectIdentifier: (utterance: AVSpeechUtterance, n: Int)] = [:]
    private(set) var current: Int?

    init(
        engine: SpeechEngine = AVSpeechSynthesizer(),
        voices: @escaping () -> [AVSpeechSynthesisVoice] = { AVSpeechSynthesisVoice.speechVoices() },
        defaultVoiceId: @escaping () -> String? = { AVSpeechSynthesisVoice(language: AVSpeechSynthesisVoice.currentLanguageCode())?.identifier },
        activate: @escaping () -> Void = {}
    ) {
        self.engine = engine
        self.voices = voices
        self.defaultVoiceId = defaultVoiceId
        self.activate = activate
        super.init()
        engine.delegate = self
    }

    // MARK: Voices

    /// The page's id for a voice: dots written as `_`, so the id passes the bridge's `validId`.
    static func wireId(_ identifier: String) -> String {
        identifier.replacingOccurrences(of: ".", with: "_")
    }

    /// Voices that may be offered: not a Personal Voice (the person's own voice) and not a novelty voice. Every voice here
    /// runs on the iPhone, so none is a network voice and none is "not installed".
    static func voiceInfos(_ all: [AVSpeechSynthesisVoice], defaultId: String? = nil) -> [VoiceInfo] {
        var seen = Set<String>()
        var out: [VoiceInfo] = []
        for v in all where !v.voiceTraits.contains(.isPersonalVoice) && !v.voiceTraits.contains(.isNoveltyVoice) {
            let id = wireId(v.identifier)
            guard seen.insert(id).inserted else { continue }
            out.append(VoiceInfo(id: id, lang: v.language, network: false, notInstalled: false, isDefault: v.identifier == defaultId))
        }
        return out
    }

    private func usable() -> [VoiceInfo] {
        let defaultId = defaultVoiceId().map(Self.wireId)
        return SpeechVoicesKt.usableVoices(all: Self.voiceInfos(voices()), defaultId: defaultId)
    }

    func voicesJson() -> String {
        BridgeProtocolKt.encodeVoices(list: usable())
    }

    // MARK: Rate

    /// The page's rate (0.5 to 3.0, 1 is normal) as an utterance rate: 1 is the system's default 0.5; above 1 the top of the
    /// scale is reached at 3.0.
    static func rate(_ r: Double) -> Float {
        let raw = r <= 1 ? 0.5 * r : 0.5 + 0.25 * (r - 1)
        let low = Double(AVSpeechUtteranceMinimumSpeechRate)
        let high = Double(AVSpeechUtteranceMaximumSpeechRate)
        return Float(min(max(raw, low), high))
    }

    // MARK: Speak

    func say(n: Int, rate: Double, voiceId: String, text: String, reply: @escaping (String) -> Void) {
        // The voice must be one the page was offered now; anything else is an error, never a fallback voice.
        guard usable().contains(where: { $0.id == voiceId }),
              let voice = voices().first(where: { Self.wireId($0.identifier) == voiceId }),
              let spoken = AVSpeechSynthesisVoice(identifier: voice.identifier)
        else {
            reply(BridgeProtocolKt.encodeTts(n: Int32(n), ev: "error"))
            return
        }
        stopNow()
        activate()
        self.reply = reply
        let utterance = AVSpeechUtterance(string: text)
        utterance.voice = spoken
        utterance.rate = Self.rate(rate)
        utterance.prefersAssistiveTechnologySettings = false
        numbers[ObjectIdentifier(utterance)] = (utterance, n)
        current = n
        Self.log.debug("say \(text.count)")
        engine.speak(utterance)
    }

    /// Flushes whatever is queued. The cancelled utterance's callback is ignored (see `cancelled`).
    private func stopNow() {
        engine.stopNow()
        current = nil
    }

    func stop(silent: Bool) {
        let n = current
        stopNow()
        if !silent, let n { reply?(BridgeProtocolKt.encodeTts(n: Int32(n), ev: "stopped")) }
    }

    func drop() {
        stop(silent: true)
        reply = nil
    }

    // MARK: Engine events

    func started(_ id: ObjectIdentifier) {
        guard let n = number(id), n == current else { return }
        reply?(BridgeProtocolKt.encodeTts(n: Int32(n), ev: "start"))
    }

    func finished(_ id: ObjectIdentifier) {
        let n = number(id)
        numbers[id] = nil
        guard let n, n == current else { return }
        current = nil
        reply?(BridgeProtocolKt.encodeTts(n: Int32(n), ev: "end"))
    }

    /// A cancelled utterance is silent: whoever cancelled it has already said what the page should hear.
    func cancelled(_ id: ObjectIdentifier) {
        numbers[id] = nil
    }

    private func number(_ id: ObjectIdentifier) -> Int? {
        numbers[id]?.n
    }

    nonisolated func speechSynthesizer(_ synthesizer: AVSpeechSynthesizer, didStart utterance: AVSpeechUtterance) {
        let id = ObjectIdentifier(utterance)
        MainActor.assumeIsolated { started(id) }
    }

    nonisolated func speechSynthesizer(_ synthesizer: AVSpeechSynthesizer, didFinish utterance: AVSpeechUtterance) {
        let id = ObjectIdentifier(utterance)
        MainActor.assumeIsolated { finished(id) }
    }

    nonisolated func speechSynthesizer(_ synthesizer: AVSpeechSynthesizer, didCancel utterance: AVSpeechUtterance) {
        let id = ObjectIdentifier(utterance)
        MainActor.assumeIsolated { cancelled(id) }
    }
}
