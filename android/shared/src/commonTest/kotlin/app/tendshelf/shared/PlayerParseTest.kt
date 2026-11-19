package app.tendshelf.shared

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNotNull
import kotlin.test.assertNull

class PlayerParseTest {
    @Test
    fun badShapesAreNullNotExceptions() {
        val paths = ApiPaths("https://books.example")
        assertNull(parseSettings("nope"))
        assertNull(parseSettings("{}"))
        assertNull(parseManifest("[]", paths))
        assertNull(parseEpisode("{}"))
        assertNull(parseStations("nope"))
        assertNull(parseRadioNow("x"))
        assertNull(parseShowCards("nope"))
        assertNull(parseEpisodeCards("nope"))
        assertNull(parseProgress("x"))
        assertNull(parseNextItemId("{}"))
        assertNull(parseBookBatch("{}"))
        assertNull(parseEpisodeBatch("{}"))
    }

    @Test
    fun goodAnswersParseAsBefore() {
        assertNotNull(parseSettings("""{"speed":1.25,"skipBackS":15,"skipForwardS":30,"autoRewind":true,"nextInSeries":false}"""))
        assertEquals(null, parseProgress("""{"progress":null}""")!!.progress)
        assertEquals("b1", parseNextItemId("""{"book":{"id":"b1"}}"""))
        assertEquals("Song", parseRadioNow("""{"title":"Song"}"""))
    }
}
