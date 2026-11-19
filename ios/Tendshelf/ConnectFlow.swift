import UIKit
@preconcurrency import TendshelfShared

/// What the flow needs from the window: load a page, ask a yes-or-no question.
@MainActor
protocol ShellHost: AnyObject {
    func load(_ url: URL)
    /// Shows [title] and [message] with two buttons; [completion] gets true for [ok].
    func confirm(title: String, message: String, ok: String, cancel: String, completion: @escaping (Bool) -> Void)
    /// Shows [message] with one OK button.
    func notice(_ message: String)
    /// Deletes the web view's cookies, storage and cache and replaces the view with a new one: no history survives.
    func resetWeb() async
}

/// Connecting this phone (decision 0048; the Android `ConnectFlow.kt` twin). The person signs in on the server's own page;
/// the page's consent tap redirects to `/app/connect/done`, which the navigation policy catches; a code is traded for a
/// device token with the PKCE verifier held here (I1c). The password is never typed into native code.
///
/// Launch, the address check, the plain-http warning, the change-of-server question, the sign-in, the code swap, the
/// reconnect rules, and invitation and pairing links. Sign-out is `SignOut`.
final class ConnectFlow {
    private let store: TokenStore
    private let client: ServerAPI
    private let pages: BundledPages
    private weak var host: ShellHost?
    private let signOut: SignOut
    private let deviceName: String

    /// The provider pages the sign-in window may keep (decision 0037): learned from the server's
    /// `GET /login/oidc/origins` at launch and whenever its `/login` page finishes; replaced as a whole.
    private(set) var signInOrigins: Set<Origin> = []

    // The invitation or pairing link the person confirmed, held while the health check, the http warning and the
    // change-of-server question run; `begin` uses it once.
    private var pending: Handoff?
    // The token of the pairing whose sign-in page is loading; the same link arriving again changes nothing.
    private var pairToken: String?

    /// Called with the person's id once a code was traded, before anything is saved: the downloads of another person go
    /// (`ConnectFlow.kt:351`). Leaving a server already wiped them in `SignOut.leave`.
    var beforeSave: ((String) -> Void)?

    private(set) var state = ConnectState.noserver
    private var verifier: String?
    /// True while a code is awaited: the navigation policy takes `/app/connect/done` only then.
    var codePending: Bool { verifier != nil }

    private var suppressAutoConnect = false
    // True from beginConnect until a sign-in page has finished: several pages can finish while the connect page is
    // still loading, and none of them may start another connect.
    private var connectLoading = false

    init(store: TokenStore, client: ServerAPI, pages: BundledPages, host: ShellHost, signOut: SignOut, deviceName: String? = nil) {
        self.store = store
        self.client = client
        self.pages = pages
        self.host = host
        self.signOut = signOut
        self.deviceName = deviceName ?? UIDevice.current.model
        signOut.after = { [unowned self] in
            verifier = nil
            pending = nil
            pairToken = nil
            state = .noserver
            signInOrigins = []
            // A page that was loading when the wipe began may still finish; it must not start a connect on its own.
            suppressAutoConnect = true
        }
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
        refreshSignInOrigins(o)
        // One check at launch: a revoked or expired token ends here; a network failure leaves the person where they are.
        if let held = store.current() {
            Task { @MainActor in
                if await client.me(o, token: held.token) is MeUnauthorized { signOut.run(reason: words(Words.Key.signedOut)) }
            }
        }
    }

    private func refreshSignInOrigins(_ server: Origin) {
        Task { @MainActor in
            let body = await client.signInOriginsBody(server)
            // The answer is for the server that was asked; a person who has moved on keeps the newer set.
            guard store.origin == server else { return }
            signInOrigins = body.map { SignInOriginsKt.parseSignInOrigins(body: $0, server: server) } ?? []
        }
    }

    func showStart(_ s: StartState) {
        pages.state = s
        host?.load(BundledPages.startUrl)
    }

    func showJoin(words: String) {
        pages.joinState = StartState(note: words)
        host?.load(BundledPages.joinUrl)
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
        host?.confirm(
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
            pending = nil
            state = .connected
            load(o.homeUrl(shell: .ios))
            return
        }
        if let old, old != o, store.hasToken() {
            confirmLeave(old, o, id)
            return
        }
        store.origin = o
        store.serverId = id
        begin(o)
    }

    /// Another server takes the sign-in away, so the person is told and chooses. A token never follows the person to
    /// another address (invariant 3).
    private func confirmLeave(_ old: Origin, _ o: Origin, _ id: String?) {
        host?.confirm(
            title: words(Words.Key.changeServerTitle),
            message: words(Words.Key.changeServerBody, old.text.afterScheme, o.text.afterScheme),
            ok: words(Words.Key.changeServerContinue),
            cancel: words(Words.Key.httpWarningCancel)
        ) { [self] yes in
            if yes {
                state = .connecting
                signOut.leave(oldOrigin: old) { [self] in
                    store.origin = o
                    store.serverId = id
                    begin(o)
                }
            } else {
                // A link cancels back to the page behind it; a typed address goes back to the start page.
                let fromLink = pending != nil
                pending = nil
                state = store.hasToken() ? .connected : .noserver
                if !fromLink { showStart(StartState(address: old.text.replacingOccurrences(of: "http://", with: ""))) }
            }
        }
    }

    /// What the confirmed address was for: an invitation, a pairing, or the plain sign-in. [pending] is used once.
    private func begin(_ o: Origin) {
        let h = pending
        pending = nil
        switch h?.kind {
        case .invite: beginInvite(o, h!.token)
        case .pair: beginPair(o, h!.token)
        default: beginConnect(o)
        }
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

    /// The invitation page inside the shell. No verifier yet: the first-run rule opens the consent page once the person has joined.
    private func beginInvite(_ o: Origin, _ token: String) {
        connectLoading = true
        suppressAutoConnect = false
        state = .needssignin
        load(o.inviteUrl(token: token, shell: .ios))
    }

    /// The pairing page inside the shell; the verifier is held now, its challenge goes in the page address (decision 0028).
    private func beginPair(_ o: Origin, _ token: String) {
        let verifier = PkceSwift.newVerifier()
        self.verifier = verifier
        pairToken = token
        connectLoading = true
        suppressAutoConnect = false
        state = .needssignin
        load(o.pairUrl(token: token, challenge: PkceSwift.challenge(for: verifier), name: deviceName, shell: .ios))
    }

    /// `tendshelf://open?link=` arrives here. Anything can fire it, so it only reaches the same confirmation as a pasted
    /// link: no token is read, nothing connects without a tap.
    func openLink(_ url: URL) {
        guard url.scheme?.lowercased() == "tendshelf", url.host?.lowercased() == "open" else { return }
        onLink(url.absoluteString)
    }

    /// The bundled join page's form, or an opened link. Nothing is requested before the person taps Connect.
    func onLink(_ text: String) {
        guard let h = HandoffKt.parseHandoff(text: text) else {
            // A signed-in phone keeps its page: a stray link only gets a sentence, never a new screen.
            if store.hasToken() { host?.notice(words(Words.Key.linkOnly)) } else { showJoin(words: words(Words.Key.linkOnly)) }
            return
        }
        if store.hasToken() && store.origin == h.origin {
            host?.notice(words(Words.Key.linkConnected))
            return
        }
        if h.kind == .pair && pairToken == h.token && verifier != nil { return }
        var body = words(Words.Key.linkBody, h.origin.text)
        if h.origin.plainHttp {
            body += "\n\n" + words(Words.Key.httpWarningBody)
            if !h.origin.privateAddress { body += " " + words(Words.Key.httpWarningPublic) }
        }
        host?.confirm(
            title: words(Words.Key.linkTitle),
            message: body,
            ok: words(Words.Key.linkConnect),
            cancel: words(Words.Key.httpWarningCancel)
        ) { [self] yes in
            guard yes else { return }
            if h.origin.plainHttp { store.markHttpWarned(h.origin) }
            checkAndProceed(h)
        }
    }

    /// The person confirmed the exact address: only now does the first request go out, and it sends no credential.
    private func checkAndProceed(_ h: Handoff) {
        Task { @MainActor in
            let health = await client.health(h.origin)
            let id = health == .ok ? await client.identity(h.origin)?.id : nil
            if health != .ok {
                showJoin(words: healthWords(health))
            } else {
                pending = h
                proceed(h.origin, id)
            }
        }
    }

    /// The one place a code is used: reached only through the navigation policy, main frame, with a verifier pending.
    /// The verifier is spent by this one swap (invariant 5).
    func onCode(_ code: String) {
        guard let v = verifier else { return }
        verifier = nil
        pairToken = nil
        guard let o = store.origin else { return }
        let bearer = store.current()?.token
        state = .connecting
        Task { @MainActor in
            let r = await client.swap(o, code: code, verifier: v, name: deviceName, bearer: bearer)
            swapped(o, r, hadToken: bearer != nil)
        }
    }

    private func swapped(_ o: Origin, _ r: Swap, hadToken: Bool) {
        switch r {
        case let done as SwapDone:
            let a = done.answer
            // Someone else's downloads are gone before this person can reach anything (I3 invariant 2).
            beforeSave?(a.userId)
            if let token = a.token {
                store.save(token: token, deviceId: a.deviceId, userId: a.userId)
            } else {
                store.saveIds(deviceId: a.deviceId, userId: a.userId)
            }
            state = .connected
            load(o.homeUrl(shell: .ios))
            refreshSignInOrigins(o)
        case is SwapExpired:
            showStart(StartState(address: o.text, note: words(Words.Key.expired)))
        case is SwapConflict:
            if hadToken {
                signOut.run(reason: words(Words.Key.otherPerson))
            } else {
                // The device limit: the person can sign a device out on this page, then connect again.
                suppressAutoConnect = true
                load(o.text + "/me/devices")
            }
        default:
            showStart(StartState(address: o.text, note: words(Words.Key.unreachable)))
        }
    }

    /// Called when the main frame finishes a page: the reconnect and first-run rules.
    func pageFinished(_ url: String) {
        guard let o = store.origin, let path = o.pathOf(url: url) else { return }
        if ConnectRulesKt.signInPath(path: path) { connectLoading = false }
        if path == "/login" { refreshSignInOrigins(o) }
        let held = store.current()
        let step = ConnectRulesKt.afterPageFinished(
            state: state,
            path: path,
            hasToken: held != nil,
            connectLoading: connectLoading,
            suppressAutoConnect: suppressAutoConnect
        )
        switch step {
        case .beginconnect:
            beginConnect(o)
        case .checkme:
            guard let held else { return }
            Task { @MainActor in
                let r = await client.me(o, token: held.token)
                if r is MeUnauthorized {
                    signOut.run(reason: words(Words.Key.signedOut))
                } else if r is MeOk {
                    state = .reconnecting
                }
            }
        default:
            break
        }
    }

    /// A page of the server could not load: back to the start page with the address and the plain words.
    func loadFailed(_ url: String) {
        guard let o = store.origin, o.matches(url: url) else { return }
        showStart(StartState(address: o.text, note: words(Words.Key.unreachable)))
    }

    private func load(_ text: String) {
        if let url = URL(string: text) { host?.load(url) }
    }
}

private extension String {
    /// The address without `https://` or `http://`, for a sentence.
    var afterScheme: String { components(separatedBy: "://").last ?? self }
}
