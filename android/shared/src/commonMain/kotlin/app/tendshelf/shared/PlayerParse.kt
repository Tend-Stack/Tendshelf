package app.tendshelf.shared

// What the player reads from the server, answered as null on a bad shape instead of throwing. On Android the caller
// catches the exception; on iOS an uncaught Kotlin exception ends the app, and none of the parsers is marked `@Throws`,
// so the iPhone's player calls these and treats null as "the answer was not what it should be". Same parsers, same results.

private inline fun <T : Any> safely(block: () -> T): T? = try {
    block()
} catch (_: Exception) {
    null
}

/** The car's lists (CarPlay): `/continue` and `/queue`, a shelf's first page, and the shelf names. */
fun parseItemBooks(body: String): List<BookCard>? = safely { ApiJson.itemBooks(body) }

fun parseShelfBooks(body: String): List<BookCard>? = safely { ApiJson.shelfBooks(body) }

fun parseCarHome(body: String): CarHome? = safely { CarHomeJson.home(body) }

fun parseListBooks(body: String): List<BookCard>? = safely { ApiJson.listBooks(body) }

fun parseShelves(body: String): List<ShelfCard>? = safely { ApiJson.shelves(body) }

fun parseSettings(body: String): PlayerSettings? = safely { ApiJson.settings(body) }

fun parseManifest(body: String, paths: ApiPaths): BookManifest? = safely { ApiJson.manifest(body, paths) }

fun parseEpisode(body: String): EpisodeDoc? = safely { PodcastJson.episode(body) }

fun parseEpisodeChapters(body: String, durationMs: Long): List<Chapter>? = safely { PodcastJson.chapters(body, durationMs) }

/** The followed shows (`GET /api/v1/podcasts`) and one show's episode cards, or null on a bad shape (the iPhone's automatic episodes). */
fun parseShowCards(body: String): List<ShowCard>? = safely { PodcastJson.showCards(body) }

fun parseEpisodeCards(body: String): List<EpisodeCard>? = safely { PodcastJson.episodeCards(body) }

fun parseStations(body: String): List<StationCard>? = safely { RadioJson.stations(body) }

fun parseRadioNow(body: String): String? = safely { RadioJson.now(body) }

/** `GET /api/v1/progress/{id}`: [progress] is null when the person never played it. The reply itself is null on a bad shape. */
class ProgressReply(val progress: RemoteProgress?)

fun parseProgress(body: String): ProgressReply? = safely { ProgressReply(ApiJson.progress(body)) }

/** The id of the item a queue-next answer names, or null on a bad shape. */
fun parseNextItemId(body: String): String? = safely { ApiJson.nextItemId(body) }

fun parseBookBatch(body: String): List<BatchItemResult>? = safely { ApiJson.batchResults(body) }

fun parseEpisodeBatch(body: String): List<BatchItemResult>? = safely { PodcastJson.batchResults(body) }
