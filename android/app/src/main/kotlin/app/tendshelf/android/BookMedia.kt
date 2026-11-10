package app.tendshelf.android

import androidx.media3.common.MediaItem
import androidx.media3.common.MediaMetadata
import androidx.media3.common.util.UnstableApi
import androidx.media3.datasource.DataSource
import androidx.media3.exoplayer.source.ConcatenatingMediaSource2
import androidx.media3.exoplayer.source.MediaSource
import androidx.media3.exoplayer.source.ProgressiveMediaSource
import androidx.media3.exoplayer.source.SilenceMediaSource
import androidx.media3.extractor.DefaultExtractorsFactory
import app.tendshelf.shared.ApiPaths
import app.tendshelf.shared.BookManifest
import app.tendshelf.shared.EpisodeDoc
import app.tendshelf.shared.cacheKey

/**
 * A book as one Media3 window (decision D1): the files are concatenated, so the player's position is the
 * book position and a seek can land in a part that is not open yet. A file the server lacks becomes silence
 * of its length (D2), which keeps every later part at its true book time.
 */
@UnstableApi
object BookMedia {
    fun build(manifest: BookManifest, paths: ApiPaths, network: DataSource.Factory, downloaded: Boolean = false): MediaSource {
        // A downloaded book reads its files from the phone first; only such a book touches the cache, so another
        // person's streamed book can never be answered from bytes that person did not ask the server for.
        val factory = if (downloaded) Offline.readFactory(network) else network
        // Files without a Xing header seek by constant bitrate (D8); measured in the acceptance run.
        val extractors = DefaultExtractorsFactory().setConstantBitrateSeekingEnabled(true)
        val sources = ProgressiveMediaSource.Factory(factory, extractors).setLoadErrorHandlingPolicy(RetryPolicy())
        val whole = ConcatenatingMediaSource2.Builder()
            .setMediaItem(MediaItem.Builder().setMediaId(manifest.id).build())
        for (f in manifest.files.sortedBy { it.idx }) {
            if (f.playable) {
                // A file the phone cannot decode as stored is always asked for in the browser's rendition (D6).
                val item = MediaItem.Builder().setMediaId(f.id).setUri(paths.streamUrl(f.id, f.needsCompat))
                    .setCustomCacheKey(if (downloaded) cacheKey(f.id, f.needsCompat) else null).build()
                whole.add(sources.createMediaSource(item), f.durationMs)
            } else {
                whole.add(SilenceMediaSource.Factory().setDurationUs(f.durationMs * 1000).createMediaSource(), f.durationMs)
            }
        }
        return whole.build()
    }

    /**
     * A podcast episode as a one-file item (A1): the media id is the episode's `pe_` id, the address is the server's own
     * audio route. The server passes one `Range` on to the publisher, so a seek or a resume does not download what comes before.
     */
    fun episode(doc: EpisodeDoc, url: String, network: DataSource.Factory, downloaded: Boolean = false): MediaSource {
        // Podcast files are often VBR MP3 without a Xing header; constant-bitrate seeking lands close enough and never fails.
        val extractors = DefaultExtractorsFactory().setConstantBitrateSeekingEnabled(true)
        // A downloaded episode is read from the phone first, under the key it was stored with (its id); only such an episode
        // touches the cache, so a streamed one is never answered from bytes that person did not ask the server for.
        val factory = if (downloaded) Offline.readFactory(network) else network
        val item = MediaItem.Builder().setMediaId(doc.id).setUri(url).setCustomCacheKey(if (downloaded) doc.id else null)
            .setMediaMetadata(MediaMetadata.Builder().setTitle(doc.title).setAlbumTitle(doc.show).setArtist(doc.show).setIsPlayable(true).build())
        if (doc.mime.startsWith("audio/")) item.setMimeType(doc.mime)
        return ProgressiveMediaSource.Factory(factory, extractors).setLoadErrorHandlingPolicy(RetryPolicy()).createMediaSource(item.build())
    }

    /**
     * A live station (R3): the server's relay as an endless one-file item. A failure is fatal at once (no load retry), because
     * the engine owns the retry rules (`liveRetryDelayMs`): a busy relay is tried again a few times, then the person is told.
     * Nothing seeks or caches: the item has no length, so the player treats it as live.
     */
    fun station(id: String, url: String, name: String, network: DataSource.Factory): MediaSource {
        val item = MediaItem.Builder().setMediaId(id).setUri(url)
            .setMediaMetadata(MediaMetadata.Builder().setTitle(name).setIsPlayable(true).build()).build()
        return ProgressiveMediaSource.Factory(network, DefaultExtractorsFactory()).setLoadErrorHandlingPolicy(LivePolicy).createMediaSource(item)
    }

    /**
     * What stands in the player while a station is paused (R3): the relay is let go so nothing buffers behind the live edge and
     * the server's slot is free, yet the player stays ready, so the notification and the car keep their Play button.
     */
    fun pausedLive(): MediaSource = SilenceMediaSource.Factory().setDurationUs(PAUSED_LIVE_US).createMediaSource()

    private const val PAUSED_LIVE_US = 3_600_000_000L

    private object LivePolicy : androidx.media3.exoplayer.upstream.DefaultLoadErrorHandlingPolicy() {
        override fun getMinimumLoadableRetryCount(dataType: Int): Int = 0
        override fun getRetryDelayMsFor(loadErrorInfo: androidx.media3.exoplayer.upstream.LoadErrorHandlingPolicy.LoadErrorInfo): Long = androidx.media3.common.C.TIME_UNSET
    }
}
