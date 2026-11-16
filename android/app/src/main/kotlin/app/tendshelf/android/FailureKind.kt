package app.tendshelf.android

import androidx.media3.common.util.UnstableApi
import androidx.media3.datasource.HttpDataSource
import app.tendshelf.shared.playFailureKind

/**
 * The error word for any failure: walks the cause chain for the HTTP status (a server answer from our own [Api] or from
 * the player's data source) and for [OtherServerException], then lets the one shared mapping decide.
 */
@UnstableApi
fun failureKindOf(e: Throwable, live: Boolean = false): String {
    var status = 0
    var other = false
    var c: Throwable? = e
    while (c != null) {
        if (c is OtherServerException) other = true
        if (status == 0 && c is ApiException) status = c.code
        if (status == 0 && c is HttpDataSource.InvalidResponseCodeException) status = c.responseCode
        c = c.cause
    }
    return playFailureKind(status, other, live)
}
