import Foundation
import OSLog
import SQLite3
@preconcurrency import TendshelfShared

/// One file of a downloaded book (or episode) as the engine keeps it: the address to fetch, what the manifest planned, how it
/// stands, and what arrived.
nonisolated struct OfflineFile {
    let bookId: String
    let cacheKey: String
    let url: String
    /// The size the manifest listed (0 for an episode: the feed's figure is often wrong).
    let planned: Int64
    let mime: String
    let auto: Bool
    var step: FileStep
    var failures: Int
    /// The `Content-Length` the server answered with, 0 when it sent none.
    var length: Int64
    var bytes: Int64
    var name: String

    init(bookId: String, cacheKey: String, url: String, planned: Int64, mime: String, auto: Bool, step: FileStep = .queued,
         failures: Int = 0, length: Int64 = 0, bytes: Int64 = 0, name: String = "") {
        self.bookId = bookId
        self.cacheKey = cacheKey
        self.url = url
        self.planned = planned
        self.mime = mime
        self.auto = auto
        self.step = step
        self.failures = failures
        self.length = length
        self.bytes = bytes
        self.name = name
    }
}

/// The downloaded books on disk, `offline/offline.sqlite` (port of the `offline_books` tables of `SqliteOutbox.kt`, in a file of
/// its own so that removing the folder is the whole wipe). The shared `OfflineStore` is the contract; the rest is what the engine
/// needs. Statements are prepared and bound, never built from values. A Kotlin protocol cannot throw, so a failed write sets
/// `lastWriteFailed` and reads of a store that did not open are empty. Main actor only.
final class SqliteOfflineStore: NSObject, OfflineStore {
    private static let log = Logger(subsystem: "app.tendshelf.ios", category: "offline")
    private static let transient = unsafeBitCast(-1, to: sqlite3_destructor_type.self)

    private nonisolated(unsafe) var db: OpaquePointer?
    var lastWriteFailed = false

    private let path: URL

    init(path: URL) {
        self.path = path
        super.init()
        open()
    }

    private func open() {
        var handle: OpaquePointer?
        if sqlite3_open_v2(path.path, &handle, SQLITE_OPEN_READWRITE | SQLITE_OPEN_CREATE | SQLITE_OPEN_FULLMUTEX, nil) == SQLITE_OK {
            db = handle
            sqlite3_busy_timeout(handle, 2000)
            exec("PRAGMA foreign_keys=ON")
            exec("PRAGMA journal_mode=WAL")
            migrate()
            for suffix in ["", "-wal", "-shm"] where FileManager.default.fileExists(atPath: path.path + suffix) {
                try? FileManager.default.setAttributes(
                    [.protectionKey: FileProtectionType.completeUntilFirstUserAuthentication], ofItemAtPath: path.path + suffix)
            }
        } else {
            Self.log.warning("offline store: could not open the file")
            sqlite3_close(handle)
            lastWriteFailed = true
        }
    }

    deinit { sqlite3_close(db) }

    func close() {
        sqlite3_close(db)
        db = nil
    }

    /// Opens the file again after `close` (the wipe removed the folder; the next download makes it anew).
    func reopen() {
        guard db == nil else { return }
        open()
    }

    // MARK: Schema

    private func migrate() {
        guard version() < 1 else { return }
        exec(
            "CREATE TABLE IF NOT EXISTS offline_books (book_id TEXT PRIMARY KEY, user_id TEXT NOT NULL, title TEXT NOT NULL, "
                + "author TEXT NOT NULL, duration_ms INTEGER NOT NULL, size_bytes INTEGER NOT NULL, manifest TEXT NOT NULL, "
                + "state TEXT NOT NULL, added_at INTEGER NOT NULL, checked_at INTEGER NOT NULL DEFAULT 0, "
                + "place_ms INTEGER NOT NULL DEFAULT 0, place_at INTEGER NOT NULL DEFAULT 0, text_state TEXT NOT NULL DEFAULT 'none', "
                + "text_revision TEXT NOT NULL DEFAULT '', edition_id TEXT NOT NULL DEFAULT '', kind TEXT NOT NULL DEFAULT 'book', "
                + "show_id TEXT NOT NULL DEFAULT '', published_at INTEGER NOT NULL DEFAULT 0, played INTEGER NOT NULL DEFAULT 0)")
        exec(
            "CREATE TABLE IF NOT EXISTS offline_files (book_id TEXT NOT NULL REFERENCES offline_books(book_id) ON DELETE CASCADE, "
                + "cache_key TEXT NOT NULL, url TEXT NOT NULL, planned INTEGER NOT NULL, mime TEXT NOT NULL, auto INTEGER NOT NULL, "
                + "step TEXT NOT NULL, failures INTEGER NOT NULL DEFAULT 0, length INTEGER NOT NULL DEFAULT 0, "
                + "bytes INTEGER NOT NULL DEFAULT 0, name TEXT NOT NULL DEFAULT '', PRIMARY KEY (book_id, cache_key))")
        exec(
            "CREATE TABLE IF NOT EXISTS reading_out (seq INTEGER PRIMARY KEY AUTOINCREMENT, user_id TEXT NOT NULL, "
                + "book_id TEXT NOT NULL, form TEXT NOT NULL, at INTEGER NOT NULL)")
        exec("PRAGMA user_version=1")
    }

    private func version() -> Int {
        var out = 0
        query("PRAGMA user_version", []) { out = Int(sqlite3_column_int($0, 0)) }
        return out
    }

    // MARK: Helpers

    private enum Arg {
        case int(Int64)
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
            switch a {
            case .int(let v): sqlite3_bind_int64(stmt, Int32(i + 1), v)
            case .text(let v): sqlite3_bind_text(stmt, Int32(i + 1), v, -1, Self.transient)
            }
        }
        return stmt
    }

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

    private func str(_ c: OpaquePointer, _ i: Int32) -> String {
        sqlite3_column_text(c, i).map { String(cString: $0) } ?? ""
    }

    static func state(named name: String) -> OfflineState {
        switch name {
        case "DOWNLOADING": return .downloading
        case "PAUSED": return .paused
        case "READY": return .ready
        case "FAILED": return .failed
        case "STALE": return .stale
        default: return .queued
        }
    }

    static func step(named name: String) -> FileStep {
        switch name {
        case "RUNNING": return .running
        case "DONE": return .done
        case "BROKEN": return .broken
        case "FAILED": return .failed
        default: return .queued
        }
    }

    private static let bookColumns = "book_id, user_id, title, author, duration_ms, size_bytes, state, place_ms, place_at, kind, show_id, published_at, played"

    private func readBook(_ c: OpaquePointer) -> OfflineBook {
        OfflineBook(
            bookId: str(c, 0), userId: str(c, 1), title: str(c, 2), author: str(c, 3), durationMs: sqlite3_column_int64(c, 4),
            sizeBytes: sqlite3_column_int64(c, 5), state: Self.state(named: str(c, 6)), placeMs: sqlite3_column_int64(c, 7),
            placeAt: sqlite3_column_int64(c, 8), kind: str(c, 9), showId: str(c, 10), publishedAt: sqlite3_column_int64(c, 11),
            played: sqlite3_column_int(c, 12) != 0)
    }

    // MARK: OfflineStore

    /// Oldest first, as they were added.
    func all() -> [OfflineBook] {
        var out: [OfflineBook] = []
        query("SELECT \(Self.bookColumns) FROM offline_books ORDER BY added_at, book_id", []) { out.append(readBook($0)) }
        return out
    }

    func get(id: String) -> OfflineBook? {
        var out: OfflineBook?
        query("SELECT \(Self.bookColumns) FROM offline_books WHERE book_id = ?", [.text(id)]) { out = readBook($0) }
        return out
    }

    func put(b: OfflineBook, manifestJson: String) {
        put(b, manifestJson, at: Int64(Date().timeIntervalSince1970 * 1000))
    }

    /// Replaces the row; its old file rows go with it (the foreign key cascades), and the caller writes the new ones.
    func put(_ b: OfflineBook, _ manifestJson: String, at: Int64) {
        run("DELETE FROM offline_books WHERE book_id = ?", [.text(b.bookId)])
        run(
            "INSERT INTO offline_books (book_id, user_id, title, author, duration_ms, size_bytes, manifest, state, added_at, place_ms, place_at, kind, show_id, published_at, played) "
                + "VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
            [.text(b.bookId), .text(b.userId), .text(b.title), .text(b.author), .int(b.durationMs), .int(b.sizeBytes),
             .text(manifestJson), .text(b.state.name), .int(at), .int(b.placeMs), .int(b.placeAt), .text(b.kind),
             .text(b.showId), .int(b.publishedAt), .int(b.played ? 1 : 0)])
    }

    func manifest(id: String) -> String? {
        var out: String?
        query("SELECT manifest FROM offline_books WHERE book_id = ?", [.text(id)]) { out = str($0, 0) }
        return out
    }

    func setState(id: String, s: OfflineState) {
        run("UPDATE offline_books SET state = ? WHERE book_id = ?", [.text(s.name), .text(id)])
    }

    func notePlace(id: String, ms: Int64, at: Int64) {
        run("UPDATE offline_books SET place_ms = ?, place_at = ? WHERE book_id = ?", [.int(ms), .int(at), .text(id)])
    }

    func setPlayed(id: String, played: Bool) {
        run("UPDATE offline_books SET played = ? WHERE book_id = ?", [.int(played ? 1 : 0), .text(id)])
    }

    func setSize(id: String, bytes: Int64) {
        run("UPDATE offline_books SET size_bytes = ? WHERE book_id = ?", [.int(bytes), .text(id)])
    }

    func remove(id: String) {
        run("DELETE FROM offline_books WHERE book_id = ?", [.text(id)])
    }

    /// Every row of every person.
    func clear() {
        run("DELETE FROM offline_books", [])
        run("DELETE FROM reading_out", [])
    }

    // MARK: Reading places made at the stored reader (I3d)

    /// Keeps a reading place the stored reader answered, until the server's own page takes it (`rd`).
    func addReading(userId: String, bookId: String, form: String, at: Int64) {
        run("INSERT INTO reading_out (user_id, book_id, form, at) VALUES (?, ?, ?, ?)", [.text(userId), .text(bookId), .text(form), .int(at)])
    }

    /// The oldest places of [userId], at most [limit] rows and about [bytes] of forms (the first row always comes).
    func readingRows(userId: String, limit: Int, bytes: Int) -> [ReadingItem] {
        var out: [ReadingItem] = []
        var size = 0
        var full = false
        query("SELECT seq, book_id, form FROM reading_out WHERE user_id = ? ORDER BY seq LIMIT ?", [.text(userId), .int(Int64(limit))]) { c in
            let form = str(c, 2)
            if full || (!out.isEmpty && size + form.utf8.count > bytes) {
                full = true
                return
            }
            size += form.utf8.count
            out.append(ReadingItem(seq: sqlite3_column_int64(c, 0), bookId: str(c, 1), form: form))
        }
        return out
    }

    /// The places of [userId] up to [seq] were taken by the page.
    func deleteReading(userId: String, upTo seq: Int64) {
        run("DELETE FROM reading_out WHERE user_id = ? AND seq <= ?", [.text(userId), .int(seq)])
    }

    func readingCount() -> Int {
        var n = 0
        query("SELECT COUNT(*) FROM reading_out", []) { n = Int(sqlite3_column_int64($0, 0)) }
        return n
    }

    // MARK: Extras

    func checkedAt(_ id: String) -> Int64 {
        var out: Int64 = 0
        query("SELECT checked_at FROM offline_books WHERE book_id = ?", [.text(id)]) { out = sqlite3_column_int64($0, 0) }
        return out
    }

    func touchChecked(_ id: String, at: Int64) {
        run("UPDATE offline_books SET checked_at = ? WHERE book_id = ?", [.int(at), .text(id)])
    }

    func setManifest(_ id: String, _ json: String, at: Int64) {
        run("UPDATE offline_books SET manifest = ?, checked_at = ? WHERE book_id = ?", [.text(json), .int(at), .text(id)])
    }

    /// The text state, revision and edition of a book; nil for no row.
    func text(_ id: String) -> (state: String, revision: String, edition: String)? {
        var out: (state: String, revision: String, edition: String)?
        query("SELECT text_state, text_revision, edition_id FROM offline_books WHERE book_id = ?", [.text(id)]) {
            out = (state: str($0, 0), revision: str($0, 1), edition: str($0, 2))
        }
        return out
    }

    func setText(_ id: String, state: String, revision: String, edition: String) {
        run("UPDATE offline_books SET text_state = ?, text_revision = ?, edition_id = ? WHERE book_id = ?",
            [.text(state), .text(revision), .text(edition), .text(id)])
    }

    /// The ids of the books whose text is in [state].
    func withText(_ state: String) -> [String] {
        var out: [String] = []
        query("SELECT book_id FROM offline_books WHERE text_state = ? ORDER BY added_at, book_id", [.text(state)]) { out.append(str($0, 0)) }
        return out
    }

    // MARK: Files

    func putFiles(_ bookId: String, _ files: [OfflineFile]) {
        exec("BEGIN")
        run("DELETE FROM offline_files WHERE book_id = ?", [.text(bookId)])
        for f in files {
            run(
                "INSERT INTO offline_files (book_id, cache_key, url, planned, mime, auto, step, failures, length, bytes, name) "
                    + "VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
                [.text(f.bookId), .text(f.cacheKey), .text(f.url), .int(f.planned), .text(f.mime), .int(f.auto ? 1 : 0),
                 .text(f.step.name), .int(Int64(f.failures)), .int(f.length), .int(f.bytes), .text(f.name)])
        }
        exec("COMMIT")
    }

    private static let fileColumns = "book_id, cache_key, url, planned, mime, auto, step, failures, length, bytes, name"

    private func readFile(_ c: OpaquePointer) -> OfflineFile {
        OfflineFile(
            bookId: str(c, 0), cacheKey: str(c, 1), url: str(c, 2), planned: sqlite3_column_int64(c, 3), mime: str(c, 4),
            auto: sqlite3_column_int(c, 5) != 0, step: Self.step(named: str(c, 6)), failures: Int(sqlite3_column_int(c, 7)),
            length: sqlite3_column_int64(c, 8), bytes: sqlite3_column_int64(c, 9), name: str(c, 10))
    }

    func files(_ bookId: String) -> [OfflineFile] {
        var out: [OfflineFile] = []
        query("SELECT \(Self.fileColumns) FROM offline_files WHERE book_id = ? ORDER BY rowid", [.text(bookId)]) { out.append(readFile($0)) }
        return out
    }

    func allFiles() -> [OfflineFile] {
        var out: [OfflineFile] = []
        query("SELECT \(Self.fileColumns) FROM offline_files ORDER BY book_id, rowid", []) { out.append(readFile($0)) }
        return out
    }

    func setStep(_ bookId: String, _ key: String, _ step: FileStep, failures: Int) {
        run("UPDATE offline_files SET step = ?, failures = ? WHERE book_id = ? AND cache_key = ?",
            [.text(step.name), .int(Int64(failures)), .text(bookId), .text(key)])
    }

    /// A person pressed Download on an automatic item: its files are the person's own from now on.
    func setAuto(_ bookId: String, auto: Bool) {
        run("UPDATE offline_files SET auto = ? WHERE book_id = ?", [.int(auto ? 1 : 0), .text(bookId)])
    }

    func setArrived(_ bookId: String, _ key: String, length: Int64, bytes: Int64, name: String) {
        run("UPDATE offline_files SET length = ?, bytes = ?, name = ? WHERE book_id = ? AND cache_key = ?",
            [.int(length), .int(bytes), .text(name), .text(bookId), .text(key)])
    }

    /// The bytes received so far, for the percent.
    func setBytes(_ bookId: String, _ key: String, bytes: Int64) {
        run("UPDATE offline_files SET bytes = ? WHERE book_id = ? AND cache_key = ?", [.int(bytes), .text(bookId), .text(key)])
    }
}
