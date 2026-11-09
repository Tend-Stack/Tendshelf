package app.tendshelf.android

import android.content.BroadcastReceiver
import android.content.ComponentName
import android.content.Context
import android.content.Intent
import android.os.Handler
import android.os.Looper
import android.util.Log
import androidx.media3.common.util.UnstableApi
import androidx.media3.session.MediaController
import androidx.media3.session.SessionToken
import app.tendshelf.shared.WidgetTap
import app.tendshelf.shared.widgetPlayTap
import com.google.common.util.concurrent.MoreExecutors

/**
 * The widget's buttons (L9). Not exported: only the widget's own PendingIntents reach it. A tap is the person's own
 * start, so the system lets the playback service come to the front. It does what the media notification does, through
 * the same session: a controller connects to [PlaybackService], plays, pauses or seeks by the person's skip amounts,
 * and with nothing loaded asks the service to start the last book (the engine takes the place from the server). A start
 * the system refuses ends in the service's own "Tap to keep listening" notification, never here.
 */
@UnstableApi
class WidgetActions : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        val action = intent.action ?: return
        if (action != TendWidget.ACTION_TOGGLE && action != TendWidget.ACTION_BACK && action != TendWidget.ACTION_FORWARD) return
        val app = context.applicationContext
        if (!TokenStore(app).hasToken()) {
            TendWidget.refresh(app) // signed out: the buttons have nothing to do
            return
        }
        val pending = goAsync()
        val main = Handler(Looper.getMainLooper())
        val done = { pending.finish() }
        try {
            val future = MediaController.Builder(app, SessionToken(app, ComponentName(app, PlaybackService::class.java))).buildAsync()
            future.addListener({
                try {
                    val controller = future.get()
                    run(app, action, controller)
                    // A command goes out at once, but give it a moment before letting go of the connection.
                    main.postDelayed({
                        MediaController.releaseFuture(future)
                        done()
                    }, RELEASE_MS)
                } catch (e: Exception) {
                    Log.w(PlaybackService.TAG, "widget could not reach the player: ${e.javaClass.simpleName}")
                    done()
                }
            }, MoreExecutors.directExecutor())
        } catch (e: Exception) {
            Log.w(PlaybackService.TAG, "widget could not start the player: ${e.javaClass.simpleName}")
            done()
        }
    }

    private fun run(app: Context, action: String, controller: MediaController) {
        val remote = ControllerRemote(controller)
        val snap = remote.snapshot()
        val loaded = snap.bookId.isNotEmpty() && controller.mediaItemCount > 0
        when (action) {
            // seekBack and seekForward are the notification's own buttons: BookPlayer applies the person's skip amounts.
            TendWidget.ACTION_BACK -> if (loaded) controller.seekBack()
            TendWidget.ACTION_FORWARD -> if (loaded) controller.seekForward()
            else -> when (widgetPlayTap(snap.playing, loaded)) {
                WidgetTap.PAUSE -> remote.pause()
                WidgetTap.PLAY -> remote.play()
                WidgetTap.RESUME_LAST -> WidgetState.bookId(app).takeIf { it.isNotEmpty() }?.let { remote.start(it, null) }
            }
        }
    }

    private companion object {
        const val RELEASE_MS = 1500L
    }
}
