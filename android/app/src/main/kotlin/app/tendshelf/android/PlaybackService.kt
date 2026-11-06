package app.tendshelf.android

import android.app.PendingIntent
import android.content.Intent
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.util.Log
import androidx.media3.common.AudioAttributes
import androidx.media3.common.C
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.datasource.DefaultHttpDataSource
import androidx.media3.exoplayer.DefaultLoadControl
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.session.CommandButton
import androidx.media3.session.MediaLibraryService
import androidx.media3.session.MediaSession
import androidx.media3.session.SessionCommand
import androidx.media3.session.SessionResult
import com.google.common.collect.ImmutableList
import com.google.common.util.concurrent.Futures
import com.google.common.util.concurrent.ListenableFuture

/**
 * One ExoPlayer in one session, in a foreground service of type mediaPlayback. A book is one window in book
 * time (see [BookMedia]); [BookPlayer] makes next and previous chapters; [BookEngine] starts books. The
 * activity only asks it to start a book (custom command, this app only) and can die without stopping the sound.
 */
@UnstableApi
class PlaybackService : MediaLibraryService() {
    internal var session: MediaLibrarySession? = null
        private set
    internal lateinit var exo: ExoPlayer
        private set
    internal lateinit var player: BookPlayer
        private set
    internal lateinit var engine: BookEngine
        private set
    private val http = DefaultHttpDataSource.Factory()
    internal val main = Handler(Looper.getMainLooper())
    internal val io = java.util.concurrent.Executors.newCachedThreadPool()

    /** Where the engine finds the server and token: the device token the connect flow stored (A4). */
    @Volatile
    var connections: ConnectionSource = ConnectionSource { null }

    override fun onCreate() {
        super.onCreate()
        connections = TokenStore(this)
        // Ride out a dropped network from the buffer: 5 minutes, time before size (D6).
        val load = DefaultLoadControl.Builder()
            .setBufferDurationsMs(50_000, 300_000, 2_500, 5_000)
            .setTargetBufferBytes(32 * 1024 * 1024)
            .setPrioritizeTimeOverSizeThresholds(true)
            .build()
        exo = ExoPlayer.Builder(this)
            .setLoadControl(load)
            .setAudioAttributes(
                AudioAttributes.Builder()
                    .setUsage(C.USAGE_MEDIA)
                    .setContentType(C.AUDIO_CONTENT_TYPE_SPEECH)
                    .build(),
                true, // audio focus: pause for calls, duck for notifications
            )
            .setHandleAudioBecomingNoisy(true)
            .setWakeMode(C.WAKE_MODE_NETWORK) // keeps CPU and Wi-Fi awake while streaming with the screen off
            .build()
        player = BookPlayer(exo)
        exo.addListener(object : Player.Listener {
            override fun onPlaybackStateChanged(state: Int) {
                Log.i(TAG, "state=$state pos=${exo.currentPosition}")
            }
        })
        val open = PendingIntent.getActivity(
            this, 0,
            Intent(this, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP or Intent.FLAG_ACTIVITY_NEW_TASK),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )
        session = MediaLibrarySession.Builder(this, player, SessionCallback())
            .setSessionActivity(open) // the notification tap opens the page again
            .setMediaButtonPreferences(skipButtons())
            .build()
        engine = BookEngine(exo, player, http, main, io, { connections }) { extras -> session?.setSessionExtras(extras) }
    }

    /** Back and Forward beside the chapter buttons (D4). */
    private fun skipButtons(): ImmutableList<CommandButton> = ImmutableList.of(
        CommandButton.Builder(CommandButton.ICON_SKIP_BACK)
            .setPlayerCommand(Player.COMMAND_SEEK_BACK)
            .setDisplayName("Back")
            .setSlots(CommandButton.SLOT_OVERFLOW)
            .build(),
        CommandButton.Builder(CommandButton.ICON_SKIP_FORWARD)
            .setPlayerCommand(Player.COMMAND_SEEK_FORWARD)
            .setDisplayName("Forward")
            .setSlots(CommandButton.SLOT_OVERFLOW)
            .build(),
    )

    /** For the debug start path only: nobody has connected, so nobody added the session. */
    internal fun registerSession() {
        session?.let { addSession(it) }
    }

    override fun onGetSession(controllerInfo: MediaSession.ControllerInfo): MediaLibrarySession? = session

    /** Release builds have no debug start path: [DebugHooks] is a stub there, and the real one lives in src/debug. */
    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (DebugHooks.onServiceStart(this, intent)) return START_NOT_STICKY
        return super.onStartCommand(intent, flags, startId)
    }

    /** Swiping the app away must not stop a book that is playing. */
    override fun onTaskRemoved(rootIntent: Intent?) {
        if (!exo.playWhenReady || exo.mediaItemCount == 0) stopSelf()
    }

    override fun onDestroy() {
        session?.release()
        session = null
        exo.release()
        io.shutdownNow()
        super.onDestroy()
    }

    private inner class SessionCallback : MediaLibrarySession.Callback {
        override fun onConnect(session: MediaSession, controller: MediaSession.ControllerInfo): MediaSession.ConnectionResult {
            // Custom commands are for this app only; Auto, System UI, headsets get the standard ones (invariant 3).
            val commands = MediaSession.ConnectionResult.DEFAULT_SESSION_AND_LIBRARY_COMMANDS.buildUpon().apply {
                if (controller.packageName == packageName) {
                    add(START_COMMAND)
                    add(STOP_COMMAND)
                }
            }.build()
            return MediaSession.ConnectionResult.AcceptedResultBuilder(session)
                .setAvailableSessionCommands(commands)
                .build()
        }

        override fun onCustomCommand(
            session: MediaSession,
            controller: MediaSession.ControllerInfo,
            customCommand: SessionCommand,
            args: Bundle,
        ): ListenableFuture<SessionResult> {
            if (controller.packageName != packageName) {
                return Futures.immediateFuture(SessionResult(SessionResult.RESULT_ERROR_PERMISSION_DENIED))
            }
            when (customCommand.customAction) {
                START_ACTION -> engine.start(args.getString("book").orEmpty(), args.getLong("atMs", -1L).takeIf { it >= 0 })
                STOP_ACTION -> {
                    engine.stopAndClear()
                    stopSelf()
                }
                else -> return Futures.immediateFuture(SessionResult(SessionResult.RESULT_ERROR_NOT_SUPPORTED))
            }
            return Futures.immediateFuture(SessionResult(SessionResult.RESULT_SUCCESS))
        }
    }

    companion object {
        internal const val TAG = "Tendshelf"
        const val START_ACTION = "app.tendshelf.START_BOOK"
        const val STOP_ACTION = "app.tendshelf.STOP_AND_CLEAR"
        val START_COMMAND = SessionCommand(START_ACTION, Bundle.EMPTY)
        val STOP_COMMAND = SessionCommand(STOP_ACTION, Bundle.EMPTY)
    }
}
