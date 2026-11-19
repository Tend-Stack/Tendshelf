package app.tendshelf.shared

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue

class LibraryTreeTest {
    @Test
    fun nodesRoundTrip() {
        val all = listOf(Node.Root, Node.Continue, Node.UpNext, Node.Shelves, Node.Downloaded, Node.Shelf("01HX_a-b"), Node.Book("01HZ"))
        for (n in all) assertEquals(n, parseNode(nodeId(n)), nodeId(n))
    }

    @Test
    fun homeAndLibraryNodesRoundTrip() {
        val ids = listOf("home", "library", "library-not-started", "library-recent", "library-title", "library-author")
        for (id in ids) assertEquals(id, parseNode(id)?.let(::nodeId), id)
        assertEquals(Node.Home, parseNode("home"))
        assertEquals(Node.LibraryAuthor, parseNode("library-author"))
    }

    @Test
    fun badIdsAreNull() {
        for (bad in listOf("", "book:", "book:a/b", "book:a b", "shelf:?x", "shelf:", "series", "Root", "book:" + "a".repeat(65), "book:a\nb", "../x")) {
            assertNull(parseNode(bad), bad)
        }
    }

    @Test
    fun slices() {
        assertEquals(0..9, slice(25, Page(0, 10)))
        assertEquals(10..19, slice(25, Page(1, 10)))
        assertEquals(20..24, slice(25, Page(2, 10)))
        assertTrue(slice(25, Page(3, 10)).isEmpty())
        assertTrue(slice(0, Page(0, 10)).isEmpty())
        assertTrue(slice(5, Page(-1, 10)).isEmpty())
        assertTrue(slice(5, Page(0, 0)).isEmpty())
        assertEquals(0..4, slice(5, Page(0, Int.MAX_VALUE)))
        assertTrue(slice(5, Page(Int.MAX_VALUE, Int.MAX_VALUE)).isEmpty())
    }

    @Test
    fun onlyTrustedControllersBrowse() {
        // trusted, auto, own
        val table = listOf(
            Triple(false, false, false) to Allowed(false, false, false, false), // any other installed app
            Triple(true, false, false) to Allowed(true, false, false, true),
            Triple(false, true, false) to Allowed(true, false, true, true),
            Triple(false, false, true) to Allowed(true, true, false, true),
            Triple(true, true, true) to Allowed(true, true, true, true),
        )
        for ((input, want) in table) assertEquals(want, allowedCommands(input.first, input.second, input.third), input.toString())
        assertFalse(allowedCommands(false, false, false).speed)
        assertFalse(allowedCommands(trusted = true, auto = false, own = false).speed, "the system's controls get no speed button")
        assertTrue(allowedCommands(trusted = false, auto = true, own = false).speed, "the car does")
        assertFalse(allowedCommands(trusted = true, auto = false, own = true).speed, "nor does this app's own controller")
        assertTrue(mayRate(true))
        assertFalse(mayRate(false), "only this app's page sets a rate directly")
        assertFalse(allowedCommands(false, false, false).readsPlayer, "another app must not read what is playing")
    }

    @Test
    fun speedCycles() {
        assertEquals(1.25, speedCycle(1.0))
        assertEquals(2.0, speedCycle(1.75))
        assertEquals(0.75, speedCycle(2.0))
        assertEquals(1.0, speedCycle(0.75))
        assertEquals(1.0, speedCycle(1.1)) // not a step: start again
        assertEquals(1.75, speedCycle(1.499)) // within a hair of 1.5
    }

    @Test
    fun speedLabels() {
        assertEquals("1.0x", speedLabel(1.0))
        assertEquals("1.25x", speedLabel(1.25))
        assertEquals("0.75x", speedLabel(0.75))
    }

    @Test
    fun listUrls() {
        val api = ApiPaths("http://h/")
        assertEquals("http://h/api/v1/continue?limit=24", api.continueUrl(24))
        assertEquals("http://h/api/v1/shelves/s1", api.shelfUrl("s1"))
        assertEquals("http://h/api/v1/search?q=a+b%26c&limit=1", api.searchUrl("a b&c", 1))
        assertEquals("http://h/api/v1/books/b1/speed", api.speedUrl("b1"))
        assertFailsWith<IllegalArgumentException> { api.shelfUrl("a/b") }
    }

    private fun dl(id: String, title: String, user: String = "u", state: OfflineState = OfflineState.READY, place: Long = 0, at: Long = 0) =
        OfflineBook(id, user, title, "A", 1000, 1, state, place, at)

    @Test
    fun carMineKeepsWholeRowsOfThePerson() {
        val all = listOf(
            dl("a", "A"), dl("b", "B", state = OfflineState.STALE), dl("c", "C", state = OfflineState.PAUSED),
            dl("d", "D", user = "other"),
        )
        assertEquals(listOf("a", "b"), carMine(all, "u").map { it.bookId })
        assertEquals(listOf("a", "b", "d"), carMine(all, null).map { it.bookId })
    }

    @Test
    fun carDownloadedBooksSortByLowercasedTitle() {
        val rows = listOf(dl("1", "banana"), dl("2", "Apple"), dl("3", "cherry"))
        val cards = carDownloadedBooks(rows) { it == "2" }
        assertEquals(listOf("2", "1", "3"), cards.map { it.id })
        assertEquals(listOf(true, false, false), cards.map { it.hasCover })
    }

    @Test
    fun carContinueOfflineIsPlacedBooksNewestFirst() {
        val rows = listOf(dl("1", "A", place = 5, at = 10), dl("2", "B"), dl("3", "C", place = 7, at = 30), dl("4", "D", place = 1, at = 20))
        assertEquals(listOf("3", "4", "1"), carContinueOffline(rows) { true }.map { it.id })
    }

    @Test
    fun carBookListsLeaveDownloadedEpisodesOut() {
        val episode = dl("e", "An episode", place = 9, at = 99).copy(kind = KIND_EPISODE, showId = "s")
        val rows = listOf(dl("1", "Book", place = 5, at = 10), episode)
        assertEquals(listOf("1"), carDownloadedBooks(rows) { true }.map { it.id })
        assertEquals(listOf("1"), carContinueOffline(rows) { true }.map { it.id })
    }
}
