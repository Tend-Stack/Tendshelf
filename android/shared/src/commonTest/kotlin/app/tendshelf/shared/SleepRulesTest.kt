package app.tendshelf.shared

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull

class SleepRulesTest {
    private val chapters = listOf(Chapter(0, 0, 100_000, "One"), Chapter(1, 100_000, 300_000, "Two"))

    @Test
    fun aStationHasNoEnd() = assertNull(sleepChapterEnd(true, false, chapters, 10_000, 300_000))

    @Test
    fun aWholeEpisodeEndsASecondBeforeItsDuration() {
        assertEquals(299_000L, sleepChapterEnd(false, true, chapters, 10_000, 300_000))
        assertNull(sleepChapterEnd(false, true, chapters, 10_000, 0))
    }

    @Test
    fun noChaptersIsOneChapter() {
        assertEquals(299_000L, sleepChapterEnd(false, false, emptyList(), 10_000, 300_000))
        assertNull(sleepChapterEnd(false, false, emptyList(), 10_000, 0))
    }

    @Test
    fun aMiddleChapterEndsWhereItEnds() = assertEquals(100_000L, sleepChapterEnd(false, false, chapters, 10_000, 300_000))

    @Test
    fun theLastChapterIsCappedASecondShort() = assertEquals(299_000L, sleepChapterEnd(false, false, chapters, 150_000, 300_000))

    @Test
    fun restPointIsCapped() {
        assertEquals(299_000L, sleepRestAt(300_000, 300_000))
        assertEquals(100_000L, sleepRestAt(100_000, 300_000))
        assertEquals(0L, sleepRestAt(500, 500))
        assertEquals(500L, sleepRestAt(500, 0)) // unknown duration: as given
    }
}
