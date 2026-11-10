package app.tendshelf.android

import android.os.Bundle
import android.os.Handler
import android.util.Log
import androidx.media3.common.PlaybackException
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.datasource.DefaultHttpDataSource
import androidx.media3.datasource.HttpDataSource
import androidx.media3.exoplayer.ExoPlayer
import app.tendshelf.shared.BookManifest
import app.tendshelf.shared.EpisodeDoc
import app.tendshelf.shared.ShowSettings
import app.tendshelf.shared.SkipRules
import app.tendshelf.shared.episodeSpeed
import app.tendshelf.shared.isEpisodeId
import app.tendshelf.shared.skipRules
import app.tendshelf.shared.PlayerSettings
import app.tendshelf.shared.RemoteProgress
import app.tendshelf.shared.Save
import app.tendshelf.shared.placeWrittenAt
import app.tendshelf.shared.silentRanges
import app.tendshelf.shared.skipSilence
import app.tendshelf.shared.startPlace
import java.io.IOException
import java.util.concurrent.Executor
import java.util.concurrent.atomic.AtomicInteger

/**
 * Starts a book and keeps the state around the player that is not in `Player`: which book, which chapter and
 * what went wrong (session extras `ts.bookId`, `ts.chapterIdx`, `ts.error`). [start] reads settings, manifest
 * and cover on the [io] executor, then hands the result to the main thread. A newer start cancels an older
 * one by sequence number, so only the newest book ever plays.
 */
@UnstableApi
class BookEngine(
    private val exo: ExoPlayer,
    private val player: BookPlayer,
    private val http: DefaultHttpDataSource.Factory,
    private val main: Handler,
    private val io: Executor,
    private val connections: () -> ConnectionSource,
    private val publish: (Bundle) -> Unit,
) {
    private val sequence = AtomicInteger()
    private var bookId = ""
    private var error = ""
    private var silent: List<LongRange> = emptyList()

    // The loaded episode's skip rules (A1); null for a book.
    private var showSettings: ShowSettings? = null
    private var rules: SkipRules? = null
    private var ruleDurationMs = 0L
    private var outroDone = false

    /** A5 hooks: called on the main thread. */
    var onPositionTick: (() -> Unit)?
        get() = player.onTick
        set(v) {
            player.onTick = v
        }
    var onEnded: (() -> Unit)? = null

    /** Worker thread: the newest unsent save of a book, which may be newer than the stored place. */
    var pendingFor: (String) -> Save? = { null }

    /** Main thread, a book was loaded: its id, when the stored place was written, whether the start was given. */
    var onBookStarted: ((String, Long?, Boolean) -> Unit)? = null

    /** The settings of the loaded book. */
    var settings: PlayerSettings = PlayerSettings(1.0, 30, 30, true, false)
        private set
    val currentBook: String get() = bookId

    /**
     * Main thread: quiet skip (skip silence) on or off, at once (decision 0033). The cached settings are changed too, so an
     * offline start keeps it; they are read back first because [settings] holds only the built-in values before any book.
     */
    fun setSkipQuiet(on: Boolean) {
        exo.skipSilenceEnabled = on
        settings = settings.copy(skipQuiet = on)
        Offline.saveSettings(Offline.loadSettings().copy(skipQuiet = on))
    }

    /** Main thread, a start or the player failed: the kind word (`signedOut`, `stream`, `book`, `background`). */
    var onFailed: ((String) -> Unit)? = null

    /** Main thread, just before the loaded book is replaced or cleared: save its place under its own id. */
    var onBookChanging: (() -> Unit)? = null

    init {
        exo.addListener(object : Player.Listener {
            override fun onPlayerError(e: PlaybackException) {
                Log.w(TAG, "player error ${e.errorCodeName}")
                fail(errorOf(e))
                exo.playWhenReady = false
            }

            override fun onPlaybackStateChanged(state: Int) {
                if (state == Player.STATE_READY && error.isNotEmpty()) {
                    error = ""
                    publishState()
                }
                if (state == Player.STATE_ENDED) onEnded?.invoke()
            }

            override fun onPositionDiscontinuity(
                oldPosition: Player.PositionInfo, newPosition: Player.PositionInfo, reason: Int,
            ) {
                // DISCONTINUITY_REASON_SILENCE_SKIP is ignored on purpose: quiet skip (decision 0033) must not trigger the missing-file jump.
                if (reason != Player.DISCONTINUITY_REASON_AUTO_TRANSITION && reason != Player.DISCONTINUITY_REASON_SEEK) return
                val to = skipSilence(silent, exo.currentPosition) ?: return
                val end = exo.duration
                exo.seekTo(if (end > 0 && to >= end) end else to)
            }
        })
        player.addListener(object : Player.Listener {
            override fun onMediaMetadataChanged(mediaMetadata: androidx.media3.common.MediaMetadata) = publishState()
        })
    }

    /** [atMs] null or negative: the best known place (the stored one, or the beginning). */
    fun start(book: String, atMs: Long?) {
        val mine = sequence.incrementAndGet()
        val conn = connections().current()
        if (conn == null) {
            main.post { if (sequence.get() == mine) fail("signedOut") }
            return
        }
        if (isEpisodeId(book)) {
            startEpisode(book, atMs, conn, mine)
            return
        }
        if (Offline.playable(book) != null) {
            startDownloaded(book, atMs, conn, mine)
            return
        }
        io.execute {
            try {
                val api = Api(conn.origin)
                val settings = api.settings(conn.token).also { Offline.saveSettings(it) }
                if (sequence.get() != mine) return@execute
                val manifest = api.manifest(conn.token, book)
                if (sequence.get() != mine) return@execute
                val art = api.cover(conn.token, manifest.id)
                val pending = pendingFor(manifest.id)
                main.post { if (sequence.get() == mine) begin(api, conn, manifest, settings, art, atMs, pending) }
            } catch (e: ApiException) {
                Log.w(TAG, "could not start the book: server answered ${e.code}")
                main.post { if (sequence.get() == mine) fail(if (e.code == 401) "signedOut" else "book") }
            } catch (e: IOException) {
                Log.w(TAG, "could not start the book: ${e.javaClass.simpleName}")
                main.post { if (sequence.get() == mine) fail("stream") }
            } catch (e: Exception) {
                Log.w(TAG, "could not read the book: ${e.javaClass.simpleName}")
                main.post { if (sequence.get() == mine) fail("book") }
            }
        }
    }

    /**
     * A downloaded book starts from what the phone holds, without touching the network (A7, D5): the stored answer, the last
     * settings, the stored cover and the newest place written offline. The place question is asked after it starts, as usual.
     */
    private fun startDownloaded(book: String, atMs: Long?, conn: Connection, mine: Int) {
        io.execute {
            try {
                val row = Offline.store.get(book)
                val api = Api(conn.origin)
                var manifest = api.parseManifest(Offline.store.manifest(book) ?: throw IOException("no stored answer"))
                if (row != null && row.placeAt > (manifest.progress?.clientUpdatedAt ?: 0)) {
                    manifest = manifest.copy(progress = RemoteProgress(row.placeMs, false, row.placeAt, conn.deviceId, ""))
                }
                val settings = Offline.loadSettings()
                val art = Offline.coverBytes(book)
                val pending = pendingFor(manifest.id)
                main.post { if (sequence.get() == mine) begin(api, conn, manifest, settings, art, atMs, pending, true) }
            } catch (e: Exception) {
                Log.w(TAG, "could not read the downloaded book: ${e.javaClass.simpleName}")
                main.post { if (sequence.get() == mine) fail("book") }
            }
        }
    }

    /**
     * A podcast episode (A1, decision 0038): the playback document, the show's picture and the person's settings are read
     * on the [io] executor, then the main thread starts the one file. Chapters are fetched after the sound has started,
     * because the server may have to ask the publisher for them. Streamed only: downloads of episodes are A2.
     */
    private fun startEpisode(id: String, atMs: Long?, conn: Connection, mine: Int) {
        io.execute {
            try {
                val api = Api(conn.origin)
                val settings = api.settings(conn.token).also { Offline.saveSettings(it) }
                if (sequence.get() != mine) return@execute
                val doc = api.episode(conn.token, id)
                if (sequence.get() != mine) return@execute
                val art = doc.artUrl.takeIf { it.isNotEmpty() }?.let { api.podcastArt(conn.token, it) }
                val pending = pendingFor(doc.id)
                main.post { if (sequence.get() == mine) beginEpisode(api, conn, doc, settings, art, atMs, pending) }
                if (doc.chaptersUrl.isNotEmpty()) {
                    val list = api.chapters(conn.token, doc.chaptersUrl, doc.durationMs)
                    if (list.isNotEmpty()) main.post { if (sequence.get() == mine) player.setChapters(list) }
                }
            } catch (e: ApiException) {
                Log.w(TAG, "could not start the episode: server answered ${e.code}")
                main.post { if (sequence.get() == mine) fail(if (e.code == 401) "signedOut" else "book") }
            } catch (e: IOException) {
                Log.w(TAG, "could not start the episode: ${e.javaClass.simpleName}")
                main.post { if (sequence.get() == mine) fail("stream") }
            } catch (e: Exception) {
                Log.w(TAG, "could not read the episode: ${e.javaClass.simpleName}")
                main.post { if (sequence.get() == mine) fail("book") }
            }
        }
    }

    private fun beginEpisode(
        api: Api, conn: Connection, doc: EpisodeDoc, settings: PlayerSettings, art: ByteArray?, atMs: Long?, pending: Save?,
    ) {
        val url = api.paths.podcastUrl(doc.audioUrl)
        if (url == null) {
            fail("book")
            return
        }
        onBookChanging?.invoke()
        http.setDefaultRequestProperties(mapOf("Authorization" to "Bearer ${conn.token}"))
        bookId = doc.id
        error = ""
        silent = emptyList()
        this.settings = settings
        showSettings = doc.settings
        ruleDurationMs = doc.durationMs
        rules = skipRules(doc.settings, doc.durationMs)
        outroDone = false
        val given = atMs != null && atMs >= 0
        val place = startPlace(doc.progress, atMs, pending)
        // The intro is skipped by starting after it, so it is never heard; a place the person asked for is kept as asked.
        val from = if (given) place else rules!!.startAt(place)
        player.loadEpisode(doc.title, doc.show, emptyList(), settings, art)
        onBookStarted?.invoke(doc.id, placeWrittenAt(doc.progress, pending), given)
        exo.setMediaSource(BookMedia.episode(doc, url, ServerGate.gated(http)), from)
        // The show's own speed, else the person's: set before prepare, so the first sample is already at that speed.
        exo.setPlaybackSpeed(episodeSpeed(doc.settings, settings.speed).toFloat())
        exo.skipSilenceEnabled = settings.skipQuiet
        exo.prepare()
        exo.play()
        publishState()
        Log.i(TAG, "started episode")
    }

    /**
     * Main thread, about twice a second: once the playing episode reaches the show's outro, it is over (the end is
     * reached the usual way, so `finished` is written and the sleep timer is honoured). The real length wins over the feed's.
     */
    fun checkOutro() {
        val shown = showSettings ?: return
        if (outroDone || !exo.isPlaying) return
        val real = exo.duration
        if (real > 0 && real != ruleDurationMs) {
            ruleDurationMs = real
            rules = skipRules(shown, real)
        }
        if (real > 0 && rules?.inOutro(exo.currentPosition) == true) {
            outroDone = true
            exo.seekTo(real)
        }
    }

    private fun begin(
        api: Api, conn: Connection, manifest: BookManifest, settings: app.tendshelf.shared.PlayerSettings,
        art: ByteArray?, atMs: Long?, pending: Save?, downloaded: Boolean = false,
    ) {
        if (manifest.files.none { it.playable }) {
            fail("book")
            return
        }
        onBookChanging?.invoke()
        http.setDefaultRequestProperties(mapOf("Authorization" to "Bearer ${conn.token}"))
        bookId = manifest.id
        error = ""
        silent = silentRanges(manifest.files)
        showSettings = null
        rules = null
        this.settings = settings
        player.load(manifest.title, manifest.author, manifest.chapters, settings, art)
        onBookStarted?.invoke(manifest.id, placeWrittenAt(manifest.progress, pending), atMs != null && atMs >= 0)
        exo.setMediaSource(BookMedia.build(manifest, api.paths, ServerGate.gated(http), downloaded), startPlace(manifest.progress, atMs, pending))
        if (BuildConfig.DEBUG) Log.i(TAG, "speed from the manifest: ${manifest.speed}")
        exo.setPlaybackSpeed(manifest.speed.toFloat())
        exo.skipSilenceEnabled = settings.skipQuiet
        exo.prepare()
        exo.play()
        publishState()
        Log.i(TAG, "started book: ${manifest.files.size} files, ${manifest.chapters.size} chapters")
    }

    /** Stop, forget the book and the credential (sign-out, switching server). */
    fun stopAndClear() {
        sequence.incrementAndGet()
        onBookChanging?.invoke()
        exo.stop()
        exo.clearMediaItems()
        http.setDefaultRequestProperties(emptyMap())
        player.clear()
        bookId = ""
        error = ""
        silent = emptyList()
        showSettings = null
        rules = null
        publishState()
    }

    /**
     * The system refused to let the service run in the foreground (a start from the background, A5b step 0).
     * Nothing can play in that state, so stop trying: `ts.error` says `background` and a tap is the way back.
     */
    fun refused() = fail("background")

    /** The server refused the token while saving. */
    fun signedOut() = fail("signedOut")

    private fun fail(kind: String) {
        error = kind
        exo.playWhenReady = false
        publishState()
        onFailed?.invoke(kind)
    }

    private fun errorOf(e: PlaybackException): String {
        var c: Throwable? = e
        while (c != null) {
            if (c is HttpDataSource.InvalidResponseCodeException) {
                return when (c.responseCode) {
                    401 -> "signedOut"
                    404, 403, 410 -> "book"
                    else -> "stream"
                }
            }
            c = c.cause
        }
        return "stream"
    }

    private fun publishState() {
        publish(Bundle().apply {
            putString(EXTRA_BOOK, bookId)
            putInt(EXTRA_CHAPTER, player.chapterIdx)
            putString(EXTRA_ERROR, error)
        })
    }

    companion object {
        private const val TAG = "Tendshelf"
        const val EXTRA_BOOK = "ts.bookId"
        const val EXTRA_CHAPTER = "ts.chapterIdx"
        const val EXTRA_ERROR = "ts.error"
    }
}
