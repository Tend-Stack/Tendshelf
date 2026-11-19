import WebKit
import XCTest
@preconcurrency import TendshelfShared
@testable import Tendshelf

/// The stored reader's kept places, as a fake.
@MainActor
final class FakeReadingControl: ReadingControl {
    var items = [ReadingItem(seq: 3, bookId: "bk_1", form: "sid=c1&chars_read=2")]
    var done: [Int64] = []
    func readingItems() -> [ReadingItem] { items }
    func readingDone(_ seq: Int64) { done.append(seq) }
}

/// A download engine that records what the bridge asked of it.
@MainActor
final class FakeDownloadsControl: DownloadsControl {
    var adds: [String] = []
    var removes: [String] = []
    var free: Int64 = 1_000_000_000
    var dl = DlView(state: "downloading", pct: 42, known: true)

    func add(_ id: String) async { adds.append(id) }
    func remove(_ id: String, byPerson: Bool) async { removes.append(id) }
    func view(_ id: String) -> DlView { dl }
    func freeBytes() -> Int64 { free }
    var autoCalls: [Bool] = []
    func setAutoDownload(_ on: Bool) { autoCalls.append(on) }
}

@MainActor
private final class DlPageLoader: NSObject, WKNavigationDelegate {
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

/// The `dl` words and the `room` answer over the real bridge (decision 0048, I3b).
@MainActor
final class BridgeDownloadsTests: XCTestCase {
    private let origin = Origin.companion.parse(input: "https://example.test")!
    private var web: WKWebView!
    private var downloads: FakeDownloadsControl!
    private var bridge: Bridge!
    private let loader = DlPageLoader()

    override func setUp() async throws {
        web = WKWebView(frame: CGRect(x: 0, y: 0, width: 300, height: 300), configuration: WKWebViewConfiguration())
        downloads = FakeDownloadsControl()
        bridge = Bridge(
            control: FakePlayback(), downloads: downloads, userContent: web.configuration.userContentController,
            currentView: { [unowned self] in web }, currentOrigin: { [unowned self] in origin }, isConnected: { true },
            front: true, clock: { 1_000_000 })
    }

    private func load(_ body: String) async {
        bridge.attach(origin: origin)
        await loader.load(web, html: "<html><body><script>window.log=[];tsNative.addEventListener('message',e=>log.push(e.data));\(body)</script></body></html>", base: "https://example.test")
    }

    private func log() async -> String {
        let value = try? await web.evaluateJavaScript("log.join('\\n')")
        return String(describing: value ?? "")
    }

    func testAskGetsADlPushAndRoomGetsTheRoom() async {
        await load("tsNative.postMessage('hello 1'); tsNative.postMessage('dl ask bk_1'); tsNative.postMessage('dl room');")
        var text = ""
        for _ in 0..<200 {
            text = await log()
            if text.contains("\"t\":\"room\"") && text.contains("\"t\":\"dl\"") { break }
            try? await Task.sleep(nanoseconds: 20_000_000)
        }
        XCTAssertTrue(text.contains("{\"t\":\"dl\",\"bookId\":\"bk_1\",\"state\":\"downloading\",\"pct\":42}"), text)
        // The room is what the phone has, less the 200 MiB every download keeps free.
        XCTAssertTrue(text.contains("{\"t\":\"room\",\"bytes\":\(1_000_000_000 - 200 * 1024 * 1024)}"), text)
        XCTAssertTrue(downloads.adds.isEmpty)
    }

    func testAddAndRemoveReachTheEngineAndAreAnswered() async {
        await load("tsNative.postMessage('hello 1'); tsNative.postMessage('dl add bk_2'); tsNative.postMessage('dl remove bk_3');")
        await eventually("engine") { downloads.adds == ["bk_2"] && downloads.removes == ["bk_3"] }
    }

    func testAddFromASubFrameIsIgnored() async {
        await load("""
        const f = document.createElement('iframe');
        f.srcdoc = "<script>window.webkit.messageHandlers.tsShell.postMessage('dl add bk_1')<\\/script>";
        document.body.appendChild(f);
        tsNative.postMessage('hello 1');
        setTimeout(() => tsNative.postMessage('dl ask bk_9'), 300);
        """)
        for _ in 0..<200 {
            if await log().contains("bk_9") { break }
            try? await Task.sleep(nanoseconds: 20_000_000)
        }
        XCTAssertTrue(downloads.adds.isEmpty)
    }

    func testReadingAskGetsThePlacesAndDoneIsPassedOn() async {
        let reading = FakeReadingControl()
        bridge = Bridge(
            control: FakePlayback(), downloads: downloads, reading: reading, userContent: web.configuration.userContentController,
            currentView: { [unowned self] in web }, currentOrigin: { [unowned self] in origin }, isConnected: { true },
            front: true, clock: { 1_000_000 })
        await load("tsNative.postMessage('rd ask'); tsNative.postMessage('rd done 3'); tsNative.postMessage('rd done x');")
        var text = ""
        for _ in 0..<200 {
            text = await log()
            if text.contains("\"t\":\"rd\"") && !reading.done.isEmpty { break }
            try? await Task.sleep(nanoseconds: 20_000_000)
        }
        XCTAssertTrue(text.contains("{\"t\":\"rd\",\"items\":[{\"seq\":3,\"bookId\":\"bk_1\",\"form\":\"sid=c1&chars_read=2\"}]}"), text)
        XCTAssertEqual(reading.done, [3])
    }
}
