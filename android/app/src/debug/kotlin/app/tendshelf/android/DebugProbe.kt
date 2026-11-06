package app.tendshelf.android

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.os.Handler
import android.os.Looper
import android.util.Log

/**
 * Debug builds only (src/debug): emulator checks drive the WebView from `am broadcast`.
 *   -a app.tendshelf.DEBUG_EVAL --es js '<expression>'  runs it and logs the result under TsProbe
 *   -a app.tendshelf.DEBUG_READ --es op dump|outbox      logs the player or the outbox, changing no process state
 *   -a app.tendshelf.DEBUG_LOAD --es url <url>          calls loadUrl, which skips the navigation policy on purpose
 * The release APK has no copy of this class (checked with aapt2 on assembleRelease).
 */
class DebugProbe : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        if (intent.action == "app.tendshelf.DEBUG_TOKEN8") {
            // Test aid: the first 8 characters of the token go to a file in the app's private folder (read with run-as), so
            // the checks can search logs, prefs and pages for them. Never logged.
            val t = TokenStore(context).current()?.token
            java.io.File(context.filesDir, "probe-token8").writeText(t?.take(8) ?: "")
            return
        }
        if (intent.action == "app.tendshelf.DEBUG_READ") {
            // Read-only: a broadcast to the running process, never a service start (A5b invariant 3).
            if (intent.getStringExtra("op") in DebugBrowse.OPS) DebugBrowse.run(context, intent) else DebugHooks.read(intent.getStringExtra("op"))
            return
        }
        val web = WebHandle.current
        if (web == null) {
            Log.i(TAG, "no webview")
            return
        }
        Handler(Looper.getMainLooper()).post {
            when (intent.action) {
                "app.tendshelf.DEBUG_EVAL" -> web.evaluateJavascript(intent.getStringExtra("js") ?: "null") { Log.i(TAG, "eval=$it") }
                // Test aid: ends the page's session without touching the token, as an expired session would.
                "app.tendshelf.DEBUG_COOKIES_CLEAR" -> android.webkit.CookieManager.getInstance().removeAllCookies(null)
                "app.tendshelf.DEBUG_LOAD" -> web.loadUrl(intent.getStringExtra("url") ?: "about:blank")
            }
        }
    }

    private companion object {
        const val TAG = "TsProbe"
    }
}
