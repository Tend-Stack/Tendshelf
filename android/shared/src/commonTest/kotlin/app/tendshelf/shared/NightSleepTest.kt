package app.tendshelf.shared

import kotlin.test.Test
import kotlin.test.assertEquals

class NightSleepTest {
    private fun h(hours: Int, minutes: Int = 0) = hours * 60 + minutes

    @Test
    fun windowSameDay() {
        // 13:00 to 15:00
        val cases = listOf(h(12, 59) to false, h(13) to true, h(14, 59) to true, h(15) to false, h(0) to false)
        for ((now, want) in cases) assertEquals(want, inNightWindow(now, h(13), h(15)), "now=$now")
    }

    @Test
    fun windowCrossesMidnight() {
        // 22:00 to 06:00
        val cases = listOf(h(21, 59) to false, h(22) to true, h(23, 59) to true, h(0) to true, h(5, 59) to true, h(6) to false, h(12) to false)
        for ((now, want) in cases) assertEquals(want, inNightWindow(now, h(22), h(6)), "now=$now")
    }

    @Test
    fun equalEdgesAreNever() {
        for (now in listOf(0, h(6), h(12))) assertEquals(false, inNightWindow(now, h(6), h(6)))
    }

    @Test
    fun windowEnd() {
        val minute = 60_000L
        // 23:30:20: the window to 06:00 closes 390 minutes after 23:30:00.
        val nowMs = 1_000_000 * minute + 20_000
        assertEquals(1_000_000 * minute + 390 * minute, nightWindowEndMs(nowMs, h(23, 30), h(6)))
        // Inside, after midnight.
        assertEquals(1_000_000 * minute + 60 * minute, nightWindowEndMs(nowMs, h(5), h(6)))
    }

    private val night = NightSettings(true, h(22), h(6), 30)
    private val now = 5_000_000L

    @Test
    fun shouldStartTable() {
        data class Case(val name: String, val n: NightSettings, val minutes: Int, val sleepOn: Boolean, val offUntil: Long, val want: Boolean)
        val cases = listOf(
            Case("before", night, h(21, 59), false, 0, false),
            Case("inside", night, h(23), false, 0, true),
            Case("after midnight", night, h(2), false, 0, true),
            Case("at the end", night, h(6), false, 0, false),
            Case("disabled", night.copy(enabled = false), h(23), false, 0, false),
            Case("sleep already on", night, h(23), true, 0, false),
            Case("off marker ahead", night, h(23), false, now + 1, false),
            Case("off marker reached", night, h(23), false, now, true),
            Case("off marker past", night, h(23), false, now - 1, true),
            Case("empty window", night.copy(toMinutes = h(22)), h(22), false, 0, false),
        )
        for (c in cases) assertEquals(c.want, shouldStartNightSleep(c.n, now, c.minutes, c.sleepOn, c.offUntil), c.name)
    }
}
