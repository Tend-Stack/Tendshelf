import Foundation
import OSLog
import SQLite3
@preconcurrency import TendshelfShared

/// The unsent places on disk (port of `SqliteOutbox.kt`; same table and index, so the rows read alike): the shared `Outbox`
/// decides, this keeps. Every query is filtered by the current device id, so rows left by another sign-in are invisible
/// (`trim` and `clear` see every device). Statements are prepared and bound, never built from values. A Kotlin protocol
/// cannot throw, so a failed step sets `lastWriteFailed`; the writer then keeps the place in memory and writes it again at
/// the next tick. Called on the main actor only (rows are small and written about every 10 s).
final class SqliteOutbox: NSObject, OutboxStore {
    private static let log = Logger(subsystem: "app.tendshelf.ios", category: "outbox")
    private static let transient = unsafeBitCast(-1, to: sqlite3_destructor_type.self)

    private nonisolated(unsafe) var db: OpaquePointer?
    private let deviceId: () -> String

    /// True after any step failed since the writer last cleared it.
    var lastWriteFailed = false

    /// Opens (and creates) the file at [path]. A file that cannot be opened leaves the store unusable: every call then
    /// fails softly (reads are empty, writes set `lastWriteFailed`).
    init(path: URL, deviceId: @escaping () -> String) {
        self.deviceId = deviceId
        super.init()
        var handle: OpaquePointer?
        if sqlite3_open_v2(path.path, &handle, SQLITE_OPEN_READWRITE | SQLITE_OPEN_CREATE | SQLITE_OPEN_FULLMUTEX, nil) == SQLITE_OK {
            db = handle
            sqlite3_busy_timeout(handle, 2000)
            _ = exec("PRAGMA journal_mode=WAL")
            migrate()
            Self.protect(path)
        } else {
            Self.log.warning("outbox: could not open the file")
            sqlite3_close(handle)
            lastWriteFailed = true
        }
    }

    /// The app's own outbox file: Application Support, kept out of backups, readable after the first unlock (a place is
    /// written while the phone is locked).
    static func defaultPath() -> URL {
        let dir = (try? FileManager.default.url(for: .applicationSupportDirectory, in: .userDomainMask, appropriateFor: nil, create: true))
            ?? FileManager.default.temporaryDirectory
        return dir.appendingPathComponent("outbox.sqlite")
    }

    private static func protect(_ path: URL) {
        var url = path
        var values = URLResourceValues()
        values.isExcludedFromBackup = true
        try? url.setResourceValues(values)
        for suffix in ["", "-wal", "-shm"] {
            let file = path.path + suffix
            if FileManager.default.fileExists(atPath: file) {
                try? FileManager.default.setAttributes([.protectionKey: FileProtectionType.completeUntilFirstUserAuthentication], ofItemAtPath: file)
            }
        }
    }

    deinit { sqlite3_close(db) }

    func close() {
        sqlite3_close(db)
        db = nil
    }

    // MARK: Schema

    /// `user_version` 1: the table and index of `SqliteOutbox.kt:30-35` without the offline tables (downloads are I3).
    private func migrate() {
        guard version() < 1 else { return }
        _ = exec(
            "CREATE TABLE IF NOT EXISTS saves (seq INTEGER PRIMARY KEY AUTOINCREMENT, device_id TEXT NOT NULL, book_id TEXT NOT NULL, "
                + "position_ms INTEGER NOT NULL, client_updated_at INTEGER NOT NULL, finished INTEGER NOT NULL DEFAULT 0, "
                + "force INTEGER NOT NULL DEFAULT 0, speed REAL NOT NULL, listened_ms INTEGER NOT NULL DEFAULT 0, "
                + "held INTEGER NOT NULL DEFAULT 0)")
        _ = exec("CREATE INDEX IF NOT EXISTS saves_book ON saves(book_id, seq)")
        _ = exec("PRAGMA user_version=1")
    }

    private func version() -> Int {
        var out = 0
        _ = query("PRAGMA user_version", []) { out = Int(sqlite3_column_int($0, 0)) }
        return out
    }

    // MARK: Helpers

    private enum Arg {
        case int(Int64)
        case real(Double)
        case text(String)
    }

    @discardableResult
    private func exec(_ sql: String) -> Bool {
        guard let db else { return false }
        return sqlite3_exec(db, sql, nil, nil, nil) == SQLITE_OK
    }

    private func prepare(_ sql: String, _ args: [Arg]) -> OpaquePointer? {
        guard let db else { return nil }
        var stmt: OpaquePointer?
        guard sqlite3_prepare_v2(db, sql, -1, &stmt, nil) == SQLITE_OK else {
            sqlite3_finalize(stmt)
            return nil
        }
        for (i, a) in args.enumerated() {
            let n = Int32(i + 1)
            switch a {
            case .int(let v): sqlite3_bind_int64(stmt, n, v)
            case .real(let v): sqlite3_bind_double(stmt, n, v)
            case .text(let v): sqlite3_bind_text(stmt, n, v, -1, Self.transient)
            }
        }
        return stmt
    }

    /// Runs a statement that writes; false (and `lastWriteFailed`) when it did not finish.
    @discardableResult
    private func run(_ sql: String, _ args: [Arg]) -> Bool {
        guard let stmt = prepare(sql, args) else {
            lastWriteFailed = true
            return false
        }
        defer { sqlite3_finalize(stmt) }
        let ok = sqlite3_step(stmt) == SQLITE_DONE
        if !ok { lastWriteFailed = true }
        return ok
    }

    @discardableResult
    private func query(_ sql: String, _ args: [Arg], _ row: (OpaquePointer) -> Void) -> Bool {
        guard let stmt = prepare(sql, args) else { return false }
        defer { sqlite3_finalize(stmt) }
        while sqlite3_step(stmt) == SQLITE_ROW { row(stmt) }
        return true
    }

    private static let columns = "seq, book_id, position_ms, client_updated_at, finished, force, speed, listened_ms, held"

    private func read(_ c: OpaquePointer) -> Save {
        Save(
            seq: sqlite3_column_int64(c, 0),
            bookId: String(cString: sqlite3_column_text(c, 1)),
            positionMs: sqlite3_column_int64(c, 2),
            clientUpdatedAt: sqlite3_column_int64(c, 3),
            finished: sqlite3_column_int(c, 4) != 0,
            force: sqlite3_column_int(c, 5) != 0,
            speed: sqlite3_column_double(c, 6),
            listenedMs: sqlite3_column_int64(c, 7),
            held: sqlite3_column_int(c, 8) != 0)
    }

    private func saves(_ clause: String, _ args: [Arg], tail: String) -> [Save] {
        var out: [Save] = []
        query("SELECT \(Self.columns) FROM saves WHERE device_id = ? AND \(clause) ORDER BY seq \(tail)", [.text(deviceId())] + args) {
            out.append(read($0))
        }
        return out
    }

    // MARK: OutboxStore

    func last(bookId: String) -> Save? {
        saves("book_id = ?", [.text(bookId)], tail: "DESC LIMIT 1").first
    }

    func add(s: Save) -> Save {
        let ok = run(
            "INSERT INTO saves (device_id, book_id, position_ms, client_updated_at, finished, force, speed, listened_ms, held) "
                + "VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
            [.text(deviceId())] + values(s))
        guard ok, let db else { return s }
        return Save(
            seq: sqlite3_last_insert_rowid(db), bookId: s.bookId, positionMs: s.positionMs, clientUpdatedAt: s.clientUpdatedAt,
            finished: s.finished, force: s.force, speed: s.speed, listenedMs: s.listenedMs, held: s.held)
    }

    func update(s: Save) {
        run(
            "UPDATE saves SET book_id = ?, position_ms = ?, client_updated_at = ?, finished = ?, force = ?, speed = ?, listened_ms = ?, held = ? "
                + "WHERE seq = ?",
            values(s) + [.int(s.seq)])
    }

    private func values(_ s: Save) -> [Arg] {
        [.text(s.bookId), .int(s.positionMs), .int(s.clientUpdatedAt), .int(s.finished ? 1 : 0), .int(s.force ? 1 : 0),
         .real(s.speed), .int(s.listenedMs), .int(s.held ? 1 : 0)]
    }

    func firstBatch(n: Int32) -> [Save] {
        saves("held = 0", [], tail: "ASC LIMIT \(Int(n))")
    }

    func held() -> [Save] {
        saves("held = 1", [], tail: "ASC")
    }

    func delete(seqs: [KotlinLong]) {
        guard !seqs.isEmpty else { return }
        exec("BEGIN")
        for q in seqs { run("DELETE FROM saves WHERE seq = ?", [.int(q.int64Value)]) }
        exec("COMMIT")
    }

    /// Beyond [max] rows the oldest plain row goes (stats only); the newest places and finished or forced rows stay.
    func trim(max: Int32) {
        let total = count()
        guard total > Int(max) else { return }
        run(
            "DELETE FROM saves WHERE seq IN (SELECT seq FROM saves WHERE finished = 0 AND force = 0 AND held = 0 ORDER BY seq ASC LIMIT ?)",
            [.int(Int64(total - Int(max)))])
    }

    /// Every row of every device: sign-in as someone else, sign-out.
    func clear() {
        run("DELETE FROM saves", [])
    }

    /// Row count over every device, for the log and the bound check.
    func count() -> Int {
        var n = 0
        query("SELECT COUNT(*) FROM saves", []) { n = Int(sqlite3_column_int64($0, 0)) }
        return n
    }
}
