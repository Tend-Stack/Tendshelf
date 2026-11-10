package app.tendshelf.android

import android.content.ContentProvider
import android.content.ContentValues
import android.database.Cursor
import android.net.Uri
import android.os.Binder
import android.os.ParcelFileDescriptor
import android.os.Process
import androidx.media3.common.util.UnstableApi
import app.tendshelf.shared.isStationId
import app.tendshelf.shared.validId
import java.io.File
import java.io.FileNotFoundException
import java.util.concurrent.ConcurrentHashMap

/**
 * Who the car side let in (A8, D1 and D6). [PlaybackService] adds a caller's uid when a trusted controller connects
 * and takes it away when it disconnects; [CoverProvider] answers only those uids. In memory on purpose: a process that
 * died knows nobody, and the car connects again before it asks for a picture.
 */
object CarAccess {
    private val uids = ConcurrentHashMap<Int, Int>()

    fun add(uid: Int) {
        uids.merge(uid, 1, Int::plus)
    }

    fun remove(uid: Int) {
        uids.computeIfPresent(uid) { _, n -> if (n <= 1) null else n - 1 }
    }

    fun allowed(uid: Int): Boolean = uid == Process.myUid() || uids.containsKey(uid)

    fun clear() = uids.clear()
}

/**
 * Covers for the car: `content://app.tendshelf.android.covers/<bookId>`. Read-only, a plain file descriptor, no token
 * in the address. The provider is exported because a car's process reads it, so the check is the caller's uid
 * ([CarAccess]); any other app gets a refusal. The bytes come from a download (A7) or from a small cache file.
 */
@UnstableApi
class CoverProvider : ContentProvider() {
    override fun onCreate(): Boolean = true

    override fun openFile(uri: Uri, mode: String): ParcelFileDescriptor {
        if (mode != "r") throw FileNotFoundException("read only")
        if (!CarAccess.allowed(Binder.getCallingUid())) throw SecurityException("not a trusted controller")
        val id = uri.pathSegments.singleOrNull()?.takeIf(::validId) ?: throw FileNotFoundException("bad id")
        val file = coverFile(requireNotNull(context), id) ?: throw FileNotFoundException("no cover")
        return ParcelFileDescriptor.open(file, ParcelFileDescriptor.MODE_READ_ONLY)
    }

    private fun coverFile(context: android.content.Context, id: String): File? {
        Offline.init(context)
        val dir = File(context.cacheDir, DIR).apply { mkdirs() }
        val file = File(dir, id)
        if (file.isFile) return file
        val downloaded = if (isShowId(id) || isStationId(id)) null else Offline.coverBytes(id)
        val bytes = downloaded ?: run {
            val conn = TokenStore(context).current() ?: return null
            // A show's picture (A3) is fetched by the show's id; episodes in the tree borrow it, so one file serves them all.
            if (isStationId(id)) Api(conn.origin).radioArt(conn.token, id) // R3: a station's picture, by its id
            else if (isShowId(id)) Api(conn.origin).showArt(conn.token, id) else Api(conn.origin).cover(conn.token, id)
        } ?: return null
        trim(dir)
        val tmp = File(dir, "$id.tmp")
        tmp.writeBytes(bytes)
        if (!tmp.renameTo(file)) return null
        return file
    }

    /** At most [KEEP] pictures: the oldest go first. */
    private fun trim(dir: File) {
        val files = dir.listFiles()?.sortedBy { it.lastModified() } ?: return
        for (f in files.take((files.size - KEEP + 1).coerceAtLeast(0))) f.delete()
    }

    override fun getType(uri: Uri): String = "image/*"
    override fun query(uri: Uri, projection: Array<out String>?, selection: String?, selectionArgs: Array<out String>?, sortOrder: String?): Cursor? = null
    override fun insert(uri: Uri, values: ContentValues?): Uri? = null
    override fun delete(uri: Uri, selection: String?, selectionArgs: Array<out String>?): Int = 0
    override fun update(uri: Uri, values: ContentValues?, selection: String?, selectionArgs: Array<out String>?): Int = 0

    companion object {
        const val AUTHORITY = "app.tendshelf.android.covers"
        private const val DIR = "car-covers"
        private const val KEEP = 60

        /** Show ids start `ps_` (the server's ULID prefix); no book id does. */
        fun isShowId(id: String): Boolean = id.startsWith("ps_")

        fun uriOf(bookId: String): Uri = Uri.parse("content://$AUTHORITY/$bookId")

        /** The widget (L9) keeps the loaded book's picture in the same cache, so sign-out wipes it with the car's. */
        fun put(context: android.content.Context, bookId: String, bytes: ByteArray) {
            if (!validId(bookId)) return
            val dir = File(context.cacheDir, DIR).apply { mkdirs() }
            val tmp = File(dir, "$bookId.tmp")
            tmp.writeBytes(bytes)
            if (!tmp.renameTo(File(dir, bookId))) tmp.delete()
        }

        /** A picture already on this phone (the cache, or a download); never the network. */
        fun cached(context: android.content.Context, bookId: String): ByteArray? {
            if (!validId(bookId)) return null
            val file = File(File(context.cacheDir, DIR), bookId)
            if (file.isFile) return file.readBytes()
            if (isShowId(bookId) || isStationId(bookId)) return null
            Offline.init(context)
            return Offline.coverBytes(bookId)
        }

        /** Sign-out: no picture of this person's library stays behind. */
        fun wipe(context: android.content.Context) {
            File(context.cacheDir, DIR).deleteRecursively()
        }
    }
}
