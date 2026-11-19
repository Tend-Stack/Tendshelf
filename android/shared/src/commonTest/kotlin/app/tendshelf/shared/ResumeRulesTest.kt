package app.tendshelf.shared

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue

class ResumeRulesTest {
    private var t = 1_000_000L
    private val clock = ResumeClock { t }

    @Test
    fun aFreshLoadNeverAsksTheServer() {
        clock.bookLoaded(placeWrittenAt = t - 3_600_000, atGiven = false)
        val s = clock.resumed(DEFAULT_REWIND, true)!!
        assertFalse(s.checkRemote)
        assertEquals(10_000L, s.rewindMs) // an hour away: ten seconds back
    }

    @Test
    fun theBreakThresholdIsSixtySeconds() {
        clock.resumed(DEFAULT_REWIND, true) // not fresh any more
        clock.stopped(hasBook = true, buffering = false)
        t += 59_000
        assertFalse(clock.resumed(DEFAULT_REWIND, true)!!.checkRemote)
        clock.stopped(hasBook = true, buffering = false)
        t += 60_000
        val s = clock.resumed(DEFAULT_REWIND, true)!!
        assertTrue(s.checkRemote)
        assertEquals(5_000L, s.rewindMs)
    }

    @Test
    fun bufferingIsNoPause() {
        clock.stopped(hasBook = true, buffering = true)
        assertNull(clock.resumed(DEFAULT_REWIND, true))
    }

    @Test
    fun noBookIsNoPause() {
        clock.stopped(hasBook = false, buffering = false)
        assertNull(clock.resumed(DEFAULT_REWIND, true))
    }

    @Test
    fun aPlaceGivenClearsTheMark() {
        clock.bookLoaded(placeWrittenAt = t - 100_000, atGiven = true)
        assertNull(clock.resumed(DEFAULT_REWIND, true))
    }

    @Test
    fun theFirstStopKeepsItsTime() {
        clock.stopped(true, false)
        t += 20_000
        clock.stopped(true, false) // a second stop event does not move the mark
        t += 20_000
        assertEquals(2_000L, clock.resumed(DEFAULT_REWIND, true)!!.rewindMs) // 40 s away, not 20 s: the mark stayed
    }

    @Test
    fun offMeansNoStepButStillTheCheck() {
        clock.resumed(DEFAULT_REWIND, false)
        clock.stopped(true, false)
        t += 120_000
        val s = clock.resumed(DEFAULT_REWIND, false)!!
        assertEquals(0L, s.rewindMs)
        assertTrue(s.checkRemote)
    }
}
