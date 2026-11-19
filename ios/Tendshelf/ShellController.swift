import UIKit
import WebKit
@preconcurrency import TendshelfShared

/// Owns the one `WKWebView` and is its navigation delegate. Every navigation goes through the shared policy `decide`
/// (invariants 5 and 6): the app's own commands are obeyed only from the main frame of a bundled page, a code is taken
/// only from `/app/connect/done` on the exact origin while a verifier is held, and anything else leaves the window.
final class ShellController: NSObject, WKNavigationDelegate, ShellHost {
    let pages = BundledPages()
    let webView: WKWebView
    private(set) var flow: ConnectFlow!

    /// The one address the app itself is loading: it passes the policy once (a load that native code started is not a
    /// link a page offered).
    private var expected: URL?

    init(store: TokenStore = TokenStore(), client: ServerClient = ServerClient()) {
        let config = WKWebViewConfiguration()
        config.applicationNameForUserAgent = "Tendshelf-iOS"
        config.websiteDataStore = .default()
        config.setURLSchemeHandler(pages, forURLScheme: BundledPages.scheme)
        webView = WKWebView(frame: .zero, configuration: config)
        super.init()
        pages.dark = { [weak self] in self?.webView.traitCollection.userInterfaceStyle == .dark }
        webView.navigationDelegate = self
        webView.scrollView.bounces = false
        webView.allowsBackForwardNavigationGestures = true
        #if DEBUG
        webView.isInspectable = true
        #endif
        flow = ConnectFlow(store: store, client: client, pages: pages, host: self)
    }

    // MARK: ShellHost

    func load(_ url: URL) {
        expected = url
        webView.load(URLRequest(url: url))
    }

    func confirm(title: String, message: String, ok: String, cancel: String, completion: @escaping (Bool) -> Void) {
        let alert = UIAlertController(title: title, message: message, preferredStyle: .alert)
        alert.addAction(UIAlertAction(title: cancel, style: .cancel) { _ in completion(false) })
        alert.addAction(UIAlertAction(title: ok, style: .default) { _ in completion(true) })
        topViewController()?.present(alert, animated: true)
    }

    private func topViewController() -> UIViewController? {
        let scene = UIApplication.shared.connectedScenes.compactMap { $0 as? UIWindowScene }.first
        var top = scene?.windows.first(where: \.isKeyWindow)?.rootViewController
        while let next = top?.presentedViewController { top = next }
        return top
    }

    // MARK: WKNavigationDelegate

    func webView(_ webView: WKWebView, decidePolicyFor navigationAction: WKNavigationAction) async -> WKNavigationActionPolicy {
        let mainFrame = navigationAction.targetFrame?.isMainFrame ?? false
        guard let url = navigationAction.request.url else { return .cancel }
        if mainFrame, let expected, url == expected {
            self.expected = nil
            return .allow
        }
        let nav = NavigationKt.decide(
            url: url.absoluteString,
            mainFrame: mainFrame,
            origin: flow.origin,
            codePending: flow.codePending,
            fromBundled: isBundled(webView.url),
            signInOrigins: [],
            bundled: BundledSite.companion.IOS
        )
        switch nav {
        case is NavStay:
            return .allow
        case is NavExternal:
            if mainFrame { await UIApplication.shared.open(url) }
            return .cancel
        case let n as NavTakeCode:
            flow.onCode(n.code)
            return .cancel
        case let n as NavConnect:
            flow.onAddress(n.address)
            return .cancel
        case let n as NavLink:
            flow.onLink(n.text)
            return .cancel
        default:
            // Block, and the Downloads, PlayBook, RemoveBook, AutoShow and TogglePlay commands of later phases.
            return .cancel
        }
    }

    func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) {
        if let url = webView.url { flow.pageFinished(url.absoluteString) }
    }

    func webView(_ webView: WKWebView, didFailProvisionalNavigation navigation: WKNavigation!, withError error: Error) {
        failed(error)
    }

    func webView(_ webView: WKWebView, didFail navigation: WKNavigation!, withError error: Error) {
        failed(error)
    }

    private func failed(_ error: Error) {
        let ns = error as NSError
        // A load the policy cancelled is not a failure.
        if ns.domain == NSURLErrorDomain && ns.code == NSURLErrorCancelled { return }
        if ns.domain == "WebKitErrorDomain" && ns.code == 102 { return }
        if let url = ns.userInfo[NSURLErrorFailingURLErrorKey] as? URL {
            flow.loadFailed(url.absoluteString)
        }
    }

    private func isBundled(_ url: URL?) -> Bool {
        url?.scheme == BundledPages.scheme && url?.host == BundledPages.host
    }
}
