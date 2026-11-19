package app.tendshelf.shared

import kotlin.test.Test
import kotlin.test.assertEquals

class NowLinesTest {
    private val chapters = listOf(Chapter(0, 0, 100, "One"), Chapter(1, 100, 300, "Two"))

    private fun lines(live: Boolean = false, episode: String? = null, ch: List<Chapter> = chapters, pos: Long = 150, now: String = "") =
        nowLines(live, "Title", "Author", episode, ch, pos, now, "Live")

    @Test
    fun bookWithChaptersShowsTheChapter() = assertEquals(NowLines("Two", "Title", "Author", null), lines())

    @Test
    fun bookWithoutChaptersShowsTheBook() = assertEquals(NowLines("Title", "Title", "Author", null), lines(ch = emptyList()))

    @Test
    fun episodeWithAChapter() = assertEquals(NowLines("Ep 1", "Title", "Title", "Two"), lines(episode = "Ep 1"))

    @Test
    fun episodeWithoutAChapter() = assertEquals(NowLines("Ep 1", "Title", "Title", null), lines(episode = "Ep 1", ch = emptyList()))

    @Test
    fun stationWithTheSong() = assertEquals(NowLines("Title", "Title", "A song", "A song"), lines(live = true, now = " A song "))

    @Test
    fun stationWithoutTheSong() = assertEquals(NowLines("Title", "Title", "Live", "Live"), lines(live = true))
}
