package app.tendshelf.shared

/** One place in the car's browse tree (A8, D2 and D3). Books are leaves: `book:<id>` is a book, never a file. */
sealed interface Node {
    object Root : Node
    object Continue : Node
    object UpNext : Node
    object Shelves : Node
    object Downloaded : Node
    data class Shelf(val id: String) : Node
    data class Book(val id: String) : Node

    /** Podcasts (A3): a folder of three, the person's shows, and their episodes as leaves. */
    object Podcasts : Node
    object PodcastNew : Node
    object PodcastDownloaded : Node
    object PodcastShows : Node
    data class Show(val id: String) : Node
    data class Episode(val id: String) : Node
}

/** The node a media id names, or null for anything else (a bad id is an error, never a guess). */
fun parseNode(id: String): Node? = when (id) {
    "root" -> Node.Root
    "continue" -> Node.Continue
    "queue" -> Node.UpNext
    "shelves" -> Node.Shelves
    "downloaded" -> Node.Downloaded
    "podcasts" -> Node.Podcasts
    "podcasts-new" -> Node.PodcastNew
    "podcasts-downloaded" -> Node.PodcastDownloaded
    "podcasts-shows" -> Node.PodcastShows
    else -> when {
        id.startsWith("shelf:") -> id.removePrefix("shelf:").takeIf(::validId)?.let { Node.Shelf(it) }
        id.startsWith("book:") -> id.removePrefix("book:").takeIf(::validId)?.let { Node.Book(it) }
        id.startsWith("show:") -> id.removePrefix("show:").takeIf(::validId)?.let { Node.Show(it) }
        id.startsWith("episode:") -> id.removePrefix("episode:").takeIf { validId(it) && isEpisodeId(it) }?.let { Node.Episode(it) }
        else -> null
    }
}

fun nodeId(n: Node): String = when (n) {
    Node.Root -> "root"
    Node.Continue -> "continue"
    Node.UpNext -> "queue"
    Node.Shelves -> "shelves"
    Node.Downloaded -> "downloaded"
    is Node.Shelf -> "shelf:${n.id}"
    is Node.Book -> "book:${n.id}"
    Node.Podcasts -> "podcasts"
    Node.PodcastNew -> "podcasts-new"
    Node.PodcastDownloaded -> "podcasts-downloaded"
    Node.PodcastShows -> "podcasts-shows"
    is Node.Show -> "show:${n.id}"
    is Node.Episode -> "episode:${n.id}"
}

/** The id the engine starts for a node that plays (a book, or an episode), or null for a folder. */
fun playableId(n: Node?): String? = when (n) {
    is Node.Book -> n.id
    is Node.Episode -> n.id
    else -> null
}

/** Media3 asks for children in pages: page number from 0 and a size. */
data class Page(val page: Int, val size: Int)

/** The indexes of a fetched list of [total] items that page [p] shows; empty past the end or for a bad page. */
fun slice(total: Int, p: Page): IntRange {
    if (total <= 0 || p.page < 0 || p.size <= 0) return IntRange.EMPTY
    val from = p.page.toLong() * p.size
    if (from >= total) return IntRange.EMPTY
    return from.toInt()..minOf(total.toLong(), from + p.size).toInt() - 1
}

/** A book as the car lists it. [hasCover] is true when the server (or a download) has a picture. */
data class BookCard(val id: String, val title: String, val author: String, val durationMs: Long, val hasCover: Boolean)

data class ShelfCard(val id: String, val name: String, val books: Int)

/** What a controller may do (A8, D1 and D7). */
data class Allowed(val browse: Boolean, val ownCommands: Boolean, val speed: Boolean, val readsPlayer: Boolean)

/**
 * Only trusted controllers browse (the system, Android Auto and its Assistant, a car's own app: [trusted],
 * [auto]) and this app itself ([own]). Any other installed app gets playback commands and nothing from the library.
 * Custom commands are for this app only, except the speed button the car shows. The car is [auto] alone: the system's
 * own controllers are trusted and browse, but a phone's notification and lock screen get no speed button, and no
 * controller but this app may set a rate (a stray tap must never change how fast a book plays).
 */
fun allowedCommands(trusted: Boolean, auto: Boolean, own: Boolean): Allowed {
    val browse = trusted || auto || own
    // What is playing (title, timeline, session extras) is the person's own business: only those who may browse can read it.
    return Allowed(browse = browse, ownCommands = own, speed = auto, readsPlayer = browse)
}

/** The rate commands a controller may use: only this app's own page sets a speed directly; the car has its button. */
fun mayRate(own: Boolean): Boolean = own

private val SpeedSteps = listOf(1.0, 1.25, 1.5, 1.75, 2.0, 0.75)

/** The car's speed button (D7): 1.0, 1.25, 1.5, 1.75, 2.0, 0.75, then round again. Any other speed goes to 1.0. */
fun speedCycle(current: Double): Double {
    val at = SpeedSteps.indexOfFirst { kotlin.math.abs(it - current) < 0.01 }
    return if (at < 0) 1.0 else SpeedSteps[(at + 1) % SpeedSteps.size]
}

/** "1.25x" the way the button says it; a whole speed keeps one decimal ("1.0x"). */
fun speedLabel(speed: Double): String = speed.toString() + "x" // Double.toString keeps one decimal on a whole speed ("1.0")
