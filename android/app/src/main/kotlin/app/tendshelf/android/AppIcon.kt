package app.tendshelf.android

import android.content.Context
import android.graphics.Bitmap
import android.graphics.Canvas
import java.io.ByteArrayOutputStream

/**
 * The app's own icon as a picture, for a radio station that has none (R3): the lock screen and the car show the icon rather
 * than an empty square. Drawn once from the launcher resource and kept; nothing is fetched.
 */
object AppIcon {
    private const val SIZE = 512

    @Volatile
    private var cached: ByteArray? = null

    fun png(context: Context): ByteArray? {
        cached?.let { return it }
        return try {
            val drawable = context.applicationContext.getDrawable(R.mipmap.ic_launcher) ?: return null
            val bitmap = Bitmap.createBitmap(SIZE, SIZE, Bitmap.Config.ARGB_8888)
            drawable.setBounds(0, 0, SIZE, SIZE)
            drawable.draw(Canvas(bitmap))
            val out = ByteArrayOutputStream()
            bitmap.compress(Bitmap.CompressFormat.PNG, 100, out)
            bitmap.recycle()
            out.toByteArray().also { cached = it }
        } catch (e: Exception) {
            null
        }
    }
}
