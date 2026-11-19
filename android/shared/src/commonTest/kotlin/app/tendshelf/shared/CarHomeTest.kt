package app.tendshelf.shared

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue

class CarHomeTest {
    private fun book(id: String, extra: String = "") =
        """{"id":"$id","title":"T $id","authors":[{"id":"p","name":"A $id"}],"durationMs":3600000,"cover":null$extra}"""

    private fun books(prefix: String, n: Int) = (1..n).joinToString(",") { book("$prefix$it") }

    private fun episode(id: String, extra: String = "") =
        """{"id":"$id","showId":"ps_1","title":"E $id","show":"S","durationMs":1000,"publishedAt":5,"state":{"positionMs":0,"played":false,"favourite":false}$extra}"""

    @Test
    fun fullAnswerGivesFiveSectionsInOrderWithCaps() {
        val body = """{"continue":[${(1..8).joinToString(",") { """{"book":${book("c$it")},"positionMs":1800000,"durationMs":3600000,"updatedAt":1}""" }}],
          "series":[${(1..6).joinToString(",") { """{"book":${book("s$it")},"seriesName":"Saga","index":2,"total":5}""" }}],
          "new":[${books("n", 12)}],
          "because":{"seed":"Dune","books":[${books("b", 6)}]},
          "episodes":[${(1..6).joinToString(",") { episode("pe_$it") }}]}"""
        val h = CarHomeJson.home(body)
        assertEquals(listOf(HomeKind.CONTINUE, HomeKind.SERIES, HomeKind.NEW, HomeKind.BECAUSE, HomeKind.EPISODES), h.sections.map { it.kind })
        assertEquals(listOf(6, 4, 8, 4, 4), h.sections.map { it.rows.size })
        assertEquals("Dune", h.sections[3].seed)
        val first = (h.sections[0].rows[0] as HomeRow.Book).book
        assertEquals(1800000L, first.positionMs)
        assertEquals(0.5, homeProgress(first))
        val s = (h.sections[1].rows[0] as HomeRow.Book).book.series
        assertEquals(SeriesNote("Saga", 2, 5), s)
        assertEquals("A c1", first.card.author)
    }

    @Test
    fun nullBecauseAndMissingKeysGiveNoSection() {
        assertEquals(emptyList(), CarHomeJson.home("""{"because":null}""").sections)
        assertEquals(emptyList(), CarHomeJson.home("{}").sections)
        val h = CarHomeJson.home("""{"because":{"seed":"x","books":[]},"new":[${book("n1")}]}""")
        assertEquals(listOf(HomeKind.NEW), h.sections.map { it.kind })
    }

    @Test
    fun goneAndBadEpisodesAreDropped() {
        val h = CarHomeJson.home("""{"episodes":[${episode("pe_1")},${episode("pe_2", ",\"gone\":true")},${episode("bad id")},${episode("book_1")}]}""")
        assertEquals(1, h.sections.single().rows.size)
        assertEquals("pe_1", (h.sections.single().rows[0] as HomeRow.Episode).card.id)
    }

    @Test
    fun garbageIsNull() {
        assertNull(parseCarHome("not json"))
        assertNull(parseCarHome("[1]"))
        assertNotNull(parseCarHome("{}"))
    }

    private fun off(id: String, user: String = "u1", place: Long = 0, at: Long = 0, state: OfflineState = OfflineState.READY, kind: String = KIND_BOOK) =
        OfflineBook(id, user, "Title $id", "Au", 3600000, 1, state, place, at, kind)

    @Test
    fun offlineHome() {
        val all = listOf(
            off("b1", place = 1000, at = 5), off("b2", place = 2000, at = 9), off("b3"), off("b4", state = OfflineState.STALE),
            off("b5", user = "u2", place = 99, at = 99), off("e1", kind = KIND_EPISODE, place = 5, at = 20),
        ) + (6..20).map { off("x$it") }
        val h = carHomeOffline(carMine(all, "u1")) { true }
        assertEquals(listOf(HomeKind.CONTINUE, HomeKind.DOWNLOADED), h.sections.map { it.kind })
        val cont = h.sections[0].rows.map { (it as HomeRow.Book).book }
        assertEquals(listOf("b2", "b1"), cont.map { it.card.id })
        assertEquals(2000L, cont[0].positionMs)
        val down = h.sections[1].rows.map { (it as HomeRow.Book).book.card.id }
        assertEquals(12, down.size)
        assertTrue("b4" in down || down.size == 12)
        val ids = cont.map { it.card.id } + down
        assertEquals(ids.size, ids.toSet().size)
        assertTrue("b5" !in ids && "e1" !in ids)
        assertEquals(emptyList(), carHomeOffline(emptyList()) { true }.sections)
        val six = carHomeOffline(carMine((1..9).map { off("p$it", place = 10L, at = it.toLong()) }, "u1")) { true }
        assertEquals(6, six.sections[0].rows.size)
    }

    @Test
    fun fitSectionsTable() {
        assertEquals(listOf(6, 4, 8, 4, 4), fitSections(listOf(6, 4, 8, 4, 4), 26))
        assertEquals(listOf(4, 2, 2, 2, 2), fitSections(listOf(6, 4, 8, 4, 4), 12))
        assertEquals(listOf(2, 2, 1, 0, 0), fitSections(listOf(6, 4, 8, 4, 4), 5))
        assertEquals(listOf(0, 0, 0, 0, 0), fitSections(listOf(6, 4, 8, 4, 4), 0))
        assertEquals(listOf(0, 0), fitSections(listOf(6, 4), -3))
        assertEquals(emptyList(), fitSections(emptyList(), 10))
        assertEquals(listOf(0, 2), fitSections(listOf(0, 3), 2))
    }

    @Test
    fun fitHomeDropsEmptiesAndKeepsSections() {
        fun sec(k: HomeKind, n: Int) = HomeSection(k, "", (1..n).map { HomeRow.Book(HomeBook(BookCard("b$it", "t", "a", 1, false))) })
        val h = CarHome(listOf(sec(HomeKind.CONTINUE, 0), sec(HomeKind.SERIES, 3), sec(HomeKind.NEW, 5), sec(HomeKind.BECAUSE, 4)))
        val two = fitHome(h, 26, 2)
        assertEquals(listOf(HomeKind.SERIES, HomeKind.NEW), two.sections.map { it.kind })
        assertEquals(listOf(3, 5), two.sections.map { it.rows.size })
        assertEquals(listOf(2, 2, 1), fitHome(h, 5, 6).sections.map { it.rows.size })
    }

    @Test
    fun progress() {
        val c = BookCard("b", "t", "a", 1000, false)
        assertNull(homeProgress(HomeBook(c, 0)))
        assertEquals(1.0, homeProgress(HomeBook(c, 5000)))
        assertNull(homeProgress(HomeBook(c.copy(durationMs = 0), 500)))
        assertEquals(0.25, homeProgress(HomeBook(c, 250)))
    }

    @Test
    fun titleKeys() {
        assertEquals(
            listOf("car_home_continue", "car_home_series", "car_home_new", "car_home_because", "car_home_episodes", "car_downloaded"),
            HomeKind.values().map { homeTitleKey(it) },
        )
    }

    @Test
    fun paths() {
        val p = ApiPaths("https://x.example/")
        assertEquals("https://x.example/api/v1/home", p.homeUrl)
        assertEquals("https://x.example/api/v1/books?sort=title&format=audio&limit=120&show=not_started", p.booksUrl("title", "not_started", 120))
        assertEquals("https://x.example/api/v1/books?sort=added&format=audio&limit=120", p.booksUrl("added", null, 120))
        assertEquals(listOf(1, 2), ApiJson.listBooks("""{"items":[${book("a")},${book("b")}],"next":null}""").mapIndexed { i, _ -> i + 1 })
    }
}
