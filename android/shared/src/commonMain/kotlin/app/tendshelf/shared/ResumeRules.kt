package app.tendshelf.shared

/** What a start of sound after a break asks for: step back [rewindMs] (0 = none) and, maybe, look at the server's place. */
data class ResumeStep(val rewindMs: Long, val checkRemote: Boolean)

/**
 * The decision behind smart rewind and the check for a place elsewhere (A5, D6), apart from the player. The
 * caller feeds it what the player does; [now] is the clock. Main thread only.
 */
class ResumeClock(private val now: () -> Long) {
    private var idleAt: Long? = null
    private var fresh = false

    /** The player stopped making sound. Buffering with the play button on is not a pause. */
    fun stopped(hasBook: Boolean, buffering: Boolean) {
        if (!buffering && hasBook && idleAt == null) idleAt = now()
    }

    /** A book was loaded. [placeWrittenAt] is when its stored place was saved; null with [atGiven] or no place. */
    fun bookLoaded(placeWrittenAt: Long?, atGiven: Boolean) {
        idleAt = if (atGiven) null else placeWrittenAt
        fresh = true
    }

    fun clear() {
        idleAt = null
    }

    /** Sound started. Null when there was no break to speak of (no idle mark). */
    fun resumed(steps: List<RewindStep>, enabled: Boolean): ResumeStep? {
        val wasFresh = fresh
        fresh = false // the first sound after a load is the start, whatever came before it
        val since = idleAt ?: return null
        idleAt = null
        val away = (now() - since).coerceAtLeast(0)
        return ResumeStep(rewindMs(away, steps, enabled), !wasFresh && away >= BREAK_MS)
    }
}
