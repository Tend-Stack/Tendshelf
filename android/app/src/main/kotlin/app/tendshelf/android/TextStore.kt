package app.tendshelf.android

import android.content.Context
import android.util.Log
import android.webkit.CookieManager
import app.tendshelf.shared.Origin
import app.tendshelf.shared.OfflineRoute
import app.tendshelf.shared.offlineRoute
import app.tendshelf.shared.validId
import org.json.JSONObject
import java.io.File
import java.io.IOException
import java.net.HttpURLConnection
import java.net.URL
import java.security.MessageDigest

/** How a text download ended. [NONE] is "no right to this text" (a 404 at the manifest), not a failure. */
enum class TextResult { READY, NONE, SIGNIN, FAILED }

/** What the server said about a book's text: the addresses to keep and the revision that says whether they are stale. */
class TextManifest(
    val bookId: String, val editionId: String, val revision: String, val title: String, val author: String,
    val bytes: Long, val parts: List<String>, val images: List<String>, val assets: List<String>,
)

/** A stored answer: the file, and the headers the server sent that matter (invariant 4). */
class Stored(val file: File, val type: String, val csp: String?)

/**
 * The text of the downloaded books (plan A9). Files under `noBackupFilesDir/offline/text/<bookId>/` and the shared
 * `offline/static/`: each answer is a file named for the hash of its address, plus `index.json` mapping address to file,
 * `Content-Type` and `Content-Security-Policy`. Fetching uses the WebView's session cookie (D1) on the server's own origin
 * and nothing else; the cookie is read for the request and never kept, logged or stored.
 */
object TextStore {
    private const val TAG = "Tendshelf"
    private const val MAX_ANSWER = 32L * 1024 * 1024
    private const val SPARE = 50L * 1024 * 1024

    private lateinit var app: Context
    private val indexLock = Any()

    fun init(context: Context) {
        if (!::app.isInitialized) app = context.applicationContext
    }

    private val root get() = File(app.noBackupFilesDir, "offline")
    private fun textDir(book: String) = File(File(root, "text"), book)
    private val staticDir get() = File(root, "static")

    private fun name(key: String) = "s" + MessageDigest.getInstance("SHA-256").digest(key.toByteArray()).joinToString("") { "%02x".format(it) }

    // ---- Reading ----

    /** The stored answer for [route], or null. A route for a book with no stored text, or an address not stored, is null. */
    fun lookup(route: OfflineRoute): Stored? = when (route) {
        is OfflineRoute.Shell -> find(textDir(route.bookId), route.key)
        is OfflineRoute.Part -> find(textDir(route.bookId), route.key)
        is OfflineRoute.Image -> find(textDir(route.bookId), route.key)
        is OfflineRoute.Static -> find(staticDir, route.key)
    }

    private fun find(dir: File, key: String): Stored? {
        if (!::app.isInitialized) return null
        val entry = synchronized(indexLock) { readIndex(dir).optJSONObject(key) } ?: return null
        val file = File(dir, entry.optString("file"))
        if (!file.isFile || file.parentFile != dir) return null
        return Stored(file, entry.optString("type", "application/octet-stream"), entry.optString("csp").ifEmpty { null })
    }

    private fun readIndex(dir: File): JSONObject = try {
        JSONObject(File(dir, "index.json").readText())
    } catch (_: Exception) {
        JSONObject()
    }

    private fun writeIndex(dir: File, index: JSONObject) {
        val tmp = File(dir, "index.json.tmp")
        tmp.writeText(index.toString())
        if (!tmp.renameTo(File(dir, "index.json"))) throw IOException("index not written")
    }

    // ---- Removing ----

    fun remove(book: String) {
        if (!::app.isInitialized) return
        synchronized(indexLock) { textDir(book).deleteRecursively() }
    }

    /** Static files no stored shell names any more (a new build replaced them). */
    fun prune(keep: Collection<String>) {
        if (!::app.isInitialized) return
        synchronized(indexLock) {
            val used = HashSet<String>(keep)
            val books = File(root, "text").listFiles().orEmpty()
            for (dir in books) {
                val index = readIndex(dir)
                for (key in index.keys()) {
                    if (!key.startsWith("/books/")) continue
                    val f = File(dir, index.getJSONObject(key).optString("file"))
                    if (f.isFile) Regex("/static/[A-Za-z0-9._/-]+").findAll(f.readText()).forEach { used.add(it.value) }
                }
            }
            val index = readIndex(staticDir)
            var changed = false
            for (key in index.keys().asSequence().toList()) {
                if (key in used) continue
                File(staticDir, index.getJSONObject(key).optString("file")).delete()
                index.remove(key)
                changed = true
            }
            if (changed) writeIndex(staticDir, index)
        }
    }

    // ---- Fetching ----

    private class Reply(val code: Int, val body: ByteArray, val type: String, val csp: String?)

    private fun cookie(origin: Origin): String? = CookieManager.getInstance().getCookie(origin.text)

    /** One GET on the server's origin. [withCookie] false for static files. A redirect is returned, never followed. */
    private fun get(origin: Origin, path: String, withCookie: Boolean, limit: Long = MAX_ANSWER): Reply {
        if (!origin.matches(origin.text + path)) throw IOException("address outside the origin")
        // The session cookie goes only to the connected server; a 404 from anything else deletes nothing.
        if (withCookie && !ServerGate.ok(origin.text)) throw IOException("the address does not answer as the connected server")
        val conn = URL(origin.text + path).openConnection() as HttpURLConnection
        try {
            conn.instanceFollowRedirects = false
            conn.connectTimeout = 10_000
            conn.readTimeout = 30_000
            if (withCookie) {
                // The stored pages are the app's own variant, so the shell mark goes with the session even if the jar lost it.
                val jar = cookie(origin).orEmpty()
                conn.setRequestProperty("Cookie", if (Regex("(^|; )ts_shell=").containsMatchIn(jar)) jar else (if (jar.isEmpty()) "" else "$jar; ") + "ts_shell=android")
            }
            val code = conn.responseCode
            if (code !in 200..299) return Reply(code, ByteArray(0), "", null)
            val declared = conn.contentLengthLong
            if (declared > limit) throw IOException("answer too large")
            val body = conn.inputStream.use { it.readNBytes(limit.toInt() + 1) }
            if (body.size > limit) throw IOException("answer too large")
            return Reply(code, body, conn.contentType ?: "application/octet-stream", conn.getHeaderField("Content-Security-Policy"))
        } finally {
            conn.disconnect()
        }
    }

    fun parse(text: String): TextManifest {
        val j = JSONObject(text)
        fun list(name: String) = j.optJSONArray(name)?.let { a -> (0 until a.length()).map { a.getString(it) } }.orEmpty()
        return TextManifest(
            j.getString("bookId"), j.optString("editionId"), j.getString("revision"), j.optString("title"), j.optString("author"),
            j.optLong("bytes"), list("parts"), list("images"), list("assets"),
        )
    }

    /** The manifest, or the reason there is none. */
    sealed interface Manifest {
        data class Ok(val m: TextManifest) : Manifest
        data class Not(val result: TextResult) : Manifest
        data class Code(val code: Int) : Manifest
    }

    fun manifest(origin: Origin, book: String): Manifest {
        val r = try {
            get(origin, "/offline/books/$book/manifest", true, 4L * 1024 * 1024)
        } catch (e: IOException) {
            return Manifest.Not(TextResult.FAILED)
        }
        return when {
            r.code == 404 || r.code == 403 -> Manifest.Code(404)
            r.code in 300..399 -> Manifest.Not(TextResult.SIGNIN)
            r.code != 200 -> Manifest.Not(TextResult.FAILED)
            else -> try {
                Manifest.Ok(parse(String(r.body)))
            } catch (_: Exception) {
                Manifest.Not(TextResult.FAILED)
            }
        }
    }

    /**
     * Stores everything [m] lists, the contents sheet and then the shell last (invariant 2). A redirect means the session is
     * gone. The room is checked against the manifest's own size estimate first.
     */
    fun fetch(origin: Origin, m: TextManifest, freeBytes: Long): TextResult {
        if (!validId(m.bookId)) return TextResult.FAILED
        if (freeBytes < m.bytes + SPARE) return TextResult.FAILED
        val dir = textDir(m.bookId).apply { mkdirs() }
        val sdir = staticDir.apply { mkdirs() }
        try {
            // Whatever the server lists must be an address the app would answer for this book; nothing else is fetched.
            val listed = m.parts.map { offlineRoute(origin.text + it, origin) is OfflineRoute.Part } +
                m.images.map { offlineRoute(origin.text + it, origin) is OfflineRoute.Image } +
                m.assets.map { offlineRoute(origin.text + it, origin) is OfflineRoute.Static }
            val mine = (m.parts + m.images).all { it.contains("/${m.bookId}/") }
            if (!mine || listed.any { !it }) return TextResult.FAILED
            val keep = synchronized(indexLock) { readIndex(dir) }
            val fresh = JSONObject()
            for (key in m.parts + m.images) {
                val r = get(origin, key, true)
                if (r.code in 300..399) return TextResult.SIGNIN
                if (r.code != 200) {
                    // A picture that cannot be read is skipped; a missing part would leave a hole in the book.
                    if (key in m.images) continue
                    return TextResult.FAILED
                }
                put(dir, fresh, key, r)
            }
            val staticIndex = synchronized(indexLock) { readIndex(sdir) }
            for (key in m.assets) {
                if (staticIndex.has(key) && File(sdir, staticIndex.getJSONObject(key).optString("file")).isFile) continue
                val r = get(origin, key, false)
                if (r.code != 200) return TextResult.FAILED
                synchronized(indexLock) {
                    put(sdir, staticIndex, key, r)
                    writeIndex(sdir, staticIndex)
                }
            }
            // Parts first into the index, then the two shell pages: a book with no shell cannot be opened.
            synchronized(indexLock) { writeIndex(dir, merge(keep, fresh)) }
            for (path in listOf("/books/${m.bookId}/read?sheet=contents", "/books/${m.bookId}/read")) {
                val r = get(origin, "/offline/books/${m.bookId}/read" + path.substringAfter("/read"), true)
                if (r.code in 300..399) return TextResult.SIGNIN
                if (r.code != 200) return TextResult.FAILED
                synchronized(indexLock) {
                    val idx = readIndex(dir)
                    put(dir, idx, path, r)
                    writeIndex(dir, idx)
                }
            }
            return TextResult.READY
        } catch (e: IOException) {
            Log.w(TAG, "text: ${e.javaClass.simpleName}")
            return TextResult.FAILED
        }
    }

    private fun merge(old: JSONObject, fresh: JSONObject): JSONObject {
        // Old shell entries stay until the new shell replaces them; old parts the new manifest dropped go with the index.
        val out = JSONObject()
        for (k in old.keys()) if (k.startsWith("/books/") && !k.contains("/images/")) out.put(k, old.get(k))
        for (k in fresh.keys()) out.put(k, fresh.get(k))
        return out
    }

    private fun put(dir: File, index: JSONObject, key: String, r: Reply) {
        val file = name(key)
        val tmp = File(dir, "$file.tmp")
        tmp.writeBytes(r.body)
        if (!tmp.renameTo(File(dir, file))) throw IOException("file not stored")
        index.put(key, JSONObject().put("file", file).put("type", r.type).put("csp", r.csp ?: ""))
    }
}
