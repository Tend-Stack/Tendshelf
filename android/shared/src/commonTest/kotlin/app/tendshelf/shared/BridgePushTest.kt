package app.tendshelf.shared

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

class BridgePushTest {
    private fun snap(
        error: String = "",
        ask: Ask? = null,
        rewoundS: Int = 0,
        rewoundSeq: Int = 0,
        sleep: SleepInfo = SleepInfo.OFF,
    ) = PlayerSnapshot("b1", 1000, 5000, true, false, false, 1.0, 0, error, ask, rewoundS, rewoundSeq, sleep)

    private fun kinds(l: List<String>) = l.map { Regex("\"t\":\"(\\w+)\"").find(it)!!.groupValues[1] }

    @Test
    fun stateIsAlwaysFirstAndAlone() {
        assertEquals(listOf("state"), kinds(PushDiff().messages(snap())))
    }

    @Test
    fun errorIsSentOnceAndOnlyForKnownCodes() {
        val d = PushDiff()
        assertEquals(listOf("state", "error"), kinds(d.messages(snap(error = "stream"))))
        assertEquals(listOf("state"), kinds(d.messages(snap(error = "stream"))))
        assertEquals(listOf("state", "error"), kinds(d.messages(snap(error = "busy"))))
        assertEquals(listOf("state"), kinds(d.messages(snap(error = "weird"))))
        assertEquals(listOf("state"), kinds(d.messages(snap(error = "weird"))))
    }

    @Test
    fun sleepIsSentWhileOnAndOnceAfterOff() {
        val d = PushDiff()
        val on = SleepInfo(SleepMode.MINUTES, "15", 60_000, false)
        assertEquals(listOf("state", "sleep"), kinds(d.messages(snap(sleep = on))))
        assertEquals(listOf("state", "sleep"), kinds(d.messages(snap(sleep = on))))
        assertEquals(listOf("state", "sleep"), kinds(d.messages(snap())))
        assertEquals(listOf("state"), kinds(d.messages(snap())))
    }

    @Test
    fun askOpensChangesAndCloses() {
        val d = PushDiff()
        val a = Ask("b1", 9000, "Pixel")
        assertEquals(listOf("state", "ask"), kinds(d.messages(snap(ask = a))))
        assertEquals(listOf("state"), kinds(d.messages(snap(ask = a))))
        assertEquals(listOf("state", "ask"), kinds(d.messages(snap())))
        assertEquals(listOf("state"), kinds(d.messages(snap())))
    }

    @Test
    fun rewoundOnlyAfterHelloWithANewSeqAndSecondsAboveZero() {
        val d = PushDiff()
        d.hello()
        // The first push after hello only remembers the seq: an old rewind is not announced.
        assertEquals(listOf("state"), kinds(d.messages(snap(rewoundS = 5, rewoundSeq = 1))))
        assertEquals(listOf("state", "rewound"), kinds(d.messages(snap(rewoundS = 5, rewoundSeq = 2))))
        assertEquals(listOf("state"), kinds(d.messages(snap(rewoundS = 5, rewoundSeq = 2))))
        assertEquals(listOf("state"), kinds(d.messages(snap(rewoundS = 0, rewoundSeq = 3))))
    }

    @Test
    fun hiddenDropsAPendingRewound() {
        val d = PushDiff()
        d.hello()
        d.messages(snap(rewoundSeq = 1))
        d.hidden()
        assertEquals(listOf("state"), kinds(d.messages(snap(rewoundS = 5, rewoundSeq = 2))))
    }

    @Test
    fun shownResendsAnOpenAsk() {
        val d = PushDiff()
        val a = Ask("b1", 9000, "Pixel")
        d.messages(snap(ask = a))
        d.shown()
        val out = d.messages(snap(ask = a))
        assertEquals(listOf("state", "ask"), kinds(out))
        assertTrue(out[1].contains("\"deviceLabel\":\"Pixel\""))
    }
}
