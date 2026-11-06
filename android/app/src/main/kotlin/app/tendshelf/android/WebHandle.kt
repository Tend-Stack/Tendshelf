package app.tendshelf.android

import android.webkit.WebView

/** The live WebView, for the debug probe only. Set while the activity exists; release code never reads it. */
object WebHandle {
    @Volatile
    var current: WebView? = null
}
