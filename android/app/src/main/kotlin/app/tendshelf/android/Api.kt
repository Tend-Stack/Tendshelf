package app.tendshelf.android

import app.tendshelf.shared.ApiPaths
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
class ApiException(val code: Int) : IOException("server answered $code")

/**
 * The native side's small HTTP client. It talks only to URLs built from [ApiPaths] on the one origin it is
 * given, and puts the bearer token in a header, never in a URL or a log line.
 */
class Api(origin: String) {
    val paths = ApiPaths(origin)

    /** Debug-only sign-in through `POST /api/v1/auth/token`; returns the bearer token. Kept until A4. */
    fun signIn(username: String, password: String): String {
        val body = paths.tokenRequestBody(username, password, "Tendshelf Android debug")
        val conn = open(paths.tokenUrl, null)
        conn.requestMethod = "POST"
        conn.doOutput = true
        conn.setRequestProperty("Content-Type", "application/json")
        conn.outputStream.use { it.write(body.toByteArray()) }
        return JSONObject(String(read(conn))).getString("token")
    }

    fun settings(token: String): PlayerSettings {
        val j = JSONObject(String(get(paths.settingsUrl, token, 64 * 1024)))
        return PlayerSettings(
            j.getDouble("speed"), j.getInt("skipBackS"), j.getInt("skipForwardS"),
            j.getBoolean("autoRewind"), j.getBoolean("nextInSeries"),
        )
    }

    fun manifest(token: String, bookId: String): BookManifest {
        val json = JSONObject(String(get(paths.bookUrl(bookId), token, 4 * 1024 * 1024)))
        val book = json.getJSONObject("book")
        val files = json.getJSONArray("files").let { arr ->
            (0 until arr.length()).map {
                val f = arr.getJSONObject(it)
                AudioFile(
                    f.getString("id"), f.getInt("idx"), f.getLong("offsetMs"), f.getLong("durationMs"),
                    f.getString("mime"), f.getBoolean("needsCompat"), f.optBoolean("playable", true),
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
        val progress = json.optJSONObject("progress")?.let {
            RemoteProgress(
                it.getLong("positionMs"), it.getBoolean("finished"), it.getLong("clientUpdatedAt"),
                it.optString("deviceId"), it.optString("deviceLabel"),
            )
        }
        // The id in the answer is the live one when the asked-for id was merged into another book.
        val id = book.getString("id")
        return BookManifest(
            id, book.getString("title"), book.getLong("durationMs"), files, chapters,
            speed = json.optDouble("speed", 1.0),
            coverUrl = if (book.isNull("cover")) "" else paths.coverUrl(id, 512),
            author = authors,
            progress = progress,
        )
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
        val bytes = conn.inputStream.use { it.readNBytes(limit + 1) }
        if (bytes.size > limit) throw IOException("answer too large")
        return bytes
    }
}
