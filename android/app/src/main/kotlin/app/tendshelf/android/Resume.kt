package app.tendshelf.android

import android.util.Log
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.ExoPlayer
import app.tendshelf.shared.PlayerSettings
import app.tendshelf.shared.ResumeClock

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

    private val clock = ResumeClock(now)

    init {
        exo.addListener(object : Player.Listener {
            override fun onIsPlayingChanged(isPlaying: Boolean) {
                if (!isPlaying) {
                    // Buffering with the play button on is not a pause.
                    val buffering = exo.playWhenReady && exo.playbackState == Player.STATE_BUFFERING
                    clock.stopped(book().isNotEmpty(), buffering)
                } else {
                    resumed()
                }
            }
        })
    }

    /** A book was loaded. [placeWrittenAt] is when its stored place was saved; null with [atGiven] or no place. */
    fun bookLoaded(placeWrittenAt: Long?, atGiven: Boolean) {
        clock.bookLoaded(placeWrittenAt, atGiven)
    }

    fun clear() {
        clock.clear()
    }

    private fun resumed() {
        val s = settings()
        val step = clock.resumed(s.rewindSteps, s.autoRewind) ?: return
        if (step.rewindMs > 0) {
            exo.seekTo((exo.currentPosition - step.rewindMs).coerceAtLeast(0))
            Log.i(TAG, "rewound ${step.rewindMs / 1000} s")
            onRewound?.invoke((step.rewindMs / 1000).toInt())
        }
        if (step.checkRemote) checkRemote?.invoke(book())
    }

    companion object {
        private const val TAG = "Tendshelf"
    }
}
