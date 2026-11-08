package app.tendshelf.shared

/**
 * What the app may answer from its stored text when the server cannot be reached (plan A9, decision 0019 point 4).
 * The worker's part, played in native code: pure, so the table of addresses is tested without a phone.
 */
sealed interface OfflineRoute {
    /** The reader page of a book (`/books/{id}/read`, or its contents sheet). */
    data class Shell(val bookId: String, val key: String) : OfflineRoute

    /** One part of one chapter, as the manifest lists it. */
    data class Part(val bookId: String, val key: String) : OfflineRoute

    /** A picture of an EPUB. */
    data class Image(val bookId: String, val key: String) : OfflineRoute

    /** A hashed static file the stored shell names. */
    data class Static(val key: String) : OfflineRoute
}

/** The stored key of an address: its path and query, exactly as sent (order and encoding kept). */
fun storeKey(pathAndQuery: String): String = pathAndQuery

private const val CONTENTS_KEY = "sheet=contents"

/**
 * The route for [url], or null: another origin (scheme, host or port), a userinfo, an unknown path, a download link
 * (`download=1`), a reader sheet the stored shell does not have, or an id outside the bridge's charset. Callers must only
 * pass GET requests. `/books/{id}/read?ch=2` maps to the shell, because the shell reads its own query.
 */
fun offlineRoute(url: String, origin: Origin): OfflineRoute? {
    val u = parseUrl(url) ?: return null
    if (u.scheme != origin.scheme || u.host != origin.host || u.port != origin.port) return null
    val query = u.query
    val params = query?.split('&').orEmpty()
    if ("download=1" in params) return null
    val path = u.path
    val withQuery = if (query == null) path else "$path?$query"
    val seg = path.split('/')
    // ["", "static", ...]
    if (seg.size >= 3 && seg[0].isEmpty() && seg[1] == "static") {
        if (seg.drop(2).any { it.isEmpty() || it == "." || it == ".." || "%" in it }) return null
        return OfflineRoute.Static(storeKey(path))
    }
    // ["", "books", id, "read"] and ["", "books", id, "read", "images", block]
    if (seg.size >= 4 && seg[0].isEmpty() && seg[1] == "books" && seg[3] == "read" && validId(seg[2])) {
        val id = seg[2]
        if (seg.size == 4) {
            val sheets = params.filter { it.startsWith("sheet=") }
            if (sheets.isEmpty()) return OfflineRoute.Shell(id, "/books/$id/read")
            return if (sheets == listOf(CONTENTS_KEY)) OfflineRoute.Shell(id, "/books/$id/read?$CONTENTS_KEY") else null
        }
        if (seg.size == 6 && seg[4] == "images" && seg[5].isNotEmpty() && "%" !in seg[5]) return OfflineRoute.Image(id, storeKey(withQuery))
        return null
    }
    // ["", "hx", "books", id, "read", "chapters", n]
    if (seg.size == 7 && seg[0].isEmpty() && seg[1] == "hx" && seg[2] == "books" && validId(seg[3]) && seg[4] == "read" &&
        seg[5] == "chapters" && seg[6].isNotEmpty() && seg[6].all { it in '0'..'9' }
    ) return OfflineRoute.Part(seg[3], storeKey(withQuery))
    return null
}
