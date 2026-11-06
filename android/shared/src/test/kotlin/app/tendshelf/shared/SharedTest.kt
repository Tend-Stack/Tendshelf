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
