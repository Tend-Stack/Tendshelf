package app.tendshelf.shared

/** How long a stream rides out trouble, apart from the player (decision 0048): the same rule on both phone apps. */
const val STREAM_RETRY_MS = 5_000L

/** A 503 means the server is busy preparing a file; the web player waits 10 minutes: 120 tries, 5 s apart. */
const val STREAM_MAX_BUSY_TRIES = 120

/** A lost network gets 2 minutes. */
const val STREAM_MAX_IO_TRIES = 24

/**
 * The wait before the next try of a media request, or null for "give up now". [status] is the HTTP status, 0 for no
 * answer (a network error); [errorCount] counts the tries so far including this one. A 401 is fatal at once, so the
 * app can say "signed out"; any other status is not retried here.
 */
fun streamRetryDelayMs(status: Int, errorCount: Int): Long? = when (status) {
    503 -> if (errorCount <= STREAM_MAX_BUSY_TRIES) STREAM_RETRY_MS else null
    0 -> if (errorCount <= STREAM_MAX_IO_TRIES) STREAM_RETRY_MS else null
    else -> null
}
