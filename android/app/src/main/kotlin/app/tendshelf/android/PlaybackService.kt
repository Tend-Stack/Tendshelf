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
import androidx.media3.common.MediaItem
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.datasource.DefaultHttpDataSource
import androidx.media3.exoplayer.DefaultLoadControl
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.session.CommandButton
import androidx.media3.session.LibraryResult
import androidx.media3.session.MediaLibraryService.LibraryParams
import androidx.media3.session.MediaLibraryService
import androidx.media3.session.MediaSession
import androidx.media3.session.MediaSessionService
import androidx.media3.session.SessionCommand
import androidx.media3.session.SessionError
import androidx.media3.session.SessionResult
import app.tendshelf.shared.Node
import app.tendshelf.shared.allowedCommands
import app.tendshelf.shared.parseNode
import app.tendshelf.shared.speedCycle
import app.tendshelf.shared.speedLabel
import com.google.common.collect.ImmutableList
import com.google.common.util.concurrent.SettableFuture
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
    private lateinit var tree: LibraryTree
    private val http = DefaultHttpDataSource.Factory()
    internal val main = Handler(Looper.getMainLooper())
    internal val io = java.util.concurrent.Executors.newCachedThreadPool()

    /** Where the engine finds the server and token: the device token the connect flow stored (A4). */
    @Volatile
    var connections: ConnectionSource = ConnectionSource { null }

    override fun onCreate() {
        super.onCreate()
        Offline.init(this)
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
        tree = LibraryTree(this, { connections }, io, main)
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
            .setMediaButtonPreferences(skipButtons(1.0))
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
        wireCar()
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
                    .setSmallIcon(R.drawable.ic_stat_tendshelf)
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

    /** A8: a book chosen in a tree starts at the server's place; the speed button shows its speed; a failure is worded to the car. */
    private fun wireCar() {
        player.onBookChosen = { id -> engine.start(id, null) }
        val prefs = getSharedPreferences(CAR_PREFS, MODE_PRIVATE)
        val before = engine.onBookStarted
        engine.onBookStarted = { id, writtenAt, atGiven ->
            prefs.edit().putString(LAST_BOOK, id).apply() // D8: what a "resume" from a media button plays
            before?.invoke(id, writtenAt, atGiven)
        }
        engine.onFailed = { kind -> tellCar(kind) }
        exo.addListener(object : Player.Listener {
            override fun onPlaybackParametersChanged(playbackParameters: androidx.media3.common.PlaybackParameters) {
                session?.setMediaButtonPreferences(skipButtons(playbackParameters.speed.toDouble()))
            }
        })
    }

    /** Plain words to every connected car, not only a dark screen (invariant 4). Our own page already shows its own. */
    private fun tellCar(kind: String) {
        val s = session ?: return
        val (code, words) = when (kind) {
            "signedOut" -> SessionError.ERROR_SESSION_AUTHENTICATION_EXPIRED to R.string.car_sign_in
            "background" -> SessionError.ERROR_SESSION_SETUP_REQUIRED to R.string.car_keep_listening
            "stream" -> SessionError.ERROR_IO to R.string.car_no_connection
            else -> SessionError.ERROR_UNKNOWN to R.string.car_cant_play
        }
        for (c in s.connectedControllers) {
            if (c.packageName != packageName && CarAccess.allowed(c.uid)) s.sendError(c, SessionError(code, getString(words)))
        }
    }

    /** Back and Forward beside the chapter buttons (D4), and the car's speed button (A8, D7). */
    private fun skipButtons(speed: Double): ImmutableList<CommandButton> = ImmutableList.of(
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
        CommandButton.Builder(CommandButton.ICON_PLAYBACK_SPEED)
            .setSessionCommand(SPEED_COMMAND)
            .setDisplayName(speedLabel(speed))
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
        /** Who this controller is (A8, D1): trusted ones and Android Auto browse; any other app plays and nothing more. */
        private fun allowed(session: MediaSession, c: MediaSession.ControllerInfo) = allowedCommands(
            trusted = c.isTrusted,
            auto = session.isAutoCompanionController(c) || session.isAutomotiveController(c),
            own = c.packageName == packageName,
        )

        override fun onConnect(session: MediaSession, controller: MediaSession.ControllerInfo): MediaSession.ConnectionResult {
            val who = allowed(session, controller)
            // Library commands only for those who may browse; custom commands for this app only, the speed button aside.
            val base = if (who.browse) MediaSession.ConnectionResult.DEFAULT_SESSION_AND_LIBRARY_COMMANDS else MediaSession.ConnectionResult.DEFAULT_SESSION_COMMANDS
            val commands = base.buildUpon().apply {
                if (who.ownCommands) {
                    add(START_COMMAND)
                    add(STOP_COMMAND)
                    add(ANSWER_COMMAND)
                    add(VISIBLE_COMMAND)
                    add(SLEEP_COMMAND)
                }
                if (who.speed) add(SPEED_COMMAND)
            }.build()
            if (who.browse) CarAccess.add(controller.uid)
            Log.i(TAG, "controller ${controller.packageName} trusted=${controller.isTrusted} browse=${who.browse}")
            return MediaSession.ConnectionResult.AcceptedResultBuilder(session)
                .setAvailableSessionCommands(commands)
                .build()
        }

        override fun onDisconnected(session: MediaSession, controller: MediaSession.ControllerInfo) {
            if (allowed(session, controller).browse) CarAccess.remove(controller.uid)
        }

        /** The timeline is the engine's: nobody else sets or edits it. Playback and seeking stay open (headsets, notification). */
        override fun onPlayerCommandRequest(session: MediaSession, controller: MediaSession.ControllerInfo, playerCommand: Int): Int =
            if (!allowed(session, controller).browse && (playerCommand == Player.COMMAND_SET_MEDIA_ITEM || playerCommand == Player.COMMAND_CHANGE_MEDIA_ITEMS)) {
                SessionResult.RESULT_ERROR_PERMISSION_DENIED
            } else {
                SessionResult.RESULT_SUCCESS
            }

        override fun onGetLibraryRoot(session: MediaLibrarySession, browser: MediaSession.ControllerInfo, params: LibraryParams?): ListenableFuture<LibraryResult<MediaItem>> =
            Futures.immediateFuture(
                if (allowed(session, browser).browse) LibraryResult.ofItem(tree.rootItem(), params) else LibraryResult.ofError(LibraryResult.RESULT_ERROR_PERMISSION_DENIED),
            )

        override fun onGetChildren(
            session: MediaLibrarySession, browser: MediaSession.ControllerInfo, parentId: String, page: Int, pageSize: Int, params: LibraryParams?,
        ): ListenableFuture<LibraryResult<ImmutableList<MediaItem>>> =
            if (allowed(session, browser).browse) tree.children(parentId, page, pageSize) else Futures.immediateFuture(LibraryResult.ofError(LibraryResult.RESULT_ERROR_PERMISSION_DENIED))

        override fun onGetItem(session: MediaLibrarySession, browser: MediaSession.ControllerInfo, mediaId: String): ListenableFuture<LibraryResult<MediaItem>> =
            if (allowed(session, browser).browse) tree.item(mediaId) else Futures.immediateFuture(LibraryResult.ofError(LibraryResult.RESULT_ERROR_PERMISSION_DENIED))

        override fun onSearch(session: MediaLibrarySession, browser: MediaSession.ControllerInfo, query: String, params: LibraryParams?): ListenableFuture<LibraryResult<Void>> {
            if (!allowed(session, browser).browse) return Futures.immediateFuture(LibraryResult.ofError(LibraryResult.RESULT_ERROR_PERMISSION_DENIED))
            tree.runSearch(query) { n -> session.notifySearchResultChanged(browser, query, n, params) }
            return Futures.immediateFuture(LibraryResult.ofVoid())
        }

        override fun onGetSearchResult(
            session: MediaLibrarySession, browser: MediaSession.ControllerInfo, query: String, page: Int, pageSize: Int, params: LibraryParams?,
        ): ListenableFuture<LibraryResult<ImmutableList<MediaItem>>> =
            if (allowed(session, browser).browse) tree.search(query, page, pageSize) else Futures.immediateFuture(LibraryResult.ofError(LibraryResult.RESULT_ERROR_PERMISSION_DENIED))

        /** A pick from a tree, a voice query (D4), or nothing at all from an app that may not browse. The engine starts the book (D3). */
        override fun onSetMediaItems(
            mediaSession: MediaSession, controller: MediaSession.ControllerInfo, mediaItems: MutableList<MediaItem>, startIndex: Int, startPositionMs: Long,
        ): ListenableFuture<MediaSession.MediaItemsWithStartPosition> {
            val none = MediaSession.MediaItemsWithStartPosition(emptyList(), 0, C.TIME_UNSET)
            if (!allowed(mediaSession, controller).browse) return Futures.immediateFuture(none)
            val first = mediaItems.firstOrNull() ?: return Futures.immediateFuture(none)
            val query = first.requestMetadata.searchQuery
            if (query.isNullOrBlank()) {
                val books = mediaItems.filter { parseNode(it.mediaId) is Node.Book }
                return Futures.immediateFuture(MediaSession.MediaItemsWithStartPosition(books, 0, C.TIME_UNSET))
            }
            val future = SettableFuture.create<MediaSession.MediaItemsWithStartPosition>()
            tree.voice(query) { found ->
                if (found == null) {
                    session?.sendError(controller, SessionError(SessionError.ERROR_NOT_SUPPORTED, getString(R.string.car_not_found)))
                    future.set(none)
                } else {
                    future.set(MediaSession.MediaItemsWithStartPosition(listOf(found), 0, C.TIME_UNSET))
                }
            }
            return future
        }

        /** A media button with nothing loaded (D8): the book that played last. Its place comes from the server, not from here. */
        override fun onPlaybackResumption(mediaSession: MediaSession, controller: MediaSession.ControllerInfo, isForPlayback: Boolean): ListenableFuture<MediaSession.MediaItemsWithStartPosition> {
            val last = getSharedPreferences(CAR_PREFS, MODE_PRIVATE).getString(LAST_BOOK, null)
            if (last == null || connections.current() == null) return Futures.immediateFailedFuture(UnsupportedOperationException("nothing to resume"))
            val future = SettableFuture.create<MediaSession.MediaItemsWithStartPosition>()
            tree.item(last) { item ->
                if (item == null) future.setException(UnsupportedOperationException("not found")) else future.set(MediaSession.MediaItemsWithStartPosition(listOf(item), 0, C.TIME_UNSET))
            }
            return future
        }

        override fun onCustomCommand(
            session: MediaSession,
            controller: MediaSession.ControllerInfo,
            customCommand: SessionCommand,
            args: Bundle,
        ): ListenableFuture<SessionResult> {
            val who = allowed(session, controller)
            if (customCommand.customAction == SPEED_ACTION && who.speed) {
                cycleSpeed()
                return Futures.immediateFuture(SessionResult(SessionResult.RESULT_SUCCESS))
            }
            if (!who.ownCommands) {
                return Futures.immediateFuture(SessionResult(SessionResult.RESULT_ERROR_PERMISSION_DENIED))
            }
            when (customCommand.customAction) {
                START_ACTION -> engine.start(args.getString("book").orEmpty(), args.getLong("atMs", -1L).takeIf { it >= 0 })
                STOP_ACTION -> {
                    Log.i(TAG, "stop and clear done")
                    sleep.off()
                    engine.stopAndClear()
                    getSharedPreferences(CAR_PREFS, MODE_PRIVATE).edit().clear().apply() // the next person must not resume this one's book
                    CoverProvider.wipe(this@PlaybackService)
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

    /** The car's speed button (D7): next step, and the server remembers it for this book, best effort. */
    private fun cycleSpeed() {
        val next = speedCycle(exo.playbackParameters.speed.toDouble())
        player.setPlaybackSpeed(next.toFloat())
        val book = engine.currentBook
        val conn = connections.current()
        if (book.isEmpty() || conn == null) return
        io.execute {
            try {
                Api(conn.origin).putSpeed(conn.token, book, next)
            } catch (e: Exception) {
                Log.w(TAG, "speed not saved: ${e.javaClass.simpleName}")
            }
        }
    }

    companion object {
        internal const val TAG = "Tendshelf"
        const val START_ACTION = "app.tendshelf.START_BOOK"
        const val STOP_ACTION = "app.tendshelf.STOP_AND_CLEAR"
        const val ANSWER_ACTION = "app.tendshelf.ANSWER_ASK"
        const val VISIBLE_ACTION = "app.tendshelf.PAGE_VISIBLE"
        const val SPEED_ACTION = "app.tendshelf.SPEED_CYCLE"
        const val CAR_PREFS = "car"
        const val LAST_BOOK = "lastBook"
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
        val SPEED_COMMAND = SessionCommand(SPEED_ACTION, Bundle.EMPTY)
        val SLEEP_COMMAND = SessionCommand(SLEEP_ACTION, Bundle.EMPTY)
        val VISIBLE_COMMAND = SessionCommand(VISIBLE_ACTION, Bundle.EMPTY)
        val START_COMMAND = SessionCommand(START_ACTION, Bundle.EMPTY)
        val STOP_COMMAND = SessionCommand(STOP_ACTION, Bundle.EMPTY)
    }
}
