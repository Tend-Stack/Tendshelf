package app.tendshelf.android

import android.content.Context
import android.content.res.Configuration
import android.net.Uri
import android.webkit.WebResourceResponse
import androidx.webkit.WebViewAssetLoader
import androidx.media3.common.util.UnstableApi
import app.tendshelf.shared.OfflineBook
import app.tendshelf.shared.OfflineState
import app.tendshelf.shared.validId
import java.io.ByteArrayInputStream

/** What the start page shows: the address to fill in and, after a failure, one sentence of plain words. */
data class StartState(val address: String = "", val note: String? = null)

/**
 * The bundled pages, served from https://appassets.androidplatform.net by WebViewAssetLoader: /start,  /offline and
 * the /assets/ folder (the server's own stylesheets and fonts, copied at build time). Native renders the two pages from a
 * template with every value HTML-escaped. The pages hold no script at all.
 */
@UnstableApi
class StartPages(private val context: Context) {
    /** The playing book and whether it is playing, so its row offers Pause. Set by the activity. */
    @Volatile
    var playingNow: Pair<String, Boolean> = Pair("", false)

    /** The server's home address, for the library link; empty before a server is chosen. */
    @Volatile
    var homeUrl = ""

    /** The server's origin (scheme, host, port) for a book's Read link; empty before a server is chosen. */
    @Volatile
    var homeOrigin = ""

    @Volatile
    var state = StartState()

    /** The join page's note (and field, always empty: a pasted link is never put back on a page). */
    @Volatile
    var joinState = StartState()

    @Volatile
    var offlineAddress = ""

    private val loader = WebViewAssetLoader.Builder()
        .setDomain(HOST)
        // Handlers match by prefix (which must end in a slash), in this order: the assets folder first, then one handler
        // for the two pages. Anything else gets no answer and the WebView would go to the network, which no page asks for.
        .addPathHandler("/assets/", WebViewAssetLoader.AssetsPathHandler(context))
        .addPathHandler("/covers/", WebViewAssetLoader.PathHandler { id -> cover(id) })
        .addPathHandler("/", WebViewAssetLoader.PathHandler { path ->
            when (path) {
                "start" -> if (state.address.isEmpty() && state.note == null) page("choose.html", choose()) else page("start.html", start())
                "server" -> page("start.html", start())
                "join" -> page("join.html", join())
                "offline" -> page("offline.html", downloads(true))
                "downloads" -> page("offline.html", downloads(false))
                else -> null
            }
        })
        .build()

    val startUrl: String get() = "https://$HOST/start"
    val joinUrl: String get() = "https://$HOST/join"
    val offlineUrl: String get() = "https://$HOST/offline"
    val downloadsUrl: String get() = "https://$HOST/downloads"

    fun intercept(uri: Uri): WebResourceResponse? = loader.shouldInterceptRequest(uri)

    private fun choose(): Map<String, String> = mapOf(
        "TITLE" to esc(context.getString(R.string.start_title)),
        "SERVER" to esc(context.getString(R.string.start_server)),
        "INVITED" to esc(context.getString(R.string.start_invited)),
        "SCAN" to esc(context.getString(R.string.start_scan_hint)),
    )

    private fun join(): Map<String, String> {
        val note = joinState.note?.let { "<p class=\"start__note\" role=\"alert\">${esc(it)}</p>" } ?: ""
        return mapOf(
            "TITLE" to esc(context.getString(R.string.start_invited)),
            "NOTE" to note,
            "LABEL" to esc(context.getString(R.string.join_label)),
            "HINT" to esc(context.getString(R.string.join_hint)),
            "BUTTON" to esc(context.getString(R.string.join_button)),
            "SERVER" to esc(context.getString(R.string.start_server)),
        )
    }

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
            "INVITED" to esc(context.getString(R.string.start_invited)),
        )
    }

    /** A downloaded book's cover, from its own file; an id outside the charset is no file at all. */
    private fun cover(id: String): WebResourceResponse? {
        if (!validId(id)) return null
        val bytes = Offline.coverBytes(id) ?: return null
        return WebResourceResponse("image/jpeg", null, ByteArrayInputStream(bytes)).also { it.responseHeaders = mapOf("Cache-Control" to "no-store") }
    }

    /**
     * The one list of downloaded books, drawn by native with every value escaped and no script. [offline] adds the
     * "can't reach your server" words and Try again above it. While a book downloads the page reloads itself every 5 s.
     */
    private fun downloads(offline: Boolean): Map<String, String> {
        val books = Offline.store.all()
        val (playingId, playing) = playingNow
        val active = books.filter { it.state == OfflineState.QUEUED || it.state == OfflineState.DOWNLOADING || it.state == OfflineState.PAUSED || it.state == OfflineState.FAILED }
        val ready = books.filter { it.state == OfflineState.READY || it.state == OfflineState.STALE }
        val list = StringBuilder()
        if (books.isEmpty()) {
            list.append("<p><strong>${esc(context.getString(R.string.downloads_empty))}</strong><br>${esc(context.getString(R.string.downloads_empty_how))}</p>")
        }
        section(list, context.getString(R.string.downloads_active), active, playingId, playing)
        section(list, context.getString(R.string.downloads_ready), ready, playingId, playing)
        shows(list)
        val noRoom = if (Offline.autoNoRoomNoted()) "<p class=\"start__note\" role=\"status\">${esc(context.getString(R.string.downloads_auto_no_room))}</p>" else ""
        val note = noRoom + if (!offline) "" else
            "<p class=\"start__note\" role=\"status\">${esc(context.getString(R.string.offline_title))}. ${esc(context.getString(R.string.offline_body))}</p>" +
                "<form method=\"get\" action=\"/connect\"><input type=\"hidden\" name=\"address\" value=\"${esc(offlineAddress)}\">" +
                "<button type=\"submit\" class=\"ts-btn ts-btn--primary ts-btn--lg ts-btn--block\">${esc(context.getString(R.string.offline_retry))}</button></form>"
        return mapOf(
            "TITLE" to esc(context.getString(R.string.downloads_title)),
            "LEAD" to esc(context.getString(R.string.downloads_lead)),
            "NOTE" to note,
            "LIST" to list.toString(),
            "LIBRARY" to esc(context.getString(R.string.downloads_library)),
            "HOMEURL" to esc(homeUrl.ifEmpty { startUrl }),
            "REFRESH" to if (active.any { it.state == OfflineState.DOWNLOADING || it.state == OfflineState.QUEUED }) "<meta http-equiv=\"refresh\" content=\"2\">" else "",
        )
    }

    /** "Podcasts on this phone" (A2): one row per followed show with its on/off link; the choice is kept on the phone only. */
    private fun shows(out: StringBuilder) {
        val known = EpisodeAuto.knownShows()
        if (known.isEmpty()) return
        val on = EpisodeAuto.enabled()
        out.append("<h2>${esc(context.getString(R.string.downloads_podcasts))}</h2>")
        out.append("<p class=\"start__note\">${esc(context.getString(R.string.downloads_podcasts_lead))}</p><ul class=\"dl__list\">")
        for (s in known) {
            if (!validId(s.id)) continue
            val enabled = s.id in on
            val meta = if (enabled) context.getString(R.string.downloads_podcast_on_row, s.keepLatest) else context.getString(R.string.downloads_podcast_off_row)
            val label = context.getString(if (enabled) R.string.downloads_podcast_turn_off else R.string.downloads_podcast_turn_on)
            out.append("<li class=\"ts-row dl__row\"><span class=\"ts-row__text\"><span class=\"ts-row__title\">${esc(s.title)}</span><span class=\"ts-row__meta\">${esc(meta)}</span></span>")
            out.append("<span class=\"dl__actions\"><a class=\"ts-btn ts-btn--${if (enabled) "quiet" else "secondary"}\" href=\"/autoshow?id=${esc(s.id)}&amp;on=${if (enabled) 0 else 1}\">${esc(label)}</a></span></li>")
        }
        out.append("</ul>")
    }

    private fun section(out: StringBuilder, heading: String, books: List<OfflineBook>, playingId: String, playing: Boolean) {
        if (books.isEmpty()) return
        out.append("<h2>${esc(heading)} \u00b7 ${books.size}</h2><ul class=\"dl__list\">")
        for (b in books) {
            val id = esc(b.bookId)
            val here = b.bookId == playingId
            val canPlay = b.state == OfflineState.READY || b.state == OfflineState.STALE
            val meta = esc(listOf(b.author, stateWords(b)).filter { it.isNotEmpty() }.joinToString(" \u00b7 "))
            // A bar only while the percent is known; unknown shows the plain words, so nothing animates here.
            val bar = Offline.view(b.bookId).takeIf { b.state == OfflineState.DOWNLOADING && it.known }?.let {
                "<progress class=\"dl__bar\" max=\"100\" value=\"${it.pct}\" aria-label=\"${esc(context.getString(R.string.downloads_active))}\"></progress>"
            }.orEmpty()
            val cover = if (Offline.coverBytes(b.bookId) != null) "<img class=\"dl__cover\" alt=\"\" src=\"/covers/$id\">" else ""
            out.append("<li class=\"ts-row dl__row\">$cover")
            out.append("<span class=\"ts-row__text\"><span class=\"ts-row__title\">${esc(b.title)}</span><span class=\"ts-row__meta\">$meta</span>$bar</span>")
            out.append("<span class=\"dl__actions\">")
            // The two formats are two plain buttons: this page shows no place and no time, so neither one is "the resume button".
            if (Offline.readable(b.bookId) && homeOrigin.isNotEmpty()) {
                out.append("<a class=\"ts-btn ts-btn--secondary\" href=\"${esc(homeOrigin)}/books/$id/read\">${esc(context.getString(R.string.downloads_read))}</a>")
            }
            if (canPlay && Offline.hasAudio(b.bookId)) {
                val href = if (here) "/toggle" else "/play?id=$id"
                val label = if (here && playing) R.string.downloads_pause else R.string.downloads_play
                out.append("<a class=\"ts-btn ts-btn--secondary\" href=\"$href\">${esc(context.getString(label))}</a>")
            }
            out.append("<a class=\"ts-btn ts-btn--quiet\" href=\"/remove?id=$id\">${esc(context.getString(R.string.downloads_remove))}</a>")
            out.append("</span></li>")
        }
        out.append("</ul>")
    }

    private fun stateWords(b: OfflineBook): String = when (b.state) {
        // A book downloaded on its own says so, and says when it waits for Wi-Fi (decision 0035).
        OfflineState.READY, OfflineState.QUEUED, OfflineState.PAUSED ->
            if (Offline.isAuto(b.bookId)) autoWords(b) else plainWords(b)
        else -> plainWords(b)
    }

    private fun autoWords(b: OfflineBook): String = when {
        b.state == OfflineState.READY -> context.getString(R.string.downloads_auto_row)
        !Offline.unmetered() -> context.getString(R.string.downloads_auto_waiting)
        else -> plainWords(b)
    }

    private fun plainWords(b: OfflineBook): String = when (b.state) {
        OfflineState.QUEUED -> context.getString(R.string.downloads_waiting)
        OfflineState.DOWNLOADING -> Offline.view(b.bookId).let { if (it.known) context.getString(R.string.downloads_percent, it.pct) else context.getString(R.string.downloads_working) }
        OfflineState.PAUSED -> context.getString(R.string.downloads_paused_row)
        OfflineState.FAILED -> context.getString(R.string.downloads_failed_row)
        OfflineState.STALE -> context.getString(R.string.downloads_stale_row)
        OfflineState.READY -> ""
    }

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
