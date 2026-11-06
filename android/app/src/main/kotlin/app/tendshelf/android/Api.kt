package app.tendshelf.android

import app.tendshelf.shared.ApiPaths
import app.tendshelf.shared.AudioFile
import app.tendshelf.shared.BookManifest
import app.tendshelf.shared.Chapter
import org.json.JSONObject
import java.net.HttpURLConnection
import java.net.URL

/**
 * A0's small HTTP client: sign in with a device token and read a book manifest. Replaced by the one-time
 * code swap in A1/A2. It talks only to the server address it is given.
 */
class Api(server: String) {
    val paths = ApiPaths(server)

    /** Test-only sign-in through `POST /api/v1/auth/token`; returns the bearer token. */
    fun signIn(username: String, password: String): String {
        val body = paths.tokenRequestBody(username, password, "Tendshelf Android A0")
        val conn = open(paths.tokenUrl, null)
        conn.requestMethod = "POST"
        conn.doOutput = true
        conn.setRequestProperty("Content-Type", "application/json")
        conn.outputStream.use { it.write(body.toByteArray()) }
        return JSONObject(read(conn)).getString("token")
    }

    fun manifest(token: String, bookId: String): BookManifest {
        val json = JSONObject(read(open(paths.bookUrl(bookId), token)))
        val book = json.getJSONObject("book")
        val files = json.getJSONArray("files").let { arr ->
            (0 until arr.length()).map {
                val f = arr.getJSONObject(it)
                AudioFile(
                    f.getString("id"), f.getInt("idx"), f.getLong("offsetMs"),
                    f.getLong("durationMs"), f.getString("mime"), f.getBoolean("needsCompat"),
                )
            }
        }
        val chapters = json.getJSONArray("chapters").let { arr ->
            (0 until arr.length()).map {
                val c = arr.getJSONObject(it)
                Chapter(c.getInt("idx"), c.getLong("startMs"), c.getLong("endMs"), c.getString("title"))
            }
        }
        return BookManifest(book.getString("id"), book.getString("title"), book.getLong("durationMs"), files, chapters)
    }

    private fun open(url: String, token: String?): HttpURLConnection {
        val conn = URL(url).openConnection() as HttpURLConnection
        conn.connectTimeout = 10_000
        conn.readTimeout = 20_000
        if (token != null) conn.setRequestProperty("Authorization", "Bearer $token")
        return conn
    }

    private fun read(conn: HttpURLConnection): String {
        val code = conn.responseCode
        if (code !in 200..299) throw java.io.IOException("server answered $code")
        return conn.inputStream.use { String(it.readBytes()) }
    }
}
