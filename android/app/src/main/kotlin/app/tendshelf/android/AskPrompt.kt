package app.tendshelf.android

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.util.Log
import app.tendshelf.shared.Ask
import app.tendshelf.shared.clockText

/**
 * "You were at {time} on {device}. Continue there?" when no page can show it (A5, D5): a private notification
 * with two actions. Nothing moves until a button is tapped. While it is open the book's saves are held, not
 * sent. Main thread only. The page's toast is the other half, and arrives with the bridge.
 */
class AskPrompt(
    private val context: Context,
    private val writer: ProgressWriter,
    private val seekTo: (Long) -> Unit,
) {
    private val nm = context.getSystemService(NotificationManager::class.java)
    private var open: Ask? = null

    /** Random per question: the service is exported, so a button press from another app must not answer it. */
    private var nonce = 0L

    val isOpen: Boolean get() = open != null

    init {
        nm.createNotificationChannel(NotificationChannel(CHANNEL, context.getString(R.string.place_channel), NotificationManager.IMPORTANCE_DEFAULT).apply {
            setSound(null, null)
            lockscreenVisibility = Notification.VISIBILITY_PRIVATE
        })
        nm.cancel(NOTIFICATION_ID) // one from a process that is gone
    }

    fun show(ask: Ask) {
        if (open != null) return
        open = ask
        nonce = java.security.SecureRandom().nextLong()
        if (BuildConfig.DEBUG) Log.i("Tendshelf", "ask nonce=$nonce")
        writer.heldBook = ask.bookId
        val text = context.getString(R.string.continue_ask, clockText(ask.positionMs), ask.deviceLabel)
        val n = Notification.Builder(context, CHANNEL)
            .setSmallIcon(android.R.drawable.ic_media_play)
            .setContentTitle(text)
            .setVisibility(Notification.VISIBILITY_PRIVATE)
            .setAutoCancel(false)
            .addAction(action(ACTION_THERE, context.getString(R.string.continue_there)))
            .addAction(action(ACTION_HERE, context.getString(R.string.stay_here)))
            .build()
        nm.notify(NOTIFICATION_ID, n)
    }

    private fun action(name: String, label: String): Notification.Action {
        val intent = Intent(context, PlaybackService::class.java).setAction(name).putExtra(EXTRA_NONCE, nonce)
        val pi = PendingIntent.getService(context, name.hashCode(), intent, PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)
        return Notification.Action.Builder(null, label, pi).build()
    }

    /** True when [action] was one of ours. */
    fun handle(intent: Intent?): Boolean {
        val action = intent?.action
        if (action != ACTION_HERE && action != ACTION_THERE) return false
        // A press that does not carry this question's nonce is ignored (an old notification, another app).
        if (open != null && intent.getLongExtra(EXTRA_NONCE, 0L) == nonce) answer(keepHere = action == ACTION_HERE)
        return true
    }

    /** "Stay here" ([keepHere]): this place wins on the server. "Continue there": go to the other place. */
    fun answer(keepHere: Boolean) {
        val ask = open ?: return
        open = null
        nm.cancel(NOTIFICATION_ID)
        if (keepHere) {
            writer.release(ask.bookId, force = true)
        } else {
            writer.dropHeld(ask.bookId)
            seekTo(ask.positionMs)
        }
    }

    /** The book changed or the question is moot: the held place goes out plain, newest wins. */
    fun dismissPlain() {
        val ask = open ?: return
        open = null
        nm.cancel(NOTIFICATION_ID)
        writer.release(ask.bookId, force = false)
    }

    companion object {
        const val EXTRA_NONCE = "ts.ask.nonce"
        const val CHANNEL = "place"
        const val NOTIFICATION_ID = 4243
        const val ACTION_HERE = "app.tendshelf.ASK_STAY_HERE"
        const val ACTION_THERE = "app.tendshelf.ASK_CONTINUE_THERE"
    }
}
