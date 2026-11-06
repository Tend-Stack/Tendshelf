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
}

const val BUNDLED_HOST = "appassets.androidplatform.net"

/** The one navigation policy of the WebView (plan A2, invariants 1 and 4). Pure: values in, a verdict out. */
fun decide(url: String, mainFrame: Boolean, origin: Origin?, codePending: Boolean): Nav {
    val scheme = url.substringBefore(':', "").lowercase()
    if (scheme == "mailto") return Nav.External
    if (scheme != "http" && scheme != "https") return Nav.Block
    val u = parseUrl(url) ?: return Nav.Block
    if (u.scheme == "https" && u.host == BUNDLED_HOST && u.port == null) {
        if (u.path == "/connect") return Nav.Connect(queryParam(u.query, "address") ?: "")
        return Nav.Stay
    }
    if (origin != null && u.scheme == origin.scheme && u.host == origin.host && u.port == origin.port) {
        if (u.path == "/app/connect/done") {
            val code = queryParam(u.query, "code")
            return if (mainFrame && codePending && !code.isNullOrEmpty()) Nav.TakeCode(code) else Nav.Block
        }
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
