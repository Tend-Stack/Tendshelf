package app.tendshelf.shared

import kotlin.math.abs

/** How often a place goes to disk while sound plays (about twice a second the player ticks; this is the gate). */
const val SAVE_TICK_MS = 10_000L

/**
 * When the next place is due, apart from the player; both phone apps use it (decision 0048). Times are a
 * monotonic clock in milliseconds. Main thread only.
 */
class SaveCadence {
    private var lastSaveAt = 0L
    private var lastPos = -1L
    private var lastBook = ""

    /** Sound started: the first save is a full interval away. */
    fun playing(nowMs: Long) {
        lastSaveAt = nowMs
    }

    /**
     * True when the place should be written now. False inside [SAVE_TICK_MS] of the last save; true on [retry]
     * (the disk refused the last place); false, and the interval restarts, when the same book has moved under a second.
     */
    fun due(nowMs: Long, bookId: String, positionMs: Long, retry: Boolean): Boolean {
        if (nowMs - lastSaveAt < SAVE_TICK_MS) return false
        if (retry) return true
        if (bookId == lastBook && abs(positionMs - lastPos) < 1_000) {
            lastSaveAt = nowMs
            return false
        }
        return true
    }

    /** A place was written. */
    fun saved(nowMs: Long, bookId: String, positionMs: Long) {
        lastSaveAt = nowMs
        lastPos = positionMs
        lastBook = bookId
    }
}

/**
 * Listening time since the last save, as the page's `heardMs`: wall time while sound plays (not book time, so
 * speed does not change it). Pausing keeps the time counted until the next [drain].
 */
class ListenedClock {
    private var playingSince = 0L
    private var heardMs = 0L

    /** Sound started. */
    fun playing(nowMs: Long) {
        playingSince = nowMs
    }

    /** Sound stopped: the stretch since it started counts, and stays for the save that follows. */
    fun stopped(nowMs: Long) {
        if (playingSince != 0L) heardMs += nowMs - playingSince
        playingSince = 0L
    }

    /** The time counted since the last drain; the clock keeps running when [stillPlaying]. */
    fun drain(nowMs: Long, stillPlaying: Boolean): Long {
        if (playingSince != 0L) {
            heardMs += nowMs - playingSince
            playingSince = if (stillPlaying) nowMs else 0L
        }
        val out = heardMs
        heardMs = 0
        return out
    }
}

/** The wait before the next try of a failed send: [first], doubling to [max]; [reset] after a success. */
class Backoff(private val first: Long = 5_000L, private val max: Long = 300_000L) {
    private var wait = first

    /** The wait now; the following one is doubled (capped). */
    @SynchronizedOnJvm
    fun next(): Long {
        val out = wait
        wait = (wait * 2).coerceAtMost(max)
        return out
    }

    @SynchronizedOnJvm
    fun reset() {
        wait = first
    }
}
