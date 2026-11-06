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

    /** A5 hooks: called on the main thread. */
    var onPositionTick: (() -> Unit)?
        get() = player.onTick
        set(v) {
            player.onTick = v
        }
    var onEnded: (() -> Unit)? = null

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
        io.execute {
            try {
                val api = Api(conn.origin)
                val settings = api.settings(conn.token)
                if (sequence.get() != mine) return@execute
                val manifest = api.manifest(conn.token, book)
                if (sequence.get() != mine) return@execute
                val art = api.cover(conn.token, manifest.id)
                main.post { if (sequence.get() == mine) begin(api, conn, manifest, settings, art, atMs) }
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

    private fun begin(
        api: Api, conn: Connection, manifest: BookManifest, settings: app.tendshelf.shared.PlayerSettings,
        art: ByteArray?, atMs: Long?,
    ) {
        if (manifest.files.none { it.playable }) {
            fail("book")
            return
        }
        http.setDefaultRequestProperties(mapOf("Authorization" to "Bearer ${conn.token}"))
        bookId = manifest.id
        error = ""
        silent = silentRanges(manifest.files)
        player.load(manifest.title, manifest.author, manifest.chapters, settings, art)
        exo.setMediaSource(BookMedia.build(manifest, api.paths, http), startPlace(manifest.progress, atMs))
        exo.setPlaybackSpeed(manifest.speed.toFloat())
        exo.prepare()
        exo.play()
        publishState()
        Log.i(TAG, "started book: ${manifest.files.size} files, ${manifest.chapters.size} chapters")
    }

    /** Stop, forget the book and the credential (sign-out, switching server). */
    fun stopAndClear() {
        sequence.incrementAndGet()
        exo.stop()
        exo.clearMediaItems()
        http.setDefaultRequestProperties(emptyMap())
        player.clear()
        bookId = ""
        error = ""
        silent = emptyList()
        publishState()
    }

    private fun fail(kind: String) {
        error = kind
        exo.playWhenReady = false
        publishState()
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
