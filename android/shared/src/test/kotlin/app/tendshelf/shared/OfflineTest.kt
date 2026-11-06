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
}
