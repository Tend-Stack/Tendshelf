import UIKit
import WebKit
@preconcurrency import TendshelfShared

/// Owns the one `WKWebView` and is its navigation delegate. Every navigation goes through the shared policy `decide`
/// (invariants 5 and 6): the app's own commands are obeyed only from the main frame of a bundled page, a code is taken
/// only from `/app/connect/done` on the exact origin while a verifier is held, and anything else leaves the window.
final class ShellController: NSObject, WKNavigationDelegate, ShellHost {
    let pages = BundledPages()
    /// The offline reader's handler: registered with the web view's configuration, so it exists before the engine does.
    let reader = OfflineReader()
    /// What the SwiftUI view shows; the web view inside it is replaced at sign-out.
    let container = UIView()
    private(set) var webView: WKWebView
    private(set) var flow: ConnectFlow!
    private(set) var signOut: SignOut!
    private(set) var playback: Playback!
    private(set) var bridge: Bridge!
    private(set) var downloads: Downloads!
    private let configuration: WKWebViewConfiguration

    /// The one address the app itself is loading: it passes the policy once (a load that native code started is not a
    /// link a page offered).
    private var expected: URL?

    init(store: TokenStore = TokenStore(), client: ServerClient = ServerClient()) {
        let config = WKWebViewConfiguration()
        config.applicationNameForUserAgent = "Tendshelf-iOS"
        config.websiteDataStore = .default()
        config.setURLSchemeHandler(pages, forURLScheme: BundledPages.scheme)
        config.setURLSchemeHandler(reader, forURLScheme: OfflineReader.scheme)
        configuration = config
        webView = WKWebView(frame: .zero, configuration: config)
        super.init()
        pages.dark = { [weak self] in self?.webView.traitCollection.userInterfaceStyle == .dark }
        install(webView)
        signOut = SignOut(store: store, api: client, host: self, pages: pages)
        flow = ConnectFlow(store: store, client: client, pages: pages, host: self, signOut: signOut)
        playback = Playback(store: store, client: client)
        let paths = OfflinePaths()
        try? paths.prepare()
        let offline = SqliteOfflineStore(path: paths.database)
        downloads = Downloads(
            store: offline, paths: paths, transport: BackgroundTransport(incoming: paths.incoming),
            api: { PlayerApi(origin: $0, client: client) }, connection: { store.current() }, userId: { store.userId },
            free: { Downloads.freeBytes(important: $0) }, notice: { [weak self] in self?.notice($0) })
        // The stored text is fetched with the web view's session cookie for the connected origin's host only (read for the request).
        downloads.text.cookieHeader = { origin in await Self.cookieHeader(for: origin) }
        // The offline reader's own origin `tsread://app`: stored text only (decision 0048, I3d).
        reader.configure(text: downloads.text, store: offline, origin: { [weak self] in self?.flow?.origin }, userId: { store.userId })
        _ = UnmeteredMonitor.shared // starts watching the network for the Wi-Fi rule
        playback.attach(downloads)
        pages.coverData = { [weak self] in self?.downloads.coverData($0) }
        pages.downloadsInput = { [weak self] offline in self?.downloadsInput(offline: offline) ?? Self.emptyDownloads }
        // Nothing keeps playing for someone who signed out or left the server: the player stops, its last place is sent once
        // with the connection that is going, the outbox is emptied, every download and background task goes, the bridge is cut.
        signOut.beforeWipe = { [weak self] connection in
            self?.playback.signOut(connection)
            self?.downloads.wipe()
            self?.bridge.detach()
        }
        flow.beforeSave = { [weak self] userId in
            if self?.downloads.hasOtherPerson(userId) == true { self?.downloads.wipe() }
        }
        bridge = Bridge(
            control: playback,
            downloads: downloads,
            reading: downloads,
            userContent: config.userContentController,
            currentView: { [weak self] in self?.webView },
            currentOrigin: { [weak self] in self?.flow?.origin },
            isConnected: { [weak self] in self?.flow?.state == .connected },
            front: UIApplication.shared.applicationState != .background)
        bridge.onTheme = { [weak self] hex in self?.applyTheme(hex) }
        downloads.onChange = { [weak self] id in self?.bridge.downloadChanged(id) }
        let center = NotificationCenter.default
        center.addObserver(forName: UIApplication.didBecomeActiveNotification, object: nil, queue: .main) { [weak self] _ in
            MainActor.assumeIsolated {
                self?.bridge.front(true)
                // A file the system no longer holds a task for starts again.
                Task {
                    await self?.downloads.reconcile()
                    await self?.downloads.foreground()
                }
            }
        }
        center.addObserver(forName: UIApplication.didEnterBackgroundNotification, object: nil, queue: .main) { [weak self] _ in
            MainActor.assumeIsolated { self?.bridge.front(false) }
        }
        #if DEBUG
        DebugDriver.playback = playback
        DebugDriver.downloads = downloads
        #endif
    }

    // MARK: Downloads page

    private static var emptyDownloads: DownloadsInput {
        DownloadsInput(
            rows: [], shows: [], playingId: "", playing: false, unmetered: true, autoNoRoom: false, offline: false,
            offlineAddress: "", homeUrl: BundledPages.startUrl.absoluteString, readBase: "")
    }

    /// What the Downloads and offline pages list: the phone's own rows, the player's state, and the way home.
    private func downloadsInput(offline: Bool) -> DownloadsInput {
        let snapshot = playback.snapshot()
        let rows = downloads.rows().map { book in
            DownloadRow(
                book: book, view: downloads.view(book.bookId), hasCover: downloads.coverData(book.bookId) != nil,
                readable: downloads.readable(book.bookId), hasAudio: downloads.hasAudio(book.bookId), auto: downloads.isAuto(book.bookId))
        }
        let home = flow.origin?.homeUrl(shell: .ios) ?? ""
        return DownloadsInput(
            rows: rows, shows: downloads.episodes.showRows(), playingId: snapshot.bookId, playing: snapshot.playing,
            unmetered: downloads.unmetered(), autoNoRoom: downloads.autoNoRoomNoted(),
            offline: offline, offlineAddress: pages.offlineAddress, homeUrl: home.isEmpty ? BundledPages.startUrl.absoluteString : home,
            readBase: offline ? "\(OfflineReader.scheme)://\(OfflineReader.host)" : (flow.origin?.text ?? ""))
    }

    /// Runs [work] for at most [seconds]; the page does not wait longer for a slow server.
    private static func within(seconds: Double, _ work: @escaping @MainActor () async -> Void) async {
        await withTaskGroup(of: Void.self) { group in
            group.addTask { await work() }
            group.addTask { try? await Task.sleep(nanoseconds: UInt64(seconds * 1_000_000_000)) }
            await group.next()
            group.cancelAll()
        }
    }

    private func reloadDownloads(after seconds: Double = 0.4) {
        DispatchQueue.main.asyncAfter(deadline: .now() + seconds) { [weak self] in
            MainActor.assumeIsolated { self?.load(BundledPages.downloadsUrl) }
        }
    }

    /// The server did not answer (or answered with a fault) while a person is connected: the phone's own list, with Try again.
    private func showOffline(_ origin: Origin) {
        pages.offlineAddress = origin.text
        load(BundledPages.offlineUrl)
    }

    /// The reader page of a book whose text is stored, when the server cannot be reached: it opens at `tsread://app` with the same
    /// path and query, instead of the offline page. False for any other address or a book with no whole text.
    private func openStored(_ url: URL, _ origin: Origin) -> Bool {
        guard let route = OfflineRoutesKt.offlineRoute(url: url.absoluteString, origin: origin) as? OfflineRouteShell,
              downloads.readable(route.bookId), let parts = URLComponents(url: url, resolvingAgainstBaseURL: false),
              let target = OfflineReader.url(path: parts.percentEncodedPath, query: parts.percentEncodedQuery) else { return false }
        load(target)
        return true
    }

    /// The `Cookie` header the web view holds for [origin]'s host, read for one request and never kept.
    private static func cookieHeader(for origin: Origin) async -> String {
        let all = await WKWebsiteDataStore.default().httpCookieStore.allCookies()
        let host = origin.host.lowercased()
        let secure = origin.scheme == "https"
        let mine = all.filter { cookie in
            let domain = cookie.domain.lowercased().drop { $0 == "." }
            return (domain == host || (cookie.domain.hasPrefix(".") && host.hasSuffix("." + domain))) && (secure || !cookie.isSecure)
        }
        return mine.map { "\($0.name)=\($0.value)" }.joined(separator: "; ")
    }

    /// `theme #rrggbb` from the page: the colour behind the page while it overscrolls, and behind the window.
    private func applyTheme(_ hex: String) {
        guard hex.count == 7, let value = UInt32(hex.dropFirst(), radix: 16) else { return }
        let color = UIColor(
            red: CGFloat((value >> 16) & 0xff) / 255, green: CGFloat((value >> 8) & 0xff) / 255,
            blue: CGFloat(value & 0xff) / 255, alpha: 1)
        webView.underPageBackgroundColor = color
        container.backgroundColor = color
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
        // The page script goes in before a page on the connected origin loads; other pages get nothing.
        if let origin = flow.origin, origin.matches(url: url.absoluteString) { bridge.attach(origin: origin) }
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
            bundled: BundledSite.companion.IOS,
            reader: BundledSite.companion.IOS_READER
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
        case is NavDownloads:
            // The followed shows are read first (two seconds at most), so the page lists them; then the page loads.
            Task { @MainActor [weak self] in
                await Self.within(seconds: 2) { await self?.downloads.episodes.refreshShows() }
                self?.load(BundledPages.downloadsUrl)
            }
            return .cancel
        case let n as NavPlayBook:
            playback.start(n.bookId, atMs: nil)
            reloadDownloads()
            return .cancel
        case let n as NavOnline:
            // The stored reader asked for a page it does not hold: the server's page, loaded as a link the app itself followed.
            if let target = URL(string: n.url) { Task { @MainActor [weak self] in self?.load(target) } }
            return .cancel
        case is NavTogglePlay:
            if playback.snapshot().playing { playback.pause() } else { playback.play() }
            reloadDownloads()
            return .cancel
        case let n as NavRemoveBook:
            Task { @MainActor in
                if playback.snapshot().bookId == n.bookId { playback.stopAndClear() }
                await downloads.remove(n.bookId, byPerson: true)
                reloadDownloads(after: 0)
            }
            return .cancel
        case let n as NavAutoShow:
            Task { @MainActor [weak self] in
                await self?.downloads.episodes.setShow(n.showId, on: n.on)
                self?.reloadDownloads(after: 0)
            }
            return .cancel
        default:
            return .cancel
        }
    }

    /// A page of the server that answers with a fault while connected is the offline page; other answers load as they are.
    func webView(_ webView: WKWebView, decidePolicyFor navigationResponse: WKNavigationResponse) async -> WKNavigationResponsePolicy {
        if navigationResponse.isForMainFrame, let http = navigationResponse.response as? HTTPURLResponse, http.statusCode >= 500,
           let url = http.url, let origin = flow.origin, origin.matches(url: url.absoluteString), flow.state == .connected {
            if openStored(url, origin) { return .cancel }
            showOffline(origin)
            return .cancel
        }
        return .allow
    }

    func webView(_ webView: WKWebView, didStartProvisionalNavigation navigation: WKNavigation!) {
        bridge.pageStarted()
    }

    func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) {
        if let url = webView.url {
            flow.pageFinished(url.absoluteString)
            // A server page loaded, so the session exists: a text that waited for it goes on.
            if flow.origin?.matches(url: url.absoluteString) == true { Task { await downloads.pageLoaded() } }
        }
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
        guard let url = ns.userInfo[NSURLErrorFailingURLErrorKey] as? URL else { return }
        // A connected person whose server cannot be reached sees what the phone holds, not the start page.
        if flow.state == .connected, let origin = flow.origin, origin.matches(url: url.absoluteString) {
            if openStored(url, origin) { return }
            showOffline(origin)
        } else {
            flow.loadFailed(url.absoluteString)
        }
    }

    private func isBundled(_ url: URL?) -> Bool {
        url?.scheme == BundledPages.scheme && url?.host == BundledPages.host
    }
}
