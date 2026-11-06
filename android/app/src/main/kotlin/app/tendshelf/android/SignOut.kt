package app.tendshelf.android

import android.webkit.CookieManager
import android.webkit.WebStorage
import android.webkit.WebView
import java.util.concurrent.Executors

/**
 * Leaves nothing behind (plan A2, invariant 7): token, cookies, web storage, cache and history go; then the start
 * page opens with the address filled in. The revoke call is best effort and runs after the local wipe, with the token
 * read first. The service stops with it (A4). A7 adds the downloads.
 */
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

    fun run(reason: Int?) {
        val origin = store.origin
        val conn = store.current()
        val token = conn?.token
        wipeLocal()
        pages.state = StartState(origin?.text ?: "", reason?.let(words))
        web.loadUrl(pages.startUrl)
        after?.invoke()
        val appContext = web.context.applicationContext
        // A5 (D9): one try to send the saved places, then every row goes, whatever the answer.
        io.execute {
            ProgressWriter.signOutFlush(appContext, conn)
            if (origin != null && token != null) api.revoke(origin, token)
        }
    }

    /** Everything the page side and the token hold, without opening the start page. */
    fun wipeLocal() {
        stopPlayback()
        store.clearToken()
        CookieManager.getInstance().removeAllCookies(null)
        CookieManager.getInstance().flush()
        WebStorage.getInstance().deleteAllData()
        web.clearCache(true)
        web.clearHistory()
        web.clearFormData()
    }

    /** The person chose another server: the old token is useless there, so it is revoked and everything is wiped. */
    fun leave(oldOrigin: app.tendshelf.shared.Origin) {
        val token = store.current()?.token
        wipeLocal()
        val appContext = web.context.applicationContext
        io.execute {
            ProgressWriter.signOutFlush(appContext, null) // the rows belong to the old server
            if (token != null) api.revoke(oldOrigin, token)
        }
    }
}
