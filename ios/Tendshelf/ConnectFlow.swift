import Foundation
@preconcurrency import TendshelfShared

/// What the flow needs from the window: load a page, ask a yes-or-no question.
@MainActor
protocol ShellHost: AnyObject {
    func load(_ url: URL)
    /// Shows [title] and [message] with two buttons; [completion] gets true for [ok].
    func confirm(title: String, message: String, ok: String, cancel: String, completion: @escaping (Bool) -> Void)
}

/// Connecting this phone (decision 0048; the Android `ConnectFlow.kt` twin). The person signs in on the server's own page;
/// the page's consent tap redirects to `/app/connect/done`, which the navigation policy catches; a code is traded for a
/// device token with the PKCE verifier held here (I1c). The password is never typed into native code.
///
/// I1b has the first half: launch, the address check, the plain-http warning and the start of the sign-in. The code swap,
/// sign-out, invitation and pairing links come with I1c; their entry points exist and do the safe minimum.
final class ConnectFlow {
    private let store: TokenStore
    private let client: ServerClient
    private let pages: BundledPages
    private unowned let host: ShellHost

    private(set) var state = ConnectState.noserver
    private var verifier: String?
    /// True while a code is awaited: the navigation policy takes `/app/connect/done` only then.
    var codePending: Bool { verifier != nil }

    private var suppressAutoConnect = false
    // True from beginConnect until a sign-in page has finished: several pages can finish while the connect page is
    // still loading, and none of them may start another connect.
    private var connectLoading = false

    init(store: TokenStore, client: ServerClient, pages: BundledPages, host: ShellHost) {
        self.store = store
        self.client = client
        self.pages = pages
        self.host = host
    }

    var origin: Origin? { store.origin }

    func launch() {
        guard let o = store.origin else {
            showStart(StartState())
            return
        }
        if store.current() == nil && !store.hasToken() {
            // Signed out, or a connect that never finished: the start page, with the address filled in.
            suppressAutoConnect = true
            showStart(StartState(address: o.text))
            return
        }
        state = .connected
        load(o.homeUrl(shell: .ios))
    }

    func showStart(_ s: StartState) {
        pages.state = s
        host.load(BundledPages.startUrl)
    }

    func showJoin(words: String) {
        pages.joinState = StartState(note: words)
        host.load(BundledPages.joinUrl)
    }

    /// The start page's form (or Try again): check the address, warn about plain http once, then connect.
    func onAddress(_ typed: String) {
        let trimmed = typed.trimmingCharacters(in: .whitespacesAndNewlines)
        let candidates = Origin.companion.candidates(typed: trimmed)
        if candidates.isEmpty {
            showStart(StartState(address: trimmed, note: words(Words.Key.addressOnly)))
            return
        }
        suppressAutoConnect = false
        state = .connecting
        Task { @MainActor in
            var found: Origin?
            var foundId: String?
            var failure = Health.unreachable
            for c in candidates {
                let h = await client.health(c)
                if h == .ok {
                    found = c
                    foundId = await client.identity(c)?.id
                    break
                }
                if failure == .unreachable { failure = h }
            }
            if let found {
                accept(found, foundId)
            } else {
                state = store.hasToken() ? .connected : .noserver
                showStart(StartState(address: trimmed, note: healthWords(failure)))
            }
        }
    }

    func healthWords(_ h: Health) -> String {
        switch h {
        case .certificate: return words(Words.Key.certificate)
        case .nottendshelf: return words(Words.Key.notTendshelf)
        case .oldserver: return words(Words.Key.oldServer)
        default: return words(Words.Key.unreachable)
        }
    }

    private func accept(_ o: Origin, _ id: String?) {
        guard o.plainHttp, !store.httpWarned(o) else {
            proceed(o, id)
            return
        }
        let body = words(Words.Key.httpWarningBody) + (o.privateAddress ? "" : " " + words(Words.Key.httpWarningPublic))
        host.confirm(
            title: words(Words.Key.httpWarningTitle),
            message: body,
            ok: words(Words.Key.httpWarningContinue),
            cancel: words(Words.Key.httpWarningCancel)
        ) { [self] yes in
            if yes {
                store.markHttpWarned(o)
                proceed(o, id)
            } else {
                state = store.hasToken() ? .connected : .noserver
                showStart(StartState(address: o.text.replacingOccurrences(of: "http://", with: "")))
            }
        }
    }

    private func proceed(_ o: Origin, _ id: String?) {
        let old = store.origin
        if old == o && store.hasToken() {
            state = .connected
            load(o.homeUrl(shell: .ios))
            return
        }
        if let old, old != o, store.hasToken() {
            // Until I1c's change-of-server question, a token never follows the person to another address
            // (invariant 3): it is dropped here, and the sign-in starts again.
            store.clearToken()
        }
        store.origin = o
        store.serverId = id
        beginConnect(o)
    }

    /// Opens the sign-in page that ends at the consent page, with a fresh challenge. The verifier is held until one swap.
    func beginConnect(_ o: Origin) {
        let verifier = PkceSwift.newVerifier()
        self.verifier = verifier
        let challenge = PkceSwift.challenge(for: verifier)
        connectLoading = true
        state = store.hasToken() ? .reconnecting : .needssignin
        load(o.connectUrl(challenge: challenge, shell: .ios))
    }

    /// `tendshelf://open?link=` arrives here (I1c: confirm, health check, invitation or pairing).
    func openLink(_ url: URL) {}

    /// The bundled join page's form (I1c).
    func onLink(_ text: String) {}

    /// The one place a code is used: reached only through the navigation policy, main frame, with a verifier pending.
    /// I1c swaps it for a token; until then the verifier is simply spent (invariant 5).
    func onCode(_ code: String) {
        guard verifier != nil else { return }
        verifier = nil
    }

    /// Called when the main frame finishes a page: the reconnect and first-run rules.
    func pageFinished(_ url: String) {
        guard let o = store.origin, let path = o.pathOf(url: url) else { return }
        if ConnectRulesKt.signInPath(path: path) { connectLoading = false }
        let step = ConnectRulesKt.afterPageFinished(
            state: state,
            path: path,
            hasToken: store.hasToken(),
            connectLoading: connectLoading,
            suppressAutoConnect: suppressAutoConnect
        )
        if step == .beginconnect { beginConnect(o) }
    }

    /// A page of the server could not load: back to the start page with the address and the plain words.
    func loadFailed(_ url: String) {
        guard let o = store.origin, o.matches(url: url) else { return }
        showStart(StartState(address: o.text, note: words(Words.Key.unreachable)))
    }

    private func load(_ text: String) {
        if let url = URL(string: text) { host.load(url) }
    }
}
