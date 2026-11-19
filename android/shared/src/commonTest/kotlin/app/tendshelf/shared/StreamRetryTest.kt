package app.tendshelf.shared

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull

class StreamRetryTest {
    @Test
    fun busyServerIsRetriedUpTo120Times() {
        assertEquals(5_000L, streamRetryDelayMs(503, 120))
        assertNull(streamRetryDelayMs(503, 121))
    }

    @Test
    fun lostNetworkIsRetriedUpTo24Times() {
        assertEquals(5_000L, streamRetryDelayMs(0, 24))
        assertNull(streamRetryDelayMs(0, 25))
    }

    @Test
    fun aRefusedTokenAndOtherStatusesAreNotRetried() {
        assertNull(streamRetryDelayMs(401, 1))
        assertNull(streamRetryDelayMs(404, 1))
    }
}
