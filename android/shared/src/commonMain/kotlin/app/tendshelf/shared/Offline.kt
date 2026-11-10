package app.tendshelf.shared

/** Where a downloaded book stands. The lowercase name is what the page is told. */
enum class OfflineState { QUEUED, DOWNLOADING, PAUSED, READY, FAILED, STALE }

/** What an [OfflineBook] row holds: an audiobook or ebook (the default), or a podcast episode (A2, decision 0038). */
const val KIND_BOOK = "book"
const val KIND_EPISODE = "episode"

/**
 * One downloaded book (or episode) as the list shows it. Times are Unix milliseconds. For an episode [bookId] is the
 * `pe_` id, [author] the show's name, [showId] the show and [publishedAt] the feed's date; [played] is set once the
 * episode was played to the end (here or on the server), which is what "remove after played" waits for.
 */
data class OfflineBook(
    val bookId: String,
    val userId: String,
    val title: String,
    val author: String,
    val durationMs: Long,
    val sizeBytes: Long,
    val state: OfflineState,
    val placeMs: Long,
    val placeAt: Long,
    val kind: String = KIND_BOOK,
    val showId: String = "",
    val publishedAt: Long = 0,
    val played: Boolean = false,
)

/** Where the downloaded books live (Android: `outbox.db`, table `offline_books`). Covers are files, never rows. */
interface OfflineStore {
    fun all(): List<OfflineBook>
    fun get(id: String): OfflineBook?

    /** Inserts or replaces the row; [manifestJson] is the server's answer, kept so the book starts without the network. */
    fun put(b: OfflineBook, manifestJson: String)
    fun manifest(id: String): String?
    fun setState(id: String, s: OfflineState)

    /** The place a listener reached offline; the outbox deletes its rows once sent, this stays. */
    fun notePlace(id: String, ms: Long, at: Long)

    /** An episode was played to the end (or the server says so): the row stays until the auto rules remove it. */
    fun setPlayed(id: String, played: Boolean)

    /** The honest size of a finished download, learned from the bytes that arrived (a feed's announced size is often wrong). */
    fun setSize(id: String, bytes: Long)
    fun remove(id: String)
    fun clear()
}

/** The key a file is stored under in the cache: the file id, and `:c` for the browser-friendly rendition. */
fun cacheKey(fileId: String, compat: Boolean): String = if (compat) "$fileId:c" else fileId

data class FilePlan(val fileId: String, val url: String, val cacheKey: String, val size: Long)

sealed interface Plan {
    data class Ok(val files: List<FilePlan>, val total: Long) : Plan
    data object NotAllowed : Plan
    data object NoRoom : Plan
    data object NothingToPlay : Plan
}

/** Room that must stay free after the book (200 MiB). */
const val SPARE_BYTES = 200L * 1024 * 1024

/** What a download may use of [freeBytes]: all of it but [SPARE_BYTES]. One rule for one book and for a whole batch. */
fun roomFor(freeBytes: Long): Long = (freeBytes - SPARE_BYTES).coerceAtLeast(0)

/**
 * What a download would fetch. The right comes first (a refusal says why in plain words), then the files the person
 * can play (a missing file is silence and is not fetched), then the room: the whole book plus [SPARE_BYTES].
 */
fun downloadPlan(m: BookManifest, paths: ApiPaths, freeBytes: Long): Plan {
    if (!m.mayDownload) return Plan.NotAllowed
    val files = m.files.filter { it.playable }.sortedBy { it.idx }
    if (files.isEmpty()) return Plan.NothingToPlay
    val plans = files.map { FilePlan(it.id, paths.downloadUrl(it.id, it.needsCompat), cacheKey(it.id, it.needsCompat), it.size) }
    val total = plans.sumOf { it.size }
    if (freeBytes < total + SPARE_BYTES) return Plan.NoRoom
    return Plan.Ok(plans, total)
}

/**
 * Whether a file is whole. The cache holds [cachedBytes] of the [contentLength] the server announced. A stored file
 * must also match the size the manifest listed ([planned]); a browser-friendly rendition is made on the fly, so its
 * length is the server's own announcement and cannot be compared with the original's.
 */
fun fileComplete(planned: Long, compat: Boolean, contentLength: Long, cachedBytes: Long): Boolean =
    contentLength > 0 && cachedBytes == contentLength && (compat || planned <= 0 || planned == contentLength)

/** One file of a running download: the size the manifest listed, what the server announced, what has arrived. */
data class FileProgress(val planned: Long, val contentLength: Long, val bytes: Long, val done: Boolean)

/** [pct] is 0 to 99 while a download runs; [known] is false when no file's size is known, so [pct] means nothing yet. */
data class DownloadPercent(val pct: Int, val known: Boolean)

/**
 * The whole book's percent: bytes that arrived over the book's total, each file weighted by the size the manifest
 * listed. A file with no listed size takes the average weight of the others (or its announced length), so it cannot
 * make the number jump. A file counts as whole only when it is done, so one still arriving stops at 99 % of itself
 * (a browser-friendly rendition has no length up front and may run past the original's size). The number never goes
 * below [previous] and never reaches 100: the Ready state says "done", not this.
 */
fun downloadPercent(files: List<FileProgress>, previous: Int): DownloadPercent {
    val floor = previous.coerceIn(0, 99)
    val sizes = files.map { if (it.planned > 0) it.planned else maxOf(it.contentLength, 0L) }
    val knownSizes = sizes.filter { it > 0 }
    if (files.isEmpty() || knownSizes.isEmpty()) return DownloadPercent(floor, false)
    val average = knownSizes.sum() / knownSizes.size
    var total = 0.0
    var got = 0.0
    for ((i, f) in files.withIndex()) {
        val weight = (if (sizes[i] > 0) sizes[i] else average).toDouble()
        val denominator = if (f.contentLength > 0) f.contentLength else f.planned
        val fraction = when {
            f.done -> 1.0
            denominator > 0 -> (f.bytes.toDouble() / denominator).coerceIn(0.0, 0.99)
            else -> 0.0
        }
        total += weight
        got += weight * fraction
    }
    val pct = (got * 100 / total).toInt().coerceIn(0, 99)
    return DownloadPercent(maxOf(pct, floor), true)
}

enum class Verdict { SAME, UPDATE, STALE, REMOVE, NOTHING }

/**
 * The daily check of a downloaded book (decision 0019, point 10). Only a plain 200 with a readable manifest or a 404
 * decides anything; a redirect, a refusal, a server error or no answer changes nothing. The same files with other
 * words (chapters, title) is an update; other files mean the bytes on the phone are old.
 */
fun checkVerdict(status: Int, old: BookManifest?, new: BookManifest?): Verdict = when {
    status == 404 -> Verdict.REMOVE
    status != 200 || new == null || old == null -> Verdict.NOTHING
    fileKeys(old) != fileKeys(new) -> Verdict.STALE
    old.copy(progress = null) == new.copy(progress = null) -> Verdict.SAME
    else -> Verdict.UPDATE
}

private fun fileKeys(m: BookManifest) =
    m.files.filter { it.playable }.sortedBy { it.idx }.map { Triple(it.id, it.size, it.needsCompat) }

// The next book, downloaded on its own (decision 0035).

/** The last two hours of audio (book time, ignoring speed) start the next book's download. */
const val AUTO_NEAR_END_MS = 2L * 60 * 60 * 1000

/** Free space that must remain after an automatic book; stricter than [SPARE_BYTES]. */
const val AUTO_SPARE_BYTES = 2L * 1024 * 1024 * 1024

/** The most that automatic downloads keep at once. */
const val AUTO_TOTAL_BYTES = 5L * 1024 * 1024 * 1024

/** How often the player looks at the position, at most. */
const val AUTO_TICK_MS = 60_000L

/** How long to wait before asking the server again about the same book. */
const val AUTO_ASK_MS = 15 * 60_000L

/** The Media3 stop reason of an automatic download that is not allowed to run (Wi-Fi gone, or not yet checked). */
const val AUTO_HOLD = 1

sealed interface AutoPlan {
    data class Go(val plan: Plan.Ok) : AutoPlan

    /** Told on the Downloads page. */
    data object NoRoom : AutoPlan

    /** Not allowed, nothing to play, or larger than the whole quota: silent. */
    data object Skip : AutoPlan
}

enum class AutoStep { NOTHING, WANT, ASK }

/** True in the last [AUTO_NEAR_END_MS] of a book; an unset (negative) duration is never near. */
fun nearEnd(positionMs: Long, durationMs: Long): Boolean =
    durationMs > 0 && positionMs >= 0 && durationMs - positionMs <= AUTO_NEAR_END_MS

/** What to do with the next book now: nothing, remember to ask on the next Wi-Fi moment, or ask the server. */
fun autoStep(enabled: Boolean, near: Boolean, unmetered: Boolean, askedAgoMs: Long?): AutoStep = when {
    !enabled || !near -> AutoStep.NOTHING
    !unmetered -> AutoStep.WANT
    askedAgoMs == null || askedAgoMs >= AUTO_ASK_MS -> AutoStep.ASK
    else -> AutoStep.NOTHING
}

/** The bytes of the rows still marked automatic, finished or not. */
fun autoBytes(rows: List<OfflineBook>, marks: Set<String>): Long =
    rows.filter { it.bookId in marks }.sumOf { it.sizeBytes }

/** Automatic rows that are not finished: the only thing an automatic path ever removes. */
fun autoUnfinished(rows: List<OfflineBook>, marks: Set<String>): List<OfflineBook> = rows.filter {
    it.bookId in marks && (it.state == OfflineState.QUEUED || it.state == OfflineState.DOWNLOADING || it.state == OfflineState.PAUSED)
}

/** [downloadPlan] first, unchanged, then the quota and the stricter spare room of an automatic book. */
fun autoDownloadPlan(m: BookManifest, paths: ApiPaths, freeBytes: Long, keptAutoBytes: Long): AutoPlan =
    when (val p = downloadPlan(m, paths, freeBytes)) {
        Plan.NotAllowed, Plan.NothingToPlay -> AutoPlan.Skip
        Plan.NoRoom -> if (autoTotalOf(m) > AUTO_TOTAL_BYTES) AutoPlan.Skip else AutoPlan.NoRoom
        is Plan.Ok -> when {
            p.total > AUTO_TOTAL_BYTES -> AutoPlan.Skip
            keptAutoBytes + p.total > AUTO_TOTAL_BYTES -> AutoPlan.NoRoom
            freeBytes - p.total < AUTO_SPARE_BYTES -> AutoPlan.NoRoom
            else -> AutoPlan.Go(p)
        }
    }

private fun autoTotalOf(m: BookManifest) = m.files.filter { it.playable }.sumOf { it.size }

/** The remembered pair (decision 0035): the book that was ended and the one the server named after it. */
fun encodeAutoNext(ended: String, next: String): String = "$ended\n$next"

/** The pair stored by [encodeAutoNext], or null for anything that is not exactly two non-empty ids. */
fun parseAutoNext(stored: String?): Pair<String, String>? {
    val parts = stored?.split('\n') ?: return null
    if (parts.size != 2 || parts.any { it.isEmpty() }) return null
    return parts[0] to parts[1]
}

/**
 * The book to start when the server could not be asked: the remembered next book, only when it was remembered for
 * [ended] and [isReady] says it is whole on the phone. Anything else is null, never a guess.
 */
fun offlineNextFor(stored: String?, ended: String, isReady: (String) -> Boolean): String? {
    val (was, next) = parseAutoNext(stored) ?: return null
    return if (was == ended && next != ended && isReady(next)) next else null
}

/** A queue delete is done on any 2xx and on 404 (already gone, or it came from the series); anything else is tried again. */
fun queueDeleteDone(status: Int): Boolean = status in 200..299 || status == 404
