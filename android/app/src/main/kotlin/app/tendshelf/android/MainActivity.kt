package app.tendshelf.android

import android.Manifest
import android.app.Activity
import android.content.ComponentName
import android.content.Intent
import android.content.pm.PackageManager
import android.graphics.Color
import android.os.Build
import android.os.Bundle
import android.view.ViewGroup
import android.view.WindowInsets
import android.webkit.WebView
import android.widget.FrameLayout
import android.window.OnBackInvokedCallback
import android.window.OnBackInvokedDispatcher
import androidx.media3.session.MediaController
import androidx.media3.session.SessionToken
import com.google.common.util.concurrent.ListenableFuture

/**
 * The shell: the server's own pages in a WebView inside a padded parent, so the page never sits under a system bar
 * (decision D3: padding on the parent, not CSS). The activity holds no sound; it can be stopped or killed while the
 * service plays. The web player bar is a remote control for it through [Bridge]. Start-intent extras and the
 * debug probe exist in debug builds only: their code lives in the debug source set ([DebugHooks]).
 */
class MainActivity : Activity() {
    private lateinit var web: WebView
    private lateinit var flow: ConnectFlow
    private lateinit var saver: FileSaver
    private lateinit var chooser: FileChooser
    private lateinit var store: TokenStore
    private lateinit var bridge: Bridge
    private lateinit var parent: FrameLayout
    private var controller: ListenableFuture<MediaController>? = null
    private var remote: ControllerRemote? = null
    private var visible = false
    private var bridgeEnabled = true
    private var backCallback: OnBackInvokedCallback? = null
    private var backRegistered = false

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        DebugHooks.onActivityCreate(this)
        bridgeEnabled = DebugHooks.bridgeEnabled(intent)
        store = TokenStore(this)
        ProgressWriter.flushOnce(this, store.current()) // rows a killed process left (A5, D10)
        val pages = StartPages(this)
        val api = AuthApi()
        web = WebView(this)
        WebHandle.current = web
        val signOut = SignOut(web, store, api, pages, { getString(it) }) { remote?.stopAndClear() }
        flow = ConnectFlow(this, web, store, api, pages, signOut)
        bridge = Bridge({ flow.state }, { store.origin }, ::applyTheme)
        flow.onState = ::syncBridge
        saver = FileSaver(this) { store.origin }
        chooser = FileChooser(this)
        ShellWeb.configure(web, this, store, pages, { flow }, ::updateBack, { bridge.pageStarted() }, saver, chooser)

        parent = FrameLayout(this)
        parent.setBackgroundResource(R.color.ground)
        parent.addView(web, FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT))
        // Target 36 draws edge to edge. The parent pads for the bars, the cutout and the keyboard; the WebView never
        // overlaps one, so env(safe-area-inset-*) stays 0 on every WebView version.
        parent.setOnApplyWindowInsetsListener { v, insets ->
            val i = insets.getInsets(WindowInsets.Type.systemBars() or WindowInsets.Type.displayCutout() or WindowInsets.Type.ime())
            v.setPadding(i.left, i.top, i.right, i.bottom)
            WindowInsets.CONSUMED
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
    }

    override fun onStart() {
        super.onStart()
        visible = true
        remote?.pageVisible(true)
        bridge.onStart()
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

    /** The bridge belongs to the connected state: attached before the first load there, gone at sign-out. */
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
        val mask = android.view.WindowInsetsController.APPEARANCE_LIGHT_STATUS_BARS or android.view.WindowInsetsController.APPEARANCE_LIGHT_NAVIGATION_BARS
        window.insetsController?.setSystemBarsAppearance(if (light) mask else 0, mask)
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
        web.destroy()
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
            bridge.remote = r
        }, mainExecutor)
    }
}
