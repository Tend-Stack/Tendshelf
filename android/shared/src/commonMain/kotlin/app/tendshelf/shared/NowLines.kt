package app.tendshelf.shared

/** The lines a lock screen or notification shows for what plays (decision 0048). [subtitle] is null when there is none. */
data class NowLines(val title: String, val album: String, val artist: String, val subtitle: String?)

/**
 * A station shows its name and, beneath it, the song on air (or [liveWord]). An episode shows its own title, the
 * show as album and artist, and the chapter beneath. A book shows the chapter as the title (the book's, with no
 * chapters), the book as album and the author as artist. [positionMs] picks the chapter.
 */
fun nowLines(
    live: Boolean,
    title: String,
    author: String,
    episode: String?,
    chapters: List<Chapter>,
    positionMs: Long,
    liveNow: String,
    liveWord: String,
): NowLines {
    if (live) {
        val line = liveSubtitle(liveNow, liveWord)
        return NowLines(title = title, album = title, artist = line, subtitle = line)
    }
    val chapter = Position.chapterAt(chapters, positionMs)
    return NowLines(
        title = episode ?: chapter?.title ?: title,
        album = title,
        artist = if (episode != null) title else author,
        subtitle = if (episode != null && chapter != null) chapter.title else null,
    )
}
