package app.tendshelf.android

import androidx.media3.common.MediaItem
import androidx.media3.common.util.UnstableApi
import androidx.media3.datasource.DataSource
import androidx.media3.exoplayer.source.ConcatenatingMediaSource2
import androidx.media3.exoplayer.source.MediaSource
import androidx.media3.exoplayer.source.ProgressiveMediaSource
import androidx.media3.exoplayer.source.SilenceMediaSource
import androidx.media3.extractor.DefaultExtractorsFactory
import app.tendshelf.shared.ApiPaths
import app.tendshelf.shared.BookManifest

/**
 * A book as one Media3 window (decision D1): the files are concatenated, so the player's position is the
 * book position and a seek can land in a part that is not open yet. A file the server lacks becomes silence
 * of its length (D2), which keeps every later part at its true book time.
 */
@UnstableApi
object BookMedia {
    fun build(manifest: BookManifest, paths: ApiPaths, factory: DataSource.Factory): MediaSource {
        // Files without a Xing header seek by constant bitrate (D8); measured in the acceptance run.
        val extractors = DefaultExtractorsFactory().setConstantBitrateSeekingEnabled(true)
        val sources = ProgressiveMediaSource.Factory(factory, extractors).setLoadErrorHandlingPolicy(RetryPolicy())
        val whole = ConcatenatingMediaSource2.Builder()
            .setMediaItem(MediaItem.Builder().setMediaId(manifest.id).build())
        for (f in manifest.files.sortedBy { it.idx }) {
            if (f.playable) {
                // A file the phone cannot decode as stored is always asked for in the browser's rendition (D6).
                val item = MediaItem.Builder().setMediaId(f.id).setUri(paths.streamUrl(f.id, f.needsCompat)).build()
                whole.add(sources.createMediaSource(item), f.durationMs)
            } else {
                whole.add(SilenceMediaSource.Factory().setDurationUs(f.durationMs * 1000).createMediaSource(), f.durationMs)
            }
        }
        return whole.build()
    }
}
