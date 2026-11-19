package app.tendshelf.shared

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class SaveCadenceTest {
    @Test
    fun nothingIsDueInsideTenSeconds() {
        val c = SaveCadence()
        c.playing(100_000)
        assertFalse(c.due(109_999, "b", 5_000, retry = false))
        assertTrue(c.due(110_000, "b", 5_000, retry = false))
    }

    @Test
    fun retryIsDueAsSoonAsTheIntervalPassed() {
        val c = SaveCadence()
        c.playing(0)
        c.saved(0, "b", 5_000)
        assertFalse(c.due(5_000, "b", 5_000, retry = true))
        assertTrue(c.due(10_000, "b", 5_000, retry = true))
    }

    @Test
    fun theSameBookMovedUnderASecondIsSkippedAndRestartsTheInterval() {
        val c = SaveCadence()
        c.playing(0)
        c.saved(0, "b", 5_000)
        assertFalse(c.due(10_000, "b", 5_999, retry = false))
        assertFalse(c.due(19_999, "b", 9_000, retry = false)) // the interval restarted at 10 s
        assertTrue(c.due(20_000, "b", 9_000, retry = false))
    }

    @Test
    fun anotherBookIsDueEvenIfNotMoved() {
        val c = SaveCadence()
        c.playing(0)
        c.saved(0, "a", 5_000)
        assertTrue(c.due(10_000, "b", 5_000, retry = false))
    }
}

class ListenedClockTest {
    @Test
    fun theLastStretchBeforeAPauseCounts() {
        // The Android bug: pausing drained the time and threw it away, so the save that follows said 0 ms.
        val l = ListenedClock()
        l.playing(1_000)
        l.stopped(61_000)
        assertEquals(60_000L, l.drain(61_000, stillPlaying = false))
        assertEquals(0L, l.drain(62_000, stillPlaying = false))
    }

    @Test
    fun drainKeepsCountingWhilePlaying() {
        val l = ListenedClock()
        l.playing(1_000)
        assertEquals(9_000L, l.drain(10_000, stillPlaying = true))
        assertEquals(10_000L, l.drain(20_000, stillPlaying = true))
        assertEquals(5_000L, l.drain(25_000, stillPlaying = false))
        assertEquals(0L, l.drain(30_000, stillPlaying = false))
    }

    @Test
    fun timeBeforeAPauseAndAfterAPlayAddUp() {
        val l = ListenedClock()
        l.playing(1_000)
        l.stopped(11_000)
        l.playing(50_000)
        assertEquals(15_000L, l.drain(55_000, stillPlaying = true))
    }
}

class BackoffTest {
    @Test
    fun doublesToTheCapAndResets() {
        val b = Backoff()
        assertEquals(listOf(5_000L, 10_000L, 20_000L, 40_000L, 80_000L, 160_000L, 300_000L, 300_000L), List(8) { b.next() })
        b.reset()
        assertEquals(5_000L, b.next())
    }
}
