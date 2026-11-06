package app.tendshelf.shared

/**
 * The server addresses the app uses, all under the one origin the person chose. Nothing here contacts
 * another host.
 */
class ApiPaths(server: String) {
    val base: String = server.trim().trimEnd('/')

    val tokenUrl: String get() = "$base/api/v1/auth/token"

    fun bookUrl(bookId: String): String = "$base/api/v1/books/${segment(bookId)}"

    /** [compat] asks for the browser-friendly rendition, for files a phone cannot decode as stored. */
    fun streamUrl(fileId: String, compat: Boolean = false): String =
        "$base/api/v1/stream/${segment(fileId)}" + if (compat) "?compat=1" else ""

    fun coverUrl(bookId: String, width: Int): String = "$base/api/v1/covers/${segment(bookId)}?w=$width"

    val settingsUrl: String get() = "$base/api/v1/settings"

    /** The page the WebView opens: the shell mark makes the server treat the visitor as the Android app. */
    val shellUrl: String get() = "$base/?shell=android&next=/"

    /** JSON body for `POST /api/v1/auth/token` (kind `mobile`). */
    fun tokenRequestBody(username: String, password: String, deviceName: String): String =
        "{\"username\":${jsonString(username)},\"password\":${jsonString(password)}," +
            "\"name\":${jsonString(deviceName)},\"kind\":\"mobile\"}"

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
