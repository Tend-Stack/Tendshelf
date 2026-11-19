import OSLog
import UIKit
import WebKit
@preconcurrency import TendshelfShared

/// What the bridge asks of the player (`Playback` in the app, a fake in the tests).
@MainActor
protocol PlaybackControl: AnyObject {
    func start(_ id: String, atMs: Int64?)
    func play()
    func pause()
    func seek(_ ms: Int64)
    func setSpeed(_ speed: Double)
    func answer(keepHere: Bool)
    func sleep(choice: String, minutes: Int)
    func snapshot() -> PlayerSnapshot
    var onChange: (() -> Void)? { get set }
}

/// Holds the bridge weakly: a `WKUserContentController` keeps its handler alive, so without this the bridge never goes.
@MainActor
private final class BridgeProxy: NSObject, WKScriptMessageHandler {
    weak var bridge: Bridge?

    func userContentController(_ userContentController: WKUserContentController, didReceive message: WKScriptMessage) {
        bridge?.receive(message)
    }
}

/// The page-to-app and app-to-page channel (port of `Bridge.kt`). Invariant 2: a message is taken only from the main frame
/// whose security origin is the connected origin, while connected, as a string, after the rate limit, and only if the shared
/// `parseBridge` knows it. What goes to the page is only shared `encode*` output, through one delivery call. Nothing the page
/// sent is logged: logs hold counts.
@MainActor
final class Bridge {
    private static let log = Logger(subsystem: "app.tendshelf.ios", category: "bridge")
    private static let perSecond: Int32 = 30

    private let control: PlaybackControl
    private let userContent: WKUserContentController
    private let currentView: () -> WKWebView?
    private let currentOrigin: () -> Origin?
    private let isConnected: () -> Bool
    private let clock: () -> Int64
    private let proxy = BridgeProxy()
    private let feel = ReaderFeel()
    private var diff = PushDiff()
    private var bucket: TokenBucket
    private var hello = false
    private var visible: Bool
    private var ticker: Timer?
    private var dropped = 0
    private var attached = false

    /// The theme colour the page asked for (`theme #rrggbb`).
    var onTheme: (String) -> Void = { _ in }
    /// The screen's keep-awake switch (`UIApplication.shared.isIdleTimerDisabled`).
    var setIdleTimerDisabled: (Bool) -> Void = { UIApplication.shared.isIdleTimerDisabled = $0 }

    init(
        control: PlaybackControl,
        userContent: WKUserContentController,
        currentView: @escaping () -> WKWebView?,
        currentOrigin: @escaping () -> Origin?,
        isConnected: @escaping () -> Bool,
        front: Bool = true,
        clock: @escaping () -> Int64 = { Int64(Date().timeIntervalSince1970 * 1000) }
    ) {
        self.control = control
        self.userContent = userContent
        self.currentView = currentView
        self.currentOrigin = currentOrigin
        self.isConnected = isConnected
        self.clock = clock
        visible = front
        bucket = TokenBucket(perSecond: Self.perSecond, nowMs: clock())
        proxy.bridge = self
        _ = feel.front(on: front)
        control.onChange = { [weak self] in self?.pushState() }
    }

    // MARK: Attach

    /// Adds the page script (for [origin]) and the message handler; idempotent: whatever was added before is removed first.
    func attach(origin: Origin) {
        removeHandlers()
        userContent.addUserScript(WKUserScript(
            source: NativeScript.source(origin: origin), injectionTime: .atDocumentStart, forMainFrameOnly: true, in: .page))
        userContent.add(proxy, contentWorld: .page, name: NativeScript.handlerName)
        attached = true
    }

    /// Removes both (sign-out, change of server) and gives the screen back its auto-lock.
    func detach() {
        removeHandlers()
        attached = false
        hello = false
        ticker?.invalidate()
        if let on = feel.reset() { setIdleTimerDisabled(on.boolValue) }
    }

    private func removeHandlers() {
        userContent.removeAllUserScripts()
        userContent.removeScriptMessageHandler(forName: NativeScript.handlerName, contentWorld: .page)
    }

    /// A new page started loading in the main frame: whatever it said before is forgotten.
    func pageStarted() {
        hello = false
        ticker?.invalidate()
        if let on = feel.reset() { setIdleTimerDisabled(on.boolValue) }
    }

    /// The app is in front (true) or hidden (false). Hidden: nothing is pushed, and the next push repeats what the page missed.
    func front(_ on: Bool) {
        visible = on
        if on {
            diff.shown()
        } else {
            diff.hidden()
            ticker?.invalidate()
        }
        if let screenOn = feel.front(on: on) { setIdleTimerDisabled(screenOn.boolValue) }
        if on { pushState() }
    }

    // MARK: Receive

    fileprivate func receive(_ message: WKScriptMessage) {
        guard bucket.take(now: clock()) else {
            drop()
            return
        }
        guard message.frameInfo.isMainFrame, let web = currentView(), message.webView === web, isConnected(),
              let origin = currentOrigin() else { return }
        let so = message.frameInfo.securityOrigin
        let source = "\(so.protocol)://\(so.host)" + (so.port != 0 ? ":\(so.port)" : "")
        guard origin.sameSource(sourceOrigin: source), let body = message.body as? String else { return }
        guard let parsed = BridgeProtocolKt.parseBridge(raw: body) else {
            drop()
            return
        }
        handle(parsed)
    }

    private func handle(_ message: BridgeMessage) {
        switch message {
        case is BridgeMessageHello:
            hello = true
            diff.hello()
            pushState()
        case let m as BridgeMessageStart:
            control.start(m.bookId, atMs: m.atMs?.int64Value)
        case is BridgeMessagePlay:
            control.play()
        case is BridgeMessagePause:
            control.pause()
        case let m as BridgeMessageSeek:
            control.seek(m.ms)
        case let m as BridgeMessageSpeed:
            control.setSpeed(m.speed)
        case let m as BridgeMessageAnswer:
            control.answer(keepHere: m.keepHere)
        case let m as BridgeMessageSleep:
            control.sleep(choice: m.choice, minutes: Int(m.minutes))
        case let m as BridgeMessageWake:
            if let on = feel.awake(on: m.on) { setIdleTimerDisabled(on.boolValue) }
        case let m as BridgeMessageTheme:
            onTheme(m.hex)
        default:
            // back, dl, tts, say, fav, quiet, autodl, pagekeys: later phases or Android only.
            break
        }
    }

    private func drop() {
        dropped += 1
        if dropped % 100 == 1 { Self.log.info("bridge dropped messages: \(self.dropped)") }
    }

    // MARK: Push

    /// Pushes what changed since the last push, once the page has said hello and while the app is in front and connected.
    func pushState() {
        guard attached, hello, visible, isConnected(), let web = currentView() else { return }
        let snapshot = control.snapshot()
        for json in diff.messages(s: snapshot) { deliver(json, web) }
        ticker?.invalidate()
        if snapshot.playing {
            // The page's bar moves while sound plays: one push a second.
            ticker = Timer.scheduledTimer(withTimeInterval: 1, repeats: false) { [weak self] _ in
                MainActor.assumeIsolated { self?.pushState() }
            }
        }
    }

    private func deliver(_ json: String, _ web: WKWebView) {
        Self.log.debug("bridge push \(json.count)")
        web.evaluateJavaScript("window.__tsDeliver(" + ApiPathsKt.jsonString(s: json) + ")") { _, _ in }
    }
}
