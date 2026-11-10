package app.tendshelf.shared

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue

/** R3: the station id rule, the station document, the live-item rules, the car's radio nodes and the widget's live look. */
class RadioTest {
    @Test
    fun stationIdRule() {
        assertTrue(isStationId("rs_01H"))
        assertFalse(isStationId("rs_"))
        assertFalse(isStationId("pe_01H"))
        assertFalse(isStationId("ps_01H"))
        assertFalse(isStationId("bk_1"))
        assertFalse(isStationId("xrs_1"))
        assertTrue(validId("rs_01HABC")) // the bridge's id grammar takes it, so "start rs_.. -" reaches the engine
        assertFalse(isEpisodeId("rs_01H"))
    }

    @Test
    fun stationListParses() {
        val body = """{"items":[
            {"id":"rs_1","name":"Jazz FM","codec":"mp3","genre":"Jazz","country":"Spain","favourite":true,"addedAt":5,"lastPlayedAt":9,"liveUrl":"/api/v1/radio/live/rs_1","problem":"","artUrl":"/api/v1/radio/art/rs_1"},
            {"id":"bk_9","name":"A book"},
            {"id":"rs_2","name":"","problem":"Could not reach it."},
            {"name":"no id"}
          ],"serverNow":1}"""
        val list = RadioJson.stations(body)
        assertEquals(listOf("rs_1", "rs_2"), list.map { it.id })
        assertEquals("Jazz FM", stationRowTitle(list[0]))
        assertEquals("Jazz, Spain", stationSubline(list[0]))
        assertEquals("/api/v1/radio/art/rs_1", list[0].artUrl)
        assertTrue(list[0].favourite)
        assertEquals(9, list[0].lastPlayedAt)
        assertEquals("Radio station", stationRowTitle(list[1]))
        assertEquals("", stationSubline(list[1]))
        assertEquals("Could not reach it.", list[1].problem)
        assertEquals("", list[1].artUrl)
    }

    @Test
    fun nowTitleIsCleaned() {
        assertEquals("Miles Davis - So What", RadioJson.now("""{"title":"  Miles Davis - So What "}"""))
        assertEquals("", RadioJson.now("""{"title":""}"""))
        assertEquals("ab", nowTitle("a\u0000b\u001b"))
        assertEquals(200, nowTitle("x".repeat(500)).length)
        assertEquals("On air", liveSubtitle("", "On air"))
        assertEquals("Song", liveSubtitle(" Song ", "On air"))
    }

    @Test
    fun busyRelayIsRetriedThenReported() {
        assertEquals(5_000L, liveRetryDelayMs(503, 0, 0))
        assertEquals(7_000L, liveRetryDelayMs(503, 1, 7))
        assertEquals(15_000L, liveRetryDelayMs(503, 2, 600)) // a long Retry-After is capped
        assertEquals(2_000L, liveRetryDelayMs(503, 0, 1)) // and a tiny one is not a hammer
        assertNull(liveRetryDelayMs(503, 3, 5))
        assertEquals("busy", liveFailureKind(503))
    }

    @Test
    fun brokenStreamIsRetriedTwiceRefusalsNever() {
        assertEquals(3_000L, liveRetryDelayMs(0, 0, 0))
        assertEquals(3_000L, liveRetryDelayMs(502, 1, 0))
        assertNull(liveRetryDelayMs(0, 2, 0))
        for (code in listOf(401, 403, 404, 410)) assertNull(liveRetryDelayMs(code, 0, 0), "$code")
        assertEquals("signedOut", liveFailureKind(401))
        assertEquals("book", liveFailureKind(404))
        assertEquals("stream", liveFailureKind(502))
        assertEquals("stream", liveFailureKind(0))
    }

    @Test
    fun radioNodesRoundTrip() {
        for (n in listOf(Node.Radio, Node.RadioStations, Node.RadioRecent, Node.Station("rs_01H"))) assertEquals(n, parseNode(nodeId(n)), nodeId(n))
        assertEquals("rs_01H", playableId(Node.Station("rs_01H")))
        assertNull(playableId(Node.RadioStations))
        assertNull(parseNode("station:bk_1")) // a book id is not a station
        assertNull(parseNode("station:rs_"))
        assertNull(parseNode("station:rs_a/b"))
        assertNull(parseNode("radio-other"))
    }

    @Test
    fun liveWidgetHasNoSkipButtons() {
        val input = WidgetInput(true, "rs_1", "Jazz FM", "", true, 15, 45, show = "Live", live = true)
        val look = widgetLook(input, 400, 150)
        assertEquals("Jazz FM", look.title)
        assertEquals("Live", look.chapter)
        assertTrue(look.showChapter)
        assertFalse(look.showSkip)
        assertTrue(widgetLook(input.copy(live = false), 400, 150).showSkip)
    }

    @Test
    fun radioUrls() {
        val p = ApiPaths("https://x.example/")
        assertEquals("https://x.example/api/v1/radio/live/rs_1", p.liveUrl("rs_1"))
        assertEquals("https://x.example/api/v1/radio/now/rs_1", p.radioNowUrl("rs_1"))
        assertEquals("https://x.example/api/v1/radio/stations", p.radioStationsUrl)
        assertEquals("https://x.example/api/v1/radio/art/rs_1", p.radioArtUrl("rs_1"))
        assertEquals("https://x.example/api/v1/radio/art/rs_1", p.radioUrl("/api/v1/radio/art/rs_1"))
        assertNull(p.radioUrl("https://evil.example/api/v1/radio/art/rs_1"))
        assertNull(p.radioUrl("/api/v1/podcasts/art/ps_1"))
        assertNull(p.radioUrl("/api/v1/radio/../books/x"))
        assertNull(p.radioUrl("/api/v1/radio/art/rs_1?w=1"))
    }
}
