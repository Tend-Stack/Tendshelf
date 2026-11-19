package app.tendshelf.android

import android.content.Context
import android.content.res.Configuration
import android.net.Uri
import android.webkit.WebResourceResponse
import androidx.webkit.WebViewAssetLoader
import androidx.media3.common.util.UnstableApi
import app.tendshelf.shared.AutoShowRow
import app.tendshelf.shared.DownloadRow
import app.tendshelf.shared.DownloadsInput
import app.tendshelf.shared.DownloadsWords
import app.tendshelf.shared.downloadsPage
import app.tendshelf.shared.htmlEscape
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
     * The one list of downloaded books, drawn by the shared renderer with every value escaped and no script. [offline] adds
     * the "can't reach your server" words and Try again above it. While a book downloads the page reloads itself every 2 s.
     */
    private fun downloads(offline: Boolean): Map<String, String> {
        val (playingId, playing) = playingNow
        val rows = Offline.store.all().map {
            DownloadRow(it, Offline.view(it.bookId), Offline.coverBytes(it.bookId) != null, Offline.readable(it.bookId), Offline.hasAudio(it.bookId), Offline.isAuto(it.bookId))
        }
        val on = EpisodeAuto.enabled()
        val shows = EpisodeAuto.knownShows().map { AutoShowRow(it.id, it.title, it.keepLatest, it.id in on) }
        val input = DownloadsInput(
            rows = rows,
            shows = shows,
            playingId = playingId,
            playing = playing,
            unmetered = Offline.unmetered(),
            autoNoRoom = Offline.autoNoRoomNoted(),
            offline = offline,
            offlineAddress = offlineAddress,
            homeUrl = homeUrl.ifEmpty { startUrl },
            readBase = homeOrigin,
        )
        return downloadsPage(input, downloadsWords())
    }

    /** The words raw, as `strings.xml` holds them; the shared renderer fills `%1$d` itself. */
    private fun downloadsWords() = DownloadsWords(
        title = context.getString(R.string.downloads_title),
        lead = context.getString(R.string.downloads_lead),
        empty = context.getString(R.string.downloads_empty),
        emptyHow = context.getString(R.string.downloads_empty_how),
        active = context.getString(R.string.downloads_active),
        ready = context.getString(R.string.downloads_ready),
        percent = context.getString(R.string.downloads_percent),
        working = context.getString(R.string.downloads_working),
        waiting = context.getString(R.string.downloads_waiting),
        pausedRow = context.getString(R.string.downloads_paused_row),
        failedRow = context.getString(R.string.downloads_failed_row),
        staleRow = context.getString(R.string.downloads_stale_row),
        play = context.getString(R.string.downloads_play),
        read = context.getString(R.string.downloads_read),
        pause = context.getString(R.string.downloads_pause),
        remove = context.getString(R.string.downloads_remove),
        library = context.getString(R.string.downloads_library),
        autoRow = context.getString(R.string.downloads_auto_row),
        autoWaiting = context.getString(R.string.downloads_auto_waiting),
        autoNoRoom = context.getString(R.string.downloads_auto_no_room),
        podcasts = context.getString(R.string.downloads_podcasts),
        podcastsLead = context.getString(R.string.downloads_podcasts_lead),
        podcastOnRow = context.getString(R.string.downloads_podcast_on_row),
        podcastOffRow = context.getString(R.string.downloads_podcast_off_row),
        turnOn = context.getString(R.string.downloads_podcast_turn_on),
        turnOff = context.getString(R.string.downloads_podcast_turn_off),
        offlineTitle = context.getString(R.string.offline_title),
        offlineBody = context.getString(R.string.offline_body),
        offlineRetry = context.getString(R.string.offline_retry),
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

    private fun esc(s: String): String = htmlEscape(s)

    private companion object {
        const val HOST = "appassets.androidplatform.net"
    }
}
