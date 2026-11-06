package app.tendshelf.android

import app.tendshelf.shared.readAtMost
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
import app.tendshelf.shared.PlayerSettings
import app.tendshelf.shared.RemoteProgress
import org.json.JSONObject
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

    fun settings(token: String): PlayerSettings {
        val j = JSONObject(String(get(paths.settingsUrl, token, 64 * 1024)))
        return PlayerSettings(
            j.getDouble("speed"), j.getInt("skipBackS"), j.getInt("skipForwardS"),
            j.getBoolean("autoRewind"), j.getBoolean("nextInSeries"), ApiJson.rewindSteps(j),
        )
    }

    fun manifest(token: String, bookId: String): BookManifest = parseManifest(fetchManifestJson(token, bookId))

    /** The server's answer as text, so a download can keep it as it came. */
    fun fetchManifestJson(token: String, bookId: String): String = String(get(paths.bookUrl(bookId), token, 4 * 1024 * 1024))

    fun parseManifest(text: String): BookManifest {
        val json = JSONObject(text)
        val book = json.getJSONObject("book")
        val files = json.getJSONArray("files").let { arr ->
            (0 until arr.length()).map {
                val f = arr.getJSONObject(it)
                AudioFile(
                    f.getString("id"), f.getInt("idx"), f.getLong("offsetMs"), f.getLong("durationMs"),
                    f.getString("mime"), f.getBoolean("needsCompat"), f.optBoolean("playable", true), f.optLong("size", 0),
                )
            }
        }
        val chapters = json.getJSONArray("chapters").let { arr ->
            (0 until arr.length()).map {
                val c = arr.getJSONObject(it)
                Chapter(c.getInt("idx"), c.getLong("startMs"), c.getLong("endMs"), c.getString("title"))
            }
        }
        val authors = book.optJSONArray("authors")?.let { arr ->
            (0 until arr.length()).joinToString(", ") { arr.getJSONObject(it).getString("name") }
        }.orEmpty()
        val progress = json.optJSONObject("progress")?.let(ApiJson::remoteProgress)
        // The id in the answer is the live one when the asked-for id was merged into another book.
        val id = book.getString("id")
        return BookManifest(
            id, book.getString("title"), book.getLong("durationMs"), files, chapters,
            speed = json.optDouble("speed", 1.0),
            coverUrl = if (book.isNull("cover")) "" else paths.coverUrl(id, 512),
            author = authors,
            progress = progress,
            mayDownload = json.optJSONObject("permissions")?.optBoolean("download", false) ?: false,
        )
    }

    /** Sends unsent saves in order; the answer has one item per save. A failure is an exception, never a retry here. */
    fun batch(token: String, saves: List<Save>): List<BatchItemResult> =
        ApiJson.batchResults(String(post(paths.batchUrl, token, batchBody(saves), 1024 * 1024)!!))

    /** The stored place of a book, or null when there is none. */
    fun progress(token: String, bookId: String): RemoteProgress? =
        JSONObject(String(get(paths.progressUrl(bookId), token, 64 * 1024))).optJSONObject("progress")?.let(ApiJson::remoteProgress)

    /**
     * Takes the next book off the queue (or the series) once [ended] is over: its id, or null for 204. Sent at
     * most once: a lost answer must not pop a second book, so there is no retry here and no reused connection.
     */
    fun queueNext(token: String, ended: String): String? {
        val bytes = post(paths.queueNextUrl, token, queueNextBody(ended), 64 * 1024) ?: return null
        return JSONObject(String(bytes)).getJSONObject("book").getString("id")
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
