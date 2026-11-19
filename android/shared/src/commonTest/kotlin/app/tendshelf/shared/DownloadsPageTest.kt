package app.tendshelf.shared

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class DownloadsPageTest {
    private val raw = mapOf(
        "downloads_title" to "Downloads",
        "downloads_lead" to "Books & episodes saved here play without a connection.",
        "downloads_empty" to "No downloads yet",
        "downloads_empty_how" to "Download a book or an episode to listen without a connection.",
        "downloads_active" to "Downloading",
        "downloads_ready" to "Ready offline",
        "downloads_percent" to "Downloading · %1\$d%%",
        "downloads_working" to "Downloading…",
        "downloads_waiting" to "Waiting",
        "downloads_paused_row" to "Paused",
        "downloads_failed_row" to "Didn't download. Open the book to try again.",
        "downloads_stale_row" to "Needs updating",
        "downloads_play" to "Play",
        "downloads_read" to "Read",
        "downloads_pause" to "Pause",
        "downloads_remove" to "Remove",
        "downloads_library" to "Open your library",
        "downloads_auto_row" to "Downloaded on its own",
        "downloads_auto_waiting" to "Waiting for Wi-Fi",
        "downloads_auto_no_room" to "An automatic download was skipped because there isn't enough room.",
        "downloads_podcasts" to "Podcasts on this phone",
        "downloads_podcasts_lead" to "Turn a show on and its newest episodes download on their own.",
        "downloads_podcast_on_row" to "Downloading new episodes on Wi-Fi · keeps the latest %1\$d",
        "downloads_podcast_off_row" to "Off",
        "downloads_podcast_turn_on" to "Turn on",
        "downloads_podcast_turn_off" to "Turn off",
        "offline_title" to "Can't reach your server",
        "offline_body" to "Check your connection and that the server is running.",
        "offline_retry" to "Try again",
    )

    private val words = DownloadsWords(
        title = raw.getValue("downloads_title"), lead = raw.getValue("downloads_lead"),
        empty = raw.getValue("downloads_empty"), emptyHow = raw.getValue("downloads_empty_how"),
        active = raw.getValue("downloads_active"), ready = raw.getValue("downloads_ready"),
        percent = raw.getValue("downloads_percent"), working = raw.getValue("downloads_working"),
        waiting = raw.getValue("downloads_waiting"), pausedRow = raw.getValue("downloads_paused_row"),
        failedRow = raw.getValue("downloads_failed_row"), staleRow = raw.getValue("downloads_stale_row"),
        play = raw.getValue("downloads_play"), read = raw.getValue("downloads_read"),
        pause = raw.getValue("downloads_pause"), remove = raw.getValue("downloads_remove"),
        library = raw.getValue("downloads_library"), autoRow = raw.getValue("downloads_auto_row"),
        autoWaiting = raw.getValue("downloads_auto_waiting"), autoNoRoom = raw.getValue("downloads_auto_no_room"),
        podcasts = raw.getValue("downloads_podcasts"), podcastsLead = raw.getValue("downloads_podcasts_lead"),
        podcastOnRow = raw.getValue("downloads_podcast_on_row"), podcastOffRow = raw.getValue("downloads_podcast_off_row"),
        turnOn = raw.getValue("downloads_podcast_turn_on"), turnOff = raw.getValue("downloads_podcast_turn_off"),
        offlineTitle = raw.getValue("offline_title"), offlineBody = raw.getValue("offline_body"),
        offlineRetry = raw.getValue("offline_retry"),
    )

    private fun book(id: String, state: OfflineState, title: String = "Title $id", author: String = "Author") =
        OfflineBook(id, "u", title, author, 1000, 1000, state, 0, 0)

    private fun row(
        id: String,
        state: OfflineState,
        pct: Int = 100,
        known: Boolean = true,
        cover: Boolean = false,
        readable: Boolean = false,
        audio: Boolean = true,
        auto: Boolean = false,
        title: String = "Title $id",
        author: String = "Author",
    ): DownloadRow {
        val b = book(id, state, title, author)
        return DownloadRow(b, dlView(b, DownloadPercent(pct, known)), cover, readable, audio, auto)
    }

    private fun input(
        rows: List<DownloadRow> = emptyList(),
        shows: List<AutoShowRow> = emptyList(),
        playingId: String = "",
        playing: Boolean = false,
        unmetered: Boolean = true,
        noRoom: Boolean = false,
        offline: Boolean = false,
        address: String = "",
        readBase: String = "",
    ) = DownloadsInput(rows, shows, playingId, playing, unmetered, noRoom, offline, address, "https://s.example/", readBase)

    private val rowHead = "<li class=\"ts-row dl__row\">"
    private val rowText = "<span class=\"ts-row__text\"><span class=\"ts-row__title\">"

    @Test
    fun empty() {
        val m = downloadsPage(input(), words)
        assertEquals("<p><strong>No downloads yet</strong><br>Download a book or an episode to listen without a connection.</p>", m["LIST"])
        assertEquals("", m["NOTE"])
        assertEquals("", m["REFRESH"])
        assertEquals("Books &amp; episodes saved here play without a connection.", m["LEAD"])
        assertEquals("https://s.example/", m["HOMEURL"])
    }

    @Test
    fun readyBookWithCoverPlaying() {
        val m = downloadsPage(input(listOf(row("b1", OfflineState.READY, cover = true)), playingId = "b1", playing = true), words)
        assertEquals(
            "<h2>Ready offline · 1</h2><ul class=\"dl__list\">" + rowHead + "<img class=\"dl__cover\" alt=\"\" src=\"/covers/b1\">" +
                rowText + "Title b1</span><span class=\"ts-row__meta\">Author</span></span><span class=\"dl__actions\">" +
                "<a class=\"ts-btn ts-btn--secondary\" href=\"/toggle\">Pause</a>" +
                "<a class=\"ts-btn ts-btn--quiet\" href=\"/remove?id=b1\">Remove</a></span></li></ul>",
            m["LIST"],
        )
        assertEquals("", m["REFRESH"])
        // Paused playback of the same book still toggles, and says Play.
        val paused = downloadsPage(input(listOf(row("b1", OfflineState.READY)), playingId = "b1"), words)
        assertTrue("href=\"/toggle\">Play</a>" in paused.getValue("LIST"))
        val other = downloadsPage(input(listOf(row("b1", OfflineState.READY)), playingId = "b2", playing = true), words)
        assertTrue("href=\"/play?id=b1\">Play</a>" in other.getValue("LIST"))
    }

    @Test
    fun downloadingKnownAndUnknown() {
        val m = downloadsPage(input(listOf(row("b1", OfflineState.DOWNLOADING, 42), row("b2", OfflineState.DOWNLOADING, 7, known = false))), words)
        assertEquals(
            "<h2>Downloading · 2</h2><ul class=\"dl__list\">" +
                rowHead + rowText + "Title b1</span><span class=\"ts-row__meta\">Author · Downloading · 42%</span>" +
                "<progress class=\"dl__bar\" max=\"100\" value=\"42\" aria-label=\"Downloading\"></progress></span>" +
                "<span class=\"dl__actions\"><a class=\"ts-btn ts-btn--quiet\" href=\"/remove?id=b1\">Remove</a></span></li>" +
                rowHead + rowText + "Title b2</span><span class=\"ts-row__meta\">Author · Downloading…</span></span>" +
                "<span class=\"dl__actions\"><a class=\"ts-btn ts-btn--quiet\" href=\"/remove?id=b2\">Remove</a></span></li></ul>",
            m["LIST"],
        )
        assertEquals("<meta http-equiv=\"refresh\" content=\"2\">", m["REFRESH"])
    }

    @Test
    fun autoRowsWhenNotUnmetered() {
        val m = downloadsPage(
            input(
                listOf(
                    row("b1", OfflineState.FAILED, auto = true),
                    row("b2", OfflineState.STALE, auto = true, audio = false),
                    row("b3", OfflineState.QUEUED, auto = true),
                    row("b4", OfflineState.READY, auto = true),
                ),
                unmetered = false, noRoom = true,
            ),
            words,
        )
        val list = m.getValue("LIST")
        assertTrue("Author · Didn't download. Open the book to try again." .replace("'", "&#39;") in list)
        assertTrue("Author · Needs updating" in list)
        assertTrue("Author · Waiting for Wi-Fi" in list)
        assertTrue("Author · Downloaded on its own" in list)
        // The stale book has no audio, so no Play; the ready one has it.
        assertFalse("/play?id=b2" in list)
        assertTrue("/play?id=b4" in list)
        assertEquals("<p class=\"start__note\" role=\"status\">An automatic download was skipped because there isn&#39;t enough room.</p>", m["NOTE"])
        assertEquals("<meta http-equiv=\"refresh\" content=\"2\">", m["REFRESH"])
        // Unmetered: a queued automatic book says plain Waiting.
        val u = downloadsPage(input(listOf(row("b3", OfflineState.QUEUED, auto = true))), words)
        assertTrue("Author · Waiting</span>" in u.getValue("LIST"))
    }

    @Test
    fun showsOnAndOff() {
        val m = downloadsPage(
            input(shows = listOf(AutoShowRow("s1", "Show <1>", 3, true), AutoShowRow("s2", "Two", 1, false), AutoShowRow("../x", "Bad", 1, true))),
            words,
        )
        assertEquals(
            "<p><strong>No downloads yet</strong><br>Download a book or an episode to listen without a connection.</p>" +
                "<h2>Podcasts on this phone</h2><p>Turn a show on and its newest episodes download on their own.</p><ul class=\"dl__list\">" +
                rowHead + rowText + "Show &lt;1&gt;</span><span class=\"ts-row__meta\">Downloading new episodes on Wi-Fi · keeps the latest 3</span></span>" +
                "<span class=\"dl__actions\"><a class=\"ts-btn ts-btn--quiet\" href=\"/autoshow?id=s1&amp;on=0\">Turn off</a></span></li>" +
                rowHead + rowText + "Two</span><span class=\"ts-row__meta\">Off</span></span>" +
                "<span class=\"dl__actions\"><a class=\"ts-btn ts-btn--secondary\" href=\"/autoshow?id=s2&amp;on=1\">Turn on</a></span></li></ul>",
            m["LIST"],
        )
    }

    @Test
    fun offlineNoteEscapesTheAddress() {
        val m = downloadsPage(input(offline = true, address = "http://h\"<&"), words)
        assertEquals(
            "<p class=\"start__note\" role=\"status\">Can&#39;t reach your server. Check your connection and that the server is running.</p>" +
                "<form method=\"get\" action=\"/connect\"><input type=\"hidden\" name=\"address\" value=\"http://h&quot;&lt;&amp;\">" +
                "<button type=\"submit\" class=\"ts-btn ts-btn--primary ts-btn--lg ts-btn--block\">Try again</button></form>",
            m["NOTE"],
        )
    }

    @Test
    fun readLinkNeedsABase() {
        val r = listOf(row("b1", OfflineState.READY, readable = true))
        assertTrue("<a class=\"ts-btn ts-btn--secondary\" href=\"https://s.example/books/b1/read\">Read</a>" in downloadsPage(input(r, readBase = "https://s.example"), words).getValue("LIST"))
        assertFalse("/read" in downloadsPage(input(r), words).getValue("LIST"))
    }

    @Test
    fun everyValueIsEscaped() {
        val m = downloadsPage(input(listOf(row("b1", OfflineState.READY, title = "<b>&\"'", author = "<i>")), playingId = "b1"), words)
        val list = m.getValue("LIST")
        assertTrue("&lt;b&gt;&amp;&quot;&#39;" in list)
        assertTrue("&lt;i&gt;" in list)
        assertFalse("<b>" in list)
    }

    @Test
    fun fillWordFormats() {
        assertEquals("Downloading · 42%", fillWord("Downloading · %1\$d%%", "42"))
        assertEquals("b a", fillWord("%2\$s %1\$s", "a", "b"))
        assertEquals("100%", fillWord("100%%"))
        assertEquals("%1\$d left", fillWord("%1\$d left"))
        assertEquals("plain", fillWord("plain"))
    }
}
