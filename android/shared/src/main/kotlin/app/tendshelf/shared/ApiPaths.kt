package app.tendshelf.shared

/**
 * The server addresses the app uses, all under the one origin the person chose. Nothing here contacts
 * another host.
 */
class ApiPaths(server: String) {
    val base: String = server.trim().trimEnd('/')

    fun bookUrl(bookId: String): String = "$base/api/v1/books/${segment(bookId)}"

    /** [compat] asks for the browser-friendly rendition, for files a phone cannot decode as stored. */
    fun streamUrl(fileId: String, compat: Boolean = false): String =
        "$base/api/v1/stream/${segment(fileId)}" + if (compat) "?compat=1" else ""

    fun coverUrl(bookId: String, width: Int): String = "$base/api/v1/covers/${segment(bookId)}?w=$width"

    val settingsUrl: String get() = "$base/api/v1/settings"

    val batchUrl: String get() = "$base/api/v1/progress/batch"
    val queueNextUrl: String get() = "$base/api/v1/queue/next"

    fun progressUrl(bookId: String): String = "$base/api/v1/progress/${segment(bookId)}"

    /** The page the WebView opens: the shell mark makes the server treat the visitor as the Android app. */
    val shellUrl: String get() = "$base/?shell=android&next=/"

    private fun segment(id: String): String {
        require(id.isNotEmpty() && id.none { it == '/' || it == '?' || it == '#' || it.isWhitespace() }) { "bad id" }
        return id
    }
}

/** Quotes a string as JSON. */
fun jsonString(s: String): String {
    val out = StringBuilder("\"")
    for (c in s) {
        when {
            c == '"' -> out.append("\\\"")
            c == '\\' -> out.append("\\\\")
            c == '\n' -> out.append("\\n")
            c == '\r' -> out.append("\\r")
            c == '\t' -> out.append("\\t")
            c < ' ' -> out.append("\\u%04x".format(c.code))
            else -> out.append(c)
        }
    }
    return out.append('"').toString()
}

/** JSON body for `POST /api/v1/progress/batch`; the device token supplies the device id. */
fun batchBody(saves: List<Save>): String = saves.joinToString(",", "{\"updates\":[", "]}") {
    "{\"bookId\":${jsonString(it.bookId)},\"positionMs\":${it.positionMs},\"clientUpdatedAt\":${it.clientUpdatedAt}," +
        "\"finished\":${it.finished},\"force\":${it.force},\"speed\":${it.speed},\"listenedMs\":${it.listenedMs}}"
}

/** JSON body for `POST /api/v1/queue/next` once [endedBookId] is over; the series is tried when the queue is empty. */
fun queueNextBody(endedBookId: String): String = "{\"ended\":${jsonString(endedBookId)},\"series\":true}"
