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
