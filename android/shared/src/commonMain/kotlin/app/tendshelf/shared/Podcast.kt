package app.tendshelf.shared

/**
 * Podcast episodes for the native player (A1, decision 0038). An episode is a one-file item whose id starts with `pe_`;
 * everything here is plain logic with no platform calls, so a later iOS app reuses it as it is.
 */

/** True for an episode id (`pe_...`); every other id is a book. */
fun isEpisodeId(id: String): Boolean = id.startsWith("pe_") && id.length > 3

/** One show's player settings from `settings{}`; [speed] is null when the person never chose one for the show. */
data class ShowSettings(val speed: Double?, val skipIntroS: Int, val skipOutroS: Int)

/** The playback document, `GET /api/v1/podcasts/episodes/{id}`. Addresses are paths on the server, never other hosts. */
data class EpisodeDoc(
    val id: String,
    val showId: String,
    val title: String,
    val show: String,
    val artUrl: String,
    val durationMs: Long,
    val audioUrl: String,
    val mime: String,
    val bytes: Long,
    val chaptersUrl: String,
    val settings: ShowSettings,
    /** The stored place, or null when the person never played it (`updatedAt` 0). */
    val progress: RemoteProgress?,
)

object PodcastJson {
    /** Reads the playback document; throws [JsonShapeException] when it is not one. */
    fun episode(body: String): EpisodeDoc {
        val j = parseJsonObject(body)
        val set = j.optObject("settings")
        val state = j.optObject("state")
        val updated = state?.optLong("updatedAt", 0) ?: 0
        val speed = set?.optDouble("speed")?.takeIf { !it.isNaN() && it in 0.5..3.5 }
        return EpisodeDoc(
            id = j.getString("id"),
            showId = j.optString("showId"),
            title = j.getString("title"),
            show = j.optString("show"),
            artUrl = j.optString("artUrl"),
            durationMs = j.optLong("durationMs", 0).coerceAtLeast(0),
            audioUrl = j.getString("audioUrl"),
            mime = j.optString("mime"),
            bytes = j.optLong("bytes", 0),
            chaptersUrl = j.optString("chaptersUrl"),
            settings = ShowSettings(
                speed,
                (set?.optInt("skipIntroS", 0) ?: 0).coerceIn(0, 600),
                (set?.optInt("skipOutroS", 0) ?: 0).coerceIn(0, 600),
            ),
            progress = if (updated > 0 && state != null) {
                // The episode state names no device, so nothing can be said about who wrote it.
                RemoteProgress(state.optLong("positionMs", 0), state.optBoolean("played"), updated, "", "")
            } else {
                null
            },
        )
    }

    /**
     * Podcasting 2.0 chapters (`{"chapters":[{"startTime":12.5,"title":"..."}]}`), in time order, each ending where the
     * next begins and the last at [durationMs]. A chapter marked `"toc": false` is not a chapter. Bad entries are skipped.
     */
    fun chapters(body: String, durationMs: Long): List<Chapter> {
        val arr = try {
            parseJsonObject(body).optArray("chapters")
        } catch (_: Exception) {
            null
        } ?: return emptyList()
        val starts = (0 until arr.size).mapNotNull { i ->
            val o = arr.optObject(i) ?: return@mapNotNull null
            if (!o.isNull("toc") && !o.optBoolean("toc", true)) return@mapNotNull null
            val t = o.optDouble("startTime")
            if (t.isNaN() || t < 0 || t > 86_400.0 * 7) return@mapNotNull null
            (t * 1000).toLong() to o.optString("title").trim().take(200)
        }.sortedBy { it.first }.distinctBy { it.first }
        return starts.mapIndexed { i, (start, title) ->
            val end = starts.getOrNull(i + 1)?.first ?: maxOf(durationMs, start)
            Chapter(i, start, end, title.ifEmpty { "Chapter ${i + 1}" })
        }
    }

    /** `{"results":[{episodeId,applied,error:{code}}]}` from the progress batch; a result carries no remote place. */
    fun batchResults(body: String): List<BatchItemResult> {
        val arr = parseJsonObject(body).getArray("results")
        return (0 until arr.size).map {
            val o = arr.getObject(it)
            BatchItemResult(o.getString("episodeId"), o.optBoolean("applied"), o.optObject("error")?.optString("code", "error"), null)
        }
    }
}

/** JSON body for `POST /api/v1/podcasts/progress/batch`: the same saves as a book's, without speed and listening time. */
fun podcastBatchBody(saves: List<Save>, deviceId: String): String = saves.joinToString(",", "{\"updates\":[", "]}") {
    "{\"episodeId\":${jsonString(it.bookId)},\"positionMs\":${it.positionMs},\"clientUpdatedAt\":${it.clientUpdatedAt}," +
        "\"deviceId\":${jsonString(deviceId)},\"finished\":${it.finished}}"
}

/**
 * Splits one outbox batch into books and episodes (each keeps its order), because the two kinds go to different routes.
 * [assembleResults] puts the answers back in the batch's order.
 */
fun splitSaves(saves: List<Save>): Pair<List<Save>, List<Save>> = saves.partition { !isEpisodeId(it.bookId) }

/**
 * The answers in the order of [saves]. A group that got no answer ([books] or [episodes] null) ends the list at its
 * first row, so [applyResults] settles only the prefix that is certain and the rest is sent again.
 */
fun assembleResults(saves: List<Save>, books: List<BatchItemResult>?, episodes: List<BatchItemResult>?): List<BatchItemResult> {
    var b = 0
    var e = 0
    val out = mutableListOf<BatchItemResult>()
    for (s in saves) {
        val episode = isEpisodeId(s.bookId)
        val group = if (episode) episodes else books
        val next = if (episode) e++ else b++
        out += group?.getOrNull(next) ?: break
    }
    return out
}

/** What the engine does with a show's skip seconds for one episode. */
data class SkipRules(val introMs: Long, val outroFromMs: Long?) {
    /** Where an episode starts: the stored place, or the end of the intro when the place lies inside it. */
    fun startAt(placeMs: Long): Long = if (placeMs < introMs) introMs else placeMs

    /** True once [positionMs] is inside the outro: the episode is treated as over. */
    fun inOutro(positionMs: Long): Boolean = outroFromMs != null && positionMs >= outroFromMs
}

/**
 * Skip rules for an episode of [durationMs] (0 when unknown). An intro as long as the episode is ignored; an outro needs a
 * known length and must leave more than the intro behind it, or it would end the episode at once.
 */
fun skipRules(settings: ShowSettings, durationMs: Long): SkipRules {
    val intro = settings.skipIntroS * 1000L
    val usableIntro = if (durationMs > 0 && intro >= durationMs) 0L else intro
    val outroFrom = if (durationMs > 0 && settings.skipOutroS > 0) durationMs - settings.skipOutroS * 1000L else null
    return SkipRules(usableIntro, outroFrom?.takeIf { it > usableIntro })
}

/** The speed an episode plays at: the show's own when it has one, else the person's default. */
fun episodeSpeed(settings: ShowSettings, personal: Double): Double = settings.speed ?: personal

// ---- What the car browses (A3) ----

/** A followed show as the car lists it. [hasArt] is true when the server has a picture for it. */
data class ShowCard(val id: String, val title: String, val author: String, val hasArt: Boolean, val newCount: Int)

/** An episode as a list row: the change feed's fields without the playback address. */
data class EpisodeCard(
    val id: String,
    val showId: String,
    val title: String,
    val show: String,
    val durationMs: Long,
    val publishedAt: Long,
    val positionMs: Long,
    val played: Boolean,
    val favourite: Boolean,
)

/** What a row's action buttons can do (decision 0038). Download and Play next are not here: see [episodeActions]. */
enum class EpisodeAction(val word: String) { PLAYED("played"), UNPLAYED("unplayed"), STAR("star"), UNSTAR("unstar") }

/**
 * The actions an episode row offers: Mark as played or unplayed, Star or Unstar, by its state. Download waits for A2
 * and Play next for the mixed queue (the queue API knows books alone), so neither is offered until it works.
 */
fun episodeActions(e: EpisodeCard): List<EpisodeAction> =
    listOf(if (e.played) EpisodeAction.UNPLAYED else EpisodeAction.PLAYED, if (e.favourite) EpisodeAction.UNSTAR else EpisodeAction.STAR)

fun episodeActionOf(word: String): EpisodeAction? = EpisodeAction.entries.firstOrNull { it.word == word }

/** The request body of an action: `{"played":true}` or `{"on":true}`. */
fun episodeActionBody(a: EpisodeAction): String = when (a) {
    EpisodeAction.PLAYED -> "{\"played\":true}"
    EpisodeAction.UNPLAYED -> "{\"played\":false}"
    EpisodeAction.STAR -> "{\"on\":true}"
    EpisodeAction.UNSTAR -> "{\"on\":false}"
}

/** 0 not played, 1 partly, 2 fully: Android Auto's completion status for a row. */
fun completionOf(e: EpisodeCard): Int = when {
    e.played -> 2
    e.positionMs > 0 -> 1
    else -> 0
}

/** Percent listened (0 to 100) for a partly played row; 0 when the length is unknown. */
fun completionPercent(e: EpisodeCard): Int =
    if (e.played) 100 else if (e.durationMs <= 0 || e.positionMs <= 0) 0 else (e.positionMs * 100 / e.durationMs).toInt().coerceIn(0, 99)

/** The row's second line: the show, then the length ("Hard Fork, 45 min"). The show alone when the length is unknown. */
fun episodeSubline(e: EpisodeCard): String {
    val minutes = (e.durationMs + 59_999) / 60_000
    val length = when {
        e.durationMs <= 0 -> ""
        minutes < 60 -> "$minutes min"
        else -> "${minutes / 60} h ${minutes % 60} min"
    }
    return listOf(e.show, length).filter { it.isNotEmpty() }.joinToString(", ")
}

/** The row's title; a nameless episode is never a blank row. */
fun episodeRowTitle(e: EpisodeCard): String = e.title.trim().ifEmpty { "Episode" }

fun showRowTitle(s: ShowCard): String = s.title.trim().ifEmpty { "Podcast" }

/** Episodes the car lists: the ones the publisher withdrew (`gone`) are left out. Order is the server's. */
fun PodcastJson.episodeCards(body: String): List<EpisodeCard> {
    val arr = parseJsonObject(body).getArray("items")
    return (0 until arr.size).mapNotNull { arr.optObject(it) }.mapNotNull { o ->
        val id = o.optString("id")
        if (!isEpisodeId(id) || !validId(id) || o.optBoolean("gone")) return@mapNotNull null
        val st = o.optObject("state")
        EpisodeCard(
            id = id,
            showId = o.optString("showId"),
            title = o.optString("title"),
            show = o.optString("show"),
            durationMs = o.optLong("durationMs", 0).coerceAtLeast(0),
            publishedAt = o.optLong("publishedAt", 0),
            positionMs = st?.optLong("positionMs", 0) ?: 0,
            played = st?.optBoolean("played") ?: false,
            favourite = st?.optBoolean("favourite") ?: false,
        )
    }
}

fun PodcastJson.showCards(body: String): List<ShowCard> {
    val arr = parseJsonObject(body).getArray("items")
    return (0 until arr.size).mapNotNull { arr.optObject(it) }.mapNotNull { o ->
        val id = o.optString("id")
        if (!validId(id)) return@mapNotNull null
        ShowCard(id, o.optString("title"), o.optString("author"), o.optString("artUrl").isNotEmpty(), o.optInt("newCount", 0).coerceAtLeast(0))
    }
}
