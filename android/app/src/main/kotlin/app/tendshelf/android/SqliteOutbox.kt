package app.tendshelf.android

import android.content.ContentValues
import android.content.Context
import android.database.Cursor
import android.database.sqlite.SQLiteDatabase
import android.database.sqlite.SQLiteOpenHelper
import app.tendshelf.shared.OutboxStore
import app.tendshelf.shared.Save

/**
 * `outbox.db`: the positions not yet on the server, in order (A5, D1). Every write is committed before the call
 * returns, so a killed process loses nothing that was added. Rows carry the device id they were written under;
 * only the current device's rows are ever returned, so one person's rows never go out under another's token.
 */
class SqliteOutbox(context: Context, private val deviceId: () -> String) :
    SQLiteOpenHelper(context.applicationContext, "outbox.db", null, 1), OutboxStore {

    override fun onCreate(db: SQLiteDatabase) {
        db.execSQL(
            "CREATE TABLE saves (seq INTEGER PRIMARY KEY AUTOINCREMENT, device_id TEXT NOT NULL, book_id TEXT NOT NULL, " +
                "position_ms INTEGER NOT NULL, client_updated_at INTEGER NOT NULL, finished INTEGER NOT NULL DEFAULT 0, " +
                "force INTEGER NOT NULL DEFAULT 0, speed REAL NOT NULL, listened_ms INTEGER NOT NULL DEFAULT 0, " +
                "held INTEGER NOT NULL DEFAULT 0)",
        )
        db.execSQL("CREATE INDEX saves_book ON saves(book_id, seq)")
    }

    override fun onUpgrade(db: SQLiteDatabase, oldVersion: Int, newVersion: Int) = Unit

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
