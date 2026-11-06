package app.tendshelf.android

import android.app.Activity
import android.content.Intent
import android.net.Uri
import android.webkit.CookieManager
import android.webkit.URLUtil
import android.widget.Toast
import app.tendshelf.shared.Origin
import java.net.HttpURLConnection
import java.net.URL
import java.util.concurrent.Executors

/**
 * Saves a file the page offers (a bookmark export, a highlights file) through the Storage Access Framework. It does not
 * use the system DownloadManager, so the session cookie stays inside the app. The request carries the WebView's Cookie
 * and never an Authorization header (the server refuses both together). Same-origin URLs only.
 */
class FileSaver(private val activity: Activity, private val origin: () -> Origin?) {
    private val io = Executors.newSingleThreadExecutor()
    private var pendingUrl: String? = null
    private var pendingType: String = "application/octet-stream"

    fun offer(url: String, contentDisposition: String?, mimeType: String?) {
        val o = origin() ?: return
        if (!o.matches(url)) return
        pendingUrl = url
        pendingType = mimeType?.takeIf { it.isNotBlank() } ?: "application/octet-stream"
        val name = URLUtil.guessFileName(url, contentDisposition, mimeType)
        val intent = Intent(Intent.ACTION_CREATE_DOCUMENT)
            .addCategory(Intent.CATEGORY_OPENABLE)
            .setType(pendingType)
            .putExtra(Intent.EXTRA_TITLE, name)
        activity.startActivityForResult(intent, REQUEST)
    }

    /** True when the result was ours. */
    fun onResult(requestCode: Int, resultCode: Int, data: Intent?): Boolean {
        if (requestCode != REQUEST) return false
        val url = pendingUrl
        val target = data?.data
        pendingUrl = null
        if (resultCode == Activity.RESULT_OK && url != null && target != null) io.execute { save(url, target) }
        return true
    }

    private fun save(url: String, target: Uri) {
        val ok = try {
            val c = URL(url).openConnection() as HttpURLConnection
            try {
                c.instanceFollowRedirects = false
                CookieManager.getInstance().getCookie(url)?.let { c.setRequestProperty("Cookie", it) }
                if (c.responseCode != 200) {
                    false
                } else {
                    activity.contentResolver.openOutputStream(target)?.use { out -> c.inputStream.use { it.copyTo(out) } } != null
                }
            } finally {
                c.disconnect()
            }
        } catch (_: Exception) {
            false
        }
        if (!ok) activity.runOnUiThread { Toast.makeText(activity, R.string.save_failed, Toast.LENGTH_SHORT).show() }
    }

    companion object {
        const val REQUEST = 4101
    }
}
