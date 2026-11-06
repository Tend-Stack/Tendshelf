package app.tendshelf.android

import android.content.ContentValues
import android.content.Context
import android.database.Cursor
import android.database.sqlite.SQLiteDatabase
import android.database.sqlite.SQLiteOpenHelper
import app.tendshelf.shared.OfflineBook
import app.tendshelf.shared.OfflineState
import app.tendshelf.shared.OfflineStore
import app.tendshelf.shared.OutboxStore
import app.tendshelf.shared.Save

/**
 * `outbox.db`: the positions not yet on the server, in order (A5, D1). Every write is committed before the call
 * returns, so a killed process loses nothing that was added. Rows carry the device id they were written under;
 * only the current device's rows are ever returned, so one person's rows never go out under another's token.
 */
class SqliteOutbox(context: Context, private val deviceId: () -> String) :
    SQLiteOpenHelper(context.applicationContext, "outbox.db", null, 3), OutboxStore {

    // Several helpers open outbox.db in one process (the writer, the launch flush, sign-out, the offline store). Their first
    // opens would migrate at the same moment and one would fail on a lock, so opening goes through one process-wide lock.
    override fun getWritableDatabase(): SQLiteDatabase = synchronized(OPEN_LOCK) { super.getWritableDatabase() }

    override fun getReadableDatabase(): SQLiteDatabase = synchronized(OPEN_LOCK) { super.getReadableDatabase() }

    override fun onCreate(db: SQLiteDatabase) {
        db.execSQL(
            "CREATE TABLE saves (seq INTEGER PRIMARY KEY AUTOINCREMENT, device_id TEXT NOT NULL, book_id TEXT NOT NULL, " +
                "position_ms INTEGER NOT NULL, client_updated_at INTEGER NOT NULL, finished INTEGER NOT NULL DEFAULT 0, " +
                "force INTEGER NOT NULL DEFAULT 0, speed REAL NOT NULL, listened_ms INTEGER NOT NULL DEFAULT 0, " +
                "held INTEGER NOT NULL DEFAULT 0)",
        )
        db.execSQL("CREATE INDEX saves_book ON saves(book_id, seq)")
        createOffline(db)
        addTextColumns(db)
    }

    private companion object {
        val OPEN_LOCK = Any()
    }

    /** Version 2 adds the downloaded books, version 3 their text (A9); the unsent saves and the rows already there are untouched. */
    override fun onUpgrade(db: SQLiteDatabase, oldVersion: Int, newVersion: Int) {
        if (oldVersion < 2) createOffline(db)
        if (oldVersion < 3) addTextColumns(db)
    }

    /** Where a book's stored text stands: none|fetching|ready|signin|failed, the revision stored and the edition it was made from. */
    private fun addTextColumns(db: SQLiteDatabase) {
        db.execSQL("ALTER TABLE offline_books ADD COLUMN text_state TEXT NOT NULL DEFAULT 'none'")
        db.execSQL("ALTER TABLE offline_books ADD COLUMN text_revision TEXT NOT NULL DEFAULT ''")
        db.execSQL("ALTER TABLE offline_books ADD COLUMN edition_id TEXT NOT NULL DEFAULT ''")
    }

    private fun createOffline(db: SQLiteDatabase) {
        db.execSQL(
            "CREATE TABLE offline_books (book_id TEXT PRIMARY KEY, user_id TEXT NOT NULL, title TEXT NOT NULL, " +
                "author TEXT NOT NULL, duration_ms INTEGER NOT NULL, size_bytes INTEGER NOT NULL, manifest TEXT NOT NULL, " +
                "etag TEXT NOT NULL DEFAULT '', state TEXT NOT NULL, added_at INTEGER NOT NULL, " +
                "checked_at INTEGER NOT NULL DEFAULT 0, place_ms INTEGER NOT NULL DEFAULT 0, place_at INTEGER NOT NULL DEFAULT 0)",
        )
    }

    private fun read(c: Cursor) = Save(
        c.getLong(0), c.getString(1), c.getLong(2), c.getLong(3), c.getInt(4) != 0, c.getInt(5) != 0,
        c.getDouble(6), c.getLong(7), c.getInt(8) != 0,
    )

    private fun query(where: String, args: Array<String>, tail: String = ""): List<Save> =
        readableDatabase.rawQuery(
            "SELECT seq, book_id, position_ms, client_updated_at, finished, force, speed, listened_ms, held " +
                "FROM saves WHERE device_id = ? AND $where ORDER BY seq $tail",
            arrayOf(deviceId()) + args,
        ).use { c -> buildList { while (c.moveToNext()) add(read(c)) } }

    @Synchronized
    override fun last(bookId: String): Save? = query("book_id = ?", arrayOf(bookId), "DESC LIMIT 1").firstOrNull()

    @Synchronized
    override fun add(s: Save): Save {
        val seq = writableDatabase.insertOrThrow("saves", null, values(s).apply { put("device_id", deviceId()) })
        return s.copy(seq = seq)
    }

    @Synchronized
    override fun update(s: Save) {
        writableDatabase.update("saves", values(s), "seq = ?", arrayOf(s.seq.toString()))
    }

    private fun values(s: Save) = ContentValues().apply {
        put("book_id", s.bookId)
        put("position_ms", s.positionMs)
        put("client_updated_at", s.clientUpdatedAt)
        put("finished", if (s.finished) 1 else 0)
        put("force", if (s.force) 1 else 0)
        put("speed", s.speed)
        put("listened_ms", s.listenedMs)
        put("held", if (s.held) 1 else 0)
    }

    @Synchronized
    override fun firstBatch(n: Int): List<Save> = query("held = 0", emptyArray(), "ASC LIMIT $n")

    @Synchronized
    override fun held(): List<Save> = query("held = 1", emptyArray(), "ASC")

    @Synchronized
    override fun delete(seqs: List<Long>) {
        val db = writableDatabase
        db.beginTransaction()
        try {
            for (q in seqs) db.delete("saves", "seq = ?", arrayOf(q.toString()))
            db.setTransactionSuccessful()
        } finally {
            db.endTransaction()
        }
    }

    /** Beyond [max] rows the oldest plain row goes (stats only); the newest places and finished rows stay. */
    @Synchronized
    override fun trim(max: Int) {
        val db = writableDatabase
        val count = db.rawQuery("SELECT COUNT(*) FROM saves", null).use { it.moveToFirst(); it.getInt(0) }
        if (count <= max) return
        db.execSQL(
            "DELETE FROM saves WHERE seq IN (SELECT seq FROM saves WHERE finished = 0 AND force = 0 AND held = 0 " +
                "ORDER BY seq ASC LIMIT ?)",
            arrayOf<Any>(count - max),
        )
    }

    /** Every row of every device: sign-in as someone else, sign-out. */
    @Synchronized
    override fun clear() {
        writableDatabase.delete("saves", null, null)
    }

    /** Row count, for the debug log and the bound check. */
    @Synchronized
    fun count(): Int = readableDatabase.rawQuery("SELECT COUNT(*) FROM saves", null).use { it.moveToFirst(); it.getInt(0) }
}

/** The downloaded books in `outbox.db` (table `offline_books`, A7). Covers and bytes live in files, not here. */
class SqliteOfflineStore(context: Context) : OfflineStore {
    private val helper = SqliteOutbox(context) { "" }

    private fun read(c: Cursor) = OfflineBook(
        c.getString(0), c.getString(1), c.getString(2), c.getString(3), c.getLong(4), c.getLong(5),
        runCatching { OfflineState.valueOf(c.getString(6)) }.getOrDefault(OfflineState.FAILED), c.getLong(7), c.getLong(8),
    )

    private val columns = "book_id, user_id, title, author, duration_ms, size_bytes, state, place_ms, place_at"

    @Synchronized
    override fun all(): List<OfflineBook> =
        helper.readableDatabase.rawQuery("SELECT $columns FROM offline_books ORDER BY added_at DESC", null)
            .use { c -> buildList { while (c.moveToNext()) add(read(c)) } }

    @Synchronized
    override fun get(id: String): OfflineBook? =
        helper.readableDatabase.rawQuery("SELECT $columns FROM offline_books WHERE book_id = ?", arrayOf(id))
            .use { c -> if (c.moveToFirst()) read(c) else null }

    @Synchronized
    override fun put(b: OfflineBook, manifestJson: String) {
        val v = ContentValues().apply {
            put("book_id", b.bookId)
            put("user_id", b.userId)
            put("title", b.title)
            put("author", b.author)
            put("duration_ms", b.durationMs)
            put("size_bytes", b.sizeBytes)
            put("manifest", manifestJson)
            put("state", b.state.name)
            put("added_at", System.currentTimeMillis())
            put("place_ms", b.placeMs)
            put("place_at", b.placeAt)
        }
        helper.writableDatabase.insertWithOnConflict("offline_books", null, v, SQLiteDatabase.CONFLICT_REPLACE)
    }

    @Synchronized
    override fun manifest(id: String): String? =
        helper.readableDatabase.rawQuery("SELECT manifest FROM offline_books WHERE book_id = ?", arrayOf(id))
            .use { c -> if (c.moveToFirst()) c.getString(0) else null }

    /** Replaces the stored answer (the daily check found the same files); the state and the place stay. */
    @Synchronized
    fun setManifest(id: String, json: String, checkedAt: Long) {
        helper.writableDatabase.update(
            "offline_books", ContentValues().apply { put("manifest", json); put("checked_at", checkedAt) }, "book_id = ?", arrayOf(id),
        )
    }

    @Synchronized
    fun checkedAt(id: String): Long =
        helper.readableDatabase.rawQuery("SELECT checked_at FROM offline_books WHERE book_id = ?", arrayOf(id))
            .use { c -> if (c.moveToFirst()) c.getLong(0) else 0L }

    @Synchronized
    fun touchChecked(id: String, at: Long) {
        helper.writableDatabase.update("offline_books", ContentValues().apply { put("checked_at", at) }, "book_id = ?", arrayOf(id))
    }

    @Synchronized
    override fun setState(id: String, s: OfflineState) {
        helper.writableDatabase.update("offline_books", ContentValues().apply { put("state", s.name) }, "book_id = ?", arrayOf(id))
    }

    @Synchronized
    override fun notePlace(id: String, ms: Long, at: Long) {
        helper.writableDatabase.update(
            "offline_books", ContentValues().apply { put("place_ms", ms); put("place_at", at) }, "book_id = ?", arrayOf(id),
        )
    }

    @Synchronized
    override fun remove(id: String) {
        helper.writableDatabase.delete("offline_books", "book_id = ?", arrayOf(id))
    }

    @Synchronized
    override fun clear() {
        helper.writableDatabase.delete("offline_books", null, null)
    }

    /** The text columns of one book: state word, revision, edition. */
    @Synchronized
    fun text(id: String): Triple<String, String, String>? =
        helper.readableDatabase.rawQuery("SELECT text_state, text_revision, edition_id FROM offline_books WHERE book_id = ?", arrayOf(id))
            .use { c -> if (c.moveToFirst()) Triple(c.getString(0), c.getString(1), c.getString(2)) else null }

    /** Books whose text is in [state] (for the retry at the next start and the next page). */
    @Synchronized
    fun withText(state: String): List<String> =
        helper.readableDatabase.rawQuery("SELECT book_id FROM offline_books WHERE text_state = ?", arrayOf(state))
            .use { c -> buildList { while (c.moveToNext()) add(c.getString(0)) } }

    @Synchronized
    fun setText(id: String, state: String, revision: String? = null, edition: String? = null) {
        helper.writableDatabase.update(
            "offline_books",
            ContentValues().apply {
                put("text_state", state)
                if (revision != null) put("text_revision", revision)
                if (edition != null) put("edition_id", edition)
            },
            "book_id = ?", arrayOf(id),
        )
    }
}
