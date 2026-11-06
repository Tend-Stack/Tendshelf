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
import java.util.concurrent.Executors

/**
 * One ExoPlayer in one session, in a foreground service of type mediaPlayback. A book is one window in book
 * time (see [BookMedia]); [BookPlayer] makes next and previous chapters; [BookEngine] starts books. The
 * activity only asks it to start a book (custom command, this app only) and can die without stopping the sound.
 */
@UnstableApi
class PlaybackService : MediaLibraryService() {
    private var session: MediaLibrarySession? = null
    private lateinit var exo: ExoPlayer
    private lateinit var player: BookPlayer
    private lateinit var engine: BookEngine
    private lateinit var writer: ProgressWriter
    private lateinit var askPrompt: AskPrompt
    private val http = DefaultHttpDataSource.Factory()
    private val main = Handler(Looper.getMainLooper())
    private val io = Executors.newCachedThreadPool()

    /** Where the engine finds the server and token. A2/A4 set it; debug starts set it from a sign-in. */
    var connections = ConnectionSource { null }

    override fun onCreate() {
        super.onCreate()
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
        wireProgress()
    }

    /** A5: saving, rewinding, the place question and the next book. None of it needs a page. */
    private fun wireProgress() {
        val w = ProgressWriter(this, exo, { connections }, main)
        writer = w
        w.bookSource { engine.currentBook }
        val ask = AskPrompt(this, w) { ms -> exo.seekTo(ms) }
        askPrompt = ask
        val resume = Resume(exo, { engine.settings }, { engine.currentBook })
        val next = NextBook(this, w, { connections }, main, io) { id -> engine.start(id, null) }
        var lastChapter = -2
        engine.pendingFor = { id -> w.pending(id) }
        engine.onBookChanging = { w.saveNow("book") }
        engine.onBookStarted = { _, writtenAt, atGiven ->
            ask.dismissPlain()
            resume.bookLoaded(writtenAt, atGiven)
            next.reset()
            lastChapter = -2
        }
        engine.onPositionTick = {
            val book = engine.currentBook
            if (book.isNotEmpty()) {
                val chapter = player.chapterIdx
                if (lastChapter != -2 && chapter != lastChapter) w.saveNow("chapter")
                lastChapter = chapter
                w.onTick(book)
            }
        }
        engine.onEnded = { next.ended(engine.currentBook, exo.currentPosition, exo.playbackParameters.speed.toDouble()) { exo.playbackState == Player.STATE_ENDED } }
        w.onSignedOut = { engine.signedOut() }
        w.onLost = { lost, conn ->
            val book = engine.currentBook
            if (!ask.isOpen) {
                val local = if (lost.save.bookId == book) exo.currentPosition else lost.save.positionMs
                app.tendshelf.shared.conflict(lost.save.bookId, local, lost.remote, conn.deviceId)?.let { ask.show(it) }
            }
        }
        resume.checkRemote = { book ->
            io.execute {
                val conn = connections.current() ?: return@execute
                try {
                    val remote = Api(conn.origin).progress(conn.token, book)
                    main.post {
                        if (!ask.isOpen && engine.currentBook == book) {
                            app.tendshelf.shared.conflict(book, exo.currentPosition, remote, conn.deviceId)?.let { ask.show(it) }
                        }
                    }
                } catch (e: Exception) {
                    Log.w(TAG, "place check failed: ${e.javaClass.simpleName}")
                }
            }
        }
        resume.onRewound = { s -> session?.let { it.setSessionExtras(Bundle(it.sessionExtras).apply { putInt(EXTRA_REWOUND, s) }) } }
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

    override fun onGetSession(controllerInfo: MediaSession.ControllerInfo): MediaLibrarySession? = session

    /** Debug builds only: drive the player from `am startservice` for the emulator checks. */
    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (::askPrompt.isInitialized && askPrompt.handle(intent?.action)) return START_NOT_STICKY
        if (BuildConfig.DEBUG && intent?.action == DEBUG_ACTION) {
            keepAliveForDebug()
            debug(intent)
            return START_NOT_STICKY
        }
        return super.onStartCommand(intent, flags, startId)
    }

    /**
     * `am start-foreground-service` demands startForeground within seconds, and the emulator's lock screen
     * keeps the app in the background. A throwaway notification satisfies that; the media notification takes
     * over once a book plays, and this one is removed after 10 s. Debug builds only.
     */
    private fun keepAliveForDebug() {
        val nm = getSystemService(android.app.NotificationManager::class.java)
        nm.createNotificationChannel(android.app.NotificationChannel("debug", "Debug", android.app.NotificationManager.IMPORTANCE_LOW))
        val n = android.app.Notification.Builder(this, "debug").setSmallIcon(android.R.drawable.ic_media_play).setContentTitle("debug").build()
        startForeground(4242, n, android.content.pm.ServiceInfo.FOREGROUND_SERVICE_TYPE_MEDIA_PLAYBACK)
        // Normally the first controller to connect adds the session; here nobody has connected.
        session?.let { addSession(it) }
        main.postDelayed({ nm.cancel(4242) }, 10_000)
    }

    private fun debug(i: Intent) {
        when (i.getStringExtra("op")) {
            "start" -> io.execute {
                try {
                    val server = i.getStringExtra("server") ?: BuildConfig.DEFAULT_SERVER
                    val token = i.getStringExtra("token")
                        ?: Api(server).signIn(i.getStringExtra("user").orEmpty(), i.getStringExtra("pass").orEmpty())
                    main.post {
                        connections = ConnectionSource { Connection(server, token, "debug") }
                        engine.start(i.getStringExtra("book").orEmpty(), if (i.hasExtra("at")) i.getLongExtra("at", -1) else null)
                    }
                } catch (e: Exception) {
                    Log.w(TAG, "debug sign-in failed: ${e.javaClass.simpleName}")
                }
            }
            "seek" -> player.seekTo(i.getLongExtra("ms", 0))
            "speed" -> player.setPlaybackSpeed(i.getFloatExtra("speed", 1f))
            "play" -> player.play()
            "pause" -> player.pause()
            "next" -> player.seekToNext()
            "prev" -> player.seekToPrevious()
            "back" -> player.seekBack()
            "forward" -> player.seekForward()
            "stop" -> engine.stopAndClear()
            "outbox" -> Log.i(TAG, "outbox rows=${writer.rowCount()}")
            "dump" -> Log.i(
                TAG, "dump pos=${exo.currentPosition} dur=${exo.duration} speed=${exo.playbackParameters.speed} " +
                    "playing=${exo.isPlaying} state=${exo.playbackState} chapter=${player.chapterIdx} " +
                    "title=${player.mediaMetadata.title} error=${session?.sessionExtras?.getString(BookEngine.EXTRA_ERROR)}",
            )
        }
    }

    /** Swiping the app away must not stop a book that is playing. */
    override fun onTaskRemoved(rootIntent: Intent?) {
        writer.saveNow("task removed")
        if (!exo.playWhenReady || exo.mediaItemCount == 0) stopSelf()
    }

    override fun onDestroy() {
        writer.saveNow("destroy")
        writer.close()
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
        private const val TAG = "Tendshelf"
        const val START_ACTION = "app.tendshelf.START_BOOK"
        const val STOP_ACTION = "app.tendshelf.STOP_AND_CLEAR"
        const val EXTRA_REWOUND = "ts.rewoundS"
        const val DEBUG_ACTION = "app.tendshelf.DEBUG_PLAYER"
        val START_COMMAND = SessionCommand(START_ACTION, Bundle.EMPTY)
        val STOP_COMMAND = SessionCommand(STOP_ACTION, Bundle.EMPTY)
    }
}
