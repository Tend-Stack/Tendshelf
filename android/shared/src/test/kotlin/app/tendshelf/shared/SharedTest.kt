package app.tendshelf.shared

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertNull

class PositionTest {
    private val files = listOf(
        AudioFile("a", 0, 0, 1000, "audio/mpeg", false),
        AudioFile("b", 1, 1000, 2000, "audio/mpeg", false),
        AudioFile("c", 2, 3000, 500, "audio/mpeg", false),
    )
    private val chapters = listOf(
        Chapter(0, 0, 1000, "One"),
        Chapter(1, 1000, 3000, "Two"),
        Chapter(2, 3000, 3500, "Three"),
    )

    @Test
    fun locateTable() {
        val cases = listOf(
            -5L to FilePosition(0, 0),
            0L to FilePosition(0, 0),
            999L to FilePosition(0, 999),
            1000L to FilePosition(1, 0),
            2500L to FilePosition(1, 1500),
            3000L to FilePosition(2, 0),
            3500L to FilePosition(2, 500),
            9999L to FilePosition(2, 500),
        )
        for ((input, want) in cases) assertEquals(want, Position.locate(files, input), "locate($input)")
    }

    @Test
    fun bookPositionRoundTrips() {
        for (ms in listOf(0L, 1L, 999L, 1000L, 2999L, 3499L)) {
            val at = Position.locate(files, ms)
            assertEquals(ms, Position.bookPosition(files, at.fileIndex, at.positionMs))
        }
    }

    @Test
    fun chapterAt() {
        assertEquals("One", Position.chapterAt(chapters, 0)?.title)
        assertEquals("Two", Position.chapterAt(chapters, 1000)?.title)
        assertEquals("Three", Position.chapterAt(chapters, 9000)?.title)
        assertNull(Position.chapterAt(emptyList(), 5))
    }

    @Test
    fun noFilesIsAnError() {
        assertFailsWith<IllegalArgumentException> { Position.locate(emptyList(), 0) }
    }
}

class ApiPathsTest {
    @Test
    fun urls() {
        val api = ApiPaths(" http://10.0.2.2:8097/ ")
        assertEquals("http://10.0.2.2:8097/api/v1/auth/token", api.tokenUrl)
        assertEquals("http://10.0.2.2:8097/api/v1/books/bk_1", api.bookUrl("bk_1"))
        assertEquals("http://10.0.2.2:8097/api/v1/stream/af_1", api.streamUrl("af_1"))
        assertEquals("http://10.0.2.2:8097/?shell=android&next=/", api.shellUrl)
    }

    @Test
    fun idsCannotChangeThePath() {
        val api = ApiPaths("http://h")
        for (bad in listOf("", "a/b", "a?x=1", "a#b", "a b")) {
            assertFailsWith<IllegalArgumentException>(bad) { api.bookUrl(bad) }
        }
    }

    @Test
    fun jsonBodyEscapes() {
        val body = ApiPaths("http://h").tokenRequestBody("me", "pa\"ss\\\n", "Pixel")
        assertEquals("{\"username\":\"me\",\"password\":\"pa\\\"ss\\\\\\n\",\"name\":\"Pixel\",\"kind\":\"mobile\"}", body)
    }
}

class ChapterNavigationTest {
    // Three 5-minute files; chapter 2 crosses the first file boundary (4:00 to 7:00).
    private val chapters = listOf(
        Chapter(0, 0, 120_000, "One"),
        Chapter(1, 120_000, 240_000, "Two"),
        Chapter(2, 240_000, 420_000, "Three"),
        Chapter(3, 420_000, 900_000, "Four"),
    )

    @Test
    fun nextTable() {
        val cases = listOf(
            0L to 120_000L,
            119_999L to 120_000L,
            120_000L to 240_000L,
            300_000L to 420_000L, // inside the chapter that crosses a file
            419_999L to 420_000L,
            420_000L to null,
            899_000L to null,
        )
        for ((at, want) in cases) assertEquals(want, nextChapterStart(chapters, at), "next($at)")
    }

    @Test
    fun previousTable() {
        val cases = listOf(
            0L to 0L,
            1_000L to 0L,
            10_000L to 0L, // 10 s in restarts the first chapter
            121_000L to 0L, // 1 s into two goes back one
            125_000L to 120_000L, // 5 s in restarts
            123_000L to 0L, // exactly 3 s counts as the start
            123_001L to 120_000L,
            300_000L to 240_000L, // across the file boundary
            900_000L to 420_000L,
        )
        for ((at, want) in cases) assertEquals(want, previousChapterStart(chapters, at), "previous($at)")
    }

    @Test
    fun noChapters() {
        assertNull(nextChapterStart(emptyList(), 5))
        assertEquals(0L, previousChapterStart(emptyList(), 5))
    }

    @Test
    fun startPlaceRules() {
        fun p(pos: Long, done: Boolean) = RemoteProgress(pos, done, 1, "d", "Phone")
        assertEquals(500L, startPlace(p(9_000, false), 500))
        assertEquals(9_000L, startPlace(p(9_000, false), null))
        assertEquals(9_000L, startPlace(p(9_000, false), -1))
        assertEquals(0L, startPlace(p(9_000, true), -1))
        assertEquals(700L, startPlace(p(9_000, true), 700))
        assertEquals(0L, startPlace(null, null))
    }

    @Test
    fun silenceIsSkipped() {
        val files = listOf(
            AudioFile("a", 0, 0, 1000, "audio/mpeg", false),
            AudioFile("b", 1, 1000, 1000, "audio/mpeg", false, playable = false),
            AudioFile("c", 2, 2000, 1000, "audio/mpeg", false, playable = false),
            AudioFile("d", 3, 3000, 1000, "audio/mpeg", false),
        )
        val ranges = silentRanges(files)
        assertNull(skipSilence(ranges, 999))
        assertEquals(3000L, skipSilence(ranges, 1000))
        assertEquals(3000L, skipSilence(ranges, 2500))
        assertNull(skipSilence(ranges, 3000))
    }
}

class ApiPlaybackUrlsTest {
    @Test
    fun urlsStayOnTheOrigin() {
        val p = ApiPaths("http://h:1/")
        assertEquals("http://h:1/api/v1/stream/f1", p.streamUrl("f1"))
        assertEquals("http://h:1/api/v1/stream/f1?compat=1", p.streamUrl("f1", true))
        assertEquals("http://h:1/api/v1/covers/b1?w=512", p.coverUrl("b1", 512))
        assertEquals("http://h:1/api/v1/settings", p.settingsUrl)
        assertFailsWith<IllegalArgumentException> { p.coverUrl("a/../b", 512) }
    }
}
