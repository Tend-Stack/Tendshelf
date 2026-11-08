package app.tendshelf.shared

/** A pause of at least [fromMs] steps back [seconds]. The server's table is the one source (`rewindSteps`). */
data class RewindStep(val fromMs: Long, val seconds: Int)

/** Used only when the server does not send a table (an older one): the same values as `rewind.go`. */
val DEFAULT_REWIND: List<RewindStep> = listOf(
    RewindStep(10_000, 2),
    RewindStep(60_000, 5),
    RewindStep(3_600_000, 10),
    RewindStep(86_400_000, 20),
)

/** How far to step back, in milliseconds, after a pause of [awayMs]; 0 when off or the pause was short. */
fun rewindMs(awayMs: Long, steps: List<RewindStep>, enabled: Boolean): Long {
    if (!enabled) return 0
    var seconds = 0
    for (s in steps.sortedBy { it.fromMs }) if (awayMs >= s.fromMs) seconds = s.seconds
    return seconds * 1000L
}

/** The place to resume at: [positionMs] less the rewind, never before the start. */
fun rewoundPosition(positionMs: Long, awayMs: Long, steps: List<RewindStep>, enabled: Boolean): Long =
    (positionMs - rewindMs(awayMs, steps, enabled)).coerceAtLeast(0)
