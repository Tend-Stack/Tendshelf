package app.tendshelf.shared

/** Where a downloaded book stands. The lowercase name is what the page is told. */
enum class OfflineState { QUEUED, DOWNLOADING, PAUSED, READY, FAILED, STALE }

/** One downloaded book as the list shows it. Times are Unix milliseconds. */
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
