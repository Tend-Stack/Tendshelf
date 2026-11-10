package app.tendshelf.shared

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertIs
import kotlin.test.assertTrue

class EpisodeDownloadsTest {
    private val paths = ApiPaths("http://10.0.2.2:8103")
    private val gib = 1024L * 1024 * 1024

    private fun doc(id: String = "pe_a", bytes: Long = 30_000_000, mime: String = "audio/mpeg") = EpisodeDoc(
        id, "ps_1", "T", "Show", "", 0, "/api/v1/podcasts/audio/$id", mime, bytes, "", ShowSettings(null, 0, 0), 0, null,
    )

    private fun row(id: String, show: String = "ps_1", at: Long = 0, size: Long = 10, played: Boolean = false, kind: String = KIND_EPISODE, state: OfflineState = OfflineState.READY) =
        OfflineBook(id, "u", "T", "Show", 0, size, state, 0, 0, kind, show, at, played)

    private fun card(id: String, at: Long, played: Boolean = false) = EpisodeCard(id, "ps_1", "T", "Show", 0, at, 0, played, false)

    @Test
    fun theFileAddressAsksForAKeptCopyAndNeedsNoRangeToStart() {
        val plan = assertIs<Plan.Ok>(episodePlan(doc(), paths, 10 * gib))
        assertEquals("http://10.0.2.2:8103/api/v1/podcasts/audio/pe_a?download=1", plan.files.single().url)
        assertEquals("pe_a", plan.files.single().cacheKey)
        assertTrue(runCatching { paths.episodeDownloadUrl("a/b") }.isFailure)
    }

    @Test
    fun aPlanChecksTheRoomButTrustsNoAnnouncedSize() {
        assertEquals(Plan.NoRoom, episodePlan(doc(bytes = 500 * 1024 * 1024), paths, 600 * 1024 * 1024))
        // The feed announced nothing: only the spare room counts.
        assertIs<Plan.Ok>(episodePlan(doc(bytes = 0), paths, SPARE_BYTES + 1))
        assertEquals(Plan.NoRoom, episodePlan(doc(bytes = 0), paths, SPARE_BYTES - 1))
        assertEquals(Plan.NothingToPlay, episodePlan(doc(mime = "video/mp4"), paths, 10 * gib))
        assertEquals(Plan.NothingToPlay, episodePlan(doc(id = "bk_1"), paths, 10 * gib))
    }

    @Test
    fun aFileIsWholeOnlyWhenTheCacheHoldsAllOfIt() {
        assertTrue(episodeFileComplete(1000, 1000, 1000))
        assertFalse(episodeFileComplete(1000, 600, 600)) // a network drop leaves a partial file: not done
        // No length from the publisher: whole when the downloader finished and the cache holds what it counted.
        assertTrue(episodeFileComplete(-1, 500, 500))
        assertFalse(episodeFileComplete(-1, 0, 0))
        assertFalse(episodeFileComplete(-1, 400, 500))
        assertEquals(1000, episodeSize(1000, 900))
        assertEquals(900, episodeSize(-1, 900)) // the announced size is never the stored one
    }

    @Test
    fun theWifiGate() {
        assertTrue(mayAutoDownloadEpisodes(true, true))
        assertFalse(mayAutoDownloadEpisodes(true, false)) // metered: not a byte
        assertFalse(mayAutoDownloadEpisodes(false, true)) // no show asks: the periodic job has nothing to do
    }

    @Test
    fun keepTheLatestN() {
        val cards = listOf(card("pe_1", 100), card("pe_2", 300), card("pe_3", 200), card("pe_4", 400), card("pe_5", 500, played = true))
        // The played one is not wanted; the newest 2 unplayed are pe_4 and pe_2.
        assertEquals(listOf("pe_4", "pe_2"), autoEpisodeWanted(cards, emptySet(), emptySet(), 2).map { it.id })
        // Already on the phone: not fetched twice.
        assertEquals(listOf("pe_2"), autoEpisodeWanted(cards, setOf("pe_4"), emptySet(), 2).map { it.id })
        // Removed on purpose: not fetched again, and an older episode does not take its place.
        assertEquals(listOf("pe_2"), autoEpisodeWanted(cards, emptySet(), setOf("pe_4"), 2).map { it.id })
        assertEquals(emptyList(), autoEpisodeWanted(emptyList(), emptySet(), emptySet(), 3))
    }

    @Test
    fun evictionsRemovePlayedAndBeyondTheLatestAndNothingTheyDoNotOwn() {
        val rows = listOf(
            row("pe_old", at = 100), row("pe_mid", at = 200), row("pe_new", at = 300),
            row("pe_done", at = 250, played = true),
            row("pe_mine", at = 50), // a manual download: not in the marks
            row("bk_1", kind = KIND_BOOK), // a book
            row("pe_other", show = "ps_2", at = 1), // a show with automatic download off
        )
        val marks = setOf("pe_old", "pe_mid", "pe_new", "pe_done", "pe_other")
        val gone = autoEpisodeEvictions(rows, marks, setOf("bk_1"), mapOf("ps_1" to 2))
        assertEquals(listOf("pe_done", "pe_old"), gone)
        assertFalse("pe_mine" in gone || "bk_1" in gone || "pe_other" in gone)
    }

    @Test
    fun evictionsHonourTheSharedQuota() {
        val big = 2 * gib
        val rows = listOf(
            row("pe_a", at = 100, size = big),
            row("pe_b", at = 200, size = big),
            row("bk_auto", kind = KIND_BOOK, size = big),
        )
        // 6 GiB of automatic downloads against a 5 GiB quota: the oldest automatic episode goes, never the next book.
        val gone = autoEpisodeEvictions(rows, setOf("pe_a", "pe_b"), setOf("bk_auto"), mapOf("ps_1" to 5))
        assertEquals(listOf("pe_a"), gone)
        // Under the quota nothing goes.
        assertEquals(emptyList(), autoEpisodeEvictions(rows.take(2), setOf("pe_a", "pe_b"), emptySet(), mapOf("ps_1" to 5)))
    }

    @Test
    fun theQuotaAndSpareRoomOfAnAutomaticEpisode() {
        assertIs<AutoPlan.Go>(autoEpisodePlan(doc(), paths, 10 * gib, 0))
        assertEquals(AutoPlan.NoRoom, autoEpisodePlan(doc(bytes = 100_000_000), paths, 10 * gib, AUTO_TOTAL_BYTES - 1000))
        assertEquals(AutoPlan.NoRoom, autoEpisodePlan(doc(), paths, AUTO_SPARE_BYTES + 1000, 0)) // too little would remain
        assertEquals(AutoPlan.Skip, autoEpisodePlan(doc(mime = "video/mp4"), paths, 10 * gib, 0))
    }

    @Test
    fun announceOnlyWhatIsNewAndUnplayed() {
        val found = listOf(card("pe_1", 100), card("pe_2", 200), card("pe_3", 300, played = true))
        assertEquals(listOf("pe_2", "pe_1"), episodesToAnnounce(found, emptySet()).map { it.id })
        assertEquals(listOf("pe_2"), episodesToAnnounce(found, setOf("pe_1")).map { it.id })
        assertEquals(emptyList(), episodesToAnnounce(found, setOf("pe_1", "pe_2")))
    }

    @Test
    fun readyEpisodesForTheCarAreNewestFirstAndWhole() {
        val rows = listOf(
            row("pe_1", at = 100), row("pe_2", at = 300), row("pe_3", at = 200, state = OfflineState.DOWNLOADING),
            row("bk_1", kind = KIND_BOOK), row("pe_4", at = 50, state = OfflineState.STALE),
        )
        assertEquals(listOf("pe_2", "pe_1", "pe_4"), readyEpisodes(rows).map { it.bookId })
        assertEquals(EpisodeCard("pe_1", "ps_1", "T", "Show", 0, 100, 0, false, false), rows[0].asEpisodeCard())
    }

    @Test
    fun theQueueNamesItemsOfEitherKind() {
        assertEquals("pe_7", ApiJson.nextItemId("""{"kind":"episode","episode":{"id":"pe_7","title":"x"},"source":"queue"}"""))
        assertEquals("b1", ApiJson.nextItemId("""{"kind":"book","book":{"id":"b1"},"source":"queue"}"""))
        assertEquals("b2", ApiJson.nextItemId("""{"book":{"id":"b2"}}""")) // an older server
        assertEquals("http://10.0.2.2:8103/api/v1/queue/next?kinds=book,episode", paths.queueNextUrl)
    }

    @Test
    fun anEpisodeIsRememberedAsTheNextItemAndStartsOnlyWhenWhole() {
        val stored = encodeAutoNext("pe_1", "pe_2")
        assertEquals("pe_2", offlineNextFor(stored, "pe_1") { it == "pe_2" })
        assertEquals(null, offlineNextFor(stored, "pe_1") { false })
        assertEquals(null, offlineNextFor(stored, "pe_9") { true })
    }

    @Test
    fun anEpisodeRowKeepsItsKindAndOldCallersStayBooks() {
        assertEquals(KIND_EPISODE, row("pe_x", at = 7).kind)
        assertEquals(KIND_BOOK, OfflineBook("b", "u", "T", "", 0, 0, OfflineState.READY, 0, 0).kind)
    }

    @Test
    fun showCardsReadKeepLatest() {
        val body = """{"items":[{"id":"ps_1","title":"A","settings":{"keepLatest":5}},{"id":"ps_2","title":"B"},{"id":"ps_3","settings":{"keepLatest":900}}]}"""
        assertEquals(listOf(5, DEFAULT_KEEP_LATEST, 50), PodcastJson.showCards(body).map { it.keepLatest })
    }
}
