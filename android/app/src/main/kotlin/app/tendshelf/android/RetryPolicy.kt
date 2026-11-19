package app.tendshelf.android

import androidx.media3.common.C
import androidx.media3.common.ParserException
import androidx.media3.datasource.HttpDataSource
import androidx.media3.exoplayer.upstream.DefaultLoadErrorHandlingPolicy
import androidx.media3.exoplayer.upstream.LoadErrorHandlingPolicy
import app.tendshelf.shared.STREAM_MAX_BUSY_TRIES
import app.tendshelf.shared.STREAM_MAX_IO_TRIES
import app.tendshelf.shared.STREAM_RETRY_MS
import app.tendshelf.shared.streamRetryDelayMs
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
        // The rule is shared with the iPhone app; a 503, a 401 and a lost network follow it, the rest is ExoPlayer's.
        if (e is HttpDataSource.InvalidResponseCodeException) {
            return if (e.responseCode == 503 || e.responseCode == 401) delay(e.responseCode, info.errorCount) else super.getRetryDelayMsFor(info)
        }
        if (e is ParserException) return super.getRetryDelayMsFor(info)
        if (e is IOException) return delay(0, info.errorCount)
        return super.getRetryDelayMsFor(info)
    }

    private fun delay(status: Int, errorCount: Int): Long = streamRetryDelayMs(status, errorCount) ?: C.TIME_UNSET

    companion object {
        const val RETRY_MS = STREAM_RETRY_MS
        const val MAX_BUSY_TRIES = STREAM_MAX_BUSY_TRIES
        const val MAX_IO_TRIES = STREAM_MAX_IO_TRIES
    }
}
