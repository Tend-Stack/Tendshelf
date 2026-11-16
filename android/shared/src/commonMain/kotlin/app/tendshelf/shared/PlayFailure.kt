package app.tendshelf.shared

/**
 * The one place that turns a playback failure into the small closed set of words the page, the car and the lock screen
 * understand. [status] is the HTTP status the server answered (0 when nothing answered), [otherServer] is true when the
 * app refused to send its token because the address no longer answers as the server it was connected to (ServerGate),
 * and [live] is true for a radio station.
 *
 * - `otherServer`: a different Tendshelf (or nothing that proves itself) answers at the connected address.
 * - `signedOut`: 401, the server no longer knows this phone's token.
 * - `book`: 403, 404 or 410, the title is gone or not allowed.
 * - `busy`: 503 from a station (the one-live-per-person or server-slot limit).
 * - `stream`: everything else (no network, a timeout, a server error).
 */
fun playFailureKind(status: Int, otherServer: Boolean, live: Boolean = false): String = when {
    otherServer -> "otherServer"
    status == 401 -> "signedOut"
    status == 403 || status == 404 || status == 410 -> "book"
    live && status == 503 -> "busy"
    else -> "stream"
}

/** The codes the page may be told about (`background` is native-only and crosses as its own code). */
val ERROR_CODES: Set<String> = setOf("stream", "book", "signedOut", "busy", "otherServer", "background")
