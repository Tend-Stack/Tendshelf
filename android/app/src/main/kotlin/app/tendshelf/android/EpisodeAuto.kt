package app.tendshelf.android

import android.Manifest
import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.util.Log
import androidx.media3.common.util.UnstableApi
import androidx.work.Constraints
import androidx.work.ExistingPeriodicWorkPolicy
import androidx.work.NetworkType
import androidx.work.PeriodicWorkRequest
import androidx.work.WorkManager
import androidx.work.Worker
import androidx.work.WorkerParameters
import app.tendshelf.shared.DEFAULT_KEEP_LATEST
import app.tendshelf.shared.EPISODE_BATCH
import app.tendshelf.shared.EPISODE_CHECK_MS
import app.tendshelf.shared.EpisodeCard
import app.tendshelf.shared.OfflineBook
import app.tendshelf.shared.ShowCard
import app.tendshelf.shared.autoEpisodeWanted
import app.tendshelf.shared.episodesToAnnounce
import app.tendshelf.shared.mayAutoDownloadEpisodes
import java.io.IOException
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean

/**
 * "Download new episodes on this phone" (A2, decision 0038). The choice is the phone's own, per show, kept here and never
 * sent to the server. A check asks only this server (`GET /api/v1/podcasts` and each chosen show's episodes), downloads the
 * newest N unplayed episodes that are not on the phone yet, on Wi-Fi only, and removes what the rules name (played, beyond
 * the latest N, over the quota shared with the next-book download). It never touches a download the person made by hand.
 *
 * It runs three ways: when the app comes to the front, once an hour at most while something plays, and as the periodic
 * `androidx.work` job ([EpisodeWorker]: unmetered network, battery not low, every 6 hours, scheduled only while some show
 * has the choice on).
 */
@UnstableApi
object EpisodeAuto {
    private const val TAG = "Tendshelf"
    private const val WORK = "tendshelf-episodes"
    private const val CHANNEL = "new_episodes"
    private const val NOTE_ID = 4247
    private const val DECLINED_MAX = 300
    private const val NOTIFIED_MAX = 600

    private lateinit var app: Context
    private val syncing = AtomicBoolean(false)
    private val lock = Any()
    private val pool = Executors.newSingleThreadExecutor()
    private val prefs get() = app.getSharedPreferences("episodeauto", Context.MODE_PRIVATE)

    fun init(context: Context) {
        if (!::app.isInitialized) app = context.applicationContext
    }

    private fun set(key: String): Set<String> =
        if (::app.isInitialized) synchronized(lock) { HashSet(prefs.getStringSet(key, emptySet()) ?: emptySet()) } else emptySet()

    // A new set every time: SharedPreferences ignores a set it was given back changed.
    private fun putSet(key: String, value: Set<String>) { prefs.edit().putStringSet(key, HashSet(value)).apply() }

    // ---- The shows that ask for it, and the episodes fetched for them ----

    fun enabled(): Set<String> = set("shows")
    fun anyOn(): Boolean = enabled().isNotEmpty()

    /** The ids of episodes fetched on a show's behalf: the only ones a rule may remove. */
    fun marks(): Set<String> = set("marks")

    fun mark(id: String) = synchronized(lock) { putSet("marks", marks() + id) }
    fun unmark(id: String) = synchronized(lock) { if (id in marks()) putSet("marks", marks() - id) }

    /** A mark without a row counts as nothing (the mark is written before the row). */
    fun pruneMarks(rows: List<OfflineBook>) {
        if (!::app.isInitialized) return
        synchronized(lock) {
            val now = marks()
            val kept = now.filter { m -> rows.any { it.bookId == m } }.toSet()
            if (kept != now) putSet("marks", kept)
        }
    }

    /** Episodes the person removed on purpose: never fetched again by a rule. Newest last, capped. */
    private fun declined(): List<String> = if (::app.isInitialized) (prefs.getString("declined", "") ?: "").split('\n').filter { it.isNotEmpty() } else emptyList()

    fun decline(id: String) {
        if (!::app.isInitialized) return
        synchronized(lock) { prefs.edit().putString("declined", (declined().filter { it != id } + id).takeLast(DECLINED_MAX).joinToString("\n")).apply() }
    }

    fun undecline(id: String) {
        if (!::app.isInitialized) return
        synchronized(lock) { prefs.edit().putString("declined", declined().filter { it != id }.joinToString("\n")).apply() }
    }

    /** The shows as the last check saw them, for the Downloads page when the server cannot be reached. */
    fun knownShows(): List<ShowCard> {
        if (!::app.isInitialized) return emptyList()
        return (prefs.getString("showsCache", "") ?: "").lines().mapNotNull { line ->
            val p = line.split('\t')
            if (p.size < 3 || p[0].isEmpty()) null else ShowCard(p[0], p[2], "", false, 0, p[1].toIntOrNull() ?: DEFAULT_KEEP_LATEST)
        }
    }

    private fun saveShows(shows: List<ShowCard>) {
        prefs.edit().putString("showsCache", shows.joinToString("\n") { "${it.id}\t${it.keepLatest}\t${it.title.replace('\t', ' ').replace('\n', ' ')}" }).apply()
    }

    /**
     * Reads the followed shows so the Downloads page can list them (also with no show switched on yet). True when the list
     * changed. Asks only this server; a missing network or podcasts being off changes nothing.
     */
    fun refreshShows(): Boolean {
        if (!::app.isInitialized) return false
        val conn = TokenStore(app).current() ?: return false
        return try {
            if (!ServerGate.ok(conn.origin)) return false
            val before = prefs.getString("showsCache", "")
            saveShows(Api(conn.origin).podcastShows(conn.token))
            prefs.getString("showsCache", "") != before
        } catch (e: Exception) {
            false
        }
    }

    /** The person switched a show's choice. On: the job is scheduled and a check runs now. Off: what is unfinished goes, what is whole stays. */
    fun setShow(id: String, on: Boolean) {
        if (!::app.isInitialized || id.isEmpty()) return
        synchronized(lock) { putSet("shows", if (on) enabled() + id else enabled() - id) }
        if (on) {
            schedule()
            pool.execute { guarded { sync() } }
        } else {
            pool.execute {
                guarded {
                    Offline.releaseShow(id)
                    if (!anyOn()) cancel()
                    Offline.autoSync()
                }
            }
        }
    }

    private fun guarded(work: () -> Unit) {
        try {
            work()
        } catch (e: Exception) {
            Log.w(TAG, "episodes: task failed: ${e.javaClass.simpleName}")
        }
    }

    // ---- The periodic job ----

    fun schedule() {
        if (!::app.isInitialized) return
        val request = PeriodicWorkRequest.Builder(EpisodeWorker::class.java, 6, TimeUnit.HOURS)
            .setConstraints(Constraints.Builder().setRequiredNetworkType(NetworkType.UNMETERED).setRequiresBatteryNotLow(true).build())
            .build()
        WorkManager.getInstance(app).enqueueUniquePeriodicWork(WORK, ExistingPeriodicWorkPolicy.KEEP, request)
    }

    fun cancel() {
        if (::app.isInitialized) WorkManager.getInstance(app).cancelUniqueWork(WORK)
    }

    /** Sign-out, another person, another server: the choices, marks and the job go with the downloads. */
    fun wipe() {
        if (!::app.isInitialized) return
        cancel()
        prefs.edit().clear().commit()
        app.getSystemService(NotificationManager::class.java).cancel(NOTE_ID)
    }

    // ---- A check ----

    /** Worker thread (the foreground path): a check when a show asks for one and the last one was over an hour ago. */
    fun syncIfDue() {
        if (!::app.isInitialized || !anyOn()) return
        schedule() // idempotent: the job exists whenever a show asks for it, even after an update that dropped it
        if (System.currentTimeMillis() - prefs.getLong("checkedAt", 0) >= EPISODE_CHECK_MS) sync()
    }

    /** The playing path: the same check, on this object's own thread so the download worker is never held up by the network. */
    fun syncSoon() {
        if (::app.isInitialized && anyOn()) pool.execute { guarded { syncIfDue() } }
    }

    /** Asks the server for the chosen shows' episodes, fetches what the rules want on Wi-Fi, removes what they name, and tells of new ones. */
    fun sync() {
        if (!::app.isInitialized || !anyOn() || !syncing.compareAndSet(false, true)) return
        try {
            val conn = TokenStore(app).current() ?: return
            if (!ServerGate.ok(conn.origin)) return
            val api = Api(conn.origin)
            val shows = try {
                api.podcastShows(conn.token)
            } catch (e: ApiException) {
                Log.i(TAG, "episodes: shows answered ${e.code}")
                return
            } catch (e: IOException) {
                Log.i(TAG, "episodes: shows not read: ${e.javaClass.simpleName}")
                return
            }
            saveShows(shows)
            val followed = shows.map { it.id }.toSet()
            // A show the person stopped following has nothing left to ask for.
            enabled().filter { it !in followed }.forEach { setShow(it, false) }
            val mine = shows.filter { it.id in enabled() }
            val keep = mine.associate { it.id to it.keepLatest }
            val wifi = Offline.unmetered()
            val primed = set("primed").toMutableSet()
            val notified = set("notified").toMutableSet()
            val fresh = ArrayList<EpisodeCard>()
            for (show in mine) {
                val cards = try {
                    api.showEpisodes(conn.token, show.id)
                } catch (e: Exception) {
                    Log.i(TAG, "episodes: a show was not read: ${e.javaClass.simpleName}")
                    continue
                }
                cards.filter { it.played }.forEach { Offline.markPlayed(it.id) }
                if (show.id in primed) {
                    fresh += episodesToAnnounce(cards, notified)
                } else {
                    // The first look at a show only learns what is there; a backlog is never announced.
                    primed += show.id
                }
                notified += cards.map { it.id }
                Offline.tidyEpisodes(keep)
                if (mayAutoDownloadEpisodes(true, wifi)) {
                    val have = Offline.store.all().map { it.bookId }.toSet()
                    for (w in autoEpisodeWanted(cards, have, declined().toSet(), show.keepLatest).take(EPISODE_BATCH)) {
                        Offline.addEpisode(w.id, conn, auto = true)
                    }
                }
            }
            Offline.tidyEpisodes(keep)
            Offline.autoSync()
            putSet("primed", primed)
            putSet("notified", if (notified.size > NOTIFIED_MAX) fresh.map { it.id }.toSet() + notified.take(NOTIFIED_MAX / 2) else notified)
            prefs.edit().putLong("checkedAt", System.currentTimeMillis()).apply()
            if (fresh.isNotEmpty()) announce(fresh)
        } finally {
            syncing.set(false)
        }
    }

    // ---- The new-episode notification ----

    private fun announce(fresh: List<EpisodeCard>) {
        if (app.checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED) return
        val nm = app.getSystemService(NotificationManager::class.java)
        nm.createNotificationChannel(NotificationChannel(CHANNEL, app.getString(R.string.episodes_channel), NotificationManager.IMPORTANCE_DEFAULT))
        val one = fresh.singleOrNull()
        val open = PendingIntent.getActivity(
            app, 3,
            Intent(app, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP or Intent.FLAG_ACTIVITY_NEW_TASK),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )
        val b = Notification.Builder(app, CHANNEL)
            .setSmallIcon(R.drawable.ic_stat_tendshelf)
            .setAutoCancel(true)
            .setContentIntent(open)
        if (one != null) {
            b.setContentTitle(one.title.ifBlank { app.getString(R.string.episodes_new_one) }).setContentText(one.show)
        } else {
            b.setContentTitle(app.resources.getQuantityString(R.plurals.episodes_new_count, fresh.size, fresh.size))
                .setContentText(fresh.map { it.show }.distinct().take(3).joinToString(", "))
        }
        nm.notify(NOTE_ID, b.build())
    }
}

/** The periodic job: unmetered network and battery not low are the system's conditions; the sync and the downloads are [EpisodeAuto]'s and [Offline]'s. */
@UnstableApi
class EpisodeWorker(context: Context, params: WorkerParameters) : Worker(context, params) {
    override fun doWork(): Result {
        Offline.init(applicationContext)
        EpisodeAuto.init(applicationContext)
        if (!EpisodeAuto.anyOn()) {
            EpisodeAuto.cancel()
            return Result.success()
        }
        return try {
            EpisodeAuto.sync()
            Offline.runHeadless(MAX_RUN_MS)
            Result.success()
        } catch (e: Exception) {
            Log.w("Tendshelf", "episodes: job failed: ${e.javaClass.simpleName}")
            Result.retry()
        }
    }

    private companion object {
        /** The system gives a job about ten minutes; what is left goes on by byte range at the next run. */
        const val MAX_RUN_MS = 8 * 60_000L
    }
}
