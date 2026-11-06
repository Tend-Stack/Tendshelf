package app.tendshelf.shared

import java.net.URLDecoder

sealed interface Nav {
    /** Load it in the WebView. */
    object Stay : Nav

    /** Hand it to the system (browser or mail), leave the WebView where it is. */
    object External : Nav

    /** Do nothing. */
    object Block : Nav

    /** The connect redirect: keep the code, cancel the load. */
    data class TakeCode(val code: String) : Nav

    /** The bundled start page's form: try this address. */
    data class Connect(val address: String) : Nav

    /** The web Downloads address, or the bundled page's own link: open the bundled Downloads page. */
    object Downloads : Nav

    /** The bundled Downloads page's row: play this downloaded book. */
    data class PlayBook(val bookId: String) : Nav

    /** The bundled Downloads page: remove this book from the phone. */
    data class RemoveBook(val bookId: String) : Nav

    /** The bundled Downloads page: pause or resume the playing book. */
    object TogglePlay : Nav
}

const val BUNDLED_HOST = "appassets.androidplatform.net"

/**
 * The one navigation policy of the WebView (plan A2, invariants 1 and 4). Pure: values in, a verdict out.
 * [fromBundled] is true only when the page the person is looking at is one of the bundled pages. The bundled
 * addresses (`/connect`, `/play`, `/remove`, `/toggle`) are commands, so a link in a book or any server page
 * (page text is untrusted) must never reach them: they are obeyed only from the main frame of a bundled page.
 */
fun decide(url: String, mainFrame: Boolean, origin: Origin?, codePending: Boolean, fromBundled: Boolean): Nav {
    val scheme = url.substringBefore(':', "").lowercase()
    if (scheme == "mailto") return Nav.External
    if (scheme != "http" && scheme != "https") return Nav.Block
    val u = parseUrl(url) ?: return Nav.Block
    if (u.scheme == "https" && u.host == BUNDLED_HOST && u.port == null) {
        if (!mainFrame || !fromBundled) return Nav.Block
        if (u.path == "/connect") return Nav.Connect(queryParam(u.query, "address") ?: "")
        if (u.path == "/toggle") return Nav.TogglePlay
        if (u.path == "/play" || u.path == "/remove") {
            // An id outside the bridge's charset is refused, never loaded (A7).
            val id = queryParam(u.query, "id")?.takeIf { validId(it) } ?: return Nav.Block
            return if (u.path == "/play") Nav.PlayBook(id) else Nav.RemoveBook(id)
        }
        return Nav.Stay
    }
    if (origin != null && u.scheme == origin.scheme && u.host == origin.host && u.port == origin.port) {
        if (u.path == "/app/connect/done") {
            val code = queryParam(u.query, "code")
            return if (mainFrame && codePending && !code.isNullOrEmpty()) Nav.TakeCode(code) else Nav.Block
        }
        if (mainFrame && u.path == "/me/downloads") return Nav.Downloads
        return Nav.Stay
    }
    return if (mainFrame) Nav.External else Nav.Block
}

private fun queryParam(query: String?, name: String): String? {
    if (query == null) return null
    for (pair in query.split('&')) {
        val key = pair.substringBefore('=')
        if (key == name) return try { URLDecoder.decode(pair.substringAfter('=', ""), "UTF-8") } catch (_: Exception) { null }
    }
    return null
}
