package app.tendshelf.android

import app.tendshelf.shared.Origin
import app.tendshelf.shared.parseSignInOrigins

/**
 * The provider origins the sign-in WebView may stay on (decision 0037, plan P3). Learned from the connected server's
 * `GET /login/oidc/origins` at launch, at Connect (which ends on `/login`) and whenever the `/login` page finishes;
 * replaced as a whole, so a provider the owner turned off stops being allowed at the next refresh. The bridge and the
 * token never see these origins: the bridge is attached to the server origin only.
 */
object SignInWindow {
    @Volatile
    var origins: Set<Origin> = emptySet()
        private set

    /** Blocks on the network; run it off the main thread. A server that does not answer leaves an empty set. */
    fun refresh(api: AuthApi, server: Origin) {
        val body = api.signInOriginsBody(server)
        origins = if (body == null) emptySet() else parseSignInOrigins(body, server)
    }

    /** Fire and forget, for the callers on the main thread. */
    fun refreshInBackground(server: Origin) {
        Thread { refresh(AuthApi(), server) }.start()
    }
}
