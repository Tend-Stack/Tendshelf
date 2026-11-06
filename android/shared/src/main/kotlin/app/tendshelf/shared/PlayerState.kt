package app.tendshelf.shared

/**
 * What any client of the player sees, in book time. [bookId] and [error] are empty when there is none.
 * [error] is one of `stream`, `book`, `signedOut`, `background` (the system refused a foreground start).
 * [ask] is the open place question, [rewoundS] the seconds the last resume went back.
 */
data class PlayerSnapshot(
    val bookId: String,
    val positionMs: Long,
    val durationMs: Long,
    val playing: Boolean,
    val buffering: Boolean,
    val ended: Boolean,
    val speed: Double,
    val chapterIdx: Int,
    val error: String,
    val ask: Ask? = null,
    val rewoundS: Int = 0,
    /** Counts resumes that went back, so two in a row of the same length are still two events. */
    val rewoundSeq: Int = 0,
)
