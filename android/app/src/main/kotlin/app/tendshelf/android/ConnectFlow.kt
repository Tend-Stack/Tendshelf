package app.tendshelf.android

import android.app.Activity
import android.app.AlertDialog
import android.os.Build
import android.webkit.WebView
import app.tendshelf.shared.Origin
import app.tendshelf.shared.Pkce
import java.net.URI
import java.security.MessageDigest
import java.security.SecureRandom
import java.util.concurrent.Executors

/**
 * Connecting this phone, as A1 section 8 says. The person signs in on the server's own page; the page's consent tap
 * redirects to /app/connect/done, which the navigation policy catches ([onCode]); the code is swapped for a device
 * token with the PKCE verifier held here. The password is never typed into native code.
 */
class ConnectFlow(
    private val activity: Activity,
    private val web: WebView,
    private val store: TokenStore,
    private val api: AuthApi,
    private val pages: StartPages,
    private val signOut: SignOut,
) {
    enum class State { NoServer, NeedsSignIn, Connecting, Connected, Reconnecting }

    /** The bridge follows this: it attaches in Connected and detaches in NoServer (plan A4). */
    var onState: ((State) -> Unit)? = null

    var state = State.NoServer
        private set(value) {
            field = value
            onState?.invoke(value)
        }

    @Volatile
    private var verifier: String? = null
    val codePending: Boolean get() = verifier != null

    private var clearHistoryNext = false
    private var suppressAutoConnect = false

    // True from beginConnect until a sign-in page (login, setup, consent) has finished: several pages can finish while
    // the connect page is still loading, and none of them may start another connect.
    private var connectLoading = false
    private val io = Executors.newSingleThreadExecutor()

    init {
        signOut.after = {
            verifier = null
            state = State.NoServer
            clearHistoryNext = true
            // A page that was loading when the wipe began may still finish; it must not start a connect on its own.
            suppressAutoConnect = true
        }
    }

    fun launch() {
        val o = store.origin
        if (o == null) {
            showStart(StartState())
            return
        }
        val held = store.current()
        if (held == null) {
            // Signed out, or a connect that never finished: the start page, with the address filled in.
            suppressAutoConnect = true
            showStart(StartState(o.text))
            return
        }
        state = State.Connected
        web.loadUrl(o.homeUrl())
        // One check at launch: a revoked or expired token ends here; a network failure leaves the person where they are.
        io.execute {
            if (api.me(o, held.token) == Me.Unauthorized) ui { signOut.run(R.string.signed_out) }
        }
    }

    fun showStart(s: StartState) {
        pages.state = s
        web.loadUrl(pages.startUrl)
    }

    /** The start page's form (or Try again): check the address, warn about plain http once, then connect. */
    fun onAddress(typed: String) {
        val trimmed = typed.trim()
        val candidates = Origin.candidates(trimmed)
        if (candidates.isEmpty()) {
            showStart(StartState(trimmed, activity.getString(R.string.address_only)))
            return
        }
        suppressAutoConnect = false
        state = State.Connecting
        io.execute {
            var found: Origin? = null
            var failure = Health.Unreachable
            for (c in candidates) {
                val h = api.health(c)
                if (h == Health.Ok) {
                    found = c
                    break
                }
                if (failure == Health.Unreachable) failure = h
            }
            ui {
                if (found == null) {
                    state = if (store.hasToken()) State.Connected else State.NoServer
                    showStart(StartState(trimmed, healthWords(failure)))
                } else {
                    accept(found)
                }
            }
        }
    }

    private fun healthWords(h: Health): String = activity.getString(
        when (h) {
            Health.Certificate -> R.string.certificate
            Health.NotTendshelf -> R.string.not_tendshelf
            Health.OldServer -> R.string.old_server
            else -> R.string.unreachable
        },
    )

    private fun accept(o: Origin) {
        if (o.plainHttp && !store.httpWarned(o)) {
            val body = activity.getString(R.string.http_warning_body) +
                if (o.privateAddress) "" else " " + activity.getString(R.string.http_warning_public)
            AlertDialog.Builder(activity)
                .setTitle(R.string.http_warning_title)
                .setMessage(body)
                .setCancelable(false)
                .setPositiveButton(R.string.http_warning_continue) { _, _ ->
                    store.markHttpWarned(o)
                    proceed(o)
                }
                .setNegativeButton(R.string.http_warning_cancel) { _, _ ->
                    state = if (store.hasToken()) State.Connected else State.NoServer
                    showStart(StartState(o.text.removePrefix("http://")))
                }
                .show()
        } else {
            proceed(o)
        }
    }

    private fun proceed(o: Origin) {
        val old = store.origin
        if (old == o && store.hasToken()) {
            state = State.Connected
            web.loadUrl(o.homeUrl())
            return
        }
        if (old != null && old != o) signOut.leave(old)
        store.origin = o
        beginConnect(o)
    }

    /** Opens the sign-in page that ends at the consent page, with a fresh challenge. */
    private fun beginConnect(o: Origin) {
        val random = ByteArray(32).also { SecureRandom().nextBytes(it) }
        val v = Pkce.verifier(random)
        val challenge = Pkce.challenge(MessageDigest.getInstance("SHA-256").digest(v.toByteArray(Charsets.US_ASCII)))
        verifier = v
        connectLoading = true
        state = if (store.hasToken()) State.Reconnecting else State.NeedsSignIn
        web.loadUrl(o.connectUrl(challenge))
    }

    /** The one place a code is used: only reached through the navigation policy, main frame, with a verifier pending. */
    fun onCode(code: String) {
        val v = verifier ?: return
        verifier = null
        val o = store.origin ?: return
        val bearer = store.current()?.token
        state = State.Connecting
        io.execute {
            val r = api.swap(o, code, v, Build.MODEL, bearer)
            ui { swapped(o, r, bearer != null) }
        }
    }

    private fun swapped(o: Origin, r: Swap, hadToken: Boolean) {
        when (r) {
            is Swap.Done -> {
                val a = r.answer
                // Someone else's downloads are gone before this person can reach anything (A7, invariant 2).
                if (Offline.hasOtherPerson(a.userId)) Offline.wipe()
                if (a.token != null) store.save(a.token!!, a.deviceId, a.userId) else store.saveIds(a.deviceId, a.userId)
                state = State.Connected
                clearHistoryNext = true
                web.loadUrl(o.homeUrl())
            }
            Swap.Expired -> showStart(StartState(o.text, activity.getString(R.string.expired)))
            Swap.Conflict ->
                if (hadToken) {
                    signOut.run(R.string.other_person)
                } else {
                    // The device limit: the person can sign a device out on this page, then connect again.
                    suppressAutoConnect = true
                    web.loadUrl(o.text + "/me/devices")
                }
            Swap.Unreachable -> showStart(StartState(o.text, activity.getString(R.string.unreachable)))
        }
    }

    /** Called when the main frame finishes a page; drives the reconnect and first-run rules. */
    fun onPageFinished(url: String) {
        if (clearHistoryNext) {
            clearHistoryNext = false
            web.clearHistory()
        }
        val o = store.origin ?: return
        if (!o.matches(url)) return
        val path = try { URI(url).rawPath ?: "" } catch (_: Exception) { return }
        val held = store.current()
        if (signInPath(path)) connectLoading = false
        if (path == "/login" && held != null && state != State.Reconnecting) {
            io.execute {
                when (api.me(o, held.token)) {
                    Me.Unauthorized -> ui { signOut.run(R.string.signed_out) }
                    is Me.Ok -> ui { state = State.Reconnecting }
                    Me.Unreachable -> {}
                }
            }
            return
        }
        if (state == State.Reconnecting && held != null && !connectLoading && path != "/login" && !path.startsWith("/app/connect")) {
            beginConnect(o)
            return
        }
        if (held == null && !suppressAutoConnect && !connectLoading && state != State.Connecting && !signInPath(path)) beginConnect(o)
    }

    private fun signInPath(path: String): Boolean =
        path == "/login" || path == "/setup" || path == "/welcome" || path.startsWith("/invite/") || path.startsWith("/app/connect")

    private fun ui(block: () -> Unit) = activity.runOnUiThread(block)
}
