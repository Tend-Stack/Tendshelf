package app.tendshelf.shared

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue

class SleepTest {
    private fun run(s: SleepStep) = (s as SleepStep.Run).volume

    @Test
    fun minutesCountOnlyWhilePlaying() {
        val c = SleepClock()
        c.set("1", 1, 0)
        run(c.step(30_000, true, 0, 1.0, 0))
        run(c.step(30_000, false, 0, 1.0, 0)) // paused: not counted
        assertEquals(30_000, c.info(0, 1.0).leftMs)
        assertEquals(SleepMode.MINUTES, c.info(0, 1.0).mode)
    }

    @Test
    fun speedDoesNotChangeMinutes() {
        val c = SleepClock()
        c.set("1", 1, 0)
        c.step(15_000, true, 0, 2.0, 0)
        assertEquals(45_000, c.info(0, 2.0).leftMs)
    }

    @Test
    fun fadeRampsFromTenSecondsLeft() {
        val c = SleepClock()
        c.set("1", 1, 0)
        assertEquals(1f, run(c.step(49_000, true, 0, 1.0, 0)))  // 11 s left
        assertEquals(1f, run(c.step(1_000, true, 0, 1.0, 0)), 0.001f) // 10 s left: the ramp starts at 1.0
        assertTrue(c.info(0, 1.0).fading)
        assertEquals(0.5f, run(c.step(5_000, true, 0, 1.0, 0)), 0.001f)
        val end = c.step(5_000, true, 0, 1.0, 0)
        assertEquals(SleepStep.Finish(null), end)
        assertFalse(c.isOn)
    }

    @Test
    fun plusAddsAndStopsFade() {
        val c = SleepClock()
        c.set("1", 1, 0)
        c.step(55_000, true, 0, 1.0, 0)
        assertTrue(c.info(0, 1.0).fading)
        c.plus(0, 1.0)
        assertFalse(c.info(0, 1.0).fading)
        assertEquals(605_000, c.info(0, 1.0).leftMs)
        assertEquals("", c.info(0, 1.0).choice)
        assertEquals(1f, run(c.step(0, true, 0, 1.0, 0)))
    }

    @Test
    fun plusOnOffDoesNothing() {
        val c = SleepClock()
        c.plus(0, 1.0)
        assertFalse(c.isOn)
    }

    @Test
    fun chapterTimerIsTheChapterAtTheSpeed() {
        val c = SleepClock()
        c.set("chapter", 0, 100_000)
        assertEquals(40_000, c.info(40_000, 1.5).leftMs) // 60 s of book at 1.5x
        c.plus(40_000, 1.5)
        assertEquals(SleepMode.MINUTES, c.info(40_000, 1.5).mode)
        assertEquals(640_000, c.info(40_000, 1.5).leftMs)
    }

    @Test
    fun chapterRestsAtItsEnd() {
        val c = SleepClock()
        c.set("chapter", 0, 100_000)
        c.step(250, true, 99_900, 1.0, 100_000)
        val s = c.step(250, true, 100_000, 1.0, 200_000)
        assertEquals(SleepStep.Finish(100_000), s)
    }

    @Test
    fun skippedAheadFollowsOnlyPastTheWindow() {
        val a = SleepClock()
        a.set("chapter", 0, 100_000)
        a.step(250, true, 101_499, 1.0, 200_000) // 1,499 past: not skipped; left <= 0 so it finishes at 100,000
        val b = SleepClock()
        b.set("chapter", 0, 100_000)
        b.step(250, true, 101_501, 1.0, 200_000) // 1,501 past: follows to the next end
        assertEquals(SleepMode.CHAPTER, b.info(101_501, 1.0).mode)
        assertEquals(98_499, b.info(101_501, 1.0).leftMs)
        assertFalse(a.isOn)
    }

    @Test
    fun chapterEndRule() {
        val ch = listOf(Chapter(0, 0, 10_000, "a"), Chapter(1, 10_000, 20_000, "b"))
        assertEquals(10_000, chapterEndFor(ch, 7_999))
        assertEquals(20_000, chapterEndFor(ch, 8_001))
        assertEquals(20_000, chapterEndFor(ch, 19_000)) // last chapter: its own end
        assertNull(chapterEndFor(emptyList(), 0))
    }

    @Test
    fun shake() {
        val d = ShakeDetector()
        assertFalse(d.sample(20f, 1_000)) // one jolt
        assertFalse(d.sample(20f, 1_100)) // under the gap
        assertTrue(d.sample(20f, 1_200)) // second jolt inside the window
        assertFalse(d.sample(20f, 1_400)) // counted again from one
        assertFalse(d.sample(20f, 2_600)) // outside the window: one
        assertFalse(d.sample(5f, 2_700)) // too weak
        assertTrue(d.sample(20f, 3_000))
    }
}
