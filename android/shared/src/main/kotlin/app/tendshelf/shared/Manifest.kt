package app.tendshelf.shared

/** One audio file of a book, as `GET /api/v1/books/{id}` lists it. Times are milliseconds. */
data class AudioFile(
    val id: String,
    val idx: Int,
    val offsetMs: Long,
    val durationMs: Long,
    val mime: String,
    val needsCompat: Boolean,
    /** False when the file is missing on the server; the player fills its length with silence. */
    val playable: Boolean = true,
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
    val speed: Double = 1.0,
    val coverUrl: String = "",
    val author: String = "",
    val progress: RemoteProgress? = null,
)

/** The person's stored place in a book, from the server. */
data class RemoteProgress(
    val positionMs: Long,
    val finished: Boolean,
    val clientUpdatedAt: Long,
    val deviceId: String,
    val deviceLabel: String,
)

/** The player settings the person chose, from `GET /api/v1/settings`. */
data class PlayerSettings(
    val speed: Double,
    val skipBackS: Int,
    val skipForwardS: Int,
    val autoRewind: Boolean,
    val nextInSeries: Boolean,
    val rewindSteps: List<RewindStep> = DEFAULT_REWIND,
)
