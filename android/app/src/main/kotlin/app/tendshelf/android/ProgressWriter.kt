package app.tendshelf.android

import android.content.Context
import android.net.ConnectivityManager
import android.net.Network
import android.os.Handler
import android.os.SystemClock
import android.util.Log
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.ExoPlayer
import app.tendshelf.shared.isStationId
import app.tendshelf.shared.Lost
import app.tendshelf.shared.Outbox
import app.tendshelf.shared.Save
import java.io.IOException
import java.util.concurrent.ExecutorService
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean

/**
 * Saves the place of the playing book and sends it (A5). A position is written to `outbox.db` first (one `disk`
 * thread, in order) and only then does a `net` thread try the server, so a kill loses nothing that was added.
 * It never involves the page. Logs hold counts only, never tokens or positions.
 */
@UnstableApi
class ProgressWriter(
    context: Context,
    private val exo: ExoPlayer,
    private val connections: () -> ConnectionSource,
    private val main: Handler,
) {
    private val prefs = context.applicationContext.getSharedPreferences("outbox", Context.MODE_PRIVATE)
    private val store = SqliteOutbox(context) { device }
    val outbox = Outbox(store) { System.currentTimeMillis() }
    private val disk: ExecutorService = Executors.newSingleThreadExecutor()
    private val net: ExecutorService = Executors.newSingleThreadExecutor()
    private val queued = AtomicBoolean(false)
    private val retry = Runnable { flush() }
    private val cm = context.getSystemService(ConnectivityManager::class.java)

    @Volatile private var device = prefs.getString("device", "") ?: ""
    @Volatile private var backoffMs = FIRST_BACKOFF_MS

    /** The book whose question is open: its saves are held, not sent. Set on the main thread. */
    @Volatile var heldBook: String? = null

    /** A newer place on the server than this device's save (answer `applied:false`); called on the main thread. */
    var onLost: ((Lost, Connection) -> Unit)? = null

    /** The server refused the token (401). The rows stay; the person is signed out elsewhere. */
    var onSignedOut: (() -> Unit)? = null

    private var playingSince = 0L
    private var heardMs = 0L
    private var lastSaveAt = 0L
    private var lastSavedPos = -1L
    private var lastSavedBook = ""

    private val afterSeek = Runnable { record("seek") }

    private val network = object : ConnectivityManager.NetworkCallback() {
        override fun onAvailable(network: Network) {
            backoffMs = FIRST_BACKOFF_MS
            flush()
        }
    }

    init {
        exo.addListener(object : Player.Listener {
            override fun onIsPlayingChanged(isPlaying: Boolean) {
                val now = SystemClock.elapsedRealtime()
                if (isPlaying) {
                    playingSince = now
                    lastSaveAt = now
                } else {
                    drainListened()
                    record("pause")
                }
            }

            override fun onPositionDiscontinuity(
                oldPosition: Player.PositionInfo, newPosition: Player.PositionInfo, reason: Int,
            ) {
                if (reason == Player.DISCONTINUITY_REASON_SEEK && !exo.isPlaying) {
                    main.removeCallbacks(afterSeek)
                    main.postDelayed(afterSeek, SEEK_SETTLE_MS)
                }
            }
        })
        onDisk { outbox.releaseAllHeld() } // a question from a dead process is gone: newest wins
        try {
            cm?.registerDefaultNetworkCallback(network)
        } catch (e: Exception) {
            Log.w(TAG, "network callback not registered: ${e.javaClass.simpleName}")
        }
        flush() // service start
    }

    /** Listening time since the last save, as the page's `heardMs`: wall time while playing. */
    fun drainListened(): Long {
        val now = SystemClock.elapsedRealtime()
        if (playingSince != 0L) {
            heardMs += now - playingSince
            playingSince = if (exo.isPlaying) now else 0L
        }
        val out = heardMs
        heardMs = 0
        return out
    }

    /** Called about twice a second while playing: every 10 s the place goes to disk, if it moved at least a second. */
    fun onTick(bookId: String) {
        val now = SystemClock.elapsedRealtime()
        if (now - lastSaveAt < TICK_MS) return
        if (unsaved != null) {
            record("retry") // the disk refused the last place: try again with the current one
            return
        }
        if (bookId == lastSavedBook && kotlin.math.abs(exo.currentPosition - lastSavedPos) < 1_000) {
            lastSaveAt = now
            return
        }
        record("tick")
    }

    /** Chapter change, task removal, destroy: save now. */
    fun saveNow(reason: String) = record(reason)

    private var book: () -> String = { "" }

    /** Where the writer asks which book is loaded. */
    fun bookSource(b: () -> String) {
        book = b
    }

    /** A place the disk refused: the book and the listening time it carried. The position is read again at the retry. */
    private class Unsaved(val bookId: String, val listened: Long)

    @Volatile private var unsaved: Unsaved? = null

    /** Disk work that must never end the process: any failure is logged by name and dropped. */
    private fun onDisk(work: () -> Unit) = disk.execute {
        try {
            work()
        } catch (e: Exception) {
            Log.w(TAG, "outbox: disk task failed (${e.javaClass.simpleName})")
        }
    }

    private fun record(why: String) {
        val id = book()
        if (id.isEmpty() || exo.mediaItemCount == 0) return
        if (isStationId(id)) return // a live station has no place to save (R3)
        // A finished book has its own `finished` row (NextBook); a plain place saved after it would un-finish it.
        if (exo.playbackState == Player.STATE_ENDED) return
        val conn = connections().current() ?: return // no sign-in: nothing is saved under nobody's token
        val pos = exo.currentPosition
        val speed = exo.playbackParameters.speed.toDouble()
        val listened = drainListened()
        val hold = heldBook == id
        lastSaveAt = SystemClock.elapsedRealtime()
        lastSavedPos = pos
        lastSavedBook = id
        onDisk {
            try {
                adopt(conn)
                // A place that could not be written earlier is older than this one; its listening time is kept.
                val before = unsaved?.takeIf { it.bookId == id }?.listened ?: 0L
                outbox.tick(id, pos, speed, listened + before, hold)
                Offline.store.notePlace(id, pos, System.currentTimeMillis()) // a downloaded book keeps its place beside the row (D6)
                unsaved = null
                Log.d(TAG, "outbox: saved ($why)")
            } catch (e: Exception) {
                // A full disk or a locked database: playing goes on, the place stays in memory and the next tick writes it again.
                val before = unsaved?.takeIf { it.bookId == id }?.listened ?: 0L
                unsaved = Unsaved(id, listened + before)
                Log.w(TAG, "outbox: place not written (${e.javaClass.simpleName}), kept in memory")
                return@onDisk
            }
            flush()
        }
    }

    /** The book is over: write `finished` and try to send it now, on a worker thread. True when everything went out. */
    fun finishAndFlush(bookId: String, positionMs: Long, speed: Double, listened: Long): Boolean {
        val conn = connections().current() ?: return false
        return try {
            disk.submit<Boolean> {
                adopt(conn)
                outbox.finish(bookId, positionMs, speed, listened)
                Offline.store.notePlace(bookId, positionMs, System.currentTimeMillis())
                true
            }.get(5, TimeUnit.SECONDS)
            net.submit<Boolean> { flushLoop() }.get(40, TimeUnit.SECONDS)
        } catch (e: Exception) {
            Log.w(TAG, "finish not flushed: ${e.javaClass.simpleName}")
            false
        }
    }

    /** The question was answered: "Stay here" ([force]) wins on the server, otherwise the held place goes plain. */
    fun release(bookId: String, force: Boolean) {
        heldBook = null
        onDisk {
            outbox.release(bookId, force)
            flush()
        }
    }

    fun dropHeld(bookId: String) {
        heldBook = null
        onDisk { outbox.dropHeld(bookId) }
    }

    /** The newest unsent row of the book (the caller is on a worker thread). */
    fun pending(bookId: String): Save? = outbox.newest(bookId)

    /** Rows of another sign-in must never go out under this token: a different device id empties the outbox. */
    private fun adopt(conn: Connection) {
        if (device == conn.deviceId) return
        outbox.clear()
        device = conn.deviceId
        prefs.edit().putString("device", device).apply()
        Log.i(TAG, "outbox: another sign-in, rows cleared")
    }

    /** Try to send what is waiting, once; failures schedule their own retry. */
    fun flush() {
        if (!queued.compareAndSet(false, true)) return
        net.execute {
            queued.set(false)
            flushLoop()
        }
    }

    private fun flushLoop(): Boolean = try {
        sendLoop()
    } catch (e: Exception) {
        // The outbox could not be read or settled (a full disk, a locked file): the rows stay, the next try is later.
        Log.w(TAG, "outbox: flush failed (${e.javaClass.simpleName})")
        retryLater()
        false
    }

    private fun sendLoop(): Boolean {
        while (true) {
            val conn = connections().current()
            if (conn == null) {
                Log.d(TAG, "outbox: flush skipped, no sign-in")
                return false
            }
            adopt(conn)
            val batch = outbox.nextBatch()
            if (batch.isEmpty()) {
                backoffMs = FIRST_BACKOFF_MS
                return true
            }
            val results = try {
                Api(conn.origin).batch(conn.token, batch, conn.deviceId)
            } catch (e: ApiException) {
                outbox.failed()
                // Api only sends to the connected server (ServerGate), so this 401 is that server's. The rows stay.
                when (e.code) {
                    401 -> main.post { onSignedOut?.invoke() }
                    429 -> retryIn(e.retryAfterS * 1000L)
                    else -> retryLater()
                }
                Log.w(TAG, "outbox: server answered ${e.code}, ${batch.size} rows kept")
                return false
            } catch (e: Exception) {
                outbox.failed()
                retryLater()
                Log.w(TAG, "outbox: not sent (${e.javaClass.simpleName}), ${batch.size} rows kept")
                return false
            }
            val applied = outbox.settled(batch, results)
            Log.i(TAG, "outbox: sent ${batch.size}, settled ${applied.deleteSeqs.size}")
            for (lost in applied.lost) main.post { onLost?.invoke(lost, conn) }
            if (applied.deleteSeqs.size < batch.size) {
                retryLater() // some rows were refused for a reason that may pass; do not spin on them
                return false
            }
        }
    }

    private fun retryLater() {
        retryIn(backoffMs)
        backoffMs = (backoffMs * 2).coerceAtMost(MAX_BACKOFF_MS)
    }

    private fun retryIn(ms: Long) {
        main.removeCallbacks(retry)
        main.postDelayed(retry, ms)
    }

    /** Sign-out: one try to send what is left, then every row goes. */
    fun flushAndClear() {
        net.execute {
            try {
                flushLoop()
            } finally {
                try {
                    outbox.clear()
                } catch (e: Exception) {
                    Log.w(TAG, "outbox: not cleared (${e.javaClass.simpleName})")
                }
            }
        }
    }

    fun rowCount(): Int = store.count()

    fun close() {
        main.removeCallbacks(afterSeek)
        main.removeCallbacks(retry)
        try {
            cm?.unregisterNetworkCallback(network)
        } catch (_: IllegalArgumentException) {
        }
        disk.shutdown()
        disk.awaitTermination(1, TimeUnit.SECONDS)
        net.shutdownNow()
        store.close()
    }

    companion object {
        private const val TAG = "Tendshelf"
        private const val TICK_MS = 10_000L
        private const val SEEK_SETTLE_MS = 2_000L
        private const val FIRST_BACKOFF_MS = 5_000L
        private const val MAX_BACKOFF_MS = 300_000L

        /**
         * Activity start (D10): rows left by a killed process go out now, on a worker thread, without a service. A
         * place the server holds against them is left alone here; the next resume asks. Stops at the first failure.
         */
        fun flushOnce(context: Context, conn: Connection?) {
            if (conn == null) return
            Thread {
                val store = SqliteOutbox(context) { conn.deviceId }
                val outbox = Outbox(store) { System.currentTimeMillis() }
                try {
                    while (true) {
                        val batch = outbox.nextBatch()
                        if (batch.isEmpty()) break
                        val applied = try {
                            outbox.settled(batch, Api(conn.origin).batch(conn.token, batch, conn.deviceId))
                        } catch (e: Exception) {
                            outbox.failed()
                            Log.w(TAG, "launch flush failed: ${e.javaClass.simpleName}")
                            break
                        }
                        Log.i(TAG, "outbox: launch flush sent ${batch.size}")
                        if (applied.deleteSeqs.size < batch.size) break
                    }
                } catch (e: Exception) {
                    Log.w(TAG, "launch flush failed: ${e.javaClass.simpleName}")
                } finally {
                    store.close()
                }
            }.start()
        }

        /** How many places wait unsent in the outbox; 0 when it cannot be read. */
        fun unsentCount(context: Context): Int {
            val store = SqliteOutbox(context) { "" }
            return try {
                store.count()
            } catch (e: Exception) {
                0
            } finally {
                store.close()
            }
        }

        /**
         * A change of server: one try to send what waits to the old server with the old connection, on the calling
         * worker thread. Every row stays; they are dropped by [signOutFlush] after the new sign-in has worked.
         */
        fun sendOnly(context: Context, conn: Connection?) {
            if (conn == null) return
            val store = SqliteOutbox(context) { conn.deviceId }
            val outbox = Outbox(store) { System.currentTimeMillis() }
            try {
                while (true) {
                    val batch = outbox.nextBatch()
                    if (batch.isEmpty()) break
                    val applied = outbox.settled(batch, Api(conn.origin).batch(conn.token, batch, conn.deviceId))
                    if (applied.deleteSeqs.size < batch.size) break
                }
            } catch (e: Exception) {
                Log.w(TAG, "leave flush failed: ${e.javaClass.simpleName}")
            } finally {
                store.close()
            }
        }

        /**
         * Used by sign-out, which has no service: one try to send the rows of this sign-in on the calling worker
         * thread, then delete every row. A stale device id is not a reason to send: rows of another device are skipped.
         */
        fun signOutFlush(context: Context, conn: Connection?) {
            val store = SqliteOutbox(context) { conn?.deviceId.orEmpty() }
            val outbox = Outbox(store) { System.currentTimeMillis() }
            try {
                if (conn != null) {
                    val batch = outbox.nextBatch()
                    if (batch.isNotEmpty()) {
                        try {
                            outbox.settled(batch, Api(conn.origin).batch(conn.token, batch, conn.deviceId))
                        } catch (e: Exception) {
                            Log.w(TAG, "sign-out flush failed: ${e.javaClass.simpleName}")
                        }
                    }
                }
            } catch (e: Exception) {
                Log.w(TAG, "sign-out flush failed: ${e.javaClass.simpleName}")
            } finally {
                try {
                    outbox.clear()
                } catch (e: Exception) {
                    Log.w(TAG, "outbox: not cleared (${e.javaClass.simpleName})")
                }
                store.close()
            }
        }
    }
}
