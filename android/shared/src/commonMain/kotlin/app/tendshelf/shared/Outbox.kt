package app.tendshelf.shared

/** One position to send. Times are milliseconds; [clientUpdatedAt] rises strictly for one book. */
data class Save(
    val seq: Long,
    val bookId: String,
    val positionMs: Long,
    val clientUpdatedAt: Long,
    val finished: Boolean,
    val force: Boolean,
    val speed: Double,
    val listenedMs: Long,
    /** Waiting for the person to answer "Continue there?"; a held row is never sent. */
    val held: Boolean,
)

/** Where the unsent saves live. The Android one is SQLite; an iOS one can replace it. Order is by [Save.seq]. */
interface OutboxStore {
    /** The newest row of the book, sent or not (rows are removed once sent). */
    fun last(bookId: String): Save?

    /** Stores a new row and returns it with its [Save.seq]. */
    fun add(s: Save): Save
    fun update(s: Save)

    /** The oldest [n] rows that are not held. */
    fun firstBatch(n: Int): List<Save>

    /** Every held row, any book. */
    fun held(): List<Save>
    fun delete(seqs: List<Long>)

    /** Keeps at most [max] rows: beyond that the oldest plain row goes, and a finished or forced row stays. */
    fun trim(max: Int)
    fun clear()
}

/** What came back for one row of a batch. [errorCode] is null on success. */
data class BatchItemResult(
    val bookId: String,
    val applied: Boolean,
    val errorCode: String?,
    val progress: RemoteProgress?,
)

/** What a batch answer settles: rows to delete, and places the server holds that this device lost to. */
data class Applied(val deleteSeqs: List<Long>, val lost: List<Lost>)

data class Lost(val save: Save, val remote: RemoteProgress)

/** Error codes after which a row is useless and is dropped; any other error keeps the row for a retry. */
private val DROP_CODES = setOf("not_found", "invalid")

/**
 * Matches an answer to the rows sent, in order. A short answer settles only the rows it covers; an answer
 * whose book id does not match its row settles nothing from there on.
 */
fun applyResults(sent: List<Save>, results: List<BatchItemResult>): Applied {
    val delete = mutableListOf<Long>()
    val lost = mutableListOf<Lost>()
    for (i in sent.indices) {
        val r = results.getOrNull(i) ?: break
        val s = sent[i]
        if (r.bookId != s.bookId) break
        when {
            r.errorCode == null -> {
                delete += s.seq
                if (!r.applied && r.progress != null) lost += Lost(s, r.progress)
            }
            r.errorCode in DROP_CODES -> delete += s.seq
        }
    }
    return Applied(delete, lost)
}

const val OUTBOX_MAX_ROWS = 2_000
const val BATCH_SIZE = 100

/** Most listening one unsent row may hold: the server counts at most 10 minutes for one save. */
const val COALESCE_MAX_MS = 300_000L

/**
 * The durable queue of positions (A5). A save is written to the [store] before any network attempt. Safe to
 * call from several threads.
 */
class Outbox(private val store: OutboxStore, private val now: () -> Long) {
    /** Rows up to this seq are on the wire and must not change. */
    private var sendingUpTo = 0L

    /** A plain save while playing or after a pause: merged into the last unsent row when that is safe. */
    @Synchronized
    fun tick(bookId: String, positionMs: Long, speed: Double, listenedMs: Long, hold: Boolean = false): Save =
        write(bookId, positionMs, speed, listenedMs, finished = false, force = false, hold = hold)

    @Synchronized
    fun pause(bookId: String, positionMs: Long, speed: Double, listenedMs: Long, hold: Boolean = false): Save =
        tick(bookId, positionMs, speed, listenedMs, hold)

    /** The book is over: its own row, never merged. */
    @Synchronized
    fun finish(bookId: String, positionMs: Long, speed: Double, listenedMs: Long): Save =
        write(bookId, positionMs, speed, listenedMs, finished = true, force = false, hold = false)

    /** A save that must win on the server ("Stay here"). */
    @Synchronized
    fun force(bookId: String, positionMs: Long, speed: Double, listenedMs: Long): Save =
        write(bookId, positionMs, speed, listenedMs, finished = false, force = true, hold = false)

    /** The place to start from: the newest row of the book, if one is waiting. */
    @Synchronized
    fun newest(bookId: String): Save? = store.last(bookId)

    /** The question was answered. "Stay here" ([force]) makes the held row win; otherwise it goes out plain. */
    @Synchronized
    fun release(bookId: String, force: Boolean): Save? {
        val last = store.last(bookId) ?: return null
        if (!last.held) return last
        val next = last.copy(held = false, force = force, clientUpdatedAt = maxOf(last.clientUpdatedAt + 1, now()))
        store.update(next)
        return next
    }

    /** "Continue there": the held place is dropped. */
    @Synchronized
    fun dropHeld(bookId: String) {
        val last = store.last(bookId) ?: return
        if (last.held) store.delete(listOf(last.seq))
    }

    /** After a restart no question is open: held rows go out plain, newest wins. */
    @Synchronized
    fun releaseAllHeld() {
        for (h in store.held()) store.update(h.copy(held = false))
    }

    /** The next rows to send, which are then frozen until [settled] or [failed]. */
    @Synchronized
    fun nextBatch(n: Int = BATCH_SIZE): List<Save> {
        val rows = store.firstBatch(n)
        if (rows.isNotEmpty()) sendingUpTo = rows.last().seq
        return rows
    }

    /** Applies the server's answer to the rows [nextBatch] gave. */
    @Synchronized
    fun settled(sent: List<Save>, results: List<BatchItemResult>): Applied {
        val applied = applyResults(sent, results)
        if (applied.deleteSeqs.isNotEmpty()) store.delete(applied.deleteSeqs)
        sendingUpTo = 0
        return applied
    }

    /** The send did not get an answer; the rows stay as they were. */
    @Synchronized
    fun failed() {
        sendingUpTo = 0
    }

    @Synchronized
    fun clear() {
        store.clear()
        sendingUpTo = 0
    }

    private fun write(
        bookId: String, positionMs: Long, speed: Double, listenedMs: Long,
        finished: Boolean, force: Boolean, hold: Boolean,
    ): Save {
        val last = store.last(bookId)
        val at = maxOf((last?.clientUpdatedAt ?: 0) + 1, now())
        val position = positionMs.coerceAtLeast(0)
        val listened = listenedMs.coerceAtLeast(0)
        if (last != null && canMerge(last, speed, listened, finished, force, hold)) {
            val merged = last.copy(
                positionMs = position, clientUpdatedAt = at, listenedMs = last.listenedMs + listened,
            )
            store.update(merged)
            return merged
        }
        val row = store.add(
            Save(0, bookId, position, at, finished, force, speed, listened, hold),
        )
        store.trim(OUTBOX_MAX_ROWS)
        return row
    }

    private fun canMerge(last: Save, speed: Double, listened: Long, finished: Boolean, force: Boolean, hold: Boolean): Boolean =
        last.seq > sendingUpTo &&
            !last.finished && !last.force && !finished && !force &&
            last.held == hold &&
            last.speed == speed &&
            last.listenedMs + listened <= COALESCE_MAX_MS
}
