import UIKit
import WebKit
@preconcurrency import TendshelfShared

/// Owns the one `WKWebView` and is its navigation delegate. Every navigation goes through the shared policy `decide`
/// (invariants 5 and 6): the app's own commands are obeyed only from the main frame of a bundled page, a code is taken
/// only from `/app/connect/done` on the exact origin while a verifier is held, and anything else leaves the window.
final class ShellController: NSObject, WKNavigationDelegate, ShellHost {
    let pages = BundledPages()
    /// What the SwiftUI view shows; the web view inside it is replaced at sign-out.
    let container = UIView()
    private(set) var webView: WKWebView
    private(set) var flow: ConnectFlow!
    private(set) var signOut: SignOut!
    private let configuration: WKWebViewConfiguration

    /// The one address the app itself is loading: it passes the policy once (a load that native code started is not a
    /// link a page offered).
    private var expected: URL?

    init(store: TokenStore = TokenStore(), client: ServerClient = ServerClient()) {
        let config = WKWebViewConfiguration()
        config.applicationNameForUserAgent = "Tendshelf-iOS"
        config.websiteDataStore = .default()
        config.setURLSchemeHandler(pages, forURLScheme: BundledPages.scheme)
        configuration = config
        webView = WKWebView(frame: .zero, configuration: config)
        super.init()
        pages.dark = { [weak self] in self?.webView.traitCollection.userInterfaceStyle == .dark }
        install(webView)
        signOut = SignOut(store: store, api: client, host: self, pages: pages)
        flow = ConnectFlow(store: store, client: client, pages: pages, host: self, signOut: signOut)
    }

    /// Puts [view] in the container and makes this controller its delegate.
    private func install(_ view: WKWebView) {
        view.navigationDelegate = self
        view.scrollView.bounces = false
        view.allowsBackForwardNavigationGestures = true
        #if DEBUG
        view.isInspectable = true
        #endif
        view.translatesAutoresizingMaskIntoConstraints = false
        container.addSubview(view)
        NSLayoutConstraint.activate([
            view.leadingAnchor.constraint(equalTo: container.leadingAnchor),
            view.trailingAnchor.constraint(equalTo: container.trailingAnchor),
            view.topAnchor.constraint(equalTo: container.topAnchor),
            view.bottomAnchor.constraint(equalTo: container.bottomAnchor),
        ])
    }

    // MARK: ShellHost

    func load(_ url: URL) {
        expected = url
        webView.load(URLRequest(url: url))
    }

    func confirm(title: String, message: String, ok: String, cancel: String, completion: @escaping (Bool) -> Void) {
        #if DEBUG
        if DebugDriver.autoYes {
            completion(true)
            return
        }
        #endif
        let alert = UIAlertController(title: title, message: message, preferredStyle: .alert)
        alert.addAction(UIAlertAction(title: cancel, style: .cancel) { _ in completion(false) })
        alert.addAction(UIAlertAction(title: ok, style: .default) { _ in completion(true) })
        topViewController()?.present(alert, animated: true)
    }

    func notice(_ message: String) {
        let alert = UIAlertController(title: nil, message: message, preferredStyle: .alert)
        alert.addAction(UIAlertAction(title: "OK", style: .default))
        topViewController()?.present(alert, animated: true)
    }

    /// Sign-out and change of server: every cookie, storage and cache entry goes, and a new view replaces the old one so
    /// no history, form data or page memory survives.
    func resetWeb() async {
        let old = webView
        old.navigationDelegate = nil
        old.stopLoading()
        old.removeFromSuperview()
        expected = nil
        await WKWebsiteDataStore.default().removeData(ofTypes: WKWebsiteDataStore.allWebsiteDataTypes(), modifiedSince: .distantPast)
        let fresh = WKWebView(frame: .zero, configuration: configuration)
        webView = fresh
        install(fresh)
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
        // The page's own Sign out is a form POST to /logout: it tells a chosen sign-out from an expired one.
        if mainFrame, navigationAction.request.httpMethod == "POST", url.path == "/logout", flow.origin?.matches(url: url.absoluteString) == true {
            signOut.logoutSeen = true
        }
        let nav = NavigationKt.decide(
            url: url.absoluteString,
            mainFrame: mainFrame,
            origin: flow.origin,
            codePending: flow.codePending,
            fromBundled: isBundled(webView.url),
            signInOrigins: flow.signInOrigins,
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
        #if DEBUG
        DebugDriver.pageFinished(webView)
        #endif
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
