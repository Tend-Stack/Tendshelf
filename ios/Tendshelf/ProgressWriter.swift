import Foundation
import Network
import OSLog
import UIKit
@preconcurrency import TendshelfShared

/// What the writer reads from the player.
@MainActor
protocol SaveSource: AnyObject {
    var bookId: String { get }
    var positionMs: Int64 { get }
    var rate: Double { get }
    var ended: Bool { get }
    /// An item is ready and no start seek waits, so `positionMs` is a real place.
    var hasPlace: Bool { get }
    var playing: Bool { get }
}

extension AudioPlayer: SaveSource {}

/// Work that must finish after the app leaves the screen (the iPhone's `beginBackgroundTask`, the analogue of Android's
/// 30 s wake lock).
@MainActor
protocol BackgroundWork: AnyObject {
    func begin(_ name: String) -> Int
    func end(_ id: Int)
}

@MainActor
final class SystemBackground: BackgroundWork {
    private var tasks: [Int: UIBackgroundTaskIdentifier] = [:]
    private var next = 0

    func begin(_ name: String) -> Int {
        next += 1
        let key = next
        tasks[key] = UIApplication.shared.beginBackgroundTask(withName: name) { [weak self] in self?.end(key) }
        return key
    }

    func end(_ id: Int) {
        guard let task = tasks.removeValue(forKey: id) else { return }
        UIApplication.shared.endBackgroundTask(task)
    }
}

@MainActor
final class NoBackground: BackgroundWork {
    func begin(_ name: String) -> Int { 0 }
    func end(_ id: Int) {}
}

/// Saves the place of the playing book and sends it (port of `ProgressWriter.kt`). A place is written to SQLite first and
/// only then does a send try the server, so a kill loses nothing that was added. The shared `Outbox` is called on the main
/// actor only (invariant 5); network waits are `async`. It never involves the page. Logs hold counts, never tokens or places.
@MainActor
final class ProgressWriter {
    private static let log = Logger(subsystem: "app.tendshelf.ios", category: "outbox")
    private static let deviceKey = "outboxDevice"

    let store: SqliteOutbox
    let outbox: Outbox
    private let defaults: UserDefaults
    private let connection: () -> Connection?
    private let makeApi: (Origin) -> PlayerAPI
    private let background: BackgroundWork
    private let mono: () -> Int64
    private weak var source: SaveSource?
    private let deviceBox: DeviceBox
    /// A downloaded book keeps its place beside the row (I3c, `ProgressWriter.kt:157,178`), so a start with no network resumes there.
    var offline: OfflineSource?

    private let listened = ListenedClock()
    private let cadence = SaveCadence()
    private let backoff = Backoff(first: 5_000, max: 300_000)

    /// The book whose question is open: its saves are held, not sent.
    var heldBook: String?
    /// A newer place on the server than this device's save (an `applied:false` answer).
    var onLost: ((Lost, Connection) -> Void)?
    /// The server refused the token (401). The rows stay; the person is signed out elsewhere.
    var onSignedOut: (() -> Void)?
    /// The wait after a seek while paused, before its place is saved. A var so tests need not wait 2 s.
    var settleMs: UInt64 = 2_000

    private var tail: Task<Bool, Never>?
    private var queued = false
    private var closing = false
    private var retryTask: Task<Void, Never>?
    private var settleTask: Task<Void, Never>?
    private var monitor: NWPathMonitor?

    /// A place the disk refused: the book and the listening time it carried. The position is read again at the retry.
    private struct Unsaved {
        let bookId: String
        let listened: Int64
    }

    private var unsaved: Unsaved?

    /// Lets the store read the device id without a reference to the writer.
    nonisolated final class DeviceBox: @unchecked Sendable {
        var value: String
        init(_ value: String) { self.value = value }
    }

    init(
        store: SqliteOutbox, deviceBox: DeviceBox, defaults: UserDefaults = .standard, connection: @escaping () -> Connection?,
        makeApi: @escaping (Origin) -> PlayerAPI, background: BackgroundWork,
        now: @escaping () -> Int64 = { Int64(Date().timeIntervalSince1970 * 1000) },
        mono: @escaping () -> Int64 = { Int64(ProcessInfo.processInfo.systemUptime * 1000) + 1 }
    ) {
        self.store = store
        self.deviceBox = deviceBox
        self.defaults = defaults
        self.connection = connection
        self.makeApi = makeApi
        self.background = background
        self.mono = mono
        outbox = Outbox(store: store, now: { KotlinLong(value: now()) })
        deviceBox.value = defaults.string(forKey: Self.deviceKey) ?? ""
    }

    /// Names the player the writer reads (tests use this alone, without the start's flush and network monitor).
    func attach(source: SaveSource) { self.source = source }

    /// The writer's start: a question from a dead process is gone (newest wins), rows left by a killed process go out, and
    /// a network that comes back flushes again.
    func start(source: SaveSource) {
        attach(source: source)
        outbox.releaseAllHeld()
        flush()
        let m = NWPathMonitor()
        m.pathUpdateHandler = { [weak self] path in
            guard path.status == .satisfied else { return }
            Task { @MainActor in
                self?.backoff.reset()
                self?.flush()
            }
        }
        m.start(queue: DispatchQueue(label: "app.tendshelf.ios.path"))
        monitor = m
    }

    // MARK: Listening time and cadence

    /// The sound started or stopped. A stop counts the stretch before it and saves the place.
    func playingChanged(_ playing: Bool) {
        let now = mono()
        if playing {
            listened.playing(nowMs: now)
            cadence.playing(nowMs: now)
        } else {
            listened.stopped(nowMs: now)
            record("pause")
        }
    }

    /// Listening time since the last save, as the page's `heardMs`: wall time while playing.
    func drainListened() -> Int64 {
        listened.drain(nowMs: mono(), stillPlaying: source?.playing ?? false)
    }

    /// A seek while paused: the place is saved once it settles (a run of seeks saves once).
    func sought() {
        guard let source, !source.playing else { return }
        settleTask?.cancel()
        let wait = settleMs
        settleTask = Task { @MainActor [weak self] in
            try? await Task.sleep(nanoseconds: wait * 1_000_000)
            if Task.isCancelled { return }
            self?.record("seek")
        }
    }

    /// About twice a second while playing: every 10 s the place is written, if it moved at least a second.
    func onTick() {
        guard let source, !source.bookId.isEmpty else { return }
        let retry = unsaved != nil
        if !cadence.due(nowMs: mono(), bookId: source.bookId, positionMs: source.positionMs, retry: retry) { return }
        record(retry ? "retry" : "tick")
    }

    /// Chapter change, book change, sign-out: save now.
    func saveNow(_ why: String) { record(why) }

    // MARK: Writing

    private func record(_ why: String) {
        guard let source, !source.bookId.isEmpty, source.hasPlace, !source.ended else { return }
        let id = source.bookId
        if RadioKt.isStationId(id: id) { return } // a live station has no place to save
        // A finished book has its own `finished` row; a plain place saved after it would un-finish it.
        guard let conn = connection() else { return } // no sign-in: nothing is saved under nobody's token
        let position = source.positionMs
        let speed = source.rate
        let before = (unsaved?.bookId == id ? unsaved?.listened : nil) ?? 0
        let heard = drainListened() + before
        let hold = heldBook == id
        cadence.saved(nowMs: mono(), bookId: id, positionMs: position)
        adopt(conn)
        store.lastWriteFailed = false
        _ = outbox.tick(bookId: id, positionMs: position, speed: speed, listenedMs: heard, hold: hold)
        if store.lastWriteFailed {
            // A full disk or a locked file: playing goes on, the place stays in memory and the next tick writes it again.
            unsaved = Unsaved(bookId: id, listened: heard)
            Self.log.warning("outbox: place not written, kept in memory")
            return
        }
        unsaved = nil
        offline?.notePlace(id, ms: position, at: Int64(Date().timeIntervalSince1970 * 1000))
        Self.log.debug("outbox: saved (\(why))")
        flush()
    }

    /// The book is over: write `finished` and try to send it now. True when everything went out (40 s at most).
    func finishAndFlush(bookId: String, positionMs: Int64, speed: Double, listened: Int64) async -> Bool {
        guard let conn = connection() else { return false }
        adopt(conn)
        store.lastWriteFailed = false
        _ = outbox.finish(bookId: bookId, positionMs: positionMs, speed: speed, listenedMs: listened)
        if store.lastWriteFailed {
            Self.log.warning("finish not written")
            return false
        }
        offline?.notePlace(bookId, ms: positionMs, at: Int64(Date().timeIntervalSince1970 * 1000))
        let job = enqueue()
        return await withTaskGroup(of: Bool.self) { group in
            group.addTask { await job.value }
            group.addTask {
                try? await Task.sleep(nanoseconds: 40_000_000_000)
                return false
            }
            let first = await group.next() ?? false
            group.cancelAll()
            return first
        }
    }

    /// The question was answered: "Stay here" ([force]) wins on the server, otherwise the held place goes plain.
    func release(bookId: String, force: Bool) {
        heldBook = nil
        _ = outbox.release(bookId: bookId, force: force)
        flush()
    }

    func dropHeld(bookId: String) {
        heldBook = nil
        outbox.dropHeld(bookId: bookId)
    }

    /// The newest unsent row of the book.
    func pending(_ bookId: String) -> Save? { outbox.newest(bookId: bookId) }

    /// Rows of another sign-in must never go out under this token: a different device id empties the outbox.
    private func adopt(_ conn: Connection) {
        if deviceBox.value == conn.deviceId { return }
        outbox.clear()
        deviceBox.value = conn.deviceId
        defaults.set(conn.deviceId, forKey: Self.deviceKey)
        Self.log.info("outbox: another sign-in, rows cleared")
    }

    // MARK: Sending

    /// Tries to send what is waiting, once; failures schedule their own retry. One loop at a time, one waiting behind it.
    func flush() {
        guard !closing, !queued else { return }
        queued = true
        _ = enqueue()
    }

    /// The loops run one after another, so two never send the same rows.
    private func enqueue() -> Task<Bool, Never> {
        let previous = tail
        let job = Task { @MainActor [weak self] () -> Bool in
            _ = await previous?.value
            guard let self else { return false }
            queued = false
            let id = background.begin("outbox")
            defer { background.end(id) }
            return await sendLoop()
        }
        tail = job
        return job
    }

    private func sendLoop() async -> Bool {
        while true {
            guard let conn = connection() else {
                Self.log.debug("outbox: flush skipped, no sign-in")
                return false
            }
            adopt(conn)
            let batch = outbox.nextBatch(n: 100)
            if batch.isEmpty {
                backoff.reset()
                return true
            }
            let results: [BatchItemResult]
            do {
                results = try await makeApi(conn.origin).batch(token: conn.token, saves: batch, deviceId: conn.deviceId)
            } catch let e as ApiError {
                outbox.failed()
                switch e.status {
                case 401: onSignedOut?()
                case 429: retryIn(seconds: Double(e.retryAfterS))
                default: retryLater()
                }
                Self.log.warning("outbox: server answered \(e.status), \(batch.count) rows kept")
                return false
            } catch {
                outbox.failed()
                retryLater()
                return false
            }
            let applied = outbox.settled(sent: batch, results: results)
            Self.log.info("outbox: sent \(batch.count), settled \(applied.deleteSeqs.count)")
            for lost in applied.lost { onLost?(lost, conn) }
            if applied.deleteSeqs.count < batch.count {
                retryLater() // some rows were refused for a reason that may pass; do not spin on them
                return false
            }
        }
    }

    private func retryLater() { retryIn(seconds: Double(backoff.next()) / 1000) }

    private func retryIn(seconds: Double) {
        retryTask?.cancel()
        retryTask = Task { @MainActor [weak self] in
            try? await Task.sleep(nanoseconds: UInt64(max(0, seconds) * 1_000_000_000))
            if Task.isCancelled { return }
            self?.flush()
        }
    }

    /// Sign-out: one try to send the rows of this sign-in with the connection held before the token went, then every row
    /// goes. Rows of another device are never sent.
    func flushAndClear(connection conn: Connection?) async {
        closing = true
        retryTask?.cancel()
        let id = background.begin("signout")
        defer {
            background.end(id)
            closing = false
        }
        _ = await tail?.value
        if let conn, deviceBox.value == conn.deviceId {
            let batch = outbox.nextBatch(n: 100)
            if !batch.isEmpty {
                do {
                    let results = try await makeApi(conn.origin).batch(token: conn.token, saves: batch, deviceId: conn.deviceId)
                    _ = outbox.settled(sent: batch, results: results)
                } catch {
                    outbox.failed()
                    Self.log.warning("sign-out flush failed")
                }
            }
        }
        outbox.clear()
        heldBook = nil
        unsaved = nil
    }

    func rowCount() -> Int { store.count() }

    func stop() {
        monitor?.cancel()
        retryTask?.cancel()
        settleTask?.cancel()
    }
}
