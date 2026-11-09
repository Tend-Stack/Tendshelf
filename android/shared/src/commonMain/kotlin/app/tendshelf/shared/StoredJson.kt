package app.tendshelf.shared

import kotlinx.serialization.json.JsonObject

/*
 * JSON the app keeps on the phone: the settings kept for an offline start and the index of stored reading text.
 * Strings written by earlier app versions (org.json) must still load, so the readers accept what org.json wrote
 * (whole numbers without ".0", "\/" escapes) and the writers keep the same key order and the same shapes.
 */

/** The default settings of a phone that has never heard from the server. */
fun defaultPlayerSettings(): PlayerSettings = PlayerSettings(1.0, 30, 30, true, false)

/** A whole number prints without ".0", as org.json printed it; anything else as Kotlin prints a Double. */
private fun numberText(d: Double): String =
    if (d == d.toLong().toDouble() && d > -1e15 && d < 1e15) d.toLong().toString() else d.toString()

/** Settings as kept for an offline start. The rewind table is not kept: the built-in one serves offline. */
fun encodeKeptSettings(s: PlayerSettings): String =
    "{\"speed\":${numberText(s.speed)},\"skipBackS\":${s.skipBackS},\"skipForwardS\":${s.skipForwardS}," +
        "\"autoRewind\":${s.autoRewind},\"nextInSeries\":${s.nextInSeries},\"skipQuiet\":${s.skipQuiet},\"autoDownload\":${s.autoDownload}}"

/** The kept settings; the built-in ones when there are none or they cannot be read. */
fun decodeKeptSettings(text: String?): PlayerSettings {
    if (text == null) return defaultPlayerSettings()
    return try {
        val j = parseJsonObject(text)
        PlayerSettings(
            j.getDouble("speed"), j.getInt("skipBackS"), j.getInt("skipForwardS"), j.getBoolean("autoRewind"), j.getBoolean("nextInSeries"),
            skipQuiet = j.optBoolean("skipQuiet", false),
            autoDownload = j.optBoolean("autoDownload", false),
        )
    } catch (_: Exception) {
        defaultPlayerSettings()
    }
}

/** The text manifest of a downloaded book (`/api/v1/books/{id}/read/manifest`). */
class TextManifest(
    val bookId: String,
    val editionId: String,
    val revision: String,
    val title: String,
    val author: String,
    val bytes: Long,
    val parts: List<String>,
    val images: List<String>,
    val assets: List<String>,
) {
    companion object {
        /** Throws [JsonShapeException] for a manifest without `bookId` or `revision`, or a list holding a non-value. */
        fun parse(text: String): TextManifest {
            val j = parseJsonObject(text)
            fun list(name: String) = j.optArray(name)?.let { a -> (0 until a.size).map { a.getString(it) } }.orEmpty()
            return TextManifest(
                j.getString("bookId"), j.optString("editionId"), j.getString("revision"), j.optString("title"), j.optString("author"),
                j.optLong("bytes"), list("parts"), list("images"), list("assets"),
            )
        }
    }
}

/** One answer kept on disk: the file's name, its content type, and the Content-Security-Policy (empty when none). */
class TextIndexEntry(val file: String, val type: String, val csp: String)

/**
 * `index.json` of a stored-text folder: address to [TextIndexEntry], in the order written. Reading never throws:
 * text that is not an object is an empty index, and a value that is not an object is not an entry.
 */
class TextIndex private constructor(private val entries: LinkedHashMap<String, TextIndexEntry>) {
    constructor() : this(LinkedHashMap())

    val keys: List<String> get() = entries.keys.toList()
    operator fun contains(key: String): Boolean = entries.containsKey(key)
    operator fun get(key: String): TextIndexEntry? = entries[key]
    operator fun set(key: String, entry: TextIndexEntry) {
        entries[key] = entry
    }
    fun remove(key: String) {
        entries.remove(key)
    }

    /** Old shell entries stay until the new shell replaces them; old parts the new manifest dropped go. */
    fun mergedWith(fresh: TextIndex): TextIndex {
        val out = LinkedHashMap<String, TextIndexEntry>()
        for ((k, v) in entries) if (k.startsWith("/books/") && !k.contains("/images/")) out[k] = v
        for ((k, v) in fresh.entries) out[k] = v
        return TextIndex(out)
    }

    fun toJson(): String = entries.entries.joinToString(",", "{", "}") { (k, e) ->
        "${jsonString(k)}:{\"file\":${jsonString(e.file)},\"type\":${jsonString(e.type)},\"csp\":${jsonString(e.csp)}}"
    }

    companion object {
        fun parse(text: String): TextIndex {
            val out = LinkedHashMap<String, TextIndexEntry>()
            val j: JsonObject = try {
                parseJsonObject(text)
            } catch (_: Exception) {
                return TextIndex(out)
            }
            for ((k, v) in j) {
                val o = v as? JsonObject ?: continue
                out[k] = TextIndexEntry(o.optString("file"), o.optString("type", "application/octet-stream"), o.optString("csp"))
            }
            return TextIndex(out)
        }
    }
}
