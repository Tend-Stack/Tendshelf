package app.tendshelf.android

import android.content.Context
import android.content.res.Configuration
import android.net.Uri
import android.webkit.WebResourceResponse
import androidx.webkit.WebViewAssetLoader
import java.io.ByteArrayInputStream

/** What the start page shows: the address to fill in and, after a failure, one sentence of plain words. */
data class StartState(val address: String = "", val note: String? = null)

/**
 * The bundled pages, served from https://appassets.androidplatform.net by WebViewAssetLoader: /start,  /offline and
 * the /assets/ folder (the server's own stylesheets and fonts, copied at build time). Native renders the two pages from a
 * template with every value HTML-escaped. The pages hold no script at all.
 */
class StartPages(private val context: Context) {
    @Volatile
    var state = StartState()

    @Volatile
    var offlineAddress = ""

    private val loader = WebViewAssetLoader.Builder()
        .setDomain(HOST)
        .addPathHandler("/start", WebViewAssetLoader.PathHandler { page("start.html", start()) })
        .addPathHandler("/offline", WebViewAssetLoader.PathHandler { page("offline.html", offline()) })
        .addPathHandler("/assets/", WebViewAssetLoader.AssetsPathHandler(context))
        .build()

    val startUrl: String get() = "https://$HOST/start"
    val offlineUrl: String get() = "https://$HOST/offline"

    fun intercept(uri: Uri): WebResourceResponse? = loader.shouldInterceptRequest(uri)

    private fun start(): Map<String, String> {
        val s = state
        val note = if (s.note == null) "" else "<p class=\"start__note\" role=\"alert\">${esc(s.note)}</p>"
        return mapOf(
            "TITLE" to esc(context.getString(R.string.start_title)),
            "BODY" to esc(context.getString(R.string.start_body)),
            "NOTE" to note,
            "LABEL" to esc(context.getString(R.string.start_label)),
            "HINT" to esc(context.getString(R.string.start_hint)),
            "BUTTON" to esc(context.getString(R.string.start_button)),
            "ADDRESS" to esc(s.address),
        )
    }

    private fun offline(): Map<String, String> = mapOf(
        "TITLE" to esc(context.getString(R.string.offline_title)),
        "BODY" to esc(context.getString(R.string.offline_body)),
        "BUTTON" to esc(context.getString(R.string.offline_retry)),
        "ADDRESS" to esc(offlineAddress),
    )

    private fun page(asset: String, values: Map<String, String>): WebResourceResponse {
        var html = context.assets.open(asset).bufferedReader().use { it.readText() }
        val dark = (context.resources.configuration.uiMode and Configuration.UI_MODE_NIGHT_MASK) == Configuration.UI_MODE_NIGHT_YES
        html = html.replace("{{THEME}}", if (dark) "dark" else "light")
        for ((k, v) in values) html = html.replace("{{$k}}", v)
        val r = WebResourceResponse("text/html", "utf-8", ByteArrayInputStream(html.toByteArray(Charsets.UTF_8)))
        r.responseHeaders = mapOf("Cache-Control" to "no-store")
        return r
    }

    private fun esc(s: String): String = buildString {
        for (ch in s) when (ch) {
            '&' -> append("&amp;")
            '<' -> append("&lt;")
            '>' -> append("&gt;")
            '"' -> append("&quot;")
            '\'' -> append("&#39;")
            else -> append(ch)
        }
    }

    private companion object {
        const val HOST = "appassets.androidplatform.net"
    }
}
