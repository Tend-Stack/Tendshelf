package app.tendshelf.android

import android.Manifest
import android.app.Activity
import android.content.ComponentName
import android.content.Intent
import android.content.pm.PackageManager
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
 * service plays. Start-intent extras work in debug builds only (for the emulator checks): `server` fills the address,
 * and `book`, `user` and `pass` start a book through A0's service command.
 */
class MainActivity : Activity() {
    private lateinit var web: WebView
    private lateinit var flow: ConnectFlow
    private lateinit var saver: FileSaver
    private lateinit var chooser: FileChooser
    private lateinit var store: TokenStore
    private var controller: ListenableFuture<MediaController>? = null
    private var backCallback: OnBackInvokedCallback? = null
    private var backRegistered = false

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        // Debug builds only: the shared emulator sits behind a lock screen with a credential nobody enters.
        if (BuildConfig.DEBUG) {
            setShowWhenLocked(true)
            setTurnScreenOn(true)
        }
        store = TokenStore(this)
        ProgressWriter.flushOnce(this, store.current()) // rows a killed process left (A5, D10)
        val pages = StartPages(this)
        val api = AuthApi()
        web = WebView(this)
        WebHandle.current = web
        val signOut = SignOut(web, store, api, pages) { getString(it) }
        flow = ConnectFlow(this, web, store, api, pages, signOut)
        saver = FileSaver(this) { store.origin }
        chooser = FileChooser(this)
        ShellWeb.configure(web, this, store, pages, { flow }, ::updateBack, saver, chooser)

        val parent = FrameLayout(this)
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
            backCallback = OnBackInvokedCallback { if (web.canGoBack()) web.goBack() }
            if (checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED) {
                requestPermissions(arrayOf(Manifest.permission.POST_NOTIFICATIONS), 1)
            }
        }
        connectService()
        val server = if (BuildConfig.DEBUG) intent.getStringExtra("server") else null
        if (server != null) flow.onAddress(server) else flow.launch()
        handle(intent)
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        setIntent(intent)
        handle(intent)
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
        if (web.canGoBack()) web.goBack() else {
            @Suppress("DEPRECATION")
            super.onBackPressed()
        }
    }

    /** The callback is registered only while there is history, so with none the system back (leave the app) applies. */
    private fun updateBack() {
        if (Build.VERSION.SDK_INT < 33) return
        val cb = backCallback ?: return
        val want = web.canGoBack()
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
        controller = MediaController.Builder(this, token).buildAsync()
    }

    /** A0's emulator hook, closed in release builds: the extras carry credentials, so a release APK ignores them. */
    private fun handle(intent: Intent) {
        if (!BuildConfig.DEBUG) return
        val book = intent.getStringExtra("book") ?: return
        val args = Bundle().apply {
            putString("server", intent.getStringExtra("server") ?: BuildConfig.DEFAULT_SERVER)
            putString("user", intent.getStringExtra("user"))
            putString("pass", intent.getStringExtra("pass"))
            putString("book", book)
        }
        val future = controller ?: return
        future.addListener({
            future.get().sendCustomCommand(PlaybackService.START_COMMAND, args)
        }, mainExecutor)
        intent.removeExtra("pass")
    }
}
