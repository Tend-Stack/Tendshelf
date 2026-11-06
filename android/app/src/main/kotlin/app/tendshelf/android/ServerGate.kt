package app.tendshelf.android

import android.content.Context
import android.net.ConnectivityManager
import android.net.Network
import android.os.SystemClock
import app.tendshelf.shared.Origin
import app.tendshelf.shared.sameServer

/**
 * Whether the address still answers as the Tendshelf this phone was connected to. A plain-http or LAN address can be
 * answered by anything on another network, so before the bearer token leaves, and before a 401 or a 404 from the address
 * is allowed to sign out, delete a place or delete a download, `/healthz` must say `app: tendshelf` with the install id
 * pinned at connect time. A yes is trusted for a minute and is forgotten the moment the default network changes.
 * The probe sends no credential. Blocking: call it from a worker thread.
 */
object ServerGate {
    private const val FRESH_MS = 60_000L

    private val lock = Any()
    private var tokens: TokenStore? = null
    private var verifiedFor = ""
    private var verifiedAt = 0L
    private var registered = false

    fun init(context: Context) = synchronized(lock) {
        if (registered) return@synchronized
        registered = true
        val app = context.applicationContext
        tokens = TokenStore(app)
        try {
            app.getSystemService(ConnectivityManager::class.java).registerDefaultNetworkCallback(object : ConnectivityManager.NetworkCallback() {
                override fun onAvailable(network: Network) = forget()
                override fun onLost(network: Network) = forget()
            })
        } catch (_: Exception) {
        }
    }

    fun forget() = synchronized(lock) { verifiedAt = 0L }

    /** True when [originText] is the connected server. Without [init] (a unit run) nothing is trusted. */
    fun ok(originText: String): Boolean {
        val store = synchronized(lock) { tokens } ?: return false
        val origin = Origin.parse(originText) ?: return false
        if (store.origin != origin) return false // only the one server the person chose
        synchronized(lock) {
            if (verifiedFor == origin.text && SystemClock.elapsedRealtime() - verifiedAt in 0 until FRESH_MS) return true
        }
        val answer = AuthApi().identity(origin)
        val pinned = store.serverId
        val same = sameServer(pinned, answer)
        synchronized(lock) {
            if (same) {
                // A server that gained an id since the connect is pinned the first time it is seen.
                if (pinned.isNullOrEmpty() && !answer?.id.isNullOrEmpty()) store.serverId = answer?.id
                verifiedFor = origin.text
                verifiedAt = SystemClock.elapsedRealtime()
            } else {
                verifiedAt = 0L
            }
        }
        return same
    }

    /** Wraps a data source factory so every open of a stream or download first asks [ok] for the address it is about to read. */
    @androidx.media3.common.util.UnstableApi
    fun gated(upstream: androidx.media3.datasource.DataSource.Factory): androidx.media3.datasource.DataSource.Factory =
        androidx.media3.datasource.ResolvingDataSource.Factory(upstream) { spec ->
            val u = spec.uri
            if (!ok("${u.scheme}://${u.authority}")) throw java.io.IOException("the address does not answer as the connected server")
            spec
        }
}
