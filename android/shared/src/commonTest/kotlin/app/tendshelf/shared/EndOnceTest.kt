package app.tendshelf.shared

import kotlin.test.Test
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class EndOnceTest {
    @Test
    fun anEndIsHandledOnce() {
        val e = EndOnce()
        assertTrue(e.claim("ep1"))
        assertFalse(e.claim("ep1"))
    }

    @Test
    fun aReplayOfTheSameItemIsHandledAgainAfterReset() {
        val e = EndOnce()
        assertTrue(e.claim("ep1")) // ended under the sleep timer
        e.reset() // the player left its ended state
        assertTrue(e.claim("ep1"))
    }

    @Test
    fun emptyIdIsNeverHandled() = assertFalse(EndOnce().claim(""))
}
