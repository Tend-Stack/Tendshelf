package app.tendshelf.android

import android.content.ComponentName
import android.content.Context
import android.content.Intent
import android.util.Log
import androidx.media3.common.MediaItem
import androidx.media3.common.util.UnstableApi
import androidx.media3.session.MediaBrowser
import androidx.media3.session.SessionCommand
import androidx.media3.session.SessionToken
import androidx.core.content.ContextCompat

/**
 * Debug builds only: a Media3 browser inside the app, so the car's tree is tested before any head unit exists (A8).
 * `am broadcast -a app.tendshelf.DEBUG_READ --es op <op> ...`, answers in logcat under tag TsBrowse:
 *   browse --es node <id> [--ei page n --ei size n]   children of a node
 *   item --es node <id>                                one item
 *   search --es q <text>                               onSearch then the result
 *   voice --es q <text>                                a voice query: starts the first match
 *   play --es node book:<id>                           what a tap in the car does
 *   speed                                              the speed button's command
 *   buttons                                            the buttons and commands the controller was given
 *   cover --es node book:<id>                          reads the cover through the provider as this app
 */
@UnstableApi
object DebugBrowse {
    private const val TAG = "TsBrowse"
    val OPS = setOf("browse", "item", "search", "voice", "play", "speed", "buttons", "cover")

    fun run(context: Context, intent: Intent) {
        val app = context.applicationContext
        val op = intent.getStringExtra("op") ?: return
        val node = intent.getStringExtra("node").orEmpty()
        val q = intent.getStringExtra("q").orEmpty()
        val main = ContextCompat.getMainExecutor(app)
        val token = SessionToken(app, ComponentName(app, PlaybackService::class.java))
        val future = MediaBrowser.Builder(app, token).buildAsync()
        future.addListener({
            val b = try {
                future.get()
            } catch (e: Exception) {
                Log.i(TAG, "$op connect failed ${e.cause?.javaClass?.simpleName}")
                return@addListener
            }
            fun done() = b.release()
            fun items(label: String, r: androidx.media3.session.LibraryResult<com.google.common.collect.ImmutableList<MediaItem>>) {
                val v = r.value
                Log.i(
                    TAG,
                    "$label code=${r.resultCode} err=${r.sessionError?.message} n=${v?.size} " +
                        "titles=${v?.map { it.mediaMetadata.title }} ids=${v?.map { it.mediaId }} " +
                        "art=${v?.map { it.mediaMetadata.artworkUri }}",
                )
                done()
            }
            when (op) {
                "browse" -> b.getChildren(node, intent.getIntExtra("page", 0), intent.getIntExtra("size", 50), null).let { f ->
                    f.addListener({ items("browse $node", f.get()) }, main)
                }
                "item" -> b.getItem(node).let { f ->
                    f.addListener({
                        val r = f.get()
                        Log.i(TAG, "item $node code=${r.resultCode} err=${r.sessionError?.message} title=${r.value?.mediaMetadata?.title} playable=${r.value?.mediaMetadata?.isPlayable}")
                        done()
                    }, main)
                }
                "search" -> b.search(q, null).let { f ->
                    f.addListener({
                        Log.i(TAG, "search '$q' accepted code=${f.get().resultCode}")
                        // The result arrives when the service says so; asking straight away reads the same cache.
                        val g = b.getSearchResult(q, 0, 50, null)
                        g.addListener({ items("search '$q'", g.get()) }, main)
                    }, main)
                }
                "voice" -> {
                    b.setMediaItem(MediaItem.Builder().setRequestMetadata(MediaItem.RequestMetadata.Builder().setSearchQuery(q).build()).build())
                    b.prepare()
                    b.play()
                    main.execute { Log.i(TAG, "voice '$q' sent"); b.release() }
                }
                "play" -> {
                    b.setMediaItem(MediaItem.Builder().setMediaId(node).build())
                    b.prepare()
                    b.play()
                    Log.i(TAG, "play $node sent")
                    main.execute { b.release() }
                }
                "speed" -> b.sendCustomCommand(PlaybackService.SPEED_COMMAND, android.os.Bundle.EMPTY).let { f ->
                    f.addListener({ Log.i(TAG, "speed code=${f.get().resultCode}"); done() }, main)
                }
                "buttons" -> {
                    Log.i(
                        TAG,
                        "buttons ${b.mediaButtonPreferences.map { "${it.displayName}/${it.sessionCommand?.customAction}/${it.playerCommand}" }} " +
                            "next=${b.isCommandAvailable(androidx.media3.common.Player.COMMAND_SEEK_TO_NEXT)} prev=${b.isCommandAvailable(androidx.media3.common.Player.COMMAND_SEEK_TO_PREVIOUS)}",
                    )
                    done()
                }
                "cover" -> {
                    val id = node.removePrefix("book:")
                    val n = try {
                        app.contentResolver.openInputStream(CoverProvider.uriOf(id))?.use { it.readBytes().size }
                    } catch (e: Exception) {
                        -1
                    }
                    Log.i(TAG, "cover $id bytes=$n")
                    done()
                }
                else -> done()
            }
        }, main)
    }
}
