package app.tendshelf.android

import android.net.Uri
import android.os.Handler
import android.os.Looper
import android.os.SystemClock
import android.util.Log
import android.webkit.WebView
import androidx.webkit.JavaScriptReplyProxy
import androidx.webkit.WebMessageCompat
import androidx.webkit.WebViewCompat
import androidx.webkit.WebViewFeature
import app.tendshelf.shared.BridgeMessage
import app.tendshelf.shared.Origin
import app.tendshelf.shared.PlayerSnapshot
import app.tendshelf.shared.TokenBucket
import app.tendshelf.shared.encodeBack
import app.tendshelf.shared.encodeError
import app.tendshelf.shared.encodeState
import app.tendshelf.shared.parseBridge

/**
 * The web player as a remote control (plan A4). The page's player bar talks to this object, which drives
 * [PlayerRemote]; sound, position and saves belong to the service. The object `tsNative` offers `postMessage` and
 * `onmessage` only, and no message can carry a token, a URL or a path (see `BridgeProtocol.kt`).
 *
 * The rule that makes injected script harmless (the ceiling rule): a message gives the page nothing its own session
 * cookie could not already do from that page. Every message re-checks the main frame, the exact origin and the
 * [ConnectFlow] state, because the injection rule alone cannot tell a same-origin iframe from the page.
 */
class Bridge(
    private val flow: () -> ConnectFlow.State,
    private val currentOrigin: () -> Origin?,
    /** Bar colour (the page's `theme-color`), or null to go back to the app's own. */
    private val onTheme: (String?) -> Unit,
) {
    var remote: PlayerRemote? = null
        set(value) {
            field = value
            value?.observe { push(it) }
            // The remote may arrive after the page said hello.
            if (value != null) pushState()
        }

    private val main = Handler(Looper.getMainLooper())
    private var web: WebView? = null
    private var attached: Origin? = null
    private var proxy: JavaScriptReplyProxy? = null
    private var started = false
    private var bucket = TokenBucket(RATE, SystemClock.uptimeMillis())
    private var dropped = 0
    private var lastError = ""
    private var pendingBack: (() -> Unit)? = null

    /** True when the page can be asked to go back (it holds the object and the person is connected). */
    val active: Boolean get() = attached != null

    /** Adds the object for exactly [origin]. Without the feature nothing is added and the page plays by itself. */
    fun attach(web: WebView, origin: Origin) {
        if (attached == origin && this.web === web) return
        detach()
        if (!WebViewFeature.isFeatureSupported(WebViewFeature.WEB_MESSAGE_LISTENER)) {
            Log.i(TAG, "bridge off: this WebView has no message listener")
            return
        }
        try {
            // One exact rule: scheme, host and port. Never "*", never addJavascriptInterface.
            WebViewCompat.addWebMessageListener(web, OBJECT, setOf(origin.text)) { _, message, sourceOrigin, isMainFrame, reply ->
                onMessage(origin, message, sourceOrigin, isMainFrame, reply)
            }
        } catch (e: IllegalArgumentException) {
            Log.w(TAG, "bridge off: the rule was refused")
            return
        }
        this.web = web
        attached = origin
    }

    /** Sign-out or another server: the object goes, so does the proxy. A new document will not have it. */
    fun detach() {
        val w = web
        if (w != null && attached != null) {
            try {
                WebViewCompat.removeWebMessageListener(w, OBJECT)
            } catch (_: Exception) {
            }
        }
        web = null
        attached = null
        proxy = null
        pendingBack = null
        main.removeCallbacksAndMessages(null)
        onTheme(null)
    }

    /** A new document is loading: whatever the old one held is stale (B10). It says hello again. */
    fun pageStarted() {
        proxy = null
    }

    fun onStart() {
        started = true
        pushState()
        tick()
    }

    fun onStop() {
        started = false
        main.removeCallbacks(ticker)
    }

    private fun onMessage(
        origin: Origin, message: WebMessageCompat, sourceOrigin: Uri, isMainFrame: Boolean, reply: JavaScriptReplyProxy,
    ) {
        // The cheapest checks first; a flood is dropped before any parsing (B11).
        if (!bucket.take(SystemClock.uptimeMillis())) {
            drop()
            return
        }
        if (!isMainFrame) return
        if (flow() != ConnectFlow.State.Connected) return
        // Exact, normalised compare (B5): a sandboxed document's origin is "null" and parses to nothing.
        val now = currentOrigin()
        if (now == null || now != origin || !origin.sameSource(sourceOrigin.toString())) return
        if (message.type != WebMessageCompat.TYPE_STRING) return
        val msg = parseBridge(message.data)
        if (msg == null) {
            drop()
            return
        }
        handle(msg, reply)
    }

    private fun handle(msg: BridgeMessage, reply: JavaScriptReplyProxy) {
        val r = remote
        when (msg) {
            BridgeMessage.Hello -> {
                proxy = reply
                pushState()
            }
            is BridgeMessage.Start -> r?.start(msg.bookId, msg.atMs)
            BridgeMessage.Play -> r?.play()
            BridgeMessage.Pause -> r?.pause()
            is BridgeMessage.Seek -> r?.seek(msg.ms)
            is BridgeMessage.Speed -> r?.setSpeed(msg.speed)
            is BridgeMessage.Back -> {
                val next = pendingBack
                pendingBack = null
                main.removeCallbacks(backTimeout)
                if (!msg.handled) next?.invoke()
            }
            is BridgeMessage.Theme -> onTheme(msg.hex)
        }
    }

    /** Android Back: the page closes a sheet or dialog if it has one; otherwise [fallback] (history) after 150 ms. */
    fun askBack(fallback: () -> Unit) {
        val p = proxy
        if (p == null || !started || flow() != ConnectFlow.State.Connected) {
            fallback()
            return
        }
        pendingBack?.let { main.removeCallbacks(backTimeout) }
        pendingBack = fallback
        send(p, encodeBack())
        main.postDelayed(backTimeout, BACK_WAIT_MS)
    }

    private val backTimeout = Runnable {
        val next = pendingBack
        pendingBack = null
        next?.invoke()
    }

    private fun push(s: PlayerSnapshot) {
        if (!started) return
        val p = proxy ?: return
        if (flow() != ConnectFlow.State.Connected) return
        send(p, encodeState(s))
        // Errors cross as one of two fixed codes; `signedOut` does not (sign-out replaces the page).
        if (s.error != lastError) {
            lastError = s.error
            if (s.error == "stream" || s.error == "book") send(p, encodeError(s.error))
        }
        tick()
    }

    private fun pushState() {
        val r = remote ?: return
        push(r.snapshot())
    }

    private val ticker = Runnable {
        pushState()
    }

    /** One push a second while playing and the activity is started; none otherwise. */
    private fun tick() {
        main.removeCallbacks(ticker)
        if (started && remote?.snapshot()?.playing == true) main.postDelayed(ticker, 1000)
    }

    private fun send(p: JavaScriptReplyProxy, text: String) {
        try {
            // The length only: the log never holds what was sent.
            Log.d(TAG, "bridge push ${text.length}")
            p.postMessage(text)
        } catch (_: Exception) {
            // The frame is gone; the next hello gives a new proxy.
        }
    }

    /** Counted, never echoed: the log holds a number and nothing the page sent. */
    private fun drop() {
        dropped++
        if (dropped % 100 == 1) Log.i(TAG, "bridge dropped messages: $dropped")
    }

    companion object {
        const val OBJECT = "tsNative"
        private const val TAG = "Tendshelf"
        private const val RATE = 30
        private const val BACK_WAIT_MS = 150L
    }
}
