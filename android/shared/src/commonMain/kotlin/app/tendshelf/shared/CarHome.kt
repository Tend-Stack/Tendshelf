package app.tendshelf.shared

import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject

// The car's Home tab (CH1, docs/plans/car-home.md): one server answer turned into sections, the same offline, and the
// fitting rules both cars share. Android Auto uses it now; CarPlay reads the same functions through the framework.

enum class HomeKind { CONTINUE, SERIES, NEW, BECAUSE, EPISODES, DOWNLOADED }

/** Most rows Home shows on Android Auto, and most sections; a book list in the Library tab holds at most [LIBRARY_LIST_MAX]. */
const val HOME_MAX_ROWS = 26
const val HOME_MAX_SECTIONS = 6
const val LIBRARY_LIST_MAX = 120

/** How many rows a section holds before the car's own limit cuts it further. */
fun homeCap(k: HomeKind): Int = when (k) {
    HomeKind.CONTINUE -> 6
    HomeKind.SERIES -> 4
    HomeKind.NEW -> 8
    HomeKind.BECAUSE -> 4
    HomeKind.EPISODES -> 4
    HomeKind.DOWNLOADED -> 12
}

/** "Book 2 of 5 · Saga": where a book sits in its series. */
data class SeriesNote(val name: String, val index: Int, val total: Int)

data class HomeBook(val card: BookCard, val positionMs: Long = 0, val series: SeriesNote? = null)

sealed interface HomeRow {
    data class Book(val book: HomeBook) : HomeRow
    data class Episode(val card: EpisodeCard) : HomeRow
}

/** One group of rows. [seed] is the book title a BECAUSE section is about; empty for every other kind. */
data class HomeSection(val kind: HomeKind, val seed: String, val rows: List<HomeRow>)

data class CarHome(val sections: List<HomeSection>)

object CarHomeJson {
    /** Reads GET /api/v1/home. Missing keys are empty sections; empty sections are dropped; each is cut to [homeCap]. */
    fun home(body: String): CarHome {
        val j = parseJsonObject(body)
        val sections = mutableListOf<HomeSection>()
        fun add(kind: HomeKind, seed: String, rows: List<HomeRow>) {
            if (rows.isNotEmpty()) sections += HomeSection(kind, seed, rows.take(homeCap(kind)))
        }
        add(
            HomeKind.CONTINUE, "",
            objects(j.optArray("continue")).mapNotNull { o ->
                val book = o.optObject("book") ?: return@mapNotNull null
                HomeRow.Book(HomeBook(ApiJson.bookCard(book), o.optLong("positionMs", 0).coerceAtLeast(0)))
            },
        )
        add(
            HomeKind.SERIES, "",
            objects(j.optArray("series")).mapNotNull { o ->
                val book = o.optObject("book") ?: return@mapNotNull null
                val note = SeriesNote(o.optString("seriesName"), o.optInt("index", 0), o.optInt("total", 0))
                HomeRow.Book(HomeBook(ApiJson.bookCard(book), 0, note))
            },
        )
        add(HomeKind.NEW, "", objects(j.optArray("new")).map { HomeRow.Book(HomeBook(ApiJson.bookCard(it))) })
        val because = j.optObject("because")
        if (because != null) {
            add(
                HomeKind.BECAUSE, because.optString("seed"),
                objects(because.optArray("books")).map { HomeRow.Book(HomeBook(ApiJson.bookCard(it))) },
            )
        }
        add(HomeKind.EPISODES, "", objects(j.optArray("episodes")).mapNotNull { PodcastJson.episodeCardOf(it)?.let { c -> HomeRow.Episode(c) } })
        return CarHome(sections)
    }

    private fun objects(arr: JsonArray?): List<JsonObject> =
        if (arr == null) emptyList() else (0 until arr.size).mapNotNull { arr.optObject(it) }
}

/**
 * Home with no network: Continue listening (downloaded books with a place, newest first) and Downloaded (the rest of the
 * person's downloaded books, by title). No book twice; episodes are left out.
 */
fun carHomeOffline(mine: List<OfflineBook>, hasCover: (String) -> Boolean): CarHome {
    val place = mine.filter { it.kind == KIND_BOOK }.associate { it.bookId to it.placeMs }
    val cont = carContinueOffline(mine, hasCover).take(homeCap(HomeKind.CONTINUE))
    val seen = cont.map { it.id }.toSet()
    val rest = carDownloadedBooks(mine, hasCover).filter { it.id !in seen }.take(homeCap(HomeKind.DOWNLOADED))
    val sections = mutableListOf<HomeSection>()
    if (cont.isNotEmpty()) {
        sections += HomeSection(HomeKind.CONTINUE, "", cont.map { HomeRow.Book(HomeBook(it, place[it.id] ?: 0)) })
    }
    if (rest.isNotEmpty()) {
        sections += HomeSection(HomeKind.DOWNLOADED, "", rest.map { HomeRow.Book(HomeBook(it)) })
    }
    return CarHome(sections)
}

/**
 * How many rows each section keeps when a car allows [max] in all: every section gets up to two first, in order, then the
 * rest are filled in order, so a small limit still shows later sections. A negative [max] is zero.
 */
fun fitSections(sizes: List<Int>, max: Int): List<Int> {
    var left = max.coerceAtLeast(0)
    val out = MutableList(sizes.size) { 0 }
    for (i in sizes.indices) {
        val n = minOf(sizes[i].coerceAtLeast(0), 2, left)
        out[i] = n
        left -= n
    }
    for (i in sizes.indices) {
        val n = minOf(sizes[i].coerceAtLeast(0) - out[i], left)
        if (n > 0) {
            out[i] += n
            left -= n
        }
    }
    return out
}

/** Home cut for a car: empty sections dropped, the first [maxSections] kept, rows cut by [fitSections]. */
fun fitHome(h: CarHome, maxItems: Int, maxSections: Int): CarHome {
    val kept = h.sections.filter { it.rows.isNotEmpty() }.take(maxSections.coerceAtLeast(0))
    val counts = fitSections(kept.map { it.rows.size }, maxItems)
    return CarHome(kept.mapIndexed { i, s -> s.copy(rows = s.rows.take(counts[i])) }.filter { it.rows.isNotEmpty() })
}

/** How far into a book the person is, 0 to 1, or null when there is no place or no length (no bar is drawn). */
fun homeProgress(b: HomeBook): Double? {
    if (b.positionMs <= 0 || b.card.durationMs <= 0) return null
    return (b.positionMs.toDouble() / b.card.durationMs).coerceIn(0.0, 1.0)
}

/** The strings key of a section's title (Android `R.string`, iOS `CarWords`). */
fun homeTitleKey(k: HomeKind): String = when (k) {
    HomeKind.CONTINUE -> "car_home_continue"
    HomeKind.SERIES -> "car_home_series"
    HomeKind.NEW -> "car_home_new"
    HomeKind.BECAUSE -> "car_home_because"
    HomeKind.EPISODES -> "car_home_episodes"
    HomeKind.DOWNLOADED -> "car_downloaded"
}
