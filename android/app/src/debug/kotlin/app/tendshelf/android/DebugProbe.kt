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
 *   -a app.tendshelf.DEBUG_LOAD --es url <url>          calls loadUrl, which skips the navigation policy on purpose
 * The release APK has no copy of this class (checked with aapt2 on assembleRelease).
 */
class DebugProbe : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        val web = WebHandle.current
        if (web == null) {
            Log.i(TAG, "no webview")
            return
        }
        Handler(Looper.getMainLooper()).post {
            when (intent.action) {
                "app.tendshelf.DEBUG_EVAL" -> web.evaluateJavascript(intent.getStringExtra("js") ?: "null") { Log.i(TAG, "eval=$it") }
                "app.tendshelf.DEBUG_LOAD" -> web.loadUrl(intent.getStringExtra("url") ?: "about:blank")
            }
        }
    }

    private companion object {
        const val TAG = "TsProbe"
    }
}
