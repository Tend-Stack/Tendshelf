package app.tendshelf.shared

/**
 * Podcast episodes kept on the phone (A2, decision 0038) and the rules of "Download new episodes on this phone".
 * Plain logic with no platform calls. The platform code (`Offline.kt`, `EpisodeAuto.kt`) asks these functions what to
 * fetch and what to remove, so the rules that must never break are tested here: the Wi-Fi gate, the quota shared with
 * the next-book download (decision 0035), keep the latest N, remove after played, and never touch a person's own download.
 */

/**
 * What downloading one episode would fetch: the one file at the server's audio route. The feed's announced size
 * ([EpisodeDoc.bytes]) is often wrong, so it only decides whether there is room; the size that counts is learned from the
 * bytes that arrive (see [episodeFileComplete]).
 */
fun episodePlan(doc: EpisodeDoc, paths: ApiPaths, freeBytes: Long): Plan {
    if (!isEpisodeId(doc.id) || !validId(doc.id)) return Plan.NothingToPlay
    // A video episode is not audio: Tendshelf plays the sound of an episode only.
    if (doc.mime.startsWith("video/")) return Plan.NothingToPlay
    val announced = doc.bytes.coerceAtLeast(0)
    if (freeBytes < announced + SPARE_BYTES) return Plan.NoRoom
    return Plan.Ok(listOf(FilePlan(doc.id, paths.episodeDownloadUrl(doc.id), doc.id, announced)), announced)
}

/**
 * Whether an episode's file is whole. A known length must match the bytes in the cache exactly. A publisher that sends no
 * length (a chunked answer) leaves it unknown; then the file is whole when the downloader finished ([downloadedBytes] > 0)
 * and the cache holds at least what it counted, so a download that stopped early is never taken for done.
 */
fun episodeFileComplete(contentLength: Long, cachedBytes: Long, downloadedBytes: Long): Boolean =
    if (contentLength > 0) cachedBytes == contentLength else downloadedBytes > 0 && cachedBytes >= downloadedBytes

/** The honest size of a finished episode: the announced length when the server gave one, else what arrived. */
fun episodeSize(contentLength: Long, cachedBytes: Long): Long = if (contentLength > 0) contentLength else cachedBytes

// ---- Download new episodes on this phone ----

/** The Wi-Fi gate: a byte moves only when some show asks for it and the active network is unmetered. */
fun mayAutoDownloadEpisodes(anyShowOn: Boolean, unmetered: Boolean): Boolean = anyShowOn && unmetered

/** How often the foreground path may ask the server for new episodes, at most (the periodic job has its own 6 h). */
const val EPISODE_CHECK_MS = 60 * 60_000L

/** The most episodes one check fetches in one go, so a show with a long unplayed backlog cannot fill the phone. */
const val EPISODE_BATCH = 10

/**
 * The episodes of one show to fetch now: the newest [keep] unplayed ones the publisher still lists, minus what the phone
 * has (any row, manual or automatic) and what the person removed on purpose ([declined]). Newest first. A removed episode
 * keeps its slot, so removing one never pulls an older episode in behind it.
 */
fun autoEpisodeWanted(cards: List<EpisodeCard>, have: Set<String>, declined: Set<String>, keep: Int): List<EpisodeCard> =
    cards.filter { !it.played && isEpisodeId(it.id) }
        .sortedByDescending { it.publishedAt }
        .take(keep.coerceIn(1, 50))
        .filter { it.id !in have && it.id !in declined }

/**
 * An episode on its own: [AutoPlan.Go] when the Wi-Fi rule's quota and spare room allow it. The quota is the one of the
 * next-book download: [keptAutoBytes] must be [autoBytes] over the marks of both kinds.
 */
fun autoEpisodePlan(doc: EpisodeDoc, paths: ApiPaths, freeBytes: Long, keptAutoBytes: Long): AutoPlan =
    when (val p = episodePlan(doc, paths, freeBytes)) {
        Plan.NotAllowed, Plan.NothingToPlay -> AutoPlan.Skip
        Plan.NoRoom -> if (doc.bytes > AUTO_TOTAL_BYTES) AutoPlan.Skip else AutoPlan.NoRoom
        is Plan.Ok -> when {
            p.total > AUTO_TOTAL_BYTES -> AutoPlan.Skip
            keptAutoBytes + p.total > AUTO_TOTAL_BYTES -> AutoPlan.NoRoom
            freeBytes - p.total < AUTO_SPARE_BYTES -> AutoPlan.NoRoom
            else -> AutoPlan.Go(p)
        }
    }

/**
 * Which automatic episodes to remove. Only rows in [marks] (episodes the phone fetched on its own) are ever named; a
 * person's own download and every book are never in the answer. In order:
 * 1. an episode that was played (here or on the server) goes;
 * 2. of a show's unplayed ones, those beyond its newest [keep] go (a show without an entry has automatic download off, and
 *    its rows are left alone here);
 * 3. while the automatic bytes of both kinds ([otherMarks] are the next-book marks) exceed [AUTO_TOTAL_BYTES], the oldest
 *    remaining automatic episode goes.
 */
fun autoEpisodeEvictions(rows: List<OfflineBook>, marks: Set<String>, otherMarks: Set<String>, keep: Map<String, Int>): List<String> {
    val mine = rows.filter { it.kind == KIND_EPISODE && it.bookId in marks }
    val out = LinkedHashSet<String>()
    mine.filter { it.played }.forEach { out += it.bookId }
    for ((show, list) in mine.filter { !it.played }.groupBy { it.showId }) {
        val n = keep[show] ?: continue
        list.sortedByDescending { it.publishedAt }.drop(n.coerceIn(1, 50)).forEach { out += it.bookId }
    }
    var total = rows.filter { it.bookId in marks || it.bookId in otherMarks }.filter { it.bookId !in out }.sumOf { it.sizeBytes }
    for (r in mine.filter { it.bookId !in out }.sortedBy { it.publishedAt }) {
        if (total <= AUTO_TOTAL_BYTES) break
        out += r.bookId
        total -= r.sizeBytes
    }
    return out.toList()
}

/**
 * New episodes to announce: found by a check, not seen by an earlier one ([notified]) and not played. The first check
 * after a show was switched on only primes [notified] (the caller does not announce a backlog), so this is the rule for
 * every later one.
 */
fun episodesToAnnounce(found: List<EpisodeCard>, notified: Set<String>): List<EpisodeCard> =
    found.filter { !it.played && it.id !in notified }.sortedByDescending { it.publishedAt }

/** The rows of the signed-in person that are episodes and whole, for the car's Downloaded node and the Downloads page. */
fun readyEpisodes(rows: List<OfflineBook>): List<OfflineBook> =
    rows.filter { it.kind == KIND_EPISODE && (it.state == OfflineState.READY || it.state == OfflineState.STALE) }
        .sortedByDescending { it.publishedAt }

/** The place stored with an episode row as the car lists it. */
fun OfflineBook.asEpisodeCard(): EpisodeCard =
    EpisodeCard(bookId, showId, title, author, durationMs, publishedAt, placeMs, played, false)
