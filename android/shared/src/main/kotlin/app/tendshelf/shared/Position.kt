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
