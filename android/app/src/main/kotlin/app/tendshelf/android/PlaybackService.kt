package app.tendshelf.android

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
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
import androidx.media3.session.MediaSessionService
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
    internal lateinit var writer: ProgressWriter
        private set
    private lateinit var askPrompt: AskPrompt
    internal lateinit var sleep: SleepTimer
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
        engine = BookEngine(exo, player, http, main, io, { connections }) { extras -> putExtras { putAll(extras) } }
        sleep = SleepTimer(exo, player, getSystemService(android.hardware.SensorManager::class.java), main) { info ->
            putExtras {
                putString(EXTRA_SLEEP_MODE, info.mode.name.lowercase())
                putString(EXTRA_SLEEP_CHOICE, info.choice)
                putLong(EXTRA_SLEEP_LEFT, info.leftMs)
                putBoolean(EXTRA_SLEEP_FADING, info.fading)
            }
        }
        wireProgress()
        setListener(RefusedListener())
        exo.addListener(object : Player.Listener {
            override fun onPlaybackStateChanged(state: Int) {
                if (state == Player.STATE_READY) nm().cancel(REFUSED_ID)
            }
        })
    }

    private fun nm() = getSystemService(NotificationManager::class.java)

    /** Session extras are one bundle: every writer merges into it, none replaces it. */
    internal fun putExtras(change: Bundle.() -> Unit) {
        val s = session ?: return
        s.setSessionExtras(Bundle(s.sessionExtras).apply(change))
    }

    /**
     * The system said no to a foreground start (A5b D1). Media3 calls this on the main thread. The player stops
     * trying within the same turn, the page can say why, and a notification gives a tap back to the app, where
     * the activity is on top and the next start is allowed.
     */
    private inner class RefusedListener : MediaSessionService.Listener {
        override fun onForegroundServiceStartNotAllowedException() {
            Log.w(TAG, "foreground start refused")
            engine.refused()
            val nm = nm()
            nm.createNotificationChannel(NotificationChannel(PLAYBACK_CHANNEL, getString(R.string.playback_channel), NotificationManager.IMPORTANCE_DEFAULT).apply {
                setSound(null, null)
                lockscreenVisibility = Notification.VISIBILITY_PRIVATE
            })
            val open = PendingIntent.getActivity(
                this@PlaybackService, 1,
                Intent(this@PlaybackService, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP or Intent.FLAG_ACTIVITY_NEW_TASK),
                PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
            )
            nm.notify(
                REFUSED_ID,
                Notification.Builder(this@PlaybackService, PLAYBACK_CHANNEL)
                    .setSmallIcon(android.R.drawable.ic_media_play)
                    .setContentTitle(getString(R.string.keep_listening))
                    .setVisibility(Notification.VISIBILITY_PRIVATE)
                    .setAutoCancel(true)
                    .setContentIntent(open)
                    .build(),
            )
        }
    }

    /** A5: saving, rewinding, the place question and the next book. None of it needs a page. */
    private fun wireProgress() {
        val w = ProgressWriter(this, exo, { connections }, main)
        writer = w
        w.bookSource { engine.currentBook }
        val ask = AskPrompt(this, w, { a -> putExtras { putAsk(a) } }) { ms -> exo.seekTo(ms) }
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
            sleep.bookChanged()
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
        engine.onEnded = {
            // A running sleep timer wins over the queue (D5): the book is finished, nothing new starts.
            val askQueue = !sleep.endedBook()
            next.ended(engine.currentBook, exo.currentPosition, exo.playbackParameters.speed.toDouble(), askQueue) { exo.playbackState == Player.STATE_ENDED }
        }
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
                    Log.i(TAG, "place check: remote ${if (remote == null) "none" else "found"}")
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
        resume.onRewound = { s ->
            putExtras {
                putInt(EXTRA_REWOUND, s)
                putInt(EXTRA_REWOUND_SEQ, getInt(EXTRA_REWOUND_SEQ) + 1)
            }
        }
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
        if (::askPrompt.isInitialized && askPrompt.handle(intent)) return START_NOT_STICKY
        if (DebugHooks.onServiceStart(this, intent)) return START_NOT_STICKY
        return super.onStartCommand(intent, flags, startId)
    }

    /** Swiping the app away must not stop a book that is playing. */
    override fun onTaskRemoved(rootIntent: Intent?) {
        writer.saveNow("task removed")
        if (!exo.playWhenReady || exo.mediaItemCount == 0) stopSelf()
    }

    override fun onDestroy() {
        clearListener()
        nm().cancel(REFUSED_ID)
        sleep.release()
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
                    add(ANSWER_COMMAND)
                    add(VISIBLE_COMMAND)
                    add(SLEEP_COMMAND)
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
                    Log.i(TAG, "stop and clear done")
                    sleep.off()
                    engine.stopAndClear()
                    stopSelf()
                }
                ANSWER_ACTION -> askPrompt.answer(args.getBoolean("keepHere"))
                VISIBLE_ACTION -> askPrompt.setPageVisible(args.getBoolean("visible"))
                SLEEP_ACTION -> sleep.command(args.getString("choice").orEmpty(), args.getInt("minutes", 0))
                else -> return Futures.immediateFuture(SessionResult(SessionResult.RESULT_ERROR_NOT_SUPPORTED))
            }
            return Futures.immediateFuture(SessionResult(SessionResult.RESULT_SUCCESS))
        }
    }

    companion object {
        internal const val TAG = "Tendshelf"
        const val START_ACTION = "app.tendshelf.START_BOOK"
        const val STOP_ACTION = "app.tendshelf.STOP_AND_CLEAR"
        const val ANSWER_ACTION = "app.tendshelf.ANSWER_ASK"
        const val VISIBLE_ACTION = "app.tendshelf.PAGE_VISIBLE"
        const val EXTRA_REWOUND = "ts.rewoundS"
        const val EXTRA_REWOUND_SEQ = "ts.rewoundSeq"
        const val EXTRA_ASK_BOOK = "ts.ask.bookId"
        const val EXTRA_ASK_POSITION = "ts.ask.positionMs"
        const val EXTRA_ASK_DEVICE = "ts.ask.device"
        const val SLEEP_ACTION = "app.tendshelf.SLEEP"
        const val EXTRA_SLEEP_MODE = "ts.sleep.mode"
        const val EXTRA_SLEEP_CHOICE = "ts.sleep.choice"
        const val EXTRA_SLEEP_LEFT = "ts.sleep.leftMs"
        const val EXTRA_SLEEP_FADING = "ts.sleep.fading"
        const val PLAYBACK_CHANNEL = "playback"
        const val REFUSED_ID = 4244
        val ANSWER_COMMAND = SessionCommand(ANSWER_ACTION, Bundle.EMPTY)
        val SLEEP_COMMAND = SessionCommand(SLEEP_ACTION, Bundle.EMPTY)
        val VISIBLE_COMMAND = SessionCommand(VISIBLE_ACTION, Bundle.EMPTY)
        val START_COMMAND = SessionCommand(START_ACTION, Bundle.EMPTY)
        val STOP_COMMAND = SessionCommand(STOP_ACTION, Bundle.EMPTY)
    }
}
