package app.tendshelf.android

import android.content.Context
import android.os.Bundle
import android.os.Handler
import androidx.annotation.StringRes
import androidx.media3.common.MediaItem
import androidx.media3.common.MediaMetadata
import androidx.media3.common.util.UnstableApi
import androidx.media3.session.LibraryResult
import androidx.media3.session.MediaConstants
import androidx.media3.session.SessionError
import app.tendshelf.shared.BookCard
import app.tendshelf.shared.Node
import app.tendshelf.shared.OfflineState
import app.tendshelf.shared.Page
import app.tendshelf.shared.nodeId
import app.tendshelf.shared.parseNode
import app.tendshelf.shared.slice
import com.google.common.collect.ImmutableList
import com.google.common.util.concurrent.ListenableFuture
import com.google.common.util.concurrent.SettableFuture
import java.io.IOException
import java.util.concurrent.Executor

/** A failure the car should read in plain words (A8, invariant 4). */
private class CarError(val code: Int, @StringRes val words: Int) : Exception()

/**
 * What the car browses (A8): four tabs (D2) over the server's own lists, books as leaves (D3). Every call runs on the
 * [io] executor and answers within [BUDGET_MS], or with plain words; the car never waits on a spinner. Nothing here
 * decides who may browse: [PlaybackService] asks [app.tendshelf.shared.allowedCommands] before it calls in.
 */
@UnstableApi
class LibraryTree(
    private val context: Context,
    private val connections: () -> ConnectionSource,
    private val io: Executor,
    private val main: Handler,
) {
    private val shelfNames = lru<String, String>(100)
    private val books = lru<String, BookCard>(300)
    private val searches = lru<String, List<BookCard>>(8)

    private fun <K, V> lru(max: Int): MutableMap<K, V> = java.util.Collections.synchronizedMap(
        object : LinkedHashMap<K, V>(16, 0.75f, true) {
            override fun removeEldestEntry(eldest: MutableMap.MutableEntry<K, V>?) = size > max
        },
    )

    private fun words(@StringRes id: Int) = context.getString(id)

    private fun error(e: CarError): SessionError = SessionError(e.code, words(e.words))

    // ---- Items ----

    fun rootItem(): MediaItem = folder(Node.Root, "Tendshelf", browsableStyle = GRID, playableStyle = LIST)

    private fun folder(n: Node, title: String, browsableStyle: Int? = null, playableStyle: Int? = null): MediaItem {
        val extras = Bundle().apply {
            browsableStyle?.let { putInt(MediaConstants.EXTRAS_KEY_CONTENT_STYLE_BROWSABLE, it) }
            playableStyle?.let { putInt(MediaConstants.EXTRAS_KEY_CONTENT_STYLE_PLAYABLE, it) }
        }
        return MediaItem.Builder().setMediaId(nodeId(n)).setMediaMetadata(
            MediaMetadata.Builder().setTitle(title).setIsBrowsable(true).setIsPlayable(false)
                .setMediaType(MediaMetadata.MEDIA_TYPE_FOLDER_MIXED).setExtras(extras).build(),
        ).build()
    }

    fun bookItem(c: BookCard): MediaItem {
        books[c.id] = c
        val md = MediaMetadata.Builder().setTitle(c.title).setArtist(c.author).setIsBrowsable(false).setIsPlayable(true)
            .setMediaType(MediaMetadata.MEDIA_TYPE_AUDIO_BOOK)
        if (c.durationMs > 0) md.setDurationMs(c.durationMs)
        if (c.hasCover) md.setArtworkUri(CoverProvider.uriOf(c.id))
        return MediaItem.Builder().setMediaId(nodeId(Node.Book(c.id))).setMediaMetadata(md.build()).build()
    }

    private fun tabs(): List<MediaItem> = listOf(
        folder(Node.Continue, words(R.string.car_continue), playableStyle = LIST),
        folder(Node.UpNext, words(R.string.car_up_next), playableStyle = LIST),
        folder(Node.Shelves, words(R.string.car_shelves), browsableStyle = GRID, playableStyle = LIST),
        folder(Node.Downloaded, words(R.string.car_downloaded), playableStyle = GRID),
    )

    // ---- Calls ----

    fun children(parentId: String, page: Int, size: Int): ListenableFuture<LibraryResult<ImmutableList<MediaItem>>> {
        val node = parseNode(parentId)
        if (node == null || node is Node.Book) {
            return SettableFuture.create<LibraryResult<ImmutableList<MediaItem>>>().also {
                it.set(LibraryResult.ofError(SessionError(SessionError.ERROR_BAD_VALUE, words(R.string.car_not_found))))
            }
        }
        return guarded { LibraryResult.ofItemList(paged(load(node), Page(page, size)), null) }
    }

    fun item(mediaId: String): ListenableFuture<LibraryResult<MediaItem>> {
        val node = parseNode(mediaId)
            ?: return SettableFuture.create<LibraryResult<MediaItem>>().also {
                it.set(LibraryResult.ofError(SessionError(SessionError.ERROR_BAD_VALUE, words(R.string.car_not_found))))
            }
        return guarded { LibraryResult.ofItem(itemOf(node), null) }
    }

    fun search(query: String, page: Int, size: Int): ListenableFuture<LibraryResult<ImmutableList<MediaItem>>> =
        guarded { LibraryResult.ofItemList(paged(found(query).map(::bookItem), Page(page, size)), null) }

    /** Runs the search once; [onDone] gets how many books matched. The result stays for [search]. */
    fun runSearch(query: String, onDone: (Int) -> Unit) {
        io.execute {
            val n = try {
                found(query).size
            } catch (e: Exception) {
                0
            }
            main.post { onDone(n) }
        }
    }

    /** Voice: the first book for [query], or null. Calls [onDone] on the main thread. */
    fun voice(query: String, onDone: (MediaItem?) -> Unit) {
        val answered = java.util.concurrent.atomic.AtomicBoolean()
        val finish = { item: MediaItem? -> if (answered.compareAndSet(false, true)) onDone(item) }
        main.postDelayed({ finish(null) }, BUDGET_MS)
        io.execute {
            val item = try {
                found(query, 1).firstOrNull()?.let(::bookItem)
            } catch (e: Exception) {
                null
            }
            main.post { finish(item) }
        }
    }

    fun item(id: String, onDone: (MediaItem?) -> Unit) {
        io.execute {
            val item = try {
                itemOf(Node.Book(id))
            } catch (e: Exception) {
                null
            }
            main.post { onDone(item) }
        }
    }

    // ---- Work, on the io executor ----

    private fun <T : Any> guarded(work: () -> LibraryResult<T>): ListenableFuture<LibraryResult<T>> {
        val future = SettableFuture.create<LibraryResult<T>>()
        val late = Runnable { future.set(LibraryResult.ofError(error(CarError(SessionError.ERROR_IO, R.string.car_no_connection)))) }
        main.postDelayed(late, BUDGET_MS)
        io.execute {
            val result = try {
                work()
            } catch (e: CarError) {
                LibraryResult.ofError(error(e))
            } catch (e: Exception) {
                android.util.Log.w(PlaybackService.TAG, "browse failed: ${e.javaClass.simpleName}")
                LibraryResult.ofError(error(CarError(SessionError.ERROR_IO, R.string.car_no_connection)))
            }
            main.removeCallbacks(late)
            future.set(result)
        }
        return future
    }

    private fun paged(all: List<MediaItem>, p: Page): List<MediaItem> {
        val r = slice(all.size, p)
        return if (r.isEmpty()) emptyList() else all.slice(r)
    }

    private fun connection(): Connection = connections().current() ?: throw CarError(SessionError.ERROR_SESSION_AUTHENTICATION_EXPIRED, R.string.car_sign_in)

    /** Runs a server call. A refused token is signed out; a missing network is [offline]; anything else is plain words. */
    private fun <T> online(offline: (() -> T)?, call: (Api, String) -> T): T {
        val conn = connection()
        try {
            return call(Api(conn.origin), conn.token)
        } catch (e: ApiException) {
            if (e.code == 401) throw CarError(SessionError.ERROR_SESSION_AUTHENTICATION_EXPIRED, R.string.car_sign_in)
            throw CarError(SessionError.ERROR_IO, R.string.car_no_connection)
        } catch (e: IOException) {
            if (offline != null) return offline()
            throw CarError(SessionError.ERROR_IO, R.string.car_no_connection)
        }
    }

    private fun load(node: Node): List<MediaItem> {
        connection() // signed out: words, even for the tabs
        return when (node) {
            Node.Root -> tabs()
            Node.Continue -> online({ downloadedBooks().filter { placeOf(it.id) > 0 }.sortedByDescending { placeAt(it.id) } }) { api, t -> api.continueList(t) }.map(::bookItem)
            Node.UpNext -> online(null) { api, t -> api.queue(t) }.map(::bookItem)
            Node.Shelves -> online(null) { api, t -> api.shelves(t) }.map { s ->
                shelfNames[s.id] = s.name
                folder(Node.Shelf(s.id), s.name, playableStyle = LIST)
            }
            is Node.Shelf -> online(null) { api, t -> api.shelf(t, node.id) }.map(::bookItem)
            Node.Downloaded -> downloadedBooks().map(::bookItem)
            is Node.Book -> emptyList()
        }
    }

    private fun itemOf(node: Node): MediaItem = when (node) {
        Node.Root -> rootItem()
        Node.Continue -> tabs()[0]
        Node.UpNext -> tabs()[1]
        Node.Shelves -> tabs()[2]
        Node.Downloaded -> tabs()[3]
        is Node.Shelf -> {
            var name = shelfNames[node.id]
            if (name == null) {
                load(Node.Shelves)
                name = shelfNames[node.id]
            }
            folder(node, name ?: throw CarError(SessionError.ERROR_BAD_VALUE, R.string.car_not_found), playableStyle = LIST)
        }
        is Node.Book -> bookItem(
            books[node.id] ?: downloadedBooks().firstOrNull { it.id == node.id }
                ?: online(null) { api, t -> api.manifest(t, node.id) }.let { BookCard(it.id, it.title, it.author, it.durationMs, it.coverUrl.isNotEmpty()) },
        )
    }

    private fun found(query: String, limit: Int = 20): List<BookCard> {
        val key = "$limit:$query"
        searches[key]?.let { return it }
        val q = query.trim()
        val list = if (q.isEmpty()) emptyList() else online({ downloadedBooks().filter { it.title.contains(q, ignoreCase = true) || it.author.contains(q, ignoreCase = true) } }) { api, t -> api.search(t, q, limit) }
        searches[key] = list
        return list
    }

    // ---- Downloaded books (A7's store): no network, the signed-in person's only ----

    private fun mine() = Offline.store.all().filter {
        (it.state == OfflineState.READY || it.state == OfflineState.STALE) && (TokenStore(context).userId.let { id -> id == null || id == it.userId })
    }

    private fun downloadedBooks(): List<BookCard> {
        Offline.init(context)
        return mine().sortedBy { it.title.lowercase() }.map { BookCard(it.bookId, it.title, it.author, it.durationMs, Offline.coverBytes(it.bookId) != null) }
    }

    private fun placeOf(id: String): Long = Offline.store.get(id)?.placeMs ?: 0
    private fun placeAt(id: String): Long = Offline.store.get(id)?.placeAt ?: 0

    companion object {
        const val BUDGET_MS = 5_000L
        private const val LIST = MediaConstants.EXTRAS_VALUE_CONTENT_STYLE_LIST_ITEM
        private const val GRID = MediaConstants.EXTRAS_VALUE_CONTENT_STYLE_GRID_ITEM
    }
}
