package app.tendshelf.android

import android.app.PendingIntent
import android.appwidget.AppWidgetManager
import android.appwidget.AppWidgetProvider
import android.content.ComponentName
import android.content.Context
import android.content.Intent
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.graphics.Canvas
import android.graphics.Paint
import android.graphics.PorterDuff
import android.graphics.PorterDuffXfermode
import android.graphics.RectF
import android.os.Bundle
import android.widget.RemoteViews
import app.tendshelf.shared.WidgetInput
import app.tendshelf.shared.WidgetKind
import app.tendshelf.shared.WidgetLook
import app.tendshelf.shared.widgetLook

/**
 * What the home-screen widget remembers (L9): the last book's id, title, chapter, whether it plays, and the person's
 * skip amounts. Words and numbers only; no token and no address. It is written by [PlaybackService] when the player
 * changes, so the widget can draw with the app closed, and it is cleared on sign-out.
 */
@androidx.media3.common.util.UnstableApi
object WidgetState {
    private const val PREFS = "widget"

    private fun prefs(context: Context) = context.applicationContext.getSharedPreferences(PREFS, Context.MODE_PRIVATE)

    fun save(context: Context, bookId: String, title: String, chapter: String, playing: Boolean, skipBackS: Int, skipForwardS: Int, show: String = "", live: Boolean = false) {
        prefs(context).edit()
            .putString("bookId", bookId)
            .putString("title", title)
            .putString("chapter", chapter)
            .putString("show", show)
            .putBoolean("live", live)
            .putBoolean("playing", playing)
            .putInt("back", skipBackS)
            .putInt("forward", skipForwardS)
            .apply()
    }

    /** The service ended: nothing plays now, whatever the last event said. A cleared state stays cleared. */
    fun stopped(context: Context) {
        if (bookId(context).isNotEmpty()) prefs(context).edit().putBoolean("playing", false).apply()
    }

    fun clear(context: Context) {
        prefs(context).edit().clear().apply()
    }

    fun bookId(context: Context): String = prefs(context).getString("bookId", "").orEmpty()

    fun input(context: Context): WidgetInput {
        val p = prefs(context)
        return WidgetInput(
            signedIn = TokenStore(context).hasToken(),
            bookId = p.getString("bookId", "").orEmpty(),
            title = p.getString("title", "").orEmpty(),
            chapter = p.getString("chapter", "").orEmpty(),
            show = p.getString("show", "").orEmpty(),
            live = p.getBoolean("live", false),
            playing = p.getBoolean("playing", false),
            skipBackS = p.getInt("back", 0),
            skipForwardS = p.getInt("forward", 0),
        )
    }
}

/**
 * The home-screen widget: cover, title, chapter, play or pause, back and forward. A plain [AppWidgetProvider]; the
 * view is [RemoteViews]. It never reaches the network: the buttons go to [WidgetActions], which talks to the playback
 * service, and the cover comes from the phone's own cover store. It is redrawn only when the player changes, on
 * sign-out and when it is resized; there is no timer.
 */
@androidx.media3.common.util.UnstableApi
class TendWidget : AppWidgetProvider() {
    override fun onUpdate(context: Context, manager: AppWidgetManager, ids: IntArray) {
        for (id in ids) manager.updateAppWidget(id, render(context, manager.getAppWidgetOptions(id)))
    }

    override fun onAppWidgetOptionsChanged(context: Context, manager: AppWidgetManager, id: Int, newOptions: Bundle) {
        manager.updateAppWidget(id, render(context, newOptions))
    }

    companion object {
        const val ACTION_TOGGLE = "app.tendshelf.android.WIDGET_TOGGLE"
        const val ACTION_BACK = "app.tendshelf.android.WIDGET_BACK"
        const val ACTION_FORWARD = "app.tendshelf.android.WIDGET_FORWARD"
        const val EXTRA_OPEN_BOOK = "ts.openBook"
        private const val COVER_PX = 192

        /** Redraw every placed widget from the saved state. Cheap when none is placed. */
        fun refresh(context: Context) {
            val app = context.applicationContext
            val manager = AppWidgetManager.getInstance(app)
            val ids = manager.getAppWidgetIds(ComponentName(app, TendWidget::class.java))
            for (id in ids) manager.updateAppWidget(id, render(app, manager.getAppWidgetOptions(id)))
        }

        /** Sign-out or another server: no title and no cover of this person may stay on the home screen. */
        fun signedOut(context: Context) {
            WidgetState.clear(context)
            CoverProvider.wipe(context)
            refresh(context)
        }

        private fun render(context: Context, options: Bundle): RemoteViews {
            // The launcher reports the size the widget has now: min width and max height in portrait.
            val widthDp = options.getInt(AppWidgetManager.OPTION_APPWIDGET_MIN_WIDTH, 250)
            val heightDp = options.getInt(AppWidgetManager.OPTION_APPWIDGET_MAX_HEIGHT, 110)
            val input = WidgetState.input(context)
            val look = widgetLook(input, widthDp, heightDp)
            val views = RemoteViews(context.packageName, R.layout.widget)
            val openApp = openApp(context, if (look.kind == WidgetKind.BOOK) input.bookId else "")
            views.setOnClickPendingIntent(R.id.widget_root, openApp)
            if (look.kind != WidgetKind.BOOK) {
                views.setViewVisibility(R.id.widget_book, android.view.View.GONE)
                views.setViewVisibility(R.id.widget_line, android.view.View.VISIBLE)
                views.setTextViewText(R.id.widget_line, context.getString(if (look.kind == WidgetKind.SIGNED_OUT) R.string.widget_signed_out else R.string.widget_no_book))
                return views
            }
            views.setViewVisibility(R.id.widget_line, android.view.View.GONE)
            views.setViewVisibility(R.id.widget_book, android.view.View.VISIBLE)
            bind(context, views, look, input.bookId, openApp)
            return views
        }

        private fun bind(context: Context, views: RemoteViews, look: WidgetLook, bookId: String, openApp: PendingIntent) {
            fun shown(id: Int, on: Boolean) = views.setViewVisibility(id, if (on) android.view.View.VISIBLE else android.view.View.GONE)
            views.setTextViewText(R.id.widget_title, look.title)
            views.setTextViewText(R.id.widget_chapter, look.chapter)
            shown(R.id.widget_chapter, look.showChapter)
            shown(R.id.widget_cover, look.showCover)
            // The cells go with the buttons, so Play is centred in what is left.
            shown(R.id.widget_back_cell, look.showSkip)
            shown(R.id.widget_forward_cell, look.showSkip)
            if (look.showCover) {
                // Without a picture the empty square is a drawable with a size: the cover takes its width from its shape.
                val cover = CoverProvider.cached(context, bookId)?.let { roundedCover(it) }
                if (cover != null) views.setImageViewBitmap(R.id.widget_cover, cover) else views.setImageViewResource(R.id.widget_cover, R.drawable.widget_cover_empty)
            }
            // The cover and the words open the book; the buttons act.
            views.setContentDescription(R.id.widget_cover, context.getString(R.string.widget_open_book, look.title))
            views.setOnClickPendingIntent(R.id.widget_cover, openApp)
            views.setOnClickPendingIntent(R.id.widget_title, openApp)
            views.setOnClickPendingIntent(R.id.widget_chapter, openApp)
            val res = context.resources
            views.setImageViewResource(R.id.widget_play, if (look.playing) R.drawable.ic_widget_pause else R.drawable.ic_widget_play)
            views.setContentDescription(R.id.widget_play, context.getString(if (look.playing) R.string.widget_pause else R.string.widget_play))
            views.setContentDescription(R.id.widget_back, res.getQuantityString(R.plurals.widget_back, look.skipBackS, look.skipBackS))
            views.setContentDescription(R.id.widget_forward, res.getQuantityString(R.plurals.widget_forward, look.skipForwardS, look.skipForwardS))
            views.setOnClickPendingIntent(R.id.widget_play, action(context, ACTION_TOGGLE, 1))
            views.setOnClickPendingIntent(R.id.widget_back, action(context, ACTION_BACK, 2))
            views.setOnClickPendingIntent(R.id.widget_forward, action(context, ACTION_FORWARD, 3))
        }

        /** The activity, explicit and immutable. It carries a book id (not a secret) and nothing else. */
        private fun openApp(context: Context, bookId: String): PendingIntent {
            val intent = Intent(context, MainActivity::class.java)
                .addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP or Intent.FLAG_ACTIVITY_NEW_TASK)
            if (bookId.isNotEmpty()) intent.putExtra(EXTRA_OPEN_BOOK, bookId)
            return PendingIntent.getActivity(context, 10, intent, PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)
        }

        /** A broadcast to this app's own receiver, which is not exported. */
        private fun action(context: Context, action: String, code: Int): PendingIntent =
            PendingIntent.getBroadcast(
                context, code,
                Intent(context, WidgetActions::class.java).setAction(action),
                PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
            )

        /** A centred square at [COVER_PX], with the 12 dp control corner; small enough for the launcher's binder limit. */
        private fun roundedCover(bytes: ByteArray): Bitmap? {
            val bounds = BitmapFactory.Options().apply { inJustDecodeBounds = true }
            BitmapFactory.decodeByteArray(bytes, 0, bytes.size, bounds)
            if (bounds.outWidth <= 0 || bounds.outHeight <= 0) return null
            var sample = 1
            while (bounds.outWidth / (sample * 2) >= COVER_PX && bounds.outHeight / (sample * 2) >= COVER_PX) sample *= 2
            val src = BitmapFactory.decodeByteArray(bytes, 0, bytes.size, BitmapFactory.Options().apply { inSampleSize = sample }) ?: return null
            val side = minOf(src.width, src.height)
            val out = Bitmap.createBitmap(COVER_PX, COVER_PX, Bitmap.Config.ARGB_8888)
            val canvas = Canvas(out)
            val paint = Paint(Paint.ANTI_ALIAS_FLAG)
            // 12 dp of a 72 dp picture.
            val radius = COVER_PX * 12f / 72f
            canvas.drawRoundRect(RectF(0f, 0f, COVER_PX.toFloat(), COVER_PX.toFloat()), radius, radius, paint)
            paint.xfermode = PorterDuffXfermode(PorterDuff.Mode.SRC_IN)
            val from = android.graphics.Rect((src.width - side) / 2, (src.height - side) / 2, (src.width + side) / 2, (src.height + side) / 2)
            canvas.drawBitmap(src, from, RectF(0f, 0f, COVER_PX.toFloat(), COVER_PX.toFloat()), paint)
            src.recycle()
            return out
        }
    }
}
