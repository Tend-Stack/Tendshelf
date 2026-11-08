package app.tendshelf.shared

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue

class FeelTest {
    @Test
    fun unarmedPasses() {
        assertEquals(PageKey.Pass, pageKeyFor(false, KEY_VOLUME_DOWN, KEY_ACTION_DOWN, 0))
        assertEquals(PageKey.Pass, pageKeyFor(false, KEY_VOLUME_UP, KEY_ACTION_DOWN, 0))
        assertEquals(PageKey.Pass, pageKeyFor(false, KEY_VOLUME_UP, 1, 0))
    }

    @Test
    fun armedTurnsOnePage() {
        assertEquals(PageKey.Pass, pageKeyFor(true, 66, KEY_ACTION_DOWN, 0))
        assertEquals(PageKey.Next, pageKeyFor(true, KEY_VOLUME_DOWN, KEY_ACTION_DOWN, 0))
        assertEquals(PageKey.Prev, pageKeyFor(true, KEY_VOLUME_UP, KEY_ACTION_DOWN, 0))
        assertEquals(PageKey.Swallow, pageKeyFor(true, KEY_VOLUME_DOWN, KEY_ACTION_DOWN, 1))
        assertEquals(PageKey.Swallow, pageKeyFor(true, KEY_VOLUME_UP, KEY_ACTION_DOWN, 3))
        assertEquals(PageKey.Swallow, pageKeyFor(true, KEY_VOLUME_DOWN, 1, 0))
        assertEquals(PageKey.Swallow, pageKeyFor(true, KEY_VOLUME_UP, 1, 0))
    }

    @Test
    fun screenFollowsPageAndFront() {
        val f = ReaderFeel()
        assertNull(f.awake(true)) // not in front yet
        assertEquals(true, f.front(true))
        assertEquals(false, f.front(false))
        assertEquals(true, f.front(true))
        assertNull(f.awake(true)) // same value twice
        assertEquals(false, f.reset())
        assertFalse(f.screenOn)
        assertNull(f.reset())
    }

    @Test
    fun keysNeedFront() {
        val f = ReaderFeel()
        f.keys(true)
        assertFalse(f.keysArmed)
        f.front(true)
        assertTrue(f.keysArmed)
        f.reset()
        assertFalse(f.keysArmed)
    }
}
