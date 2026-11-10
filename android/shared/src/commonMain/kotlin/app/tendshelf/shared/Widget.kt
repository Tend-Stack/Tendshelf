package app.tendshelf.shared

/** What the home-screen widget shows as a whole: a plain line, or the book. */
enum class WidgetKind { SIGNED_OUT, NO_BOOK, BOOK }

/** What the middle button does for a tap. */
enum class WidgetTap { PAUSE, PLAY, RESUME_LAST }

/** What the widget knows: the person's sign-in, and the last book that played. Titles and times only, never a credential. */
data class WidgetInput(
    val signedIn: Boolean,
    val bookId: String,
    val title: String,
    val chapter: String,
    val playing: Boolean,
    val skipBackS: Int,
    val skipForwardS: Int,
    /** The show's name while an episode plays (the title is then the episode's); empty for a book. */
    val show: String = "",
    /** A live station plays (R3): the title is its name, [show] the word "Live", and there is nothing to skip. */
    val live: Boolean = false,
)

/** What to draw. [skipBackS] and [skipForwardS] are always positive, for the buttons' spoken labels. */
data class WidgetLook(
    val kind: WidgetKind,
    val title: String,
    val chapter: String,
    val showCover: Boolean,
    val showChapter: Boolean,
    val showSkip: Boolean,
    val playing: Boolean,
    val skipBackS: Int,
    val skipForwardS: Int,
)

/** Skip amounts when the person's own are not known on this phone: the same as the media notification. */
const val WIDGET_DEFAULT_SKIP_S = 30

// Widths and heights in dp. As the widget narrows it drops, in this order: the skip buttons, the chapter, the cover.
// Play, and the title, stay at every size.
const val WIDGET_SKIP_MIN_WIDTH_DP = 280
const val WIDGET_CHAPTER_MIN_WIDTH_DP = 220
const val WIDGET_CHAPTER_MIN_HEIGHT_DP = 130
const val WIDGET_COVER_MIN_WIDTH_DP = 150

fun widgetLook(input: WidgetInput, widthDp: Int, heightDp: Int): WidgetLook {
    val kind = when {
        !input.signedIn -> WidgetKind.SIGNED_OUT
        input.bookId.isEmpty() -> WidgetKind.NO_BOOK
        else -> WidgetKind.BOOK
    }
    val back = input.skipBackS.takeIf { it > 0 } ?: WIDGET_DEFAULT_SKIP_S
    val forward = input.skipForwardS.takeIf { it > 0 } ?: WIDGET_DEFAULT_SKIP_S
    if (kind != WidgetKind.BOOK) {
        return WidgetLook(kind, "", "", false, false, false, false, back, forward)
    }
    // An episode: its title, and the show beneath it where a book has its chapter.
    val show = input.show.trim()
    val chapter = if (show.isNotEmpty()) show else input.chapter.trim()
    // A book without chapters titles its one stretch with its own name: that line would only repeat.
    val hasChapter = chapter.isNotEmpty() && (show.isNotEmpty() || !chapter.equals(input.title.trim(), ignoreCase = true))
    return WidgetLook(
        kind = kind,
        title = input.title,
        chapter = chapter,
        showCover = widthDp >= WIDGET_COVER_MIN_WIDTH_DP,
        showChapter = hasChapter && widthDp >= WIDGET_CHAPTER_MIN_WIDTH_DP && heightDp >= WIDGET_CHAPTER_MIN_HEIGHT_DP,
        showSkip = !input.live && widthDp >= WIDGET_SKIP_MIN_WIDTH_DP,
        playing = input.playing,
        skipBackS = back,
        skipForwardS = forward,
    )
}

/**
 * The middle button. A loaded book toggles; with nothing loaded (the service was stopped or the phone restarted)
 * a tap starts the last book, at the place the server holds.
 */
fun widgetPlayTap(playing: Boolean, loaded: Boolean): WidgetTap = when {
    !loaded -> WidgetTap.RESUME_LAST
    playing -> WidgetTap.PAUSE
    else -> WidgetTap.PLAY
}
