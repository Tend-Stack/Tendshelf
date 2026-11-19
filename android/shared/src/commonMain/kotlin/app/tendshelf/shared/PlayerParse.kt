package app.tendshelf.shared

// What the player reads from the server, answered as null on a bad shape instead of throwing. On Android the caller
// catches the exception; on iOS an uncaught Kotlin exception ends the app, and none of the parsers is marked `@Throws`,
// so the iPhone's player calls these and treats null as "the answer was not what it should be". Same parsers, same results.

private inline fun <T : Any> safely(block: () -> T): T? = try {
    block()
} catch (_: Exception) {
    null
}

fun parseSettings(body: String): PlayerSettings? = safely { ApiJson.settings(body) }

fun parseManifest(body: String, paths: ApiPaths): BookManifest? = safely { ApiJson.manifest(body, paths) }

fun parseEpisode(body: String): EpisodeDoc? = safely { PodcastJson.episode(body) }

fun parseEpisodeChapters(body: String, durationMs: Long): List<Chapter>? = safely { PodcastJson.chapters(body, durationMs) }

fun parseStations(body: String): List<StationCard>? = safely { RadioJson.stations(body) }

fun parseRadioNow(body: String): String? = safely { RadioJson.now(body) }

/** `GET /api/v1/progress/{id}`: [progress] is null when the person never played it. The reply itself is null on a bad shape. */
class ProgressReply(val progress: RemoteProgress?)

fun parseProgress(body: String): ProgressReply? = safely { ProgressReply(ApiJson.progress(body)) }

/** The id of the item a queue-next answer names, or null on a bad shape. */
fun parseNextItemId(body: String): String? = safely { ApiJson.nextItemId(body) }

fun parseBookBatch(body: String): List<BatchItemResult>? = safely { ApiJson.batchResults(body) }

fun parseEpisodeBatch(body: String): List<BatchItemResult>? = safely { PodcastJson.batchResults(body) }
