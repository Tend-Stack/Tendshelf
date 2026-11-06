package app.tendshelf.android

import android.content.Context
import android.net.ConnectivityManager
import app.tendshelf.shared.Origin
import java.net.HttpURLConnection
import java.net.URL

/**
 * Whether the server answers right now (plan A9, D2). The WebView's interceptor must give one answer per request, so it asks this
 * once: no default network is "no"; otherwise `GET /healthz` with 1.5 s to connect and to read and no credentials, remembered for
 * 10 s. Airplane mode with a VPN still up (the worker's 3 s hang in 0019) is therefore quick. A page error calls [invalidate].
 */
object OfflineNet {
    private const val HEALTH_MS = 1_500
    private const val KEEP_MS = 10_000L

    private lateinit var connectivity: ConnectivityManager
    private val lock = Any()
    private var answer = false
    private var at = 0L
    private var forOrigin = ""

    fun init(context: Context) {
        if (!::connectivity.isInitialized) connectivity = context.applicationContext.getSystemService(ConnectivityManager::class.java)
    }

    /** True when [origin] answers `/healthz`. Called on a WebView worker thread and only for addresses the store could answer. */
    fun reachable(origin: Origin, now: Long = System.currentTimeMillis()): Boolean {
        if (!::connectivity.isInitialized) return true
        synchronized(lock) {
            if (forOrigin == origin.text && now - at in 0 until KEEP_MS) return answer
        }
        val result = connectivity.activeNetwork != null && probe(origin)
        synchronized(lock) {
            answer = result
            at = now
            forOrigin = origin.text
        }
        return result
    }

    fun invalidate() = synchronized(lock) { at = 0L }

    private fun probe(origin: Origin): Boolean = try {
        val conn = URL(origin.healthUrl()).openConnection() as HttpURLConnection
        try {
            conn.instanceFollowRedirects = false
            conn.connectTimeout = HEALTH_MS
            conn.readTimeout = HEALTH_MS
            conn.responseCode in 200..299
        } finally {
            conn.disconnect()
        }
    } catch (_: Exception) {
        false
    }
}
