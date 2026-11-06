package app.tendshelf.shared

/** One audio file of a book, as `GET /api/v1/books/{id}` lists it. Times are milliseconds. */
data class AudioFile(
    val id: String,
    val idx: Int,
    val offsetMs: Long,
    val durationMs: Long,
    val mime: String,
    val needsCompat: Boolean,
)

/** A chapter of the whole book; its times are book positions, not file positions. */
data class Chapter(
    val idx: Int,
    val startMs: Long,
    val endMs: Long,
    val title: String,
)

/** The part of the download manifest the player needs. */
data class BookManifest(
    val id: String,
    val title: String,
    val durationMs: Long,
    val files: List<AudioFile>,
    val chapters: List<Chapter>,
)
