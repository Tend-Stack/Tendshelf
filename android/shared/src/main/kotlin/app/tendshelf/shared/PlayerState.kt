package app.tendshelf.shared

/**
 * What any client of the player sees, in book time. [bookId] and [error] are empty when there is none.
 * [error] is one of `stream`, `book`, `signedOut`.
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
)
