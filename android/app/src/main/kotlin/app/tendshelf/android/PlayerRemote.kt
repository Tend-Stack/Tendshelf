package app.tendshelf.android

import android.os.Bundle
import androidx.media3.common.Player
import androidx.media3.session.MediaController
import app.tendshelf.shared.Ask
import app.tendshelf.shared.PlayerSnapshot
import app.tendshelf.shared.SleepInfo
import app.tendshelf.shared.SleepMode

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

    /** The person's answer to the place question: true keeps this place, false goes to the other one. */
    fun answer(keepHere: Boolean)

    /** The page's sleep choice: `off`, `plus`, `chapter` or minutes as digits ([minutes] is 0 for the words). */
    fun sleep(choice: String, minutes: Int)

    /** Whether the page is on screen (the activity is started), so the service knows where to ask. */
    fun pageVisible(visible: Boolean)
    fun snapshot(): PlayerSnapshot
    fun observe(listener: (PlayerSnapshot) -> Unit)
    fun release()

    /** Sign-out or another server: stop, forget the book and the credential, end the service. */
    fun stopAndClear()
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

    override fun stopAndClear() {
        android.util.Log.i("Tendshelf", "stop and clear asked")
        controller.sendCustomCommand(PlaybackService.STOP_COMMAND, Bundle.EMPTY)
    }

    override fun answer(keepHere: Boolean) {
        controller.sendCustomCommand(PlaybackService.ANSWER_COMMAND, Bundle().apply { putBoolean("keepHere", keepHere) })
    }

    override fun sleep(choice: String, minutes: Int) {
        controller.sendCustomCommand(PlaybackService.SLEEP_COMMAND, Bundle().apply {
            putString("choice", choice)
            putInt("minutes", minutes)
        })
    }

    override fun pageVisible(visible: Boolean) {
        controller.sendCustomCommand(PlaybackService.VISIBLE_COMMAND, Bundle().apply { putBoolean("visible", visible) })
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
            ask = x.getString(PlaybackService.EXTRA_ASK_BOOK).orEmpty().takeIf { it.isNotEmpty() }?.let {
                Ask(it, x.getLong(PlaybackService.EXTRA_ASK_POSITION), x.getString(PlaybackService.EXTRA_ASK_DEVICE).orEmpty())
            },
            rewoundS = x.getInt(PlaybackService.EXTRA_REWOUND),
            rewoundSeq = x.getInt(PlaybackService.EXTRA_REWOUND_SEQ),
            sleep = SleepInfo(
                mode = when (x.getString(PlaybackService.EXTRA_SLEEP_MODE)) {
                    "minutes" -> SleepMode.MINUTES
                    "chapter" -> SleepMode.CHAPTER
                    else -> SleepMode.OFF
                },
                choice = x.getString(PlaybackService.EXTRA_SLEEP_CHOICE).orEmpty(),
                leftMs = x.getLong(PlaybackService.EXTRA_SLEEP_LEFT),
                fading = x.getBoolean(PlaybackService.EXTRA_SLEEP_FADING),
            ),
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
