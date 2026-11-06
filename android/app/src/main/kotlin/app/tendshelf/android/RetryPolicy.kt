package app.tendshelf.android

import androidx.media3.common.C
import androidx.media3.common.ParserException
import androidx.media3.datasource.HttpDataSource
import androidx.media3.exoplayer.upstream.DefaultLoadErrorHandlingPolicy
import androidx.media3.exoplayer.upstream.LoadErrorHandlingPolicy
import java.io.IOException

/**
 * How long a stream rides out trouble. A 503 means the server is busy preparing a file (the web player
 * waits 10 minutes: 120 tries, 5 s apart); a lost network gets 2 minutes; a 401 means the token is gone
 * and is fatal at once, so the service can say "signed out".
 */
class RetryPolicy : DefaultLoadErrorHandlingPolicy() {
    override fun getMinimumLoadableRetryCount(dataType: Int): Int = MAX_BUSY_TRIES

    override fun getRetryDelayMsFor(info: LoadErrorHandlingPolicy.LoadErrorInfo): Long {
        val e = info.exception
        if (e is HttpDataSource.InvalidResponseCodeException) {
            return when (e.responseCode) {
                503 -> if (info.errorCount <= MAX_BUSY_TRIES) RETRY_MS else C.TIME_UNSET
                401 -> C.TIME_UNSET
                else -> super.getRetryDelayMsFor(info)
            }
        }
        if (e is ParserException) return super.getRetryDelayMsFor(info)
        if (e is IOException) return if (info.errorCount <= MAX_IO_TRIES) RETRY_MS else C.TIME_UNSET
        return super.getRetryDelayMsFor(info)
    }

    companion object {
        const val RETRY_MS = 5_000L
        const val MAX_BUSY_TRIES = 120
        const val MAX_IO_TRIES = 24
    }
}
