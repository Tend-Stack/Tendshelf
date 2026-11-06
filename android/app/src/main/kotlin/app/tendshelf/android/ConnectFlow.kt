package app.tendshelf.android

import android.app.Activity
import android.app.AlertDialog
import android.os.Build
import android.webkit.WebView
import app.tendshelf.shared.Handoff
import app.tendshelf.shared.Kind
import app.tendshelf.shared.Origin
import app.tendshelf.shared.Pkce
import app.tendshelf.shared.parseHandoff
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

    // The invitation or pairing link the person confirmed, held while the health check, the http warning and the
    // change-of-server dialog run; begin() uses it once and clears it.
    private var pending: Handoff? = null

    // The token of the pairing whose sign-in page is loading; the same link arriving again changes nothing.
    private var pairToken: String? = null

    private var clearHistoryNext = false
    private var suppressAutoConnect = false

    // True from beginConnect until a sign-in page (login, setup, consent) has finished: several pages can finish while
    // the connect page is still loading, and none of them may start another connect.
    private var connectLoading = false
    private val io = Executors.newSingleThreadExecutor()

    init {
        signOut.after = {
            verifier = null
            pending = null
            pairToken = null
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
        pending = null
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
            var foundId: String? = null
            var failure = Health.Unreachable
            for (c in candidates) {
                val h = api.health(c)
                if (h == Health.Ok) {
                    found = c
                    foundId = api.identity(c)?.id
                    break
                }
                if (failure == Health.Unreachable) failure = h
            }
            ui {
                if (found == null) {
                    state = if (store.hasToken()) State.Connected else State.NoServer
                    showStart(StartState(trimmed, healthWords(failure)))
                } else {
                    accept(found, foundId)
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

    private fun accept(o: Origin, id: String?) {
        if (o.plainHttp && !store.httpWarned(o)) {
            val body = activity.getString(R.string.http_warning_body) +
                if (o.privateAddress) "" else " " + activity.getString(R.string.http_warning_public)
            AlertDialog.Builder(activity)
                .setTitle(R.string.http_warning_title)
                .setMessage(body)
                .setCancelable(false)
                .setPositiveButton(R.string.http_warning_continue) { _, _ ->
                    store.markHttpWarned(o)
                    proceed(o, id)
                }
                .setNegativeButton(R.string.http_warning_cancel) { _, _ ->
                    pending = null
                    state = if (store.hasToken()) State.Connected else State.NoServer
                    showStart(StartState(o.text.removePrefix("http://")))
                }
                .show()
        } else {
            proceed(o, id)
        }
    }

    private fun proceed(o: Origin, id: String?) {
        val old = store.origin
        if (old == o && store.hasToken()) {
            pending = null
            state = State.Connected
            web.loadUrl(o.homeUrl())
            return
        }
        if (old != null && old != o && (store.hasToken() || Offline.store.all().isNotEmpty())) {
            confirmLeave(old, o, id)
            return
        }
        store.origin = o
        store.serverId = id
        begin(o)
    }

    /** Another server takes the downloads and the sign-in away, so the person is told what goes and chooses (A-fix 6). */
    private fun confirmLeave(old: Origin, o: Origin, id: String?) {
        io.execute {
            val downloads = Offline.store.all().size
            val unsent = ProgressWriter.unsentCount(activity.applicationContext)
            ui {
                val sb = StringBuilder(activity.getString(R.string.change_server_body, old.text.substringAfter("://"), o.text.substringAfter("://")))
                if (downloads > 0) sb.append(' ').append(activity.resources.getQuantityString(R.plurals.change_server_downloads, downloads, downloads))
                if (unsent > 0) sb.append(' ').append(activity.getString(R.string.change_server_unsent))
                AlertDialog.Builder(activity)
                    .setTitle(R.string.change_server_title)
                    .setMessage(sb.toString())
                    .setCancelable(false)
                    .setPositiveButton(R.string.change_server_continue) { _, _ ->
                        state = State.Connecting
                        signOut.leave(old) {
                            store.origin = o
                            store.serverId = id
                            leaving = true
                            begin(o)
                        }
                    }
                    .setNegativeButton(R.string.http_warning_cancel) { _, _ ->
                        // A link cancels back to the page behind it; a typed address goes back to the start page, as before.
                        val fromLink = pending != null
                        pending = null
                        state = if (store.hasToken()) State.Connected else State.NoServer
                        if (!fromLink) showStart(StartState(old.text.removePrefix("http://")))
                    }
                    .show()
            }
        }
    }

    /** True from the confirmed change of server until the new sign-in has worked (then the old downloads go). */
    private var leaving = false

    /** What the confirmed address was for: an invitation, a pairing, or the plain sign-in. [pending] is used once. */
    private fun begin(o: Origin) {
        val h = pending
        pending = null
        when (h?.kind) {
            Kind.Invite -> beginInvite(o, h.token)
            Kind.Pair -> beginPair(o, h.token)
            null -> beginConnect(o)
        }
    }

    /** A fresh PKCE verifier is held; the challenge is returned for the page that will ask for the code. */
    private fun newChallenge(): String {
        val random = ByteArray(32).also { SecureRandom().nextBytes(it) }
        val v = Pkce.verifier(random)
        verifier = v
        return Pkce.challenge(MessageDigest.getInstance("SHA-256").digest(v.toByteArray(Charsets.US_ASCII)))
    }

    /** Opens the sign-in page that ends at the consent page, with a fresh challenge. */
    private fun beginConnect(o: Origin) {
        val challenge = newChallenge()
        connectLoading = true
        state = if (store.hasToken()) State.Reconnecting else State.NeedsSignIn
        web.loadUrl(o.connectUrl(challenge))
    }

    /** The invitation page inside the shell. No verifier yet: the existing first-run rule opens the consent page once the person has joined. */
    private fun beginInvite(o: Origin, token: String) {
        connectLoading = true
        suppressAutoConnect = false
        state = State.NeedsSignIn
        web.loadUrl(o.inviteUrl(token))
    }

    /** The pairing page inside the shell; the verifier is held now, its challenge goes in the page address (decision 0028). */
    private fun beginPair(o: Origin, token: String) {
        val challenge = newChallenge()
        pairToken = token
        connectLoading = true
        suppressAutoConnect = false
        state = State.NeedsSignIn
        web.loadUrl(o.pairUrl(token, challenge, Build.MODEL))
    }

    /** The bundled join page's form, or an opened `tendshelf://open` link. Nothing is requested before the person taps Connect on the address. */
    fun onLink(text: String) {
        val h = parseHandoff(text)
        if (h == null) {
            // A signed-in phone keeps its page: a stray link only gets a sentence, never a new screen.
            if (store.hasToken()) {
                AlertDialog.Builder(activity).setMessage(R.string.link_only).setPositiveButton(android.R.string.ok, null).show()
            } else {
                showJoin(activity.getString(R.string.link_only))
            }
            return
        }
        if (store.hasToken() && store.origin == h.origin) {
            // Already this server: joining or pairing would leave the page and the token as different people.
            AlertDialog.Builder(activity).setMessage(R.string.link_connected).setPositiveButton(android.R.string.ok, null).show()
            return
        }
        if (h.kind == Kind.Pair && pairToken == h.token && verifier != null) return
        val body = StringBuilder(activity.getString(R.string.link_body, h.origin.text))
        if (h.origin.plainHttp) {
            body.append("\n\n").append(activity.getString(R.string.http_warning_body))
            if (!h.origin.privateAddress) body.append(' ').append(activity.getString(R.string.http_warning_public))
        }
        val dialog = AlertDialog.Builder(activity)
            .setTitle(R.string.link_title)
            .setMessage(body.toString())
            .setCancelable(true)
            .setPositiveButton(R.string.link_connect) { _, _ ->
                if (h.origin.plainHttp) store.markHttpWarned(h.origin)
                checkAndProceed(h)
            }
            .setNegativeButton(R.string.http_warning_cancel, null)
            .create()
        dialog.show()
        // A tap through an overlay from another app does not count.
        dialog.getButton(AlertDialog.BUTTON_POSITIVE).filterTouchesWhenObscured = true
    }

    /** The person confirmed the exact address: only now does the first request go out, and it sends no credential. */
    private fun checkAndProceed(h: Handoff) {
        io.execute {
            val health = api.health(h.origin)
            val id = if (health == Health.Ok) api.identity(h.origin)?.id else null
            ui {
                if (health != Health.Ok) {
                    showJoin(healthWords(health))
                } else {
                    pending = h
                    proceed(h.origin, id)
                }
            }
        }
    }

    /** The join page with one sentence of plain words above the field. */
    fun showJoin(words: String) {
        pages.joinState = StartState(note = words)
        web.loadUrl(pages.joinUrl)
    }

    /** The one place a code is used: only reached through the navigation policy, main frame, with a verifier pending. */
    fun onCode(code: String) {
        val v = verifier ?: return
        verifier = null
        pairToken = null
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
                if (leaving) {
                    leaving = false
                    signOut.finishLeave()
                } else if (Offline.hasOtherPerson(a.userId)) {
                    Offline.wipe()
                }
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
        path == "/login" || path == "/setup" || path == "/welcome" || path.startsWith("/invite/") || path.startsWith("/pair/") || path.startsWith("/app/connect")

    private fun ui(block: () -> Unit) = activity.runOnUiThread(block)
}
