package app.tendshelf.android

import app.tendshelf.shared.readAtMost
import app.tendshelf.shared.ApiJson
import app.tendshelf.shared.ApiPaths
import app.tendshelf.shared.BatchItemResult
import app.tendshelf.shared.BookCard
import app.tendshelf.shared.ShelfCard
import app.tendshelf.shared.Save
import app.tendshelf.shared.batchBody
import app.tendshelf.shared.queueNextBody
import app.tendshelf.shared.AudioFile
import app.tendshelf.shared.BookManifest
import app.tendshelf.shared.Chapter
import app.tendshelf.shared.EpisodeDoc
import app.tendshelf.shared.EpisodeAction
import app.tendshelf.shared.EpisodeCard
import app.tendshelf.shared.PodcastJson
import app.tendshelf.shared.ShowCard
import app.tendshelf.shared.episodeActionBody
import app.tendshelf.shared.episodeCards
import app.tendshelf.shared.showCards
import app.tendshelf.shared.assembleResults
import app.tendshelf.shared.podcastBatchBody
import app.tendshelf.shared.splitSaves
import app.tendshelf.shared.PlayerSettings
import app.tendshelf.shared.RemoteProgress
import java.io.IOException
import java.net.HttpURLConnection
import java.net.URL

/** The server answered with a status that is not a success. */
class ApiException(val code: Int, val retryAfterS: Int = 0) : IOException("server answered $code")

/**
 * The native side's small HTTP client. It talks only to URLs built from [ApiPaths] on the one origin it is
 * given, and puts the bearer token in a header, never in a URL or a log line.
 */
class Api(origin: String) {
    val paths = ApiPaths(origin)

    fun settings(token: String): PlayerSettings = ApiJson.settings(String(get(paths.settingsUrl, token, 64 * 1024)))

    fun manifest(token: String, bookId: String): BookManifest = parseManifest(fetchManifestJson(token, bookId))

    /** The server's answer as text, so a download can keep it as it came. */
    fun fetchManifestJson(token: String, bookId: String): String = String(get(paths.bookUrl(bookId), token, 4 * 1024 * 1024))

    fun parseManifest(text: String): BookManifest = ApiJson.manifest(text, paths)

    /** Sends unsent saves in order; the answer has one item per save. A failure is an exception, never a retry here. */
    fun batch(token: String, saves: List<Save>, deviceId: String = ""): List<BatchItemResult> {
        val (books, episodes) = splitSaves(saves)
        if (episodes.isEmpty()) return ApiJson.batchResults(String(post(paths.batchUrl, token, batchBody(saves), 1024 * 1024)!!))
        // Books and episodes go to their own routes; the answers are put back in the batch's order. A group that fails
        // ends the list at its first row (the rest is sent again), unless nothing at all was settled: then it throws.
        var failure: Exception? = null
        val bookResults = if (books.isEmpty()) emptyList() else try {
            ApiJson.batchResults(String(post(paths.batchUrl, token, batchBody(books), 1024 * 1024)!!))
        } catch (e: Exception) {
            failure = e
            null
        }
        val episodeResults = try {
            podcastBatch(token, episodes, deviceId)
        } catch (e: Exception) {
            failure = failure ?: e
            null
        }
        val out = assembleResults(saves, bookResults, episodeResults)
        if (out.isEmpty()) throw failure ?: IOException("no answer")
        return out
    }

    /** `POST /api/v1/podcasts/progress/batch`. A 404 means podcasts are off on this server: the rows are answered `not_found` and dropped. */
    private fun podcastBatch(token: String, episodes: List<Save>, deviceId: String): List<BatchItemResult> = try {
        PodcastJson.batchResults(String(post(paths.podcastBatchUrl, token, podcastBatchBody(episodes, deviceId), 1024 * 1024)!!))
    } catch (e: ApiException) {
        if (e.code != 404) throw e
        episodes.map { BatchItemResult(it.bookId, false, "not_found", null) }
    }

    /** The playback document of an episode. */
    fun episode(token: String, episodeId: String): EpisodeDoc = PodcastJson.episode(fetchEpisodeJson(token, episodeId))

    /** The document as the server sent it: a downloaded episode keeps it, so it starts with no network. */
    fun fetchEpisodeJson(token: String, episodeId: String): String = String(get(paths.episodeUrl(episodeId), token, 256 * 1024))

    /** The chapters file as the server sent it (kept with a download), or null when there is none or it cannot be read. */
    fun chaptersJson(token: String, path: String): String? {
        val url = paths.podcastUrl(path) ?: return null
        return try {
            String(get(url, token, 512 * 1024))
        } catch (e: IOException) {
            null
        }
    }

    /** The show's picture (at most 2 MiB) from the path the document named, or null when there is none or it cannot be read. */
    fun podcastArt(token: String, artPath: String): ByteArray? {
        val url = paths.podcastUrl(artPath, 512) ?: return null
        return try {
            get(url, token, 2 * 1024 * 1024)
        } catch (e: IOException) {
            null
        }
    }

    /** The episode's chapters from the path the document named; empty when it has none or the publisher cannot be reached. */
    fun chapters(token: String, path: String, durationMs: Long): List<Chapter> {
        val url = paths.podcastUrl(path) ?: return emptyList()
        return try {
            PodcastJson.chapters(String(get(url, token, 512 * 1024)), durationMs)
        } catch (e: IOException) {
            emptyList()
        }
    }

    // ---- The podcast lists the car browses (A3). A 404 means podcasts are off on this server (ApiException). ----

    fun podcastShows(token: String): List<ShowCard> = PodcastJson.showCards(String(get(paths.showsUrl, token, 1024 * 1024)))
    fun podcastNew(token: String): List<EpisodeCard> = PodcastJson.episodeCards(String(get(paths.newEpisodesUrl, token, 2 * 1024 * 1024)))

    /** The newest 50 episodes of a followed show; older ones are on the show's page. */
    fun showEpisodes(token: String, showId: String): List<EpisodeCard> =
        PodcastJson.episodeCards(String(get(paths.showEpisodesUrl(showId, 50), token, 2 * 1024 * 1024)))

    /** A show's picture (at most 2 MiB) by the show's id, or null when there is none or it cannot be read. */
    fun showArt(token: String, showId: String): ByteArray? = try {
        get(paths.showArtUrl(showId, 512), token, 2 * 1024 * 1024)
    } catch (e: IOException) {
        null
    }

    /** Mark played or unplayed, star or unstar. Idempotent on the server, so a retry costs nothing. */
    fun episodeAction(token: String, episodeId: String, action: EpisodeAction) {
        if (action == EpisodeAction.PLAY_NEXT) {
            // The one queue holds books and episodes (P5); a book already queued stays, so a retry costs nothing.
            post(paths.queueAddUrl, token, episodeActionBody(action, episodeId), 1024 * 1024)
            return
        }
        val url = when (action) {
            EpisodeAction.PLAYED, EpisodeAction.UNPLAYED -> paths.episodePlayedUrl(episodeId)
            EpisodeAction.STAR, EpisodeAction.UNSTAR -> paths.episodeFavouriteUrl(episodeId)
            EpisodeAction.PLAY_NEXT -> return
        }
        post(url, token, episodeActionBody(action), 64 * 1024)
    }

    /** The stored place of a book, or null when there is none. */
    fun progress(token: String, bookId: String): RemoteProgress? =
        ApiJson.progress(String(get(paths.progressUrl(bookId), token, 64 * 1024)))

    /**
     * Takes the next book off the queue (or the series) once [ended] is over: its id, or null for 204. Sent at
     * most once: a lost answer must not pop a second book, so there is no retry here and no reused connection.
     */
    fun queueNext(token: String, ended: String): String? {
        val bytes = post(paths.queueNextUrl, token, queueNextBody(ended), 64 * 1024) ?: return null
        return ApiJson.nextItemId(String(bytes))
    }

    /**
     * Looks at the next book after [ended] without taking it (decision 0035): its id, or null for 204. A GET that
     * writes nothing, so a retry costs nothing. An older server answers 404 or 405, which reaches the caller.
     */
    fun queuePeek(token: String, ended: String): String? {
        val bytes = get(paths.queuePeekUrl(ended), token, 64 * 1024)
        if (bytes.isEmpty()) return null
        return ApiJson.nextItemId(String(bytes))
    }

    /**
     * Takes [bookId] off the queue (`DELETE`, idempotent). Returns the status for [queueDeleteDone] to judge; a network
     * failure is an exception. Used to tidy the queue after a book started from the phone without asking (decision 0035).
     */
    fun queueRemove(token: String, bookId: String): Int {
        val conn = open(paths.queueEntryUrl(bookId), token)
        conn.requestMethod = "DELETE"
        val code = conn.responseCode
        conn.disconnect()
        return code
    }

    // ---- The lists the car browses (A8). Each is one small GET; a failure is an exception for the caller to word. ----

    fun continueList(token: String): List<BookCard> = ApiJson.itemBooks(String(get(paths.continueUrl(24), token, 1024 * 1024)))
    fun queue(token: String): List<BookCard> = ApiJson.itemBooks(String(get(paths.queueUrl, token, 1024 * 1024)))
    fun shelves(token: String): List<ShelfCard> = ApiJson.shelves(String(get(paths.shelvesUrl, token, 1024 * 1024)))

    /** The first page of a shelf (48 books); a longer shelf is reached by search. */
    fun shelf(token: String, shelfId: String): List<BookCard> = ApiJson.shelfBooks(String(get(paths.shelfUrl(shelfId), token, 2 * 1024 * 1024)))
    fun search(token: String, query: String, limit: Int): List<BookCard> = ApiJson.searchBooks(String(get(paths.searchUrl(query, limit), token, 2 * 1024 * 1024)))

    /** Saves the speed for one book. Best effort: the caller ignores a failure. */
    fun putSpeed(token: String, bookId: String, speed: Double) {
        val conn = open(paths.speedUrl(bookId), token)
        conn.requestMethod = "PUT"
        conn.doOutput = true
        conn.setRequestProperty("Content-Type", "application/json")
        conn.outputStream.use { it.write("{\"speed\":$speed}".toByteArray()) }
        val code = conn.responseCode
        conn.disconnect()
        if (code !in 200..299) throw ApiException(code)
    }

    /** POST of JSON on a fresh connection; null for 204. */
    private fun post(url: String, token: String, body: String, limit: Int): ByteArray? {
        val conn = open(url, token)
        conn.requestMethod = "POST"
        conn.doOutput = true
        conn.setRequestProperty("Content-Type", "application/json")
        conn.setRequestProperty("Connection", "close") // no reused socket, so the platform never resends a POST by itself
        conn.outputStream.use { it.write(body.toByteArray()) }
        val code = conn.responseCode
        if (code == 204) {
            conn.disconnect()
            return null
        }
        if (code == 429) {
            val wait = conn.getHeaderField("Retry-After")?.toIntOrNull() ?: 30
            conn.disconnect()
            throw ApiException(code, wait.coerceIn(1, 300))
        }
        return read(conn, limit)
    }

    /** The cover as bytes (at most 2 MiB), or null when there is none or it cannot be read. */
    fun cover(token: String, bookId: String): ByteArray? =
        try {
            get(paths.coverUrl(bookId, 512), token, 2 * 1024 * 1024)
        } catch (e: IOException) {
            null
        }

    /** GET with one hand-followed same-origin 308 (a merged book's id), as the server answers for old ids. */
    private fun get(url: String, token: String, limit: Int): ByteArray {
        var target = url
        for (hop in 0..1) {
            val conn = open(target, token)
            val code = conn.responseCode
            if (code == 308 || code == 301) {
                val next = conn.getHeaderField("Location")?.let { URL(URL(target), it).toString() }
                conn.disconnect()
                if (hop == 0 && next != null && next.startsWith(paths.base + "/")) {
                    target = next
                    continue
                }
                throw ApiException(code)
            }
            return read(conn, limit)
        }
        throw ApiException(308)
    }

    private fun open(url: String, token: String?): HttpURLConnection {
        // The bearer goes only to the server the phone was connected to (ServerGate).
        if (token != null && !ServerGate.ok(paths.base)) throw IOException("the address does not answer as the connected server")
        val conn = URL(url).openConnection() as HttpURLConnection
        conn.instanceFollowRedirects = false
        conn.connectTimeout = 10_000
        conn.readTimeout = 20_000
        if (token != null) conn.setRequestProperty("Authorization", "Bearer $token")
        return conn
    }

    private fun read(conn: HttpURLConnection, limit: Int = 64 * 1024): ByteArray {
        val code = conn.responseCode
        if (code !in 200..299) {
            conn.disconnect()
            throw ApiException(code)
        }
        val bytes = conn.inputStream.use { it.readAtMost(limit + 1) }
        if (bytes.size > limit) throw IOException("answer too large")
        return bytes
    }
}
