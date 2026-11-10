package app.tendshelf.shared

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue

class PodcastTest {
    private val doc = """
        {"id":"pe_abc123","showId":"ps_9","title":"The Long Night","show":"Night Radio","artUrl":"/api/v1/podcasts/art/ps_9",
         "publishedAt":1700000000000,"durationMs":1800000,"kind":"full","state":{"positionMs":65000,"played":false,"favourite":false,"updatedAt":1700000100000},
         "audioUrl":"/api/v1/podcasts/audio/pe_abc123","mime":"audio/mpeg","bytes":28800000,"chaptersUrl":"/api/v1/podcasts/chapters/pe_abc123",
         "settings":{"speed":1.4,"skipIntroS":30,"skipOutroS":45,"serverAuto":false,"keepLatest":0}}
    """.trimIndent()

    @Test
    fun playbackDocumentIsParsed() {
        val d = PodcastJson.episode(doc)
        assertEquals("pe_abc123", d.id)
        assertEquals("The Long Night", d.title)
        assertEquals("Night Radio", d.show)
        assertEquals("/api/v1/podcasts/art/ps_9", d.artUrl)
        assertEquals("/api/v1/podcasts/audio/pe_abc123", d.audioUrl)
        assertEquals(1_800_000L, d.durationMs)
        assertEquals(ShowSettings(1.4, 30, 45), d.settings)
        assertEquals(RemoteProgress(65_000, false, 1_700_000_100_000, "", ""), d.progress)
    }

    @Test
    fun missingOptionalPartsGetDefaults() {
        val d = PodcastJson.episode("""{"id":"pe_x","title":"T","audioUrl":"/api/v1/podcasts/audio/pe_x","settings":{"speed":null},"state":{"positionMs":0,"updatedAt":0}}""")
        assertNull(d.settings.speed)
        assertEquals(0, d.settings.skipIntroS)
        assertNull(d.progress) // never played
        assertEquals("", d.chaptersUrl)
        assertEquals(0L, d.durationMs)
        assertFailsWith<JsonShapeException> { PodcastJson.episode("""{"id":"pe_x","title":"T"}""") }
    }

    @Test
    fun aSpeedOutsideTheRangeIsIgnored() {
        assertNull(PodcastJson.episode(doc.replace("1.4", "9")).settings.speed)
    }

    @Test
    fun chaptersAreOrderedAndClosed() {
        val body = """{"version":"1.2.0","chapters":[
            {"startTime":600,"title":"Second"},{"startTime":0,"title":"Intro"},{"startTime":300.5,"title":" "},
            {"startTime":900,"title":"Ads","toc":false},{"startTime":-5,"title":"bad"},{"title":"no time"}]}"""
        val list = PodcastJson.chapters(body, 1_800_000)
        assertEquals(listOf("Intro", "Chapter 2", "Second"), list.map { it.title })
        assertEquals(listOf(0L, 300_500L, 600_000L), list.map { it.startMs })
        assertEquals(listOf(300_500L, 600_000L, 1_800_000L), list.map { it.endMs })
        assertEquals(listOf(0, 1, 2), list.map { it.idx })
    }

    @Test
    fun chaptersThatAreNotJsonAreNone() {
        assertEquals(emptyList(), PodcastJson.chapters("<html>", 1000))
        assertEquals(emptyList(), PodcastJson.chapters("""{"chapters":"x"}""", 1000))
    }

    @Test
    fun episodeIdsAreRecognised() {
        assertTrue(isEpisodeId("pe_01HZ"))
        assertFalse(isEpisodeId("pe_"))
        assertFalse(isEpisodeId("bk_1"))
        assertFalse(isEpisodeId("book:pe_1"))
    }

    @Test
    fun introIsSkippedOnlyInsideIt() {
        val r = skipRules(ShowSettings(null, 30, 0), 1_800_000)
        assertEquals(30_000L, r.startAt(0))
        assertEquals(30_000L, r.startAt(12_000))
        assertEquals(65_000L, r.startAt(65_000))
        assertEquals(0L, skipRules(ShowSettings(null, 0, 0), 1000).startAt(0))
    }

    @Test
    fun anIntroAsLongAsTheEpisodeIsIgnored() {
        assertEquals(0L, skipRules(ShowSettings(null, 60, 0), 60_000).startAt(0))
        // an unknown length cannot be compared: the intro still applies
        assertEquals(60_000L, skipRules(ShowSettings(null, 60, 0), 0).startAt(0))
    }

    @Test
    fun outroEndsTheEpisodeOnlyWithAKnownLength() {
        val r = skipRules(ShowSettings(null, 0, 45), 1_800_000)
        assertFalse(r.inOutro(1_754_999))
        assertTrue(r.inOutro(1_755_000))
        assertFalse(skipRules(ShowSettings(null, 0, 45), 0).inOutro(5_000_000))
        assertFalse(skipRules(ShowSettings(null, 0, 0), 1_800_000).inOutro(1_799_000))
    }

    @Test
    fun anOutroThatSwallowsTheIntroIsDropped() {
        // 100 s episode, 60 s intro, 50 s outro: the outro would start at 50 s, inside the intro
        val r = skipRules(ShowSettings(null, 60, 50), 100_000)
        assertNull(r.outroFromMs)
        assertEquals(60_000L, r.startAt(0))
    }

    @Test
    fun showSpeedBeatsThePersonalOne() {
        assertEquals(1.4, episodeSpeed(ShowSettings(1.4, 0, 0), 1.0))
        assertEquals(1.25, episodeSpeed(ShowSettings(null, 0, 0), 1.25))
    }

    private fun save(id: String, seq: Long, at: Long = seq) = Save(seq, id, 1000 * seq, at, false, false, 1.0, 500, false)

    @Test
    fun episodeBodyHasNoSpeedAndNamesTheDevice() {
        val body = podcastBatchBody(listOf(save("pe_a", 1).copy(finished = true)), "dev-12345678")
        assertEquals(
            """{"updates":[{"episodeId":"pe_a","positionMs":1000,"clientUpdatedAt":1,"deviceId":"dev-12345678","finished":true}]}""",
            body,
        )
    }

    @Test
    fun batchIsSplitByKindAndPutBackInOrder() {
        val rows = listOf(save("bk_1", 1), save("pe_a", 2), save("bk_2", 3), save("pe_b", 4))
        val (books, eps) = splitSaves(rows)
        assertEquals(listOf("bk_1", "bk_2"), books.map { it.bookId })
        assertEquals(listOf("pe_a", "pe_b"), eps.map { it.bookId })
        val ok = { id: String -> BatchItemResult(id, true, null, null) }
        val out = assembleResults(rows, books.map { ok(it.bookId) }, eps.map { ok(it.bookId) })
        assertEquals(listOf("bk_1", "pe_a", "bk_2", "pe_b"), out.map { it.bookId })
    }

    @Test
    fun aFailedGroupEndsTheAnswerAtItsFirstRow() {
        val rows = listOf(save("bk_1", 1), save("pe_a", 2), save("bk_2", 3))
        val out = assembleResults(rows, listOf(BatchItemResult("bk_1", true, null, null), BatchItemResult("bk_2", true, null, null)), null)
        assertEquals(listOf("bk_1"), out.map { it.bookId })
        // and the outbox settles only that prefix
        assertEquals(listOf(1L), applyResults(rows, out).deleteSeqs)
    }

    @Test
    fun episodeBatchAnswerSettlesItsRowsAndAStaleSaveIsNotALoss() {
        val body = """{"results":[{"episodeId":"pe_a","applied":true,"state":{"positionMs":1000}},
            {"episodeId":"pe_b","applied":false,"state":{"positionMs":9}},
            {"episodeId":"pe_c","applied":false,"error":{"code":"not_found"}}],"serverNow":1}"""
        val results = PodcastJson.batchResults(body)
        assertEquals(listOf("pe_a", "pe_b", "pe_c"), results.map { it.bookId })
        val rows = listOf(save("pe_a", 1), save("pe_b", 2), save("pe_c", 3))
        val applied = applyResults(rows, results)
        assertEquals(listOf(1L, 2L, 3L), applied.deleteSeqs) // a missing episode is dropped, a newer remote place is simply kept there
        assertTrue(applied.lost.isEmpty())
    }

    @Test
    fun episodeSavesQueueLikeBookSaves() {
        val store = MemoryStore()
        val outbox = Outbox(store) { 1_000 }
        outbox.tick("pe_a", 5_000, 1.4, 4_000)
        outbox.tick("pe_a", 9_000, 1.4, 4_000) // merged into the unsent row
        outbox.finish("pe_a", 60_000, 1.4, 0)
        val batch = outbox.nextBatch()
        assertEquals(2, batch.size)
        assertEquals(9_000L, batch[0].positionMs)
        assertTrue(batch[1].finished)
        assertTrue(batch.all { isEpisodeId(it.bookId) })
        assertEquals(60_000L, outbox.newest("pe_a")?.positionMs)
    }

    @Test
    fun podcastAddressesStayOnTheServer() {
        val p = ApiPaths("https://shelf.example/")
        assertEquals("https://shelf.example/api/v1/podcasts/episodes/pe_a", p.episodeUrl("pe_a"))
        assertEquals("https://shelf.example/api/v1/podcasts/progress/batch", p.podcastBatchUrl)
        assertEquals("https://shelf.example/api/v1/podcasts/art/ps_9?w=512", p.podcastUrl("/api/v1/podcasts/art/ps_9", 512))
        assertEquals("https://shelf.example/api/v1/podcasts/chapters/pe_a", p.podcastUrl("/api/v1/podcasts/chapters/pe_a"))
        assertNull(p.podcastUrl("https://evil.example/api/v1/podcasts/audio/x"))
        assertNull(p.podcastUrl("//evil.example/api/v1/podcasts/x"))
        assertNull(p.podcastUrl("/api/v1/podcasts/../me"))
        assertNull(p.podcastUrl("/api/v1/books/x"))
        assertNull(p.podcastUrl("/api/v1/podcasts/art/ps?x=1"))
    }

    @Test
    fun sleepWordEpisodeIsAccepted() {
        assertEquals(BridgeMessage.Sleep("episode", 0), parseBridge("sleep episode"))
    }
}
