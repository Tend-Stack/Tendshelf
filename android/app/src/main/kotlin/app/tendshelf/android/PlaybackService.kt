package app.tendshelf.android

import android.app.PendingIntent
import android.content.Intent
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.util.Log
import androidx.media3.common.AudioAttributes
import androidx.media3.common.C
import androidx.media3.common.MediaItem
import androidx.media3.common.MediaMetadata
import androidx.media3.common.Player
import androidx.media3.datasource.DefaultHttpDataSource
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.source.DefaultMediaSourceFactory
import androidx.media3.session.MediaLibraryService
import androidx.media3.session.MediaSession
import androidx.media3.session.SessionCommand
import androidx.media3.session.SessionResult
import app.tendshelf.shared.Position
import com.google.common.collect.ImmutableList
import com.google.common.util.concurrent.Futures
import com.google.common.util.concurrent.ListenableFuture
import java.util.concurrent.Executors

/**
 * One ExoPlayer in one session, in a foreground service of type mediaPlayback. The activity only asks it
 * to start a book (custom command) and can die without stopping the sound. A0 plays the book's files in
 * order, so next and previous change file; chapters as next and previous are A3.
 */
class PlaybackService : MediaLibraryService() {
    private var session: MediaLibrarySession? = null
    private lateinit var player: ExoPlayer
    private val http = DefaultHttpDataSource.Factory()
    private val main = Handler(Looper.getMainLooper())
    private val io = Executors.newSingleThreadExecutor()

    override fun onCreate() {
        super.onCreate()
        player = ExoPlayer.Builder(this)
            .setMediaSourceFactory(DefaultMediaSourceFactory(http))
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
        player.addListener(object : Player.Listener {
            override fun onPlaybackStateChanged(state: Int) {
                Log.i(TAG, "state=$state index=${player.currentMediaItemIndex} pos=${player.currentPosition}")
            }

            override fun onPlayerError(error: androidx.media3.common.PlaybackException) {
                Log.e(TAG, "player error", error)
            }
        })
        val open = PendingIntent.getActivity(
            this, 0,
            Intent(this, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP or Intent.FLAG_ACTIVITY_NEW_TASK),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )
        session = MediaLibrarySession.Builder(this, player, SessionCallback())
            .setSessionActivity(open) // the notification tap opens the page again
            .build()
    }

    override fun onGetSession(controllerInfo: MediaSession.ControllerInfo): MediaLibrarySession? = session

    /** Swiping the app away must not stop a book that is playing. */
    override fun onTaskRemoved(rootIntent: Intent?) {
        if (!player.playWhenReady || player.mediaItemCount == 0) stopSelf()
    }

    override fun onDestroy() {
        session?.release()
        session = null
        player.release()
        io.shutdownNow()
        super.onDestroy()
    }

    private inner class SessionCallback : MediaLibrarySession.Callback {
        override fun onConnect(session: MediaSession, controller: MediaSession.ControllerInfo): MediaSession.ConnectionResult {
            val commands = MediaSession.ConnectionResult.DEFAULT_SESSION_AND_LIBRARY_COMMANDS.buildUpon()
                .add(START_COMMAND)
                .build()
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
            if (customCommand.customAction != START_ACTION) {
                return Futures.immediateFuture(SessionResult(SessionResult.RESULT_ERROR_NOT_SUPPORTED))
            }
            val server = args.getString("server") ?: BuildConfig.DEFAULT_SERVER
            val user = args.getString("user").orEmpty()
            val pass = args.getString("pass").orEmpty()
            val book = args.getString("book").orEmpty()
            io.execute { startBook(server, user, pass, book) }
            return Futures.immediateFuture(SessionResult(SessionResult.RESULT_SUCCESS))
        }
    }

    /** Signs in, reads the manifest and queues one item per file, each streamed with the bearer header. */
    private fun startBook(server: String, user: String, pass: String, bookId: String) {
        try {
            val api = Api(server)
            val token = api.signIn(user, pass)
            val manifest = api.manifest(token, bookId)
            http.setDefaultRequestProperties(mapOf("Authorization" to "Bearer $token"))
            val items = manifest.files.map { f ->
                val chapter = Position.chapterAt(manifest.chapters, f.offsetMs)
                MediaItem.Builder()
                    .setMediaId(f.id)
                    .setUri(api.paths.streamUrl(f.id))
                    .setMimeType(f.mime)
                    .setMediaMetadata(
                        MediaMetadata.Builder()
                            .setTitle(chapter?.title ?: manifest.title)
                            .setAlbumTitle(manifest.title)
                            .setIsPlayable(true)
                            .build(),
                    )
                    .build()
            }
            main.post {
                player.setMediaItems(ImmutableList.copyOf(items))
                player.prepare()
                player.play()
                Log.i(TAG, "started ${manifest.title}: ${items.size} files")
            }
        } catch (e: Exception) {
            Log.e(TAG, "could not start the book", e)
        }
    }

    companion object {
        private const val TAG = "TendshelfA0"
        const val START_ACTION = "app.tendshelf.START_BOOK"
        val START_COMMAND = SessionCommand(START_ACTION, Bundle.EMPTY)
    }
}
