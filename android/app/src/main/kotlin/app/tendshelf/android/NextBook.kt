package app.tendshelf.android

import android.content.Context
import android.os.Handler
import android.os.PowerManager
import android.util.Log
import java.util.concurrent.Executor

/**
 * The book ended with the screen possibly off (A5, D7). A 30 s partial wake lock covers the hand-off (ExoPlayer
 * drops its own at the end). `finished` is written and sent first; only when that went out is the queue asked, once.
 * The ask is never repeated: a lost answer costs one queued book, a retry could skip a second. No page is involved.
 */
class NextBook(
    context: Context,
    private val writer: ProgressWriter,
    private val connections: () -> ConnectionSource,
    private val main: Handler,
    private val io: Executor,
    private val start: (String) -> Unit,
) {
    private val power = context.getSystemService(PowerManager::class.java)
    private var handled = ""

    /** A new book began; its end is again a hand-off. */
    fun reset() {
        handled = ""
    }

    /** Main thread, on the player's end. [stillEnded] is checked again before the next book starts. */
    fun ended(bookId: String, positionMs: Long, speed: Double, askQueue: Boolean = true, stillEnded: () -> Boolean) {
        if (bookId.isEmpty() || handled == bookId) return
        handled = bookId
        val lock = power.newWakeLock(PowerManager.PARTIAL_WAKE_LOCK, "tendshelf:nextbook").apply {
            setReferenceCounted(false)
            acquire(30_000)
        }
        val listened = writer.drainListened()
        io.execute {
            try {
                val conn = connections().current()
                val sent = writer.finishAndFlush(bookId, positionMs, speed, listened)
                if (!askQueue) {
                    Log.i(TAG, "next book: a sleep timer ended this book, queue not asked")
                    return@execute
                }
                if (!sent || conn == null) {
                    Log.i(TAG, "next book: finished not sent, nothing popped")
                    return@execute
                }
                val next = try {
                    Api(conn.origin).queueNext(conn.token, bookId)
                } catch (e: Exception) {
                    Log.w(TAG, "next book: no answer (${e.javaClass.simpleName}), not asked again")
                    null
                }
                if (next == null) {
                    Log.i(TAG, "next book: none")
                } else {
                    Log.i(TAG, "next book: starting")
                    main.post { if (stillEnded()) start(next) }
                }
            } finally {
                main.postDelayed({ if (lock.isHeld) lock.release() }, 8_000) // the manifest fetch of the next book runs on
            }
        }
    }

    companion object {
        private const val TAG = "Tendshelf"
    }
}
