package app.tendshelf.shared

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class WidgetTest {
    private val book = WidgetInput(true, "b1", "Dune", "Chapter 3", true, 15, 45)

    @Test
    fun signedOutWinsOverABook() {
        val look = widgetLook(book.copy(signedIn = false), 300, 150)
        assertEquals(WidgetKind.SIGNED_OUT, look.kind)
        assertEquals("", look.title)
    }

    @Test
    fun noBookYet() {
        val look = widgetLook(book.copy(bookId = ""), 300, 150)
        assertEquals(WidgetKind.NO_BOOK, look.kind)
        assertEquals("", look.title)
        assertFalse(look.showCover)
    }

    @Test
    fun wideShowsEverything() {
        val look = widgetLook(book, 300, 150)
        assertEquals(WidgetKind.BOOK, look.kind)
        assertTrue(look.showCover && look.showChapter && look.showSkip)
        assertEquals(15, look.skipBackS)
        assertEquals(45, look.skipForwardS)
    }

    @Test
    fun narrowDropsSkipThenChapterThenCover() {
        val mid = widgetLook(book, WIDGET_SKIP_MIN_WIDTH_DP - 1, 150)
        assertTrue(mid.showChapter && !mid.showSkip && mid.showCover)
        val small = widgetLook(book, WIDGET_CHAPTER_MIN_WIDTH_DP - 1, 150)
        assertTrue(!small.showChapter && !small.showSkip && small.showCover)
        val tiny = widgetLook(book, WIDGET_COVER_MIN_WIDTH_DP - 1, 150)
        assertTrue(!tiny.showCover && !tiny.showChapter && !tiny.showSkip)
        assertEquals("Dune", tiny.title)
    }

    @Test
    fun shortDropsChapter() {
        assertFalse(widgetLook(book, 300, WIDGET_CHAPTER_MIN_HEIGHT_DP - 1).showChapter)
    }

    @Test
    fun chapterThatRepeatsTheTitleIsLeftOut() {
        assertFalse(widgetLook(book.copy(chapter = " dune "), 300, 150).showChapter)
        assertFalse(widgetLook(book.copy(chapter = ""), 300, 150).showChapter)
    }

    @Test
    fun unknownSkipFallsBackToTheNotificationDefault() {
        val look = widgetLook(book.copy(skipBackS = 0, skipForwardS = -1), 300, 150)
        assertEquals(WIDGET_DEFAULT_SKIP_S, look.skipBackS)
        assertEquals(WIDGET_DEFAULT_SKIP_S, look.skipForwardS)
    }

    @Test
    fun playTap() {
        assertEquals(WidgetTap.PAUSE, widgetPlayTap(playing = true, loaded = true))
        assertEquals(WidgetTap.PLAY, widgetPlayTap(playing = false, loaded = true))
        assertEquals(WidgetTap.RESUME_LAST, widgetPlayTap(playing = false, loaded = false))
        assertEquals(WidgetTap.RESUME_LAST, widgetPlayTap(playing = true, loaded = false))
    }
}
