package app.tendshelf.android

import android.os.Bundle
import androidx.media3.common.Player
import androidx.media3.session.MediaController
import app.tendshelf.shared.PlayerSnapshot

/**
 * THE seam to the player. The web bar (through the A4 bridge) and a later native player screen are both
 * clients of it; neither touches the service. Everything is in book milliseconds. Main thread only.
 */
interface PlayerRemote {
    /** [atMs] null: the best known place. */
    fun start(bookId: String, atMs: Long?)
    fun play()
    fun pause()
    fun seek(bookMs: Long)
    fun setSpeed(speed: Double)
    fun snapshot(): PlayerSnapshot
    fun observe(listener: (PlayerSnapshot) -> Unit)
    fun release()
}

/** The remote over a Media3 controller connected to [PlaybackService]. */
class ControllerRemote(private val controller: MediaController) : PlayerRemote {
    private val listeners = mutableListOf<(PlayerSnapshot) -> Unit>()
    private val watcher = object : Player.Listener {
        override fun onEvents(player: Player, events: Player.Events) = fire()
    }

    init {
        controller.addListener(watcher)
    }

    /**
     * Session extras (book, chapter, error) change through a `MediaController.Listener`, which only the
     * controller's builder can take; its owner calls this from `onExtrasChanged`.
     */
    fun extrasChanged() = fire()

    override fun start(bookId: String, atMs: Long?) {
        val args = Bundle().apply {
            putString("book", bookId)
            putLong("atMs", atMs ?: -1L)
        }
        controller.sendCustomCommand(PlaybackService.START_COMMAND, args)
    }

    override fun play() = controller.play()
    override fun pause() = controller.pause()
    override fun seek(bookMs: Long) = controller.seekTo(bookMs)
    override fun setSpeed(speed: Double) = controller.setPlaybackSpeed(speed.toFloat())

    override fun snapshot(): PlayerSnapshot {
        val x = controller.sessionExtras
        return PlayerSnapshot(
            bookId = x.getString(BookEngine.EXTRA_BOOK).orEmpty(),
            positionMs = controller.currentPosition.coerceAtLeast(0),
            durationMs = controller.duration.coerceAtLeast(0),
            playing = controller.isPlaying,
            buffering = controller.playbackState == Player.STATE_BUFFERING,
            ended = controller.playbackState == Player.STATE_ENDED,
            speed = controller.playbackParameters.speed.toDouble(),
            chapterIdx = x.getInt(BookEngine.EXTRA_CHAPTER, -1),
            error = x.getString(BookEngine.EXTRA_ERROR).orEmpty(),
        )
    }

    override fun observe(listener: (PlayerSnapshot) -> Unit) {
        listeners.add(listener)
    }

    override fun release() {
        controller.removeListener(watcher)
        listeners.clear()
        MediaController.releaseFuture(com.google.common.util.concurrent.Futures.immediateFuture(controller))
    }

    private fun fire() {
        val s = snapshot()
        for (l in listeners.toList()) l(s)
    }
}
