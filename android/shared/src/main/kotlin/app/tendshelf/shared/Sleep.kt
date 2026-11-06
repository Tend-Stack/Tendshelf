package app.tendshelf.shared

/** Off, a number of real minutes, or the end of the chapter that was playing when it was set. */
enum class SleepMode { OFF, MINUTES, CHAPTER }

/** What the page draws: [choice] is the sheet row that is on (`""` after "+10 min"); [leftMs] is real listening time. */
data class SleepInfo(val mode: SleepMode, val choice: String, val leftMs: Long, val fading: Boolean) {
    companion object {
        val OFF = SleepInfo(SleepMode.OFF, "", 0, false)
    }
}

/** One tick's answer: keep playing at [volume] (1 outside the fade), or the timer ran out. */
sealed interface SleepStep {
    data class Run(val volume: Float) : SleepStep

    /** Pause now. [restAtMs] is the chapter end to rest at for a chapter timer, else null. The clock is off again. */
    data class Finish(val restAtMs: Long?) : SleepStep
}

/**
 * The sleep timer of the web player (`player-full.js`, "Sleep timer"), pure so another client can reuse it. It counts
 * only while sound plays; minutes are real minutes at any speed; a chapter timer is the chapter's end at the current
 * speed; the last [FADE_MS] fade the volume out; skipping ahead moves a chapter timer to the chapter then playing.
 */
class SleepClock {
    private var mode = SleepMode.OFF
    private var choice = ""
    private var leftMs = 0L
    private var endMs = 0L
    private var volume = 1f
    private var fading = false

    val isOn: Boolean get() = mode != SleepMode.OFF

    /** [choice] is the sheet row (`chapter`, `custom`, or the minutes as text); [chapterEndMs] is where a chapter timer ends. */
    fun set(choice: String, minutes: Int, chapterEndMs: Long) {
        this.choice = choice
        mode = if (choice == "chapter") SleepMode.CHAPTER else SleepMode.MINUTES
        endMs = if (choice == "chapter") chapterEndMs else 0
        leftMs = minutes * 60_000L
        stopFade()
    }

    fun off() {
        mode = SleepMode.OFF
        choice = ""
        leftMs = 0
        stopFade()
    }

    /** "+10 min": adds to what is left; a chapter timer becomes a minutes timer. */
    fun plus(positionMs: Long, speed: Double) {
        if (mode == SleepMode.OFF) return
        leftMs = left(positionMs, speed) + PLUS_MS
        mode = SleepMode.MINUTES
        choice = ""
        stopFade()
    }

    /** A chapter timer follows to the chapter then playing (the book changed). */
    fun followChapter(chapterEndMs: Long) {
        if (mode == SleepMode.CHAPTER) endMs = chapterEndMs
    }

    /** [deltaMs] of wall time since the last step; [chapterEndNow] is what a chapter timer would end at right now. */
    fun step(deltaMs: Long, playing: Boolean, positionMs: Long, speed: Double, chapterEndNow: Long): SleepStep {
        if (mode == SleepMode.OFF || !playing) return SleepStep.Run(volume)
        if (mode == SleepMode.CHAPTER && positionMs > endMs + SKIPPED_AHEAD_MS) endMs = chapterEndNow
        if (mode == SleepMode.MINUTES) leftMs -= deltaMs
        val left = left(positionMs, speed)
        if (left <= 0) {
            val rest = if (mode == SleepMode.CHAPTER) endMs else null
            off()
            return SleepStep.Finish(rest)
        }
        if (left <= FADE_MS) {
            fading = true
            volume = (left.toFloat() / FADE_MS).coerceIn(0f, 1f)
        } else if (fading) {
            stopFade()
        }
        return SleepStep.Run(volume)
    }

    fun info(positionMs: Long, speed: Double): SleepInfo =
        if (mode == SleepMode.OFF) SleepInfo.OFF else SleepInfo(mode, choice, left(positionMs, speed), fading)

    private fun left(positionMs: Long, speed: Double): Long =
        if (mode == SleepMode.CHAPTER) (Math.max(0L, endMs - positionMs) / speed).toLong() else leftMs

    private fun stopFade() {
        fading = false
        volume = 1f
    }

    companion object {
        const val FADE_MS = 10_000L
        const val PLUS_MS = 600_000L
        const val SKIPPED_AHEAD_MS = 1_500L
        const val CHAPTER_MIN_LEFT_MS = 2_000L
    }
}

/**
 * The end-of-chapter choice ends at this chapter's end, or the next one's when less than [SleepClock.CHAPTER_MIN_LEFT_MS]
 * is left (the same rule as `chapterEndNow`). Null when the book has no chapters.
 */
fun chapterEndFor(chapters: List<Chapter>, positionMs: Long): Long? {
    val here = Position.chapterAt(chapters, positionMs) ?: return null
    val pick = if (here.endMs - positionMs < SleepClock.CHAPTER_MIN_LEFT_MS) chapters.firstOrNull { it.idx == here.idx + 1 } ?: here else here
    return pick.endMs
}
