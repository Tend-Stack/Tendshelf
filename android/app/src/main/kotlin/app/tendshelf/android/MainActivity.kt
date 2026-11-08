package app.tendshelf.android

import android.Manifest
import android.app.Activity
import android.content.ComponentName
import android.content.Intent
import android.content.pm.PackageManager
import android.graphics.Color
import android.os.Build
import android.os.Bundle
import android.view.KeyEvent
import android.view.ViewGroup
import android.view.WindowManager
import android.webkit.WebView
import android.widget.FrameLayout
import android.window.OnBackInvokedCallback
import android.window.OnBackInvokedDispatcher
import app.tendshelf.shared.PageKey
import app.tendshelf.shared.pageKeyFor
import androidx.core.view.ViewCompat
import androidx.core.view.WindowCompat
import androidx.core.view.WindowInsetsCompat
import androidx.media3.session.MediaController
import androidx.media3.session.SessionToken
import com.google.common.util.concurrent.ListenableFuture

/**
 * The shell: the server's own pages in a WebView inside a padded parent, so the page never sits under a system bar
 * (decision D3: padding on the parent, not CSS). The activity holds no sound; it can be stopped or killed while the
 * service plays. The web player bar is a remote control for it through [Bridge]. Start-intent extras and the
 * debug probe exist in debug builds only: their code lives in the debug source set ([DebugHooks]).
 */
@androidx.media3.common.util.UnstableApi
class MainActivity : Activity() {
    private lateinit var web: WebView
    private lateinit var flow: ConnectFlow
    private lateinit var saver: FileSaver
    private lateinit var chooser: FileChooser
    private lateinit var store: TokenStore
    private lateinit var bridge: Bridge
    private lateinit var speech: Speech
    private lateinit var pages: StartPages
    private lateinit var signOut: SignOut
    private lateinit var parent: FrameLayout
    private var controller: ListenableFuture<MediaController>? = null
    private var remote: ControllerRemote? = null
    private var visible = false
    private var rendererLost = false
    private val main = android.os.Handler(android.os.Looper.getMainLooper())
    private var bridgeEnabled = true
    private var backCallback: OnBackInvokedCallback? = null
    private var backRegistered = false

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        DebugHooks.onActivityCreate(this)
        bridgeEnabled = DebugHooks.bridgeEnabled(intent)
        Offline.init(this)
        store = TokenStore(this)
        ProgressWriter.flushOnce(this, store.current()) // rows a killed process left (A5, D10)
        pages = StartPages(this)
        val api = AuthApi()
        web = WebView(this)
        WebHandle.current = web
        signOut = SignOut(web, store, api, pages, { getString(it) }) { remote?.stopAndClear() }
        flow = ConnectFlow(this, web, store, api, pages, signOut)
        speech = Speech(this)
        bridge = Bridge({ flow.state }, { store.origin }, ::applyTheme, ::keepScreenOn, speech)
        Offline.onChange = { id -> main.post { bridge.pushDownload(id) } }
        flow.onState = ::syncBridge
        saver = FileSaver(this) { store.origin }
        chooser = FileChooser(this)
        ShellWeb.configure(web, this, store, pages, { flow }, ::updateBack, { bridge.pageStarted() }, saver, chooser, ::onApp, { signOut.logoutSeen = true }, ::onRendererGone)

        parent = FrameLayout(this)
        parent.setBackgroundResource(R.color.ground)
        parent.addView(web, FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT))
        // Target 36 draws edge to edge. The parent pads for the bars, the cutout and the keyboard; the WebView never
        // overlaps one, so env(safe-area-inset-*) stays 0 on every WebView version.
        // androidx.core carries the API 30 types (WindowInsets.Type, getInsets(int), CONSUMED) back to API 28.
        ViewCompat.setOnApplyWindowInsetsListener(parent) { v, insets ->
            val i = insets.getInsets(WindowInsetsCompat.Type.systemBars() or WindowInsetsCompat.Type.displayCutout() or WindowInsetsCompat.Type.ime())
            v.setPadding(i.left, i.top, i.right, i.bottom)
            WindowInsetsCompat.CONSUMED
        }
        setContentView(parent)

        if (Build.VERSION.SDK_INT >= 33) {
            backCallback = OnBackInvokedCallback { back() }
            if (checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED) {
                requestPermissions(arrayOf(Manifest.permission.POST_NOTIFICATIONS), 1)
            }
        }
        connectService()
        val server = DebugHooks.serverExtra(intent)
        if (server != null) flow.onAddress(server) else flow.launch()
        handleLink(intent)
    }

    /**
     * An opened `tendshelf://open?link=` address (the server page's intent: hand-over). Anything can fire this intent, so it
     * only ever reaches the same address dialog as a pasted link: no token is read here, nothing connects without a tap.
     * The data is cleared afterwards so a rebuilt activity or a return from the recents list cannot repeat it.
     */
    private fun handleLink(i: Intent?) {
        val data = i?.data ?: return
        val fromHistory = (i.flags and Intent.FLAG_ACTIVITY_LAUNCHED_FROM_HISTORY) != 0
        i.data = null
        if (fromHistory || i.action != Intent.ACTION_VIEW || data.scheme != "tendshelf" || data.host != "open") return
        flow.onLink(data.toString())
    }

    /**
     * The WebView's renderer is gone. The view is dropped here and the activity is built again (a fresh WebView, the
     * connect flow and the bridge with it) as soon as it is on screen; the playback service was never touched.
     */
    private fun onRendererGone(dead: WebView) {
        if (dead !== web || rendererLost) return
        rendererLost = true
        WebHandle.current = null
        bridge.detach()
        parent.removeView(dead)
        dead.destroy()
        if (visible) main.post { recreate() }
    }

    override fun onStart() {
        super.onStart()
        visible = true
        if (rendererLost) {
            main.post { recreate() }
            return
        }
        remote?.pageVisible(true)
        bridge.onStart()
        // Downloads that waited for the app to open go on now, from the front (A7, D2).
        if (store.current() != null) Offline.onForeground()
    }

    override fun onStop() {
        visible = false
        remote?.pageVisible(false)
        bridge.onStop()
        super.onStop()
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        setIntent(intent)
        handleLink(intent)
    }

    @Deprecated("Deprecated in Java")
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        if (chooser.onResult(requestCode, resultCode, data)) return
        if (saver.onResult(requestCode, resultCode, data)) return
        @Suppress("DEPRECATION")
        super.onActivityResult(requestCode, resultCode, data)
    }

    // API 33+ goes through OnBackInvokedDispatcher (set up in updateBack); below that this is called.
    @Deprecated("Deprecated in Java")
    override fun onBackPressed() {
        bridge.askBack {
            if (web.canGoBack()) web.goBack() else {
                @Suppress("DEPRECATION")
                super.onBackPressed()
            }
        }
    }

    /** The page gets the first say (a sheet or dialog it can close), then history, then the app leaves. */
    private fun back() = bridge.askBack { if (web.canGoBack()) web.goBack() else moveTaskToBack(true) }

    /** The bundled Downloads page asked for something; the page is drawn again afterwards. */
    private fun onApp(nav: app.tendshelf.shared.Nav) {
        val r = remote
        when (nav) {
            app.tendshelf.shared.Nav.Downloads -> {}
            is app.tendshelf.shared.Nav.PlayBook -> r?.start(nav.bookId, null)
            app.tendshelf.shared.Nav.TogglePlay -> if (r?.snapshot()?.playing == true) r.pause() else r?.play()
            is app.tendshelf.shared.Nav.RemoveBook -> {
                if (r?.snapshot()?.bookId == nav.bookId) r.stopAndClear()
                Offline.remove(nav.bookId)
            }
            else -> return
        }
        pages.homeUrl = store.origin?.homeUrl() ?: ""
        // The removal and the start take a moment; the page is drawn after, from what the phone holds then.
        web.postDelayed({ web.loadUrl(pages.downloadsUrl) }, if (nav is app.tendshelf.shared.Nav.Downloads) 0L else 400L)
    }

    /** The bridge belongs to the connected state: attached before the first load there, gone at sign-out. */
    /** The screen stays on only while the reader page holds it and the app is in front (decision 0033). */
    private fun keepScreenOn(on: Boolean) {
        if (on) window.addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
        else window.clearFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
    }

    /** Volume keys turn pages only while the reader is in front, asked for them and the bridge is connected (decision 0033). */
    override fun dispatchKeyEvent(e: KeyEvent): Boolean =
        when (pageKeyFor(bridge.pageKeysArmed, e.keyCode, e.action, e.repeatCount)) {
            PageKey.Pass -> super.dispatchKeyEvent(e)
            PageKey.Swallow -> true
            PageKey.Next -> { bridge.pageKey(true); true }
            PageKey.Prev -> { bridge.pageKey(false); true }
        }

    private fun syncBridge(state: ConnectFlow.State) {
        when (state) {
            ConnectFlow.State.Connected -> {
                val origin = store.origin
                if (bridgeEnabled && origin != null) bridge.attach(web, origin)
            }
            ConnectFlow.State.NoServer -> bridge.detach()
            else -> {}
        }
        updateBack()
    }

    /** The page's theme colour paints the parent, which shows as the bar areas; icons follow its brightness. */
    private fun applyTheme(hex: String?) {
        if (hex == null) {
            parent.setBackgroundResource(R.color.ground)
            return
        }
        val color = Color.parseColor(hex)
        parent.setBackgroundColor(color)
        val light = Color.luminance(color) > 0.5f
        val bars = WindowCompat.getInsetsController(window, parent)
        bars.isAppearanceLightStatusBars = light
        bars.isAppearanceLightNavigationBars = light
    }

    /** The callback is registered only while there is history, so with none the system back (leave the app) applies. */
    private fun updateBack() {
        if (Build.VERSION.SDK_INT < 33) return
        val cb = backCallback ?: return
        val want = web.canGoBack() || bridge.active
        if (want && !backRegistered) {
            onBackInvokedDispatcher.registerOnBackInvokedCallback(OnBackInvokedDispatcher.PRIORITY_DEFAULT, cb)
            backRegistered = true
        } else if (!want && backRegistered) {
            onBackInvokedDispatcher.unregisterOnBackInvokedCallback(cb)
            backRegistered = false
        }
    }

    override fun onDestroy() {
        controller?.let { MediaController.releaseFuture(it) }
        WebHandle.current = null
        speech.shutdown()
        if (!rendererLost) web.destroy()
        super.onDestroy()
    }

    private fun connectService() {
        val token = SessionToken(this, ComponentName(this, PlaybackService::class.java))
        val future = MediaController.Builder(this, token)
            .setListener(object : MediaController.Listener {
                // Book, chapter and error travel as session extras, which the Player listener cannot see.
                override fun onExtrasChanged(controller: MediaController, extras: Bundle) {
                    remote?.extrasChanged()
                }
            })
            .buildAsync()
        controller = future
        future.addListener({
            val c = try {
                future.get()
            } catch (_: Exception) {
                return@addListener
            }
            val r = ControllerRemote(c)
            remote = r
            if (visible) r.pageVisible(true)
            // The page is drawn on a loader thread, which must not touch the controller: it reads this copy.
            r.observe { pages.playingNow = Pair(it.bookId, it.playing) }
            bridge.remote = r
        }, mainExecutor)
    }
}
