package app.tendshelf.shared

/** A place inside one file of a book. */
data class FilePosition(val fileIndex: Int, val positionMs: Long)

/**
 * Maps between a book position (what the server stores) and a place in one of the book's files (what a
 * player has). Pure functions, so every platform can share and test them.
 */
object Position {
    /** The file that holds [bookMs], and the place in it. Past the end gives the end of the last file. */
    fun locate(files: List<AudioFile>, bookMs: Long): FilePosition {
        require(files.isNotEmpty()) { "a book has at least one file" }
        val wanted = bookMs.coerceAtLeast(0)
        for (i in files.indices.reversed()) {
            if (wanted >= files[i].offsetMs) {
                val inFile = (wanted - files[i].offsetMs).coerceAtMost(files[i].durationMs)
                return FilePosition(i, inFile)
            }
        }
        return FilePosition(0, 0)
    }

    /** The book position of [positionMs] inside file [fileIndex]. */
    fun bookPosition(files: List<AudioFile>, fileIndex: Int, positionMs: Long): Long {
        val file = files[fileIndex]
        return file.offsetMs + positionMs.coerceIn(0, file.durationMs)
    }

    /** The chapter that holds [bookMs], or null when the book has none. The last chapter owns the end. */
    fun chapterAt(chapters: List<Chapter>, bookMs: Long): Chapter? {
        if (chapters.isEmpty()) return null
        return chapters.lastOrNull { it.startMs <= bookMs } ?: chapters.first()
    }
}

/** A chapter shorter than this into itself is "at the start": previous goes to the chapter before. */
const val RESTART_WINDOW_MS = 3_000L

/** The start of the chapter after the one holding [bookMs], or null in the last chapter. Chapters are in order. */
fun nextChapterStart(chapters: List<Chapter>, bookMs: Long): Long? =
    chapters.firstOrNull { it.startMs > bookMs }?.startMs

/**
 * Where "previous" goes: the start of this chapter when more than [RESTART_WINDOW_MS] into it, else the start
 * of the chapter before (0 in the first one). Same rule as the web player's `jumpChapter`.
 */
fun previousChapterStart(chapters: List<Chapter>, bookMs: Long): Long {
    val current = Position.chapterAt(chapters, bookMs) ?: return 0
    if (bookMs - current.startMs > RESTART_WINDOW_MS) return current.startMs
    return chapters.lastOrNull { it.startMs < current.startMs }?.startMs ?: 0
}

/**
 * Where a book starts: [atMs] when given, else the stored place, and the beginning when it was finished.
 * A [pending] save not yet sent counts when it is newer than the stored place, as the web player's
 * `useNewerPendingSave` does.
 */
fun startPlace(p: RemoteProgress?, atMs: Long?, pending: Save? = null): Long = when {
    atMs != null && atMs >= 0 -> atMs
    pending != null && (p == null || pending.clientUpdatedAt > p.clientUpdatedAt) ->
        if (pending.finished) 0 else pending.positionMs.coerceAtLeast(0)
    p == null || p.finished -> 0
    else -> p.positionMs.coerceAtLeast(0)
}

/** When the stored place was written, for the rewind after a break; null when there is none. */
fun placeWrittenAt(p: RemoteProgress?, pending: Save?): Long? =
    listOfNotNull(p?.clientUpdatedAt, pending?.clientUpdatedAt).maxOrNull()

/** The book-time ranges (start inclusive, end exclusive) of files the server does not have. */
fun silentRanges(files: List<AudioFile>): List<LongRange> =
    files.filter { !it.playable }.map { it.offsetMs until it.offsetMs + it.durationMs }

/**
 * Where to jump when [bookMs] is inside a silent range: past its end, merging neighbouring silent ranges.
 * Null when the place holds sound.
 */
fun skipSilence(ranges: List<LongRange>, bookMs: Long): Long? {
    var at = bookMs
    var moved = false
    for (r in ranges.sortedBy { it.first }) {
        if (at in r) {
            at = r.last + 1
            moved = true
        }
    }
    return if (moved) at else null
}
