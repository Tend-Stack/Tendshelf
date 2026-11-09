package app.tendshelf.shared

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertIs
import kotlin.test.assertNull
import kotlin.test.assertTrue

class OfflineTest {
    private val paths = ApiPaths("http://10.0.2.2:8103/")
    private val mib = 1024L * 1024

    private fun file(id: String, idx: Int, size: Long, compat: Boolean = false, playable: Boolean = true) =
        AudioFile(id, idx, 0, 1000, "audio/mpeg", compat, playable, size)

    private fun book(vararg files: AudioFile, may: Boolean = true) =
        BookManifest("b1", "T", 3000, files.toList(), emptyList(), mayDownload = may)

    @Test
    fun cacheKeysAndUrls() {
        assertEquals("f1", cacheKey("f1", false))
        assertEquals("f1:c", cacheKey("f1", true))
        assertEquals("http://10.0.2.2:8103/api/v1/stream/f1?download=1", paths.downloadUrl("f1"))
        assertEquals("http://10.0.2.2:8103/api/v1/stream/f1?download=1&compat=1", paths.downloadUrl("f1", true))
        assertTrue(runCatching { paths.downloadUrl("a/b") }.isFailure)
    }

    @Test
    fun planRefusalsAndEdge() {
        val b = book(file("f1", 0, 10 * mib), file("f2", 1, 20 * mib))
        assertEquals(Plan.NotAllowed, downloadPlan(book(file("f1", 0, 1), may = false), paths, Long.MAX_VALUE))
        assertEquals(Plan.NothingToPlay, downloadPlan(book(file("f1", 0, 1, playable = false)), paths, Long.MAX_VALUE))
        // The 200 MiB spare is part of the need: exactly total + spare fits, one byte less does not.
        assertEquals(Plan.NoRoom, downloadPlan(b, paths, 30 * mib + SPARE_BYTES - 1))
        val ok = assertIs<Plan.Ok>(downloadPlan(b, paths, 30 * mib + SPARE_BYTES))
        assertEquals(30 * mib, ok.total)
        assertEquals(listOf("f1", "f2"), ok.files.map { it.fileId })
    }

    @Test
    fun planSkipsMissingFilesAndKeysCompat() {
        val b = book(file("f2", 1, 5, compat = true), file("f1", 0, 7, playable = false), file("f0", 0, 3))
        val ok = assertIs<Plan.Ok>(downloadPlan(b, paths, SPARE_BYTES + 100))
        assertEquals(listOf("f0", "f2"), ok.files.map { it.fileId })
        assertEquals(listOf("f0", "f2:c"), ok.files.map { it.cacheKey })
        assertTrue(ok.files[1].url.endsWith("?download=1&compat=1"))
        assertEquals(8, ok.total)
    }

    @Test
    fun completeMeansWholeAndMatching() {
        assertTrue(fileComplete(100, false, 100, 100))
        assertTrue(!fileComplete(100, false, 100, 99))
        assertTrue(!fileComplete(100, false, 90, 90)) // the server's file is not the one listed
        assertTrue(fileComplete(100, true, 90, 90)) // a rendition has its own length
        assertTrue(!fileComplete(100, true, 0, 0))
        assertTrue(!fileComplete(100, true, -1, 0))
    }

    @Test
    fun verdicts() {
        val old = book(file("f1", 0, 10))
        val moved = old.copy(progress = RemoteProgress(5, false, 1, "d", "x"))
        assertEquals(Verdict.REMOVE, checkVerdict(404, old, null))
        for (s in listOf(301, 302, 401, 403, 429, 500, 503, 0)) assertEquals(Verdict.NOTHING, checkVerdict(s, old, old), "$s")
        assertEquals(Verdict.NOTHING, checkVerdict(200, old, null))
        assertEquals(Verdict.NOTHING, checkVerdict(200, null, old))
        assertEquals(Verdict.SAME, checkVerdict(200, old, moved))
        assertEquals(Verdict.UPDATE, checkVerdict(200, old, old.copy(title = "New")))
        assertEquals(Verdict.STALE, checkVerdict(200, old, book(file("f1", 0, 11))))
        assertEquals(Verdict.STALE, checkVerdict(200, old, book(file("f9", 0, 10))))
        assertEquals(Verdict.STALE, checkVerdict(200, old, book(file("f1", 0, 10, compat = true))))
    }

    private val bundled = "https://$BUNDLED_HOST"
    private val origin = Origin.parse("http://10.0.2.2:8103")

    @Test
    fun bundledPathsCarryOnlyValidIds() {
        assertEquals(Nav.PlayBook("01HABC_d-1"), decide("$bundled/play?id=01HABC_d-1", true, origin, false, true))
        assertEquals(Nav.RemoveBook("x"), decide("$bundled/remove?id=x", true, origin, false, true))
        assertEquals(Nav.TogglePlay, decide("$bundled/toggle", true, origin, false, true))
        for (bad in listOf("", "?id=", "?id=a%20b", "?id=a/b", "?id=..%2Fx", "?id=" + "a".repeat(65), "?x=1", "?id=a%3Cb")) {
            assertEquals(Nav.Block, decide("$bundled/play$bad", true, origin, false, true), "play$bad")
            assertEquals(Nav.Block, decide("$bundled/remove$bad", true, origin, false, true), "remove$bad")
        }
    }

    @Test
    fun webDownloadsOpensTheBundledPageInTheMainFrameOnly() {
        assertEquals(Nav.Downloads, decide("http://10.0.2.2:8103/me/downloads", true, origin, false, false))
        assertEquals(Nav.Stay, decide("http://10.0.2.2:8103/me/downloads", false, origin, false, false))
        assertEquals(Nav.Stay, decide("http://10.0.2.2:8103/me", true, origin, false, false))
        assertEquals(Nav.External, decide("http://evil.example/me/downloads", true, origin, false, false))
    }

    @Test
    fun downloadRoomWordAndAnswer() {
        assertEquals(BridgeMessage.DownloadRoom, parseBridge("dl room"))
        for (bad in listOf("dl room ", "dl room b1", "dl  room", "dl ROOM")) assertNull(parseBridge(bad), bad)
        // The answer keeps the spare free, and never goes below zero.
        assertEquals("{\"t\":\"room\",\"bytes\":${1000L * 1024 * 1024 - SPARE_BYTES}}", encodeRoom(1000L * 1024 * 1024))
        assertEquals("{\"t\":\"room\",\"bytes\":0}", encodeRoom(SPARE_BYTES - 1))
        assertEquals("{\"t\":\"room\",\"bytes\":0}", encodeRoom(-5))
    }

    @Test
    fun downloadWords() {
        assertEquals(BridgeMessage.Download("add", "b1"), parseBridge("dl add b1"))
        assertEquals(BridgeMessage.Download("remove", "b-1_x"), parseBridge("dl remove b-1_x"))
        assertEquals(BridgeMessage.Download("ask", "b1"), parseBridge("dl ask b1"))
        for (bad in listOf("dl", "dl add", "dl add  b1", "dl get b1", "dl add b1 x", "dl add b/1", "dl add b?x", "DL add b1", "dl add " + "a".repeat(65), "dl add bé", "dl add b1 ", " dl add b1")) {
            assertNull(parseBridge(bad), bad)
        }
        assertTrue(parseBridge("dl add " + "a".repeat(64))!!.let { true })
    }

    @Test
    fun dlPush() {
        assertEquals("{\"t\":\"dl\",\"bookId\":\"b1\",\"state\":\"downloading\",\"pct\":40}", encodeDl("b1", "downloading", 40))
        assertEquals("{\"t\":\"dl\",\"bookId\":\"b1\",\"state\":\"none\",\"pct\":100}", encodeDl("b1", "<x>", 900))
    }

    private fun fp(planned: Long, bytes: Long, len: Long = planned, done: Boolean = false) = FileProgress(planned, len, bytes, done)

    @Test
    fun percentIsWholeBookBytesOverTotal() {
        // Three files of 10, 10 and 20 MiB: one whole, one half, one not started = 15 of 40 MiB.
        val files = listOf(fp(10 * mib, 10 * mib, done = true), fp(10 * mib, 5 * mib), fp(20 * mib, 0))
        assertEquals(DownloadPercent(37, true), downloadPercent(files, 0))
    }

    @Test
    fun percentNeverGoesBackOrReaches100() {
        assertEquals(60, downloadPercent(listOf(fp(10 * mib, mib)), 60).pct)
        val done = listOf(fp(10 * mib, 10 * mib), fp(10 * mib, 10 * mib))
        assertEquals(99, downloadPercent(done, 0).pct) // arrived but not marked whole: the Ready state decides
        assertEquals(99, downloadPercent(listOf(fp(10 * mib, 99 * mib)), 0).pct)
        assertEquals(99, downloadPercent(listOf(fp(10 * mib, 0)), 400).pct)
    }

    @Test
    fun percentToleratesUnknownSizes() {
        // A rendition with no listed size takes the average weight; it does not make the number jump.
        val a = downloadPercent(listOf(fp(10 * mib, 5 * mib), fp(0, 0, len = -1)), 0)
        assertEquals(DownloadPercent(25, true), a)
        // Its bytes arriving past an unknown length stop at 99 % of that file.
        val b = downloadPercent(listOf(fp(10 * mib, 10 * mib, done = true), fp(0, 50 * mib, len = -1)), 0)
        assertEquals(DownloadPercent(50, true), b)
        // Compat file: planned is the original's size, the announced length is the rendition's.
        val c = downloadPercent(listOf(fp(10 * mib, 3 * mib, len = 6 * mib)), 0)
        assertEquals(DownloadPercent(50, true), c)
    }

    @Test
    fun percentUnknownWhenNothingIsKnown() {
        assertEquals(DownloadPercent(0, false), downloadPercent(emptyList(), 0))
        assertEquals(DownloadPercent(12, false), downloadPercent(listOf(fp(0, 5, len = -1)), 12))
        assertEquals("{\"t\":\"dl\",\"bookId\":\"b\",\"state\":\"downloading\",\"pct\":0,\"known\":false}", encodeDl("b", "downloading", 0, false))
        assertEquals("{\"t\":\"dl\",\"bookId\":\"b\",\"state\":\"downloading\",\"pct\":5}", encodeDl("b", "downloading", 5))
    }

    // L8: the next book on its own (decision 0035).

    private val gib = 1024L * 1024 * 1024
    private fun row(id: String, state: OfflineState, size: Long = 1) =
        OfflineBook(id, "u", "T", "", 0, size, state, 0, 0)

    @Test
    fun l8NearEnd() {
        assertTrue(nearEnd(0, AUTO_NEAR_END_MS))
        assertEquals(false, nearEnd(0, AUTO_NEAR_END_MS + 1))
        assertTrue(nearEnd(1, AUTO_NEAR_END_MS + 1))
        assertEquals(false, nearEnd(0, -9223372036854775807L))
        assertEquals(false, nearEnd(0, 0))
        assertEquals(false, nearEnd(-1, 1000))
        assertTrue(nearEnd(0, 60 * 60 * 1000L))
    }

    @Test
    fun l8AutoStep() {
        assertEquals(AutoStep.NOTHING, autoStep(false, true, true, null))
        assertEquals(AutoStep.NOTHING, autoStep(true, false, true, null))
        assertEquals(AutoStep.WANT, autoStep(true, true, false, null))
        assertEquals(AutoStep.WANT, autoStep(true, true, false, 0L))
        assertEquals(AutoStep.ASK, autoStep(true, true, true, null))
        assertEquals(AutoStep.NOTHING, autoStep(true, true, true, AUTO_ASK_MS - 1))
        assertEquals(AutoStep.ASK, autoStep(true, true, true, AUTO_ASK_MS))
    }

    @Test
    fun l8AutoPlan() {
        val size = 100 * mib
        val b = book(file("f1", 0, size))
        val room = size + AUTO_SPARE_BYTES
        assertEquals(AutoPlan.Skip, autoDownloadPlan(book(file("f1", 0, 1), may = false), paths, Long.MAX_VALUE, 0))
        assertEquals(AutoPlan.Skip, autoDownloadPlan(book(file("f1", 0, 1, playable = false)), paths, Long.MAX_VALUE, 0))
        assertIs<AutoPlan.Go>(autoDownloadPlan(b, paths, room, 0))
        assertEquals(AutoPlan.NoRoom, autoDownloadPlan(b, paths, room - 1, 0))
        // below the person's own spare: NoRoom, whatever the book
        assertEquals(AutoPlan.NoRoom, autoDownloadPlan(b, paths, size + SPARE_BYTES - 1, 0))
        // the quota: kept + book at exactly 5 GiB goes, one byte over does not
        assertIs<AutoPlan.Go>(autoDownloadPlan(b, paths, 100 * gib, AUTO_TOTAL_BYTES - size))
        assertEquals(AutoPlan.NoRoom, autoDownloadPlan(b, paths, 100 * gib, AUTO_TOTAL_BYTES - size + 1))
        // a book over the whole quota is silent, with room and without
        val huge = book(file("f1", 0, AUTO_TOTAL_BYTES + 1))
        assertEquals(AutoPlan.Skip, autoDownloadPlan(huge, paths, 100 * gib, 0))
        assertEquals(AutoPlan.Skip, autoDownloadPlan(huge, paths, 1, 0))
        val exact = book(file("f1", 0, AUTO_TOTAL_BYTES))
        assertIs<AutoPlan.Go>(autoDownloadPlan(exact, paths, AUTO_TOTAL_BYTES + AUTO_SPARE_BYTES, 0))
    }

    @Test
    fun l8AutoBytesAndUnfinished() {
        val rows = OfflineState.entries.map { row("s${it.name}", it, 10) } + row("other", OfflineState.DOWNLOADING, 99)
        val marks = rows.map { it.bookId }.filter { it != "other" }.toSet() + "stale"
        assertEquals(10L * OfflineState.entries.size, autoBytes(rows, marks))
        val open = autoUnfinished(rows, marks).map { it.state }.toSet()
        assertEquals(setOf(OfflineState.QUEUED, OfflineState.DOWNLOADING, OfflineState.PAUSED), open)
        assertEquals(emptyList(), autoUnfinished(rows, emptySet()))
    }

    @Test
    fun l8PeekUrl() {
        assertEquals("http://10.0.2.2:8103/api/v1/queue/next?ended=bk1", paths.queuePeekUrl("bk1"))
        assertTrue(runCatching { paths.queuePeekUrl("a/b") }.isFailure)
        assertTrue(runCatching { paths.queuePeekUrl("") }.isFailure)
    }
}
