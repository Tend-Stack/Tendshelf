package app.tendshelf.android

import android.os.SystemClock
import androidx.media3.common.util.UnstableApi
import app.tendshelf.shared.AUTO_TICK_MS
import app.tendshelf.shared.AutoStep
import app.tendshelf.shared.autoStep
import app.tendshelf.shared.nearEnd

/**
 * Makes the next book ready on Wi-Fi while this one plays (decision 0035). Main thread only. It looks at the position
 * at most once a minute, and only while the player is playing: that is the one moment the system lets the app start
 * the download service from here. A refusal is harmless (Offline.releaseHeld keeps the book held for the next moment).
 */
@UnstableApi
class AutoNext {
    private var lastTick = 0L
    private var asked = ""
    private var askedAt = 0L

    /** A new book started: forget what was asked for the old one, and the pending wish. */
    fun reset() {
        lastTick = 0L
        asked = ""
        askedAt = 0L
        Offline.autoForget()
    }

    fun tick(book: String, positionMs: Long, durationMs: Long, playing: Boolean) {
        if (!playing || book.isEmpty()) return
        val now = SystemClock.elapsedRealtime()
        if (lastTick != 0L && now - lastTick < AUTO_TICK_MS) return
        lastTick = now
        // Holds and releases follow the network and the switch even when this book is not near its end.
        Offline.io { Offline.autoSync() }
        EpisodeAuto.syncSoon() // at most once an hour; only while some show asks for downloads
        when (autoStep(Offline.loadSettings().autoDownload, nearEnd(positionMs, durationMs), Offline.unmetered(), if (asked == book) now - askedAt else null)) {
            AutoStep.WANT -> Offline.autoWant(book)
            AutoStep.ASK -> {
                asked = book
                askedAt = now
                Offline.io { Offline.autoFetch(book) }
            }
            AutoStep.NOTHING -> {}
        }
    }
}
