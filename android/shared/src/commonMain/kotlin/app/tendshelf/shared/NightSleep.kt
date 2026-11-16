package app.tendshelf.shared

/**
 * Night sleep as the person set it (`GET /api/v1/settings`): between [fromMinutes] and [toMinutes] (minutes after
 * midnight on the device's clock) starting playback starts the sleep timer for [minutes].
 */
data class NightSettings(val enabled: Boolean, val fromMinutes: Int, val toMinutes: Int, val minutes: Int) {
    companion object {
        val OFF = NightSettings(false, 0, 0, 30)
    }
}

// The rule of the page's night sleep (`player-full.js`, "Night sleep"), pure so the app can own it.

/** Whether a time of day is in the window; it wraps midnight when [from] is later than [to]. The start is in, the end is out, from == to is empty. */
fun inNightWindow(nowMinutes: Int, from: Int, to: Int): Boolean {
    if (from == to) return false
    if (from < to) return nowMinutes >= from && nowMinutes < to
    return nowMinutes >= from || nowMinutes < to
}

/**
 * The moment the window closes (only meaningful inside it). [nowMs] is epoch milliseconds and [nowMinutes] the local
 * minutes after midnight at that moment; the seconds of the current minute are cut off, as the page does.
 */
fun nightWindowEndMs(nowMs: Long, nowMinutes: Int, to: Int): Long {
    val minutesLeft = (to - nowMinutes + 1440) % 1440
    val intoMinute = ((nowMs % 60_000L) + 60_000L) % 60_000L
    return nowMs - intoMinute + minutesLeft * 60_000L
}

/**
 * Whether playback that just started should start the timer: night sleep is on, no timer runs ([sleepOn] false), the time is
 * in the window, and the person has not switched it off until a moment still ahead ([offUntilMs]).
 */
fun shouldStartNightSleep(night: NightSettings, nowMs: Long, nowMinutes: Int, sleepOn: Boolean, offUntilMs: Long): Boolean {
    if (!night.enabled || sleepOn) return false
    if (!inNightWindow(nowMinutes, night.fromMinutes, night.toMinutes)) return false
    return offUntilMs <= nowMs
}
