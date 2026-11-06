package app.tendshelf.android

import android.util.Log
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.ExoPlayer
import app.tendshelf.shared.BREAK_MS
import app.tendshelf.shared.PlayerSettings
import app.tendshelf.shared.rewindMs

/**
 * Smart rewind and the check for a place elsewhere, on every start of sound after a break (A5, D6). It listens
 * to the player, not to `play()`, so a resume after a call (which ExoPlayer does by itself) counts too. The
 * page is not involved. Main thread only.
 */
@UnstableApi
class Resume(
    private val exo: ExoPlayer,
    private val settings: () -> PlayerSettings,
    private val book: () -> String,
    private val now: () -> Long = System::currentTimeMillis,
) {
    /** Called with the seconds stepped back, after the seek. */
    var onRewound: ((Int) -> Unit)? = null

    /** Called when the break was a minute or more and the book is the same: look at the server's place. */
    var checkRemote: ((String) -> Unit)? = null

    private var idleAt: Long? = null
    private var fresh = false

    init {
        exo.addListener(object : Player.Listener {
            override fun onIsPlayingChanged(isPlaying: Boolean) {
                if (!isPlaying) {
                    // Buffering with the play button on is not a pause.
                    val buffering = exo.playWhenReady && exo.playbackState == Player.STATE_BUFFERING
                    if (!buffering && book().isNotEmpty() && idleAt == null) idleAt = now()
                } else {
                    resumed()
                }
            }
        })
    }

    /** A book was loaded. [placeWrittenAt] is when its stored place was saved; null with [atGiven] or no place. */
    fun bookLoaded(placeWrittenAt: Long?, atGiven: Boolean) {
        idleAt = if (atGiven) null else placeWrittenAt
        fresh = true
    }

    fun clear() {
        idleAt = null
    }

    private fun resumed() {
        val since = idleAt ?: return
        idleAt = null
        val wasFresh = fresh
        fresh = false
        val away = (now() - since).coerceAtLeast(0)
        val s = settings()
        val step = rewindMs(away, s.rewindSteps, s.autoRewind)
        if (step > 0) {
            exo.seekTo((exo.currentPosition - step).coerceAtLeast(0))
            Log.i(TAG, "rewound ${step / 1000} s after ${away / 1000} s away")
            onRewound?.invoke((step / 1000).toInt())
        }
        if (!wasFresh && away >= BREAK_MS) checkRemote?.invoke(book())
    }

    companion object {
        private const val TAG = "Tendshelf"
    }
}
