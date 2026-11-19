package app.tendshelf.shared

/**
 * Where "end of chapter" is for the sleep timer, or null when there is none (decision 0048: both phone apps).
 * A station has no end; a whole episode ends 1 s before its duration. A book with no chapters is one chapter (as
 * the page treats it), so its end is the end of the book. The last chapter is cut 1 s short, so the player never
 * reaches its end and the queue is not asked (D6). A [durationMs] of 0 or less is unknown.
 */
fun sleepChapterEnd(
    live: Boolean,
    wholeEpisode: Boolean,
    chapters: List<Chapter>,
    positionMs: Long,
    durationMs: Long,
): Long? {
    if (live) return null
    if (wholeEpisode) return if (durationMs > 1000) durationMs - 1000 else null
    val list = chapters.ifEmpty {
        if (durationMs > 0) listOf(Chapter(0, 0, durationMs, "")) else emptyList()
    }
    val end = chapterEndFor(list, positionMs) ?: return null
    return if (durationMs > 1000) minOf(end, durationMs - 1000) else end
}

/** Where the paused player rests when the timer finishes: [restAtMs], but 1 s before the end of the item (D6). */
fun sleepRestAt(restAtMs: Long, durationMs: Long): Long =
    if (durationMs > 0) minOf(restAtMs, durationMs - 1000).coerceAtLeast(0) else restAtMs
