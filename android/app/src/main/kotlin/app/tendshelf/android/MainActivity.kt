package app.tendshelf.android

import android.Manifest
import android.app.Activity
import android.content.ComponentName
import android.content.Intent
import android.content.pm.PackageManager
import android.os.Build
import android.os.Bundle
import android.view.ViewGroup
import android.webkit.WebView
import android.webkit.WebViewClient
import androidx.media3.session.MediaController
import androidx.media3.session.SessionToken
import app.tendshelf.shared.ApiPaths
import com.google.common.util.concurrent.ListenableFuture

/**
 * A0 shell: the server's own page in a WebView, marked as the Android shell. The activity holds no
 * sound; it can be stopped or killed while the service plays. A start intent with extras `book`, `user`
 * and `pass` (and optionally `server`) starts that book, for the emulator checks only.
 */
class MainActivity : Activity() {
    private lateinit var web: WebView
    private var controller: ListenableFuture<MediaController>? = null

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        web = WebView(this).apply {
            settings.javaScriptEnabled = true
            settings.domStorageEnabled = true
            webViewClient = WebViewClient()
            layoutParams = ViewGroup.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT)
            // Target 36 draws edge to edge; keep the page clear of the bars (A2 does this properly).
            setOnApplyWindowInsetsListener { v, insets ->
                val bars = insets.getInsets(android.view.WindowInsets.Type.systemBars())
                v.setPadding(bars.left, bars.top, bars.right, bars.bottom)
                insets
            }
        }
        setContentView(web)
        if (Build.VERSION.SDK_INT >= 33 &&
            checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED
        ) {
            requestPermissions(arrayOf(Manifest.permission.POST_NOTIFICATIONS), 1)
        }
        val server = intent.getStringExtra("server") ?: BuildConfig.DEFAULT_SERVER
        if (savedInstanceState == null) web.loadUrl(ApiPaths(server).shellUrl) else web.restoreState(savedInstanceState)
        connectService()
        handle(intent)
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        setIntent(intent)
        handle(intent)
    }

    override fun onSaveInstanceState(outState: Bundle) {
        super.onSaveInstanceState(outState)
        web.saveState(outState)
    }

    @Deprecated("Deprecated in Java")
    override fun onBackPressed() {
        if (web.canGoBack()) web.goBack() else super.onBackPressed()
    }

    override fun onDestroy() {
        controller?.let { MediaController.releaseFuture(it) }
        web.destroy()
        super.onDestroy()
    }

    private fun connectService() {
        val token = SessionToken(this, ComponentName(this, PlaybackService::class.java))
        controller = MediaController.Builder(this, token).buildAsync()
    }

    private fun handle(intent: Intent) {
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
