import WebKit
import XCTest
@preconcurrency import TendshelfShared
@testable import Tendshelf

/// A player that records what the bridge told it.
@MainActor
final class FakePlayback: PlaybackControl {
    var starts: [(String, Int64?)] = []
    var plays = 0
    var pauses = 0
    var seeks: [Int64] = []
    var speeds: [Double] = []
    var onChange: (() -> Void)?

    func start(_ id: String, atMs: Int64?) { starts.append((id, atMs)) }
    func play() { plays += 1 }
    func pause() { pauses += 1 }
    func seek(_ ms: Int64) { seeks.append(ms) }
    func setSpeed(_ speed: Double) { speeds.append(speed) }
    func answer(keepHere: Bool) {}
    func sleep(choice: String, minutes: Int) {}

    func snapshot() -> PlayerSnapshot {
        PlayerSnapshot(
            bookId: "b1", positionMs: 1000, durationMs: 60_000, playing: false, buffering: false, ended: false, speed: 1.0,
            chapterIdx: 0, error: "", ask: nil, rewoundS: 0, rewoundSeq: 0, sleep: SleepInfo.companion.OFF)
    }
}

@MainActor
private final class PageLoader: NSObject, WKNavigationDelegate {
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

@MainActor
final class BridgeTests: XCTestCase {
    private let origin = Origin.companion.parse(input: "https://example.test")!
    private var web: WKWebView!
    private var fake: FakePlayback!
    private var bridge: Bridge!
    private var connected = true
    private var idle: [Bool] = []
    private let loader = PageLoader()

    override func setUp() async throws {
        web = WKWebView(frame: CGRect(x: 0, y: 0, width: 300, height: 300), configuration: WKWebViewConfiguration())
        fake = FakePlayback()
        connected = true
        idle = []
        bridge = Bridge(
            control: fake,
            userContent: web.configuration.userContentController,
            currentView: { [unowned self] in web },
            currentOrigin: { [unowned self] in origin },
            isConnected: { [unowned self] in connected },
            front: true,
            clock: { 1_000_000 })
        bridge.setIdleTimerDisabled = { [unowned self] on in idle.append(on) }
    }

    private func load(_ body: String, base: String = "https://example.test") async {
        bridge.attach(origin: origin)
        await loader.load(web, html: "<html><body><script>\(body)</script></body></html>", base: base)
    }

    private func js(_ script: String) async -> String {
        let value = try? await web.evaluateJavaScript(script)
        return String(describing: value ?? "nil")
    }

    func testHelloReachesTheControlAndThePageGetsOneStateEachWay() async {
        await load("""
        window.log = [];
        tsNative.onmessage = (e) => log.push('on:' + e.data);
        tsNative.addEventListener('message', (e) => log.push('ev:' + e.data));
        tsNative.postMessage('hello 1');
        """)
        var count = "0"
        for _ in 0..<200 {
            count = await js("log.filter(m => m.indexOf('\"t\":\"state\"') > 0).length")
            if count == "2" { break }
            try? await Task.sleep(nanoseconds: 20_000_000)
        }
        XCTAssertEqual(count, "2")
        let viaOn = await js("log.filter(m => m.startsWith('on:') && m.indexOf('\"t\":\"state\"') > 0).length")
        let viaEvent = await js("log.filter(m => m.startsWith('ev:') && m.indexOf('\"t\":\"state\"') > 0).length")
        XCTAssertEqual(viaOn, "1")
        XCTAssertEqual(viaEvent, "1")
    }

    func testCommandsReachTheControl() async {
        await load("tsNative.postMessage('hello 1'); tsNative.postMessage('start b9 5000'); tsNative.postMessage('seek 12000'); tsNative.postMessage('speed 1.50'); tsNative.postMessage('pause'); tsNative.postMessage('play');")
        await eventually("commands") { fake.plays == 1 && fake.pauses == 1 }
        XCTAssertEqual(fake.starts.count, 1)
        XCTAssertEqual(fake.starts.first?.0, "b9")
        XCTAssertEqual(fake.starts.first?.1, 5000)
        XCTAssertEqual(fake.seeks, [12000])
        XCTAssertEqual(fake.speeds, [1.5])
    }

    func testMessageFromAnIframeIsIgnored() async {
        await load("""
        const f = document.createElement('iframe');
        f.srcdoc = "<script>window.webkit.messageHandlers.tsShell.postMessage('play')<\\/script>";
        document.body.appendChild(f);
        setTimeout(() => tsNative.postMessage('pause'), 300);
        """)
        await eventually("the main frame's own message") { fake.pauses == 1 }
        XCTAssertEqual(fake.plays, 0)
    }

    func testAnotherOriginGetsNoNativeObject() async {
        bridge.attach(origin: origin)
        await loader.load(web, html: "<html><body></body></html>", base: "https://other.test")
        let kind = await js("typeof window.tsNative")
        XCTAssertEqual(kind, "undefined")
    }

    func testBurstOf31PassesThirty() async {
        await load("for (let i = 0; i < 31; i++) tsNative.postMessage('play');")
        await eventually("thirty plays") { fake.plays >= 30 }
        try? await Task.sleep(nanoseconds: 300_000_000)
        XCTAssertEqual(fake.plays, 30)
    }

    func testUnknownMessageIsDropped() async {
        await load("tsNative.postMessage('start x y z'); tsNative.postMessage('play');")
        await eventually("play") { fake.plays == 1 }
        XCTAssertTrue(fake.starts.isEmpty)
    }

    func testNotConnectedDropsEverything() async {
        connected = false
        await load("tsNative.postMessage('hello 1'); tsNative.postMessage('play');")
        try? await Task.sleep(nanoseconds: 500_000_000)
        XCTAssertEqual(fake.plays, 0)
    }

    func testWakeHoldsTheScreenAndDetachGivesItBack() async {
        await load("tsNative.postMessage('wake 1');")
        await eventually("idle timer off") { idle.last == true }
        bridge.detach()
        XCTAssertEqual(idle.last, false)
    }
}
