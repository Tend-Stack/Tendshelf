package app.tendshelf.shared

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

class PlayFailureTest {
    @Test
    fun statusesMapToOneKindEach() {
        val cases = listOf(
            Triple(0, false, "stream"), // no network, a timeout
            Triple(200, false, "stream"),
            Triple(301, false, "stream"),
            Triple(400, false, "stream"),
            Triple(401, false, "signedOut"),
            Triple(403, false, "book"),
            Triple(404, false, "book"),
            Triple(410, false, "book"),
            Triple(429, false, "stream"),
            Triple(500, false, "stream"),
            Triple(502, false, "stream"),
            Triple(503, false, "stream"), // a busy book server is only a bad connection; busy is a station word
        )
        for ((status, other, want) in cases) assertEquals(want, playFailureKind(status, other), "status $status")
    }

    @Test
    fun stationBusyOnlyOn503() {
        assertEquals("busy", playFailureKind(503, false, live = true))
        assertEquals("book", playFailureKind(404, false, live = true))
        assertEquals("signedOut", playFailureKind(401, false, live = true))
        assertEquals("stream", playFailureKind(502, false, live = true))
        assertEquals("stream", playFailureKind(0, false, live = true))
    }

    @Test
    fun wrongServerWinsOverAnyStatus() {
        for (status in listOf(0, 401, 404, 503)) {
            assertEquals("otherServer", playFailureKind(status, true), "status $status")
            assertEquals("otherServer", playFailureKind(status, true, live = true), "live $status")
        }
    }

    @Test
    fun everyKindCrossesTheBridge() {
        for (kind in listOf("stream", "book", "signedOut", "busy", "otherServer", "background")) {
            assertTrue(kind in ERROR_CODES)
            assertEquals("{\"t\":\"error\",\"code\":\"$kind\"}", encodeError(kind))
        }
        assertEquals("{\"t\":\"error\",\"code\":\"stream\"}", encodeError("surprise"))
    }
}
