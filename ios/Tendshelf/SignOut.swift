import Foundation
@preconcurrency import TendshelfShared

/// Leaves nothing behind (invariant 3; the Android `SignOut.kt` twin): the token and its ids go first, then the web view's
/// cookies, storage and cache go and the view itself is replaced (no history survives), then the start page opens with the
/// address filled in. The revoke call is best effort and runs last, with the token read before the wipe.
final class SignOut {
    private let store: TokenStore
    private let api: ServerAPI
    private weak var host: ShellHost?
    private let pages: BundledPages

    /// Called after the token is gone, so the flow can reset itself (verifier, state, pending link).
    var after: (() -> Void)?

    /// The page asked the server to sign out (a POST to `/logout` went by). Kept for the phases that have downloads to
    /// delete (I3); I1 has nothing else to tell a chosen sign-out from an expired one.
    var logoutSeen = false

    init(store: TokenStore, api: ServerAPI, host: ShellHost, pages: BundledPages) {
        self.store = store
        self.api = api
        self.host = host
        self.pages = pages
    }

    /// [reason] is the sentence for the start page (nil for none).
    func run(reason: String?) {
        let origin = store.origin
        let token = store.current()?.token
        logoutSeen = false
        store.clearToken()
        after?()
        Task { @MainActor in
            await host?.resetWeb()
            pages.state = StartState(address: origin?.text ?? "", note: reason)
            host?.load(BundledPages.startUrl)
            if let origin, let token { await api.revoke(origin, token: token) }
        }
    }

    /// The person confirmed another server: the old device is revoked, then the token and the web data go, so the new
    /// server's pages start clean; [done] runs when that is finished.
    func leave(oldOrigin: Origin, done: @escaping () -> Void) {
        let token = store.current()?.token
        store.clearToken()
        Task { @MainActor in
            await host?.resetWeb()
            done()
            if let token { await api.revoke(oldOrigin, token: token) }
        }
    }
}
