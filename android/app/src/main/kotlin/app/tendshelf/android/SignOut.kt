package app.tendshelf.android

import android.webkit.CookieManager
import android.webkit.WebStorage
import android.webkit.WebView
import java.util.concurrent.Executors

/**
 * Leaves nothing behind (plan A2, invariant 7): token, cookies, web storage, cache and history go; then the start
 * page opens with the address filled in. The revoke call is best effort and runs after the local wipe, with the token
 * read first. The service stops with it (A4). The downloads go with an explicit sign-out (the page's own Sign out, seen as a POST to /logout), with a sign-in as
 * someone else and with a change of server; a session that merely ran out keeps them (decision 0012, point 4).
 */
@androidx.media3.common.util.UnstableApi
class SignOut(
    private val web: WebView,
    private val store: TokenStore,
    private val api: AuthApi,
    private val pages: StartPages,
    private val words: (Int) -> String,
    /** Stops the sound and ends the service (A4). Null until the controller has connected. */
    private val stopPlayback: () -> Unit,
) {
    private val io = Executors.newSingleThreadExecutor()

    /** Called after the wipe, on the main thread, so the flow can reset itself. */
    var after: (() -> Unit)? = null

    /** The page asked the server to sign out (POST /logout went by); a revoked sign-in that follows is a real sign-out. */
    @Volatile
    var logoutSeen = false

    fun run(reason: Int?) {
        val origin = store.origin
        val conn = store.current()
        val token = conn?.token
        val downloads = logoutSeen || reason == R.string.other_person
        logoutSeen = false
        wipeLocal(downloads)
        pages.state = StartState(origin?.text ?: "", reason?.let(words))
        web.loadUrl(pages.startUrl)
        after?.invoke()
        val appContext = web.context.applicationContext
        // A session that merely ran out keeps its unsent places (ProgressWriter promises it): the token is dead, so
        // nothing could be sent, and the next sign-in of the same person carries on. A chosen sign-out sends once and clears.
        if (reason == R.string.signed_out) return
        io.execute {
            ProgressWriter.signOutFlush(appContext, conn)
            if (origin != null && token != null) api.revoke(origin, token)
        }
    }

    /** Everything the page side and the token hold, without opening the start page. */
    fun wipeLocal(downloads: Boolean) {
        stopPlayback()
        if (downloads) Offline.wipe()
        store.clearToken()
        TendWidget.signedOut(web.context) // the home screen keeps no title or cover of this person
        CookieManager.getInstance().removeAllCookies(null)
        CookieManager.getInstance().flush()
        WebStorage.getInstance().deleteAllData()
        web.clearCache(true)
        web.clearHistory()
        web.clearFormData()
    }

    /**
     * The person confirmed another server. In order: the sound stops; the unsent places go to the old server with the old
     * connection (one try, rows kept); the old device is revoked; then the token, cookies, web storage and cache go, so the
     * new server's pages start clean, and [done] runs on the main thread. The downloads and the saved rows go later, by
     * [finishLeave], once the new sign-in has worked: a connect that fails loses nothing but the sign-in.
     */
    fun leave(oldOrigin: app.tendshelf.shared.Origin, done: () -> Unit) {
        val conn = store.current()
        stopPlayback()
        val appContext = web.context.applicationContext
        io.execute {
            ProgressWriter.sendOnly(appContext, conn)
            if (conn != null) api.revoke(oldOrigin, conn.token)
            web.post {
                store.clearToken()
                TendWidget.signedOut(web.context)
                CookieManager.getInstance().removeAllCookies(null)
                CookieManager.getInstance().flush()
                WebStorage.getInstance().deleteAllData()
                web.clearCache(true)
                web.clearHistory()
                web.clearFormData()
                done()
            }
        }
    }

    /** The new server accepted the sign-in: the old server's downloads and any rows still unsent are dropped. */
    fun finishLeave() {
        Offline.wipe()
        val appContext = web.context.applicationContext
        io.execute { ProgressWriter.signOutFlush(appContext, null) }
    }
}
