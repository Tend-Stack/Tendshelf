package app.tendshelf.android

import android.content.Context
import android.os.Handler
import android.os.Looper
import android.os.StatFs
import android.util.Log
import android.widget.Toast
import androidx.media3.common.util.UnstableApi
import androidx.media3.database.StandaloneDatabaseProvider
import androidx.media3.datasource.DataSource
import androidx.media3.datasource.DefaultHttpDataSource
import androidx.media3.datasource.cache.CacheDataSource
import androidx.media3.datasource.cache.NoOpCacheEvictor
import androidx.media3.datasource.cache.SimpleCache
import androidx.media3.exoplayer.offline.Download
import androidx.media3.exoplayer.offline.DownloadManager
import androidx.media3.exoplayer.offline.DownloadRequest
import androidx.media3.exoplayer.offline.DownloadService
import androidx.media3.exoplayer.scheduler.Requirements
import app.tendshelf.shared.AUTO_HOLD
import app.tendshelf.shared.AutoPlan
import app.tendshelf.shared.KIND_EPISODE
import app.tendshelf.shared.PodcastJson
import app.tendshelf.shared.autoEpisodeEvictions
import app.tendshelf.shared.autoEpisodePlan
import app.tendshelf.shared.episodeFileComplete
import app.tendshelf.shared.episodePlan
import app.tendshelf.shared.episodeSize
import app.tendshelf.shared.isEpisodeId
import androidx.media3.datasource.cache.ContentMetadata
import androidx.media3.datasource.cache.ContentMetadataMutations
import app.tendshelf.shared.DownloadPercent
import app.tendshelf.shared.decodeKeptSettings
import app.tendshelf.shared.encodeKeptSettings
import app.tendshelf.shared.FileProgress
import app.tendshelf.shared.OfflineBook
import app.tendshelf.shared.OfflineState
import app.tendshelf.shared.PlayerSettings
import app.tendshelf.shared.Plan
import app.tendshelf.shared.SPARE_BYTES
import app.tendshelf.shared.TextManifest
import app.tendshelf.shared.Verdict
import app.tendshelf.shared.autoBytes
import app.tendshelf.shared.autoDownloadPlan
import app.tendshelf.shared.autoUnfinished
import app.tendshelf.shared.encodeAutoNext
import app.tendshelf.shared.offlineNextFor
import app.tendshelf.shared.queueDeleteDone
import app.tendshelf.shared.checkVerdict
import app.tendshelf.shared.downloadPercent
import app.tendshelf.shared.downloadPlan
import app.tendshelf.shared.fileComplete
import java.io.File
import java.io.IOException
import java.util.concurrent.Executor
import java.util.concurrent.Executors

/**
 * The books kept on the phone (plan A7). One process-wide owner of the `SimpleCache` under `noBackupFilesDir/offline`
 * (a cache directory may have one owner), the Media3 `DownloadManager` that fills it, and the rows in `outbox.db`.
 *
 * The token reaches the network as a header, made when each connection opens; it is never in a request, a key, a row or
 * a log line. The manager starts paused and runs only after the visible activity resumes it (D2: no scheduler, so the
 * system is never asked to start a data-sync service from the background).
 */
@UnstableApi
object Offline {
    private const val TAG = "Tendshelf"
    private const val DAY_MS = 24L * 60 * 60 * 1000

    private val lock = Any()
    private lateinit var app: Context
    private lateinit var tokens: TokenStore
    lateinit var store: SqliteOfflineStore
        private set
    private val main = Handler(Looper.getMainLooper())
    private val ioPool = Executors.newSingleThreadExecutor()
    private var db: StandaloneDatabaseProvider? = null
    private var cache: SimpleCache? = null
    private var manager: DownloadManager? = null

    /** Runs [work] on the one worker thread that does the slow parts. */
    fun io(work: () -> Unit) { safeIo(work) }

    /**
     * Every task of the worker goes through here. A full disk or a locked database throws on this thread, and an uncaught
     * exception on a pool thread ends the process, and the sound with it. The task is lost, its state stays as it was, and
     * the next foreground or daily check tries again.
     */
    private fun safeIo(work: () -> Unit) {
        ioPool.execute {
            try {
                work()
            } catch (e: Exception) {
                Log.w(TAG, "offline: task failed: ${e.javaClass.simpleName}")
            }
        }
    }

    /** Called on a worker thread with the book id whenever its state or progress moved. */
    @Volatile
    var onChange: ((String) -> Unit)? = null

    private val prefs get() = app.getSharedPreferences("offline", Context.MODE_PRIVATE)
    private val root get() = File(app.noBackupFilesDir, "offline")
    private fun coverFile(id: String) = File(File(root, "covers"), id)
    private fun chaptersFile(id: String) = File(File(root, "chapters"), id)

    /** The episode that is playing now; the automatic rules never remove it. */
    @Volatile
    var playingNow: String = ""

    /** True while the activity is on the screen; the headless job leaves the manager as it found it. */
    @Volatile
    private var inFront = false

    fun init(context: Context) {
        ServerGate.init(context)
        synchronized(lock) {
            if (::app.isInitialized) return
            app = context.applicationContext
            tokens = TokenStore(app)
            store = SqliteOfflineStore(app)
            TextStore.init(app)
            OfflineNet.init(app)
            EpisodeAuto.init(app)
        }
        // A wipe the last process did not finish is completed before anything else can use the cache.
        if (prefs.getBoolean("wipePending", false)) safeIo { finishWipe() }
    }

    // ---- Cache and manager ----

    fun cache(): SimpleCache = synchronized(lock) {
        cache ?: SimpleCache(File(root, "media"), NoOpCacheEvictor(), databaseProvider()).also { cache = it }
    }

    private fun databaseProvider(): StandaloneDatabaseProvider =
        db ?: StandaloneDatabaseProvider(app).also { db = it }

    /** Opens a connection to the one server with the current token; the token is read when the connection is made. */
    private val upstream = ServerGate.gated(DataSource.Factory {
        val headers = tokens.current()?.let { mapOf("Authorization" to "Bearer ${it.token}") } ?: emptyMap()
        DefaultHttpDataSource.Factory().setDefaultRequestProperties(headers).createDataSource()
    })

    /** What the player reads a downloaded book through: the cache only reads, a book being streamed is never written. */
    fun readFactory(http: DataSource.Factory): DataSource.Factory = CacheDataSource.Factory()
        .setCache(cache())
        .setUpstreamDataSourceFactory(http)
        .setCacheWriteDataSinkFactory(null)
        .setFlags(CacheDataSource.FLAG_IGNORE_CACHE_ON_ERROR)

    fun manager(): DownloadManager = synchronized(lock) {
        manager ?: DownloadManager(app, databaseProvider(), cache(), upstream, Executor { it.run() }).also { m ->
            m.setMaxParallelDownloads(1) // one at a time keeps a home server calm (D4)
            m.setMinRetryCount(120) // rides out a rendition that answers 503 while it is made
            m.setRequirements(Requirements(Requirements.NETWORK))
            m.pauseDownloads() // only the visible activity starts them again (D2)
            m.addListener(object : DownloadManager.Listener {
                override fun onDownloadChanged(downloadManager: DownloadManager, download: Download, finalException: Exception?) =
                    changed(download.request.id.substringBefore('|'))

                override fun onDownloadsPausedChanged(downloadManager: DownloadManager, downloadsPaused: Boolean) =
                    store.all().forEach { changed(it.bookId) }
            })
            manager = m
        }
    }

    private fun changed(book: String) {
        safeIo {
            try {
                recompute(book)
            } catch (e: Exception) {
                Log.w(TAG, "offline: state not updated: ${e.javaClass.simpleName}")
            }
            onChange?.invoke(book)
        }
        ensurePolling()
    }

    // ---- State ----

    private fun downloadsOf(m: DownloadManager, book: String): List<Download> {
        val out = ArrayList<Download>()
        m.downloadIndex.getDownloads().use { c -> while (c.moveToNext()) c.download.let { if (it.request.id.startsWith("$book|")) out.add(it) } }
        return out
    }

    private fun expectedFiles(book: String): Int =
        if (isEpisodeId(book)) 1 else store.manifest(book)?.let { runCatching { Api(tokens.origin?.text ?: "http://x").parseManifest(it).files.count { f -> f.playable } }.getOrNull() } ?: 0

    /** Folds the files' Media3 states into the book's one state. `READY` only when every file is whole (invariant 1). */
    private fun recompute(book: String) {
        val row = store.get(book) ?: return
        if (row.state == OfflineState.FAILED && downloadsOf(manager(), book).isEmpty()) return
        val m = manager()
        val ds = downloadsOf(m, book)
        if (ds.isEmpty()) return
        val next = when {
            ds.any { it.state == Download.STATE_FAILED } -> OfflineState.FAILED
            ds.all { it.state == Download.STATE_COMPLETED } && ds.size >= expectedFiles(book) ->
                if (ds.all { whole(it) }) (if (row.state == OfflineState.STALE) OfflineState.STALE else OfflineState.READY) else OfflineState.FAILED
            m.getDownloadsPaused() -> OfflineState.PAUSED
            ds.any { it.state == Download.STATE_DOWNLOADING } -> OfflineState.DOWNLOADING
            else -> OfflineState.QUEUED
        }
        if (next != row.state) store.setState(book, next)
        // The feed's announced size is often wrong: a finished episode shows (and counts against the quota) what arrived.
        if (next == OfflineState.READY && row.kind == KIND_EPISODE) cache().getCachedBytes(book, 0, Long.MAX_VALUE).let { if (it > 0) store.setSize(book, it) }
        Log.i(TAG, "offline: ${next.name.lowercase()}")
    }

    private fun whole(d: Download): Boolean {
        val key = d.request.customCacheKey ?: return false
        if (isEpisodeId(key)) return wholeEpisode(d, key)
        val planned = d.request.data.let { if (it.isEmpty()) 0L else String(it).toLongOrNull() ?: 0L }
        val len = d.contentLength
        return fileComplete(planned, key.endsWith(":c"), len, if (len > 0) cache().getCachedBytes(key, 0, len) else 0)
    }

    /**
     * An episode's file. The publisher may send no length, so the length is also read from what the cache learned; a file
     * that is whole with no known length gets its length written down, so a later offline play never asks the network for
     * the end of it.
     */
    private fun wholeEpisode(d: Download, key: String): Boolean {
        val len = if (d.contentLength > 0) d.contentLength else ContentMetadata.getContentLength(cache().getContentMetadata(key))
        val cached = cache().getCachedBytes(key, 0, if (len > 0) len else Long.MAX_VALUE)
        val ok = episodeFileComplete(len, cached, d.bytesDownloaded)
        if (ok && len <= 0) {
            try {
                cache().applyContentMetadataMutations(key, ContentMetadataMutations().apply { ContentMetadataMutations.setContentLength(this, episodeSize(len, cached)) })
            } catch (e: Exception) {
                Log.w(TAG, "offline: length not kept: ${e.javaClass.simpleName}")
            }
        }
        return ok
    }

    /** How a book stands for the page, the list and the notification: [state] word, [pct], and whether [pct] is known. */
    data class DlView(val state: String, val pct: Int, val known: Boolean = true)

    /** The last percent shown per book, so the number never goes back; and the poll's latest answer. */
    private val floors = java.util.concurrent.ConcurrentHashMap<String, Int>()
    private val live = java.util.concurrent.ConcurrentHashMap<String, DownloadPercent>()

    fun view(book: String): DlView {
        val row = store.get(book) ?: return DlView("none", 0)
        if (row.state == OfflineState.READY || row.state == OfflineState.STALE) {
            floors.remove(book)
            live.remove(book)
            return DlView(row.state.name.lowercase(), 100)
        }
        val p = live[book] ?: progress(book, emptyList())
        return DlView(row.state.name.lowercase(), p.pct, p.known)
    }

    /**
     * Whole-book percent. Media3 writes a file's bytes to its index only when the state changes, so a running file is
     * read from [running] (the manager's current downloads, which carry the bytes so far) and the rest from the index.
     */
    private fun progress(book: String, running: List<Download>): DownloadPercent = try {
        val byId = LinkedHashMap<String, Download>()
        downloadsOf(manager(), book).forEach { byId[it.request.id] = it }
        running.filter { it.request.id.startsWith("$book|") }.forEach { byId[it.request.id] = it }
        val files = byId.values.map { d ->
            val planned = d.request.data.let { if (it.isEmpty()) 0L else String(it).toLongOrNull() ?: 0L }
            FileProgress(planned, d.contentLength, d.bytesDownloaded, d.state == Download.STATE_COMPLETED)
        }
        // Files the manager has not been told about yet still weigh in with the manifest's size.
        val missing = expectedFiles(book) - files.size
        val all = if (missing > 0) files + List(missing) { FileProgress(0, 0, 0, false) } else files
        val out = downloadPercent(all, floors[book] ?: 0)
        floors[book] = out.pct
        out
    } catch (_: Exception) {
        DownloadPercent(floors[book] ?: 0, false)
    }

    // ---- Progress poll: once a second, only while a file is downloading ----

    private var polling = false

    /** Starts the poll if a file is downloading; the poll stops itself when none is, so nothing runs at rest. */
    private fun ensurePolling() {
        main.post {
            if (polling || !::app.isInitialized) return@post
            if (manager().currentDownloads.none { it.state == Download.STATE_DOWNLOADING }) return@post
            polling = true
            pollTick()
        }
    }

    private var autoTicks = 0

    private fun pollTick() {
        val running = manager().currentDownloads.toList()
        // An automatic download holds the moment the Wi-Fi is gone (D10): up to about a second of data after it drops.
        val autoRunning = running.filter {
            isAuto(it.request.id.substringBefore('|')) && it.state != Download.STATE_COMPLETED && it.stopReason == Download.STOP_REASON_NONE
        }
        if (autoRunning.isNotEmpty() && !unmetered()) autoRunning.forEach { manager().setStopReason(it.request.id, AUTO_HOLD) }
        // Every 30 s: an unfinished automatic book gives way when the phone is nearly full (invariant 3).
        if (++autoTicks % 30 == 0 && allMarks().isNotEmpty() && freeBytes() < SPARE_BYTES) {
            safeIo { autoUnfinished(store.all(), allMarks()).forEach { remove(it.bookId, byPerson = false) } }
        }
        if (running.none { it.state == Download.STATE_DOWNLOADING }) {
            polling = false
            return
        }
        safeIo {
            for (book in running.mapNotNull { it.request.id.substringBefore('|', "").takeIf(String::isNotEmpty) }.distinct()) {
                live[book] = progress(book, running)
                onChange?.invoke(book)
            }
        }
        main.postDelayed({ pollTick() }, 1000)
    }

    /** The one percent the notification shows: the book that is downloading, by the same bytes as the page. */
    fun notificationProgress(downloads: List<Download>): Pair<String, DownloadPercent>? {
        val first = downloads.firstOrNull { it.state == Download.STATE_DOWNLOADING } ?: return null
        val book = first.request.id.substringBefore('|')
        val title = store.get(book)?.title ?: return null
        return title to progress(book, downloads)
    }

    /** A book that plays from the phone: whole (or old, but whole) files and a stored answer. */
    fun playable(book: String): OfflineBook? {
        if (!::app.isInitialized) return null
        return store.get(book)?.takeIf { (it.state == OfflineState.READY || it.state == OfflineState.STALE) && hasAudio(book) }
    }

    /** A text-only book (A9) is kept as a row with no audio file: it can be read, never played. */
    fun hasAudio(book: String): Boolean = expectedFiles(book) > 0

    /** The reader page of a downloaded book whose text is whole, for the Downloads page's Read button; null when it has none. */
    fun readable(book: String): Boolean = ::app.isInitialized && store.text(book)?.first == "ready"

    fun coverBytes(book: String): ByteArray? = coverFile(book).takeIf { it.isFile }?.readBytes()

    /** Bytes the phone can still give downloads. */
    fun freeBytes(): Long = StatFs(app.noBackupFilesDir.apply { mkdirs() }.path).availableBytes

    // ---- Actions (call from a worker thread) ----

    /** Fetches the manifest, checks the right and the room, keeps the answer and the cover, and queues every file. */
    fun add(book: String) {
        val conn = tokens.current() ?: return
        if (isEpisodeId(book)) {
            addEpisode(book, conn, auto = false)
            return
        }
        val existing = store.get(book)
        if (existing != null && existing.state != OfflineState.FAILED && existing.state != OfflineState.STALE) {
            // Pressing Download on an automatic book makes it the person's: no hold, and it goes on at once (invariant 7).
            if (isAuto(book)) {
                unmark(book)
                if (existing.state != OfflineState.READY) {
                    val ids = downloadsOf(manager(), book).map { it.request.id }
                    main.post {
                        ids.forEach { manager().setStopReason(it, Download.STOP_REASON_NONE) }
                        manager().resumeDownloads()
                        DownloadService.sendResumeDownloads(app, TsDownloadService::class.java, true)
                    }
                }
            }
            io { retryText(book) }
            changed(book)
            return
        }
        try {
            val api = Api(conn.origin)
            val text = api.fetchManifestJson(conn.token, book)
            val manifest = api.parseManifest(text)
            if (manifest.id != book) return // a merged book is asked for by its live id
            val free = StatFs(app.noBackupFilesDir.apply { mkdirs() }.path).availableBytes
            val plan = when (val p = downloadPlan(manifest, api.paths, free)) {
                is Plan.Ok -> p
                Plan.NotAllowed -> return refuse(R.string.download_not_allowed)
                Plan.NoRoom -> return refuse(R.string.download_no_room)
                Plan.NothingToPlay -> return addTextOnly(book, conn, api, manifest, text)
            }
            enqueue(book, conn, api, manifest, text, plan, auto = false)
        } catch (e: ApiException) {
            Log.w(TAG, "download not started: server answered ${e.code}")
            refuse(if (e.code == 403) R.string.download_not_allowed else R.string.download_failed)
        } catch (e: IOException) {
            Log.w(TAG, "download not started: ${e.javaClass.simpleName}")
            refuse(R.string.download_failed)
        } catch (e: Exception) {
            Log.w(TAG, "download not started: ${e.javaClass.simpleName}")
        }
    }

    /** Keeps the cover, the row and the answer, queues every file, and fetches the text. [auto]: held until Wi-Fi (0035). */
    private fun enqueue(book: String, conn: Connection, api: Api, manifest: app.tendshelf.shared.BookManifest, text: String, plan: Plan.Ok, auto: Boolean) {
        val cover = api.cover(conn.token, book)
        if (cover != null) coverFile(book).apply { parentFile?.mkdirs() }.writeBytes(cover)
        val place = manifest.progress
        floors.remove(book)
        live.remove(book)
        // The mark is written before the row; a stale mark without a row counts as nothing (D11).
        if (auto) {
            mark(book)
            prefs.edit().remove("autoNoRoom").apply()
        } else {
            unmark(book)
        }
        store.put(
            OfflineBook(
                book, tokens.userId.orEmpty(), manifest.title, manifest.author, manifest.durationMs, plan.total,
                OfflineState.QUEUED, place?.positionMs ?: 0, place?.clientUpdatedAt ?: 0,
            ),
            text,
        )
        startFiles(book, plan, auto)
        changed(book)
        // One Download (0019, point 1): the text follows the audio when the person may read and download it.
        fetchText(book)
    }

    /**
     * Queues the files of [plan]. An automatic download is held from the moment it exists (invariant 2); a person's own goes
     * first and puts the automatic ones on hold (invariant 7). An episode's request carries no planned size: the feed's
     * is often wrong, so the file is judged by its real length.
     */
    private fun startFiles(book: String, plan: Plan.Ok, auto: Boolean) {
        val requests = plan.files.map {
            // The request holds an address with no credential, a cache key and the size the manifest listed.
            DownloadRequest.Builder("$book|${it.fileId}", android.net.Uri.parse(it.url))
                .setCustomCacheKey(it.cacheKey)
                .setData((if (isEpisodeId(book)) 0L else it.size).toString().toByteArray())
                .build()
        }
        if (auto) {
            // Held from the moment it exists; only releaseHeld lets it move a byte (invariant 2).
            main.post {
                requests.forEach { manager().addDownload(it, AUTO_HOLD) }
                releaseHeld(requests.map { it.id })
            }
        } else {
            // A person's own download goes first: the automatic ones wait their turn (invariant 7).
            val held = autoRequestIds()
            main.post {
                held.forEach { manager().setStopReason(it, AUTO_HOLD) }
                manager().resumeDownloads()
                for (r in requests) DownloadService.sendAddDownload(app, TsDownloadService::class.java, r, true)
            }
        }
    }

    /** A book with text and no audio: one row, ready once its text is whole. */
    private fun addTextOnly(book: String, conn: Connection, api: Api, manifest: app.tendshelf.shared.BookManifest, manifestJson: String) {
        val origin = tokens.origin ?: return
        val got = TextStore.manifest(origin, book)
        // No session cookie yet: the row is kept and the text goes on at the next page load (D1); anything else is a refusal.
        if (got !is TextStore.Manifest.Ok && !(got is TextStore.Manifest.Not && got.result == TextResult.SIGNIN)) return refuse(R.string.download_nothing)
        val tm = (got as? TextStore.Manifest.Ok)?.m
        val cover = api.cover(conn.token, book)
        if (cover != null) coverFile(book).apply { parentFile?.mkdirs() }.writeBytes(cover)
        store.put(
            OfflineBook(book, tokens.userId.orEmpty(), tm?.title?.ifEmpty { null } ?: manifest.title, tm?.author?.ifEmpty { null } ?: manifest.author, 0, tm?.bytes ?: 0, OfflineState.READY, 0, 0),
            manifestJson,
        )
        if (tm == null) {
            store.setText(book, "signin")
            changed(book)
            return
        }
        if (fetchText(book, tm) != TextResult.READY) {
            store.remove(book)
            floors.remove(book)
            live.remove(book)
            coverFile(book).delete()
            TextStore.remove(book)
            refuse(R.string.download_failed)
        }
        changed(book)
    }

    /** Fetches a book's text and records how it went. [known] saves asking for the manifest twice. */
    private fun fetchText(book: String, known: TextManifest? = null): TextResult {
        val origin = tokens.origin ?: return TextResult.FAILED
        store.setText(book, "fetching")
        onChange?.invoke(book)
        val result = try {
            val tm = known ?: when (val r = TextStore.manifest(origin, book)) {
                is TextStore.Manifest.Ok -> r.m
                is TextStore.Manifest.Code -> null
                is TextStore.Manifest.Not -> {
                    store.setText(book, r.result.name.lowercase())
                    return r.result
                }
            }
            if (tm == null) TextResult.NONE else {
                val free = StatFs(app.noBackupFilesDir.apply { mkdirs() }.path).availableBytes
                TextStore.fetch(origin, tm, free).also { if (it == TextResult.READY) store.setText(book, "ready", tm.revision, tm.editionId) }
            }
        } catch (e: Exception) {
            Log.w(TAG, "text: ${e.javaClass.simpleName}")
            TextResult.FAILED
        }
        if (result != TextResult.READY) store.setText(book, result.name.lowercase(), "", "")
        onChange?.invoke(book)
        return result
    }

    /** A book whose text stopped at sign-in or failed is tried again (at the next start, and for sign-in at the next page). */
    private fun retryText(book: String) {
        if (isAuto(book) && !unmetered()) return
        val now = store.text(book)?.first ?: return
        if (now == "signin" || now == "failed" || now == "fetching") fetchText(book)
    }

    /** A server page loaded: the session exists, so a text that waited for it goes on. Cheap when nothing waits. */
    fun onPageLoaded() {
        if (!::app.isInitialized) return
        safeIo { store.withText("signin").forEach { retryText(it) } }
    }

    private fun refuse(words: Int) {
        main.post { Toast.makeText(app, words, Toast.LENGTH_LONG).show() }
    }

    /**
     * Drops the book's bytes, row and cover. A person's removal of an episode is remembered, so the automatic download does
     * not fetch it again; an automatic removal ([byPerson] false) is not.
     */
    fun remove(book: String, byPerson: Boolean = true) {
        if (byPerson && isEpisodeId(book)) EpisodeAuto.decline(book)
        safeIo {
            try {
                val ids = downloadsOf(manager(), book).map { it.request.id }
                main.post { ids.forEach { manager().removeDownload(it) } }
            } catch (e: Exception) {
                Log.w(TAG, "offline: remove failed: ${e.javaClass.simpleName}")
            }
            unmark(book)
            EpisodeAuto.unmark(book)
            prefs.edit().remove("autoNoRoom").apply()
            store.remove(book)
            coverFile(book).delete()
            chaptersFile(book).delete()
            if (!isEpisodeId(book)) TextStore.remove(book)
            floors.remove(book)
            live.remove(book)
            onChange?.invoke(book)
        }
    }

    /** Called when the app comes to the front: the 6-hour timer starts again, and unfinished downloads go on (D2, D3). */
    fun onForeground() {
        if (!::app.isInitialized) return
        inFront = true
        prefs.edit().putBoolean("downloadsLimited", false).apply()
        safeIo {
            // A hold is applied before a resume, so the order below never lets a metered byte through.
            autoSync()
            // A row with no download behind it (the process died before the requests were stored) cannot finish: Try again.
            for (r in store.all()) {
                val active = r.state == OfflineState.QUEUED || r.state == OfflineState.DOWNLOADING || r.state == OfflineState.PAUSED
                if (active && downloadsOf(manager(), r.bookId).isEmpty()) store.setState(r.bookId, OfflineState.FAILED)
            }
            if (store.all().any { it.state == OfflineState.QUEUED || it.state == OfflineState.DOWNLOADING || it.state == OfflineState.PAUSED }) {
                main.post {
                    manager().resumeDownloads()
                    DownloadService.sendResumeDownloads(app, TsDownloadService::class.java, true)
                }
            }
            for (state in listOf("signin", "failed", "fetching")) store.withText(state).forEach { retryText(it) }
            prefs.getString("autoWant", "")?.takeIf { it.isNotEmpty() }?.let { autoFetch(it) }
            dailyCheck()
            EpisodeAuto.syncSoon()
        }
    }

    /** The system's time limit for a data-sync service was reached: no progress is lost, the next run goes on by range. */
    fun onTimeLimit() {
        prefs.edit().putBoolean("downloadsLimited", true).apply()
        manager().pauseDownloads()
    }

    /** At most once a day for each book (decision 0019, point 10): same files, new words; changed files, old; gone, removed. */
    private fun dailyCheck() {
        val conn = tokens.current() ?: return
        // One probe for the whole loop: an address that does not answer as the connected server (another network, a
        // plain 404 server) ends the check here, with nothing deleted and no token sent, and does not hold the worker.
        if (store.all().none { it.state == OfflineState.READY || it.state == OfflineState.STALE }) return
        if (!ServerGate.ok(conn.origin)) return
        val api = Api(conn.origin)
        val now = System.currentTimeMillis()
        for (row in store.all()) {
            if (row.state != OfflineState.READY && row.state != OfflineState.STALE) continue
            if (row.kind == KIND_EPISODE) continue // an episode has no book answer to compare; the show sync looks after it
            if (now - store.checkedAt(row.bookId) < DAY_MS) continue
            try {
                val text = api.fetchManifestJson(conn.token, row.bookId)
                val fresh = api.parseManifest(text)
                if (fresh.id != row.bookId) continue // a redirect: nothing changes
                val old = store.manifest(row.bookId)?.let(api::parseManifest)
                when (checkVerdict(200, old, fresh)) {
                    Verdict.SAME -> store.touchChecked(row.bookId, now)
                    Verdict.UPDATE -> store.setManifest(row.bookId, text, now)
                    Verdict.STALE -> {
                        store.setState(row.bookId, OfflineState.STALE)
                        store.touchChecked(row.bookId, now)
                    }
                    Verdict.REMOVE, Verdict.NOTHING -> {}
                }
                checkText(row.bookId)
            } catch (e: ApiException) {
                if (checkVerdict(e.code, null, null) == Verdict.REMOVE) remove(row.bookId)
            } catch (_: Exception) {
            }
            onChange?.invoke(row.bookId)
        }
        TextStore.prune(emptyList())
    }

    /** The text's own daily check (0019, point 10): other revision, fetch again with the shell last; 404, drop it; the rest changes nothing. */
    private fun checkText(book: String) {
        val origin = tokens.origin ?: return
        val had = store.text(book) ?: return
        if (had.first != "ready") return
        when (val r = TextStore.manifest(origin, book)) {
            is TextStore.Manifest.Ok -> if (r.m.revision != had.second) fetchText(book, r.m)
            is TextStore.Manifest.Code -> {
                TextStore.remove(book)
                store.setText(book, "none", "", "")
                // A text-only row has nothing left to offer.
                if (!hasAudio(book)) remove(book)
            }
            is TextStore.Manifest.Not -> {}
        }
    }

    // ---- Settings kept for an offline start ----

    fun saveSettings(s: PlayerSettings) {
        if (!::app.isInitialized) return
        prefs.edit().putString("settingsJson", encodeKeptSettings(s)).apply()
    }

    /** The last settings the server gave; the built-in ones before any. */
    fun loadSettings(): PlayerSettings {
        return decodeKeptSettings(prefs.getString("settingsJson", null))
    }

    /** The Download the next book switch changed in Settings: kept at once; off removes the unfinished automatic ones. */
    fun setAutoDownload(on: Boolean) {
        if (!::app.isInitialized) return
        saveSettings(loadSettings().copy(autoDownload = on))
        if (!on) safeIo { autoSync() }
    }

    // ---- The next book, on its own (decision 0035) ----

    private val marksLock = Any()

    private fun marks(): Set<String> = synchronized(marksLock) { HashSet(prefs.getStringSet("autoBooks", emptySet()) ?: emptySet()) }

    // A new set every time: SharedPreferences ignores a set it was given back changed.
    private fun setMarks(s: Set<String>) { prefs.edit().putStringSet("autoBooks", HashSet(s)).apply() }

    private fun mark(book: String) = synchronized(marksLock) { setMarks(marks() + book) }

    private fun unmark(book: String) = synchronized(marksLock) {
        val now = marks()
        if (book in now) setMarks(now - book)
    }

    /** The marks of both automatic paths: the next book or episode (decision 0035) and the shows' "Download new episodes". */
    fun allMarks(): Set<String> = marks() + EpisodeAuto.marks()

    /** True for a book or episode that was downloaded on its own and has not been made the person's own. */
    fun isAuto(book: String): Boolean = ::app.isInitialized && (book in marks() || book in EpisodeAuto.marks())

    /** True while the active network is unmetered (Wi-Fi). Everything automatic asks this before it moves a byte. */
    fun unmetered(): Boolean = Requirements(Requirements.NETWORK_UNMETERED).checkRequirements(app)

    /** True when the last automatic book was not fetched for lack of room; the Downloads page says so. */
    fun autoNoRoomNoted(): Boolean = ::app.isInitialized && prefs.getBoolean("autoNoRoom", false)

    /** The player is near the end of [book] but the network is metered: ask again at the next allowed moment. */
    fun autoWant(book: String) { prefs.edit().putString("autoWant", book).apply() }

    fun autoForget() { if (::app.isInitialized) prefs.edit().remove("autoWant").apply() }

    private fun autoRequestIds(): List<String> =
        autoUnfinished(store.all(), allMarks()).flatMap { r -> downloadsOf(manager(), r.bookId).map { it.request.id } }

    /**
     * Lets held automatic downloads go, on the main thread. The foreground start comes first: if the system refuses it
     * (not an allowed moment), nothing changes and the books stay held until the next one (invariant 6).
     */
    private fun releaseHeld(ids: List<String>) {
        if (ids.isEmpty() || !unmetered()) return
        try {
            DownloadService.sendResumeDownloads(app, TsDownloadService::class.java, true)
        } catch (e: IllegalStateException) {
            Log.i(TAG, "next book: start refused, held")
            return
        }
        ids.forEach { manager().setStopReason(it, Download.STOP_REASON_NONE) }
    }

    /** Worker thread. Applies the switch and the network to what is unfinished: off removes it, metered holds it, Wi-Fi releases it. */
    fun autoSync() {
        if (!::app.isInitialized) return
        tidyQueue()
        val rows = store.all()
        val kept = marks().filter { m -> rows.any { it.bookId == m } }.toSet()
        if (kept != marks()) synchronized(marksLock) { setMarks(kept) }
        if (!loadSettings().autoDownload) {
            autoUnfinished(rows, kept).forEach { remove(it.bookId, byPerson = false) }
            prefs.edit().remove("autoWant").remove("autoNoRoom").remove("autoNext").apply()
        }
        EpisodeAuto.pruneMarks(rows)
        // The shows' episodes follow the same network rule as the next book: held on a metered network, released on Wi-Fi.
        val ids = autoRequestIds()
        if (ids.isEmpty()) return
        val wifi = unmetered()
        main.post { if (wifi) releaseHeld(ids) else ids.forEach { manager().setStopReason(it, AUTO_HOLD) } }
    }

    /**
     * The book to start when the server could not be asked at the end of [ended]: the remembered next book, only when it
     * is whole on the phone. Cleared when used, so it is never offered twice. Any thread.
     */
    fun offlineNext(ended: String): String? {
        if (!::app.isInitialized) return null
        val next = offlineNextFor(prefs.getString("autoNext", null), ended) { playable(it) != null }
        if (next != null) prefs.edit().remove("autoNext").apply()
        return next
    }

    /** [book] started without the server's say; the server's queue still holds it until [tidyQueue] succeeds. */
    fun autoDrop(book: String) { if (::app.isInitialized) prefs.edit().putString("autoDrop", book).apply() }

    /** Worker thread. Takes a book that was started from the phone off the server's queue, once a connection works. */
    fun tidyQueue() {
        if (!::app.isInitialized) return
        val book = prefs.getString("autoDrop", null)?.takeIf { it.isNotEmpty() } ?: return
        val conn = tokens.current() ?: return
        try {
            if (!ServerGate.ok(conn.origin)) return
            val status = Api(conn.origin).queueRemove(conn.token, book)
            if (queueDeleteDone(status)) {
                // Only if it still names this book: a newer one must not be lost.
                if (prefs.getString("autoDrop", null) == book) prefs.edit().remove("autoDrop").apply()
                Log.i(TAG, "next book: queue tidied ($status)")
            } else {
                Log.i(TAG, "next book: queue tidy answered $status, later")
            }
        } catch (e: Exception) {
            Log.i(TAG, "next book: queue tidy not sent: ${e.javaClass.simpleName}")
        }
    }

    /** Worker thread. Asks the server for the book after [ended] and, if the rules allow, queues it held for Wi-Fi. */
    fun autoFetch(ended: String) {
        if (!::app.isInitialized) return
        val conn = tokens.current() ?: return
        if (!loadSettings().autoDownload || !unmetered()) return
        if (autoUnfinished(store.all(), marks()).isNotEmpty()) return
        try {
            if (!ServerGate.ok(conn.origin)) return
            val api = Api(conn.origin)
            val next = try {
                api.queuePeek(conn.token, ended)
            } catch (e: ApiException) {
                Log.i(TAG, "next book: peek answered ${e.code}")
                return
            } catch (e: IOException) {
                Log.i(TAG, "next book: peek failed ${e.javaClass.simpleName}")
                return
            }
            // Remembered before anything else, so a book the person already has counts too (the plane case).
            prefs.edit().remove("autoWant").also { if (next == null) it.remove("autoNext") else it.putString("autoNext", encodeAutoNext(ended, next)) }.apply()
            if (next == null || store.get(next) != null) return
            if (isEpisodeId(next)) {
                addEpisode(next, conn, auto = true, nextItem = true)
                return
            }
            val text = api.fetchManifestJson(conn.token, next)
            val manifest = api.parseManifest(text)
            if (manifest.id != next) return
            when (val p = autoDownloadPlan(manifest, api.paths, freeBytes(), autoBytes(store.all(), marks()))) {
                is AutoPlan.Go -> enqueue(next, conn, api, manifest, text, p.plan, auto = true)
                AutoPlan.NoRoom -> prefs.edit().putBoolean("autoNoRoom", true).apply()
                AutoPlan.Skip -> Log.i(TAG, "next book: skipped")
            }
        } catch (e: Exception) {
            Log.i(TAG, "next book: not fetched: ${e.javaClass.simpleName}")
        }
    }

    // ---- Podcast episodes (A2, decision 0038) ----

    /** The chapters file kept with a downloaded episode, or null. */
    fun chaptersText(id: String): String? = chaptersFile(id).takeIf { it.isFile }?.readText()

    /** The activity left the screen. */
    fun onBackground() { inFront = false }

    /**
     * Keeps one episode on the phone: the playback document (so it starts with no network), the show's picture, the
     * chapters, and the file through the same download manager as a book. [auto]: it came from a show's "Download new
     * episodes" (or [nextItem], from the next-book switch) and is held for Wi-Fi; the person's own is never removed by a rule.
     * Returns false when nothing was queued.
     */
    fun addEpisode(id: String, conn: Connection, auto: Boolean, nextItem: Boolean = false): Boolean {
        val existing = store.get(id)
        if (existing != null && existing.state != OfflineState.FAILED && existing.state != OfflineState.STALE) {
            // Pressing Download on an automatic episode makes it the person's: no hold, and it goes on at once.
            if (!auto && isAuto(id)) {
                unmark(id)
                EpisodeAuto.unmark(id)
                if (existing.state != OfflineState.READY) {
                    val ids = downloadsOf(manager(), id).map { it.request.id }
                    main.post {
                        ids.forEach { manager().setStopReason(it, Download.STOP_REASON_NONE) }
                        manager().resumeDownloads()
                        DownloadService.sendResumeDownloads(app, TsDownloadService::class.java, true)
                    }
                }
            }
            changed(id)
            return false
        }
        try {
            val api = Api(conn.origin)
            val json = api.fetchEpisodeJson(conn.token, id)
            val doc = PodcastJson.episode(json)
            if (doc.id != id) return false
            val free = freeBytes()
            val plan = if (auto) {
                when (val p = autoEpisodePlan(doc, api.paths, free, autoBytes(store.all(), allMarks()))) {
                    is AutoPlan.Go -> p.plan
                    AutoPlan.NoRoom -> {
                        prefs.edit().putBoolean("autoNoRoom", true).apply()
                        return false
                    }
                    AutoPlan.Skip -> return false
                }
            } else {
                when (val p = episodePlan(doc, api.paths, free)) {
                    is Plan.Ok -> p
                    Plan.NoRoom -> return false.also { refuse(R.string.download_no_room) }
                    Plan.NotAllowed, Plan.NothingToPlay -> return false.also { refuse(R.string.download_nothing) }
                }
            }
            api.podcastArt(conn.token, doc.artUrl)?.let { coverFile(id).apply { parentFile?.mkdirs() }.writeBytes(it) }
            if (doc.chaptersUrl.isNotEmpty()) api.chaptersJson(conn.token, doc.chaptersUrl)?.let { chaptersFile(id).apply { parentFile?.mkdirs() }.writeText(it) }
            floors.remove(id)
            live.remove(id)
            // The mark is written before the row; a stale mark without a row counts as nothing (D11).
            when {
                !auto -> { unmark(id); EpisodeAuto.unmark(id); EpisodeAuto.undecline(id) }
                nextItem -> { mark(id); prefs.edit().remove("autoNoRoom").apply() }
                else -> EpisodeAuto.mark(id)
            }
            store.put(
                OfflineBook(
                    id, tokens.userId.orEmpty(), doc.title, doc.show, doc.durationMs, plan.total, OfflineState.QUEUED,
                    doc.progress?.positionMs ?: 0, doc.progress?.clientUpdatedAt ?: 0,
                    KIND_EPISODE, doc.showId, doc.publishedAt, doc.progress?.finished == true,
                ),
                json,
            )
            startFiles(id, plan, auto)
            changed(id)
            return true
        } catch (e: ApiException) {
            Log.w(TAG, "episode not started: server answered ${e.code}")
            if (!auto) refuse(if (e.code == 404 || e.code == 403) R.string.download_not_allowed else R.string.download_failed)
        } catch (e: IOException) {
            Log.w(TAG, "episode not started: ${e.javaClass.simpleName}")
            if (!auto) refuse(R.string.download_failed)
        } catch (e: Exception) {
            Log.w(TAG, "episode not started: ${e.javaClass.simpleName}")
        }
        return false
    }

    /** An episode was played to the end (here, or the server says so): "remove after played" may now take it. */
    fun markPlayed(id: String) {
        if (!::app.isInitialized) return
        val row = store.get(id) ?: return
        if (row.kind == KIND_EPISODE && !row.played) store.setPlayed(id, true)
    }

    /** Worker thread. Removes the automatic episodes the rules name (played, beyond the latest N, over the quota); never the playing one. */
    fun tidyEpisodes(keep: Map<String, Int>) {
        if (!::app.isInitialized) return
        val gone = autoEpisodeEvictions(store.all(), EpisodeAuto.marks(), marks(), keep).filter { it != playingNow }
        gone.forEach { remove(it, byPerson = false) }
    }

    /** A show's automatic downloads were switched off: what is not finished is dropped; what is whole stays, and becomes the person's own. */
    fun releaseShow(showId: String) {
        for (r in store.all()) {
            if (r.kind != KIND_EPISODE || r.showId != showId || r.bookId !in EpisodeAuto.marks()) continue
            if (r.state == OfflineState.READY || r.state == OfflineState.STALE) EpisodeAuto.unmark(r.bookId) else remove(r.bookId, byPerson = false)
        }
    }

    /**
     * Worker thread of the periodic job: lets the held automatic episodes go and waits for them (up to [maxMs]), with no
     * service, because the system refuses to start a data-sync service from the background. The Wi-Fi rule is checked every
     * second; on a metered network they are held again at once. What is left goes on by byte range at the next run.
     */
    fun runHeadless(maxMs: Long) {
        if (!::app.isInitialized) return
        val ids = autoRequestIds()
        if (ids.isEmpty() || !unmetered()) return
        main.post {
            ids.forEach { manager().setStopReason(it, Download.STOP_REASON_NONE) }
            manager().resumeDownloads()
        }
        val until = System.currentTimeMillis() + maxMs
        try {
            while (System.currentTimeMillis() < until && autoUnfinished(store.all(), allMarks()).isNotEmpty()) {
                Thread.sleep(1000)
                if (!unmetered()) {
                    val now = autoRequestIds()
                    main.post { now.forEach { manager().setStopReason(it, AUTO_HOLD) } }
                    break
                }
            }
        } finally {
            val left = autoRequestIds()
            main.post {
                left.forEach { manager().setStopReason(it, AUTO_HOLD) }
                if (!inFront) manager().pauseDownloads()
            }
        }
    }

    // ---- Wipe ----

    /**
     * Sign-out, another person, another server: every downloaded byte, row and cover goes. The rows go now, so nothing
     * can be offered or played; the pref makes the rest durable, so a process that dies halfway is finished at launch.
     */
    fun wipe() {
        if (!::app.isInitialized) return
        EpisodeAuto.wipe()
        prefs.edit().putBoolean("wipePending", true).remove("autoBooks").remove("autoWant").remove("autoNoRoom").remove("autoNext").remove("autoDrop").commit()
        store.clear()
        floors.clear()
        live.clear()
        // Half a second after the sound stopped, so a loader is not reading a cache that is being released.
        safeIo {
            Thread.sleep(500)
            finishWipe()
        }
    }

    fun hasOtherPerson(userId: String): Boolean = ::app.isInitialized && store.all().any { it.userId != userId }

    private fun finishWipe() {
        val live = synchronized(lock) { manager }
        if (live != null) {
            // A running process keeps its manager: Media3's download service holds on to the one it was given, and a
            // released manager would make the next download fail on a dead thread. The manager removes the files it
            // fetched, then any resource the cache still lists goes too.
            live.removeAllDownloads()
            val until = System.currentTimeMillis() + 10_000
            while (System.currentTimeMillis() < until && !(live.isIdle && live.currentDownloads.isEmpty())) Thread.sleep(100)
            synchronized(lock) { cache?.let { c -> c.keys.toList().forEach { c.removeResource(it) } } }
        } else {
            // A wipe the last process left half done: nothing is open, so the folder and the downloads database go.
            synchronized(lock) {
                // A player may have opened the cache for a streamed book; it is closed before its folder goes.
                cache?.release()
                cache = null
                db?.close()
                db = null
                app.deleteDatabase(StandaloneDatabaseProvider.DATABASE_NAME)
                root.deleteRecursively()
            }
        }
        File(root, "covers").deleteRecursively()
        File(root, "chapters").deleteRecursively()
        File(root, "text").deleteRecursively()
        File(root, "static").deleteRecursively()
        store.clear()
        prefs.edit().remove("settingsJson").remove("autoBooks").remove("autoWant").remove("autoNoRoom").remove("autoNext").remove("autoDrop").putBoolean("wipePending", false).commit()
        Log.i(TAG, "offline: wiped")
    }
}
