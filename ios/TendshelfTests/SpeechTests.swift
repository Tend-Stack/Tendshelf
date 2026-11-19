import AVFoundation
import CoreImage
import WebKit
import XCTest
@preconcurrency import TendshelfShared
@testable import Tendshelf

/// A synthesizer that records what it was asked and speaks nothing.
@MainActor
final class FakeEngine: SpeechEngine {
    var delegate: AVSpeechSynthesizerDelegate?
    var spoken: [AVSpeechUtterance] = []
    var stops = 0
    func speak(_ utterance: AVSpeechUtterance) { spoken.append(utterance) }
    func stopNow() { stops += 1 }
}

/// Read aloud (decision 0048, I4a) with a fake engine; the voices are the simulator's own.
@MainActor
final class SpeechTests: XCTestCase {
    private var engine: FakeEngine!
    private var speech: Speech!
    private var replies: [String] = []
    private var activated = 0

    override func setUp() async throws {
        engine = FakeEngine()
        activated = 0
        replies = []
        speech = Speech(engine: engine, activate: { [unowned self] in activated += 1 })
    }

    private func firstVoiceId() throws -> String {
        let list = Speech.voiceInfos(AVSpeechSynthesisVoice.speechVoices())
        return try XCTUnwrap(list.first?.id, "the simulator has no voice")
    }

    private func say(_ n: Int, _ voice: String) {
        speech.say(n: n, rate: 1, voiceId: voice, text: "One sentence.") { [unowned self] in replies.append($0) }
    }

    func testRateTable() {
        XCTAssertEqual(Speech.rate(0.5), 0.25, accuracy: 0.0001)
        XCTAssertEqual(Speech.rate(1), 0.5, accuracy: 0.0001)
        XCTAssertEqual(Speech.rate(2), 0.75, accuracy: 0.0001)
        XCTAssertEqual(Speech.rate(3), 1.0, accuracy: 0.0001)
        XCTAssertEqual(Speech.rate(0), AVSpeechUtteranceMinimumSpeechRate, accuracy: 0.0001)
        XCTAssertEqual(Speech.rate(9), AVSpeechUtteranceMaximumSpeechRate, accuracy: 0.0001)
    }

    func testVoicesHaveNoPersonalOrNoveltyVoiceAndEveryIdIsValid() throws {
        let all = AVSpeechSynthesisVoice.speechVoices()
        let infos = Speech.voiceInfos(all)
        XCTAssertFalse(infos.isEmpty, "the simulator lists no voice")
        for v in all where v.voiceTraits.contains(.isPersonalVoice) || v.voiceTraits.contains(.isNoveltyVoice) {
            XCTAssertFalse(infos.contains { $0.id == Speech.wireId(v.identifier) }, "a personal or novelty voice is offered")
        }
        for info in infos {
            XCTAssertTrue(BridgeProtocolKt.validId(s: info.id), info.id)
            XCTAssertFalse(info.network)
            XCTAssertFalse(info.notInstalled)
        }
        XCTAssertTrue(speech.voicesJson().contains("\"t\":\"voices\""))
    }

    func testUnknownVoiceRepliesErrorAndSpeaksNothing() {
        say(4, "no_such_voice")
        XCTAssertEqual(replies, ["{\"t\":\"tts\",\"n\":4,\"ev\":\"error\"}"])
        XCTAssertTrue(engine.spoken.isEmpty)
    }

    func testSayFlushesActivatesAndMapsEvents() throws {
        let id = try firstVoiceId()
        say(7, id)
        XCTAssertEqual(engine.spoken.count, 1)
        XCTAssertEqual(engine.stops, 1, "the queue is flushed before a new sentence")
        XCTAssertEqual(activated, 1)
        XCTAssertFalse(engine.spoken[0].prefersAssistiveTechnologySettings)
        speech.started(ObjectIdentifier(engine.spoken[0]))
        speech.finished(ObjectIdentifier(engine.spoken[0]))
        XCTAssertEqual(replies, ["{\"t\":\"tts\",\"n\":7,\"ev\":\"start\"}", "{\"t\":\"tts\",\"n\":7,\"ev\":\"end\"}"])
    }

    func testAnEventForAnOldSentenceIsIgnored() throws {
        let id = try firstVoiceId()
        say(1, id)
        let old = engine.spoken[0]
        say(2, id)
        speech.cancelled(ObjectIdentifier(old))
        speech.finished(ObjectIdentifier(old))
        speech.started(ObjectIdentifier(old))
        XCTAssertEqual(replies, [], "nothing is said for a sentence that was replaced")
        speech.finished(ObjectIdentifier(engine.spoken[1]))
        XCTAssertEqual(replies, ["{\"t\":\"tts\",\"n\":2,\"ev\":\"end\"}"])
    }

    func testStopRepliesStoppedOnceAndSilentStopSaysNothing() throws {
        let id = try firstVoiceId()
        say(3, id)
        speech.stop(silent: false)
        speech.stop(silent: false)
        XCTAssertEqual(replies, ["{\"t\":\"tts\",\"n\":3,\"ev\":\"stopped\"}"])
        say(4, id)
        speech.stop(silent: true)
        XCTAssertEqual(replies.count, 1)
    }

    func testDropForgetsTheReply() throws {
        let id = try firstVoiceId()
        say(5, id)
        speech.drop()
        speech.finished(ObjectIdentifier(engine.spoken[0]))
        XCTAssertTrue(replies.isEmpty)
    }

    /// A voice must be spoken with a voice the person can pick: a real simulator voice speaks with its own identifier.
    func testTheEngineGetsTheRealIdentifier() throws {
        let id = try firstVoiceId()
        say(8, id)
        let real = try XCTUnwrap(engine.spoken[0].voice?.identifier)
        XCTAssertEqual(Speech.wireId(real), id)
    }
}

@MainActor
final class FakeSpeechControl: SpeechControl {
    var voicesAsked = 0
    var said: [(Int, String)] = []
    var stops: [Bool] = []
    var drops = 0
    var reply: ((String) -> Void)?
    func voicesJson() -> String { voicesAsked += 1; return "{\"t\":\"voices\",\"list\":[]}" }
    func say(n: Int, rate: Double, voiceId: String, text: String, reply: @escaping (String) -> Void) {
        said.append((n, text))
        self.reply = reply
        reply("{\"t\":\"tts\",\"n\":\(n),\"ev\":\"start\"}")
    }
    func stop(silent: Bool) {
        stops.append(silent)
        if !silent { reply?("{\"t\":\"tts\",\"n\":1,\"ev\":\"stopped\"}") }
    }
    func drop() { drops += 1 }
}

@MainActor
private final class SpeechPageLoader: NSObject, WKNavigationDelegate {
    private var continuation: CheckedContinuation<Void, Never>?
    func load(_ web: WKWebView, html: String, base: String) async {
        web.navigationDelegate = self
        await withCheckedContinuation { c in
            continuation = c
            web.loadHTMLString(html, baseURL: URL(string: base))
        }
    }
    func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) {
        continuation?.resume()
        continuation = nil
    }
}

/// The `tts` and `say` words over the real bridge (decision 0048, I4a).
@MainActor
final class BridgeSpeechTests: XCTestCase {
    private let origin = Origin.companion.parse(input: "https://example.test")!
    private let other = Origin.companion.parse(input: "https://other.test")!
    private var web: WKWebView!
    private var speech: FakeSpeechControl!
    private var bridge: Bridge!
    private var connected = true
    private var served: Origin!
    private let loader = SpeechPageLoader()

    override func setUp() async throws {
        web = WKWebView(frame: CGRect(x: 0, y: 0, width: 300, height: 300), configuration: WKWebViewConfiguration())
        speech = FakeSpeechControl()
        connected = true
        served = origin
        bridge = Bridge(
            control: FakePlayback(), speech: speech, userContent: web.configuration.userContentController,
            currentView: { [unowned self] in web }, currentOrigin: { [unowned self] in served }, isConnected: { [unowned self] in connected },
            front: true, clock: { 1_000_000 })
    }

    private func load(_ body: String) async {
        bridge.attach(origin: origin)
        await loader.load(web, html: "<html><body><script>window.log=[];tsNative.addEventListener('message',e=>log.push(e.data));\(body)</script></body></html>", base: "https://example.test")
    }

    private func log() async -> String {
        String(describing: (try? await web.evaluateJavaScript("log.join('\\n')")) ?? "")
    }

    private func waitFor(_ text: String) async -> String {
        var out = ""
        for _ in 0..<200 {
            out = await log()
            if out.contains(text) { break }
            try? await Task.sleep(nanoseconds: 20_000_000)
        }
        return out
    }

    func testVoicesAreDeliveredWhenConnected() async {
        await load("tsNative.postMessage('tts voices');")
        let text = await waitFor("\"t\":\"voices\"")
        XCTAssertTrue(text.contains("\"t\":\"voices\""), text)
        XCTAssertEqual(speech.voicesAsked, 1)
    }

    func testVoicesAreNotDeliveredFromAnotherOriginOrWhenDisconnected() async {
        served = other
        await load("tsNative.postMessage('tts voices');")
        try? await Task.sleep(nanoseconds: 400_000_000)
        XCTAssertEqual(speech.voicesAsked, 0)
        served = origin
        connected = false
        await load("tsNative.postMessage('tts voices');")
        try? await Task.sleep(nanoseconds: 400_000_000)
        XCTAssertEqual(speech.voicesAsked, 0)
    }

    func testVoicesFromASubFrameAreIgnored() async {
        await load("""
        const f = document.createElement('iframe');
        f.srcdoc = "<script>window.webkit.messageHandlers.tsShell.postMessage('tts voices')<\\/script>";
        document.body.appendChild(f);
        """)
        try? await Task.sleep(nanoseconds: 500_000_000)
        XCTAssertEqual(speech.voicesAsked, 0)
    }

    func testSayReachesTheEngineAndTheAnswerComesBack() async {
        await load("tsNative.postMessage('say 1 1.00 voice_1 Hello there.');")
        let text = await waitFor("\"ev\":\"start\"")
        XCTAssertEqual(speech.said.first?.1, "Hello there.")
        XCTAssertTrue(text.contains("{\"t\":\"tts\",\"n\":1,\"ev\":\"start\"}"), text)
    }

    func testLeavingTheFrontStopsAndTellsThePage() async {
        await load("tsNative.postMessage('say 1 1.00 voice_1 Hello there.');")
        _ = await waitFor("\"ev\":\"start\"")
        bridge.front(false)
        XCTAssertEqual(speech.stops, [false])
        bridge.front(true)
        let text = await waitFor("\"ev\":\"stopped\"")
        XCTAssertTrue(text.contains("\"ev\":\"stopped\""), text)
    }

    func testANewPageDropsSpeechSilently() async {
        await load("")
        bridge.pageStarted()
        XCTAssertEqual(speech.drops, 1)
        XCTAssertTrue(speech.stops.isEmpty)
    }

    func testTtsStopIsSilent() async {
        await load("tsNative.postMessage('tts stop');")
        for _ in 0..<100 where speech.stops.isEmpty { try? await Task.sleep(nanoseconds: 20_000_000) }
        XCTAssertEqual(speech.stops, [true])
    }
}

/// The scanner's access table and the way a scanned text is picked (decision 0048, I4a).
@MainActor
final class ScannerTests: XCTestCase {
    func testAccessTable() {
        XCTAssertEqual(ScanAccess.access(status: .authorized, hasCamera: true), .ready)
        XCTAssertEqual(ScanAccess.access(status: .notDetermined, hasCamera: true), .ask)
        XCTAssertEqual(ScanAccess.access(status: .denied, hasCamera: true), .denied)
        XCTAssertEqual(ScanAccess.access(status: .restricted, hasCamera: true), .denied)
        XCTAssertEqual(ScanAccess.access(status: .authorized, hasCamera: false), .noCamera)
        XCTAssertEqual(ScanAccess.access(status: .notDetermined, hasCamera: false), .noCamera)
    }

    func testAcceptPicksTheFirstGoodValue() {
        XCTAssertNil(ScanAccess.accept([]))
        XCTAssertNil(ScanAccess.accept([nil, ""]))
        XCTAssertNil(ScanAccess.accept([String(repeating: "a", count: 2049)]))
        XCTAssertEqual(ScanAccess.accept([nil, "", "https://a.example/invite/x", "second"]), "https://a.example/invite/x")
        XCTAssertEqual(ScanAccess.accept([String(repeating: "a", count: 2049), "ok"]), "ok")
        XCTAssertNotNil(ScanAccess.accept([String(repeating: "a", count: 2048)]))
    }

    /// The camera is not in the simulator: a generated QR image is decoded and its text takes the path a camera read takes.
    func testAGeneratedQrCodeDecodesAndIsAccepted() throws {
        let link = "https://tendshelf.example/invite/" + String(repeating: "a", count: 43)
        let filter = try XCTUnwrap(CIFilter(name: "CIQRCodeGenerator"))
        filter.setValue(Data(link.utf8), forKey: "inputMessage")
        filter.setValue("M", forKey: "inputCorrectionLevel")
        let image = try XCTUnwrap(filter.outputImage).transformed(by: CGAffineTransform(scaleX: 8, y: 8))
        let detector = try XCTUnwrap(CIDetector(ofType: CIDetectorTypeQRCode, context: nil, options: [CIDetectorAccuracy: CIDetectorAccuracyHigh]))
        let found = detector.features(in: image).compactMap { ($0 as? CIQRCodeFeature)?.messageString }
        let accepted = try XCTUnwrap(ScanAccess.accept(found))
        XCTAssertEqual(accepted, link)
        // And the shared link parser reads it as an invitation, as it would from a paste.
        XCTAssertNotNil(HandoffKt.parseHandoff(text: accepted))
        XCTAssertNil(HandoffKt.parseHandoff(text: "hello"))
    }
}
