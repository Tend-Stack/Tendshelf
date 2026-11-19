import Foundation
import OSLog
@preconcurrency import TendshelfShared

/// The automatic side of the engine (decision 0048, I3c; port of `Offline.kt:588-800` with the iPhone's deltas): the next book on
/// its own, the book remembered for an offline hand-off, the daily check, and the marks. The Wi-Fi rule is the request's own
/// network flags (invariant 4): a "held" automatic download is one the system has not run yet, and its row stays QUEUED. All
/// state is in `offline.*` defaults, so a wipe removes it with the downloads.
extension Downloads {
    static let settingsKey = "offline.settingsJson"
    static let autoBooksKey = "offline.autoBooks"
    static let autoWantKey = "offline.autoWant"
    static let autoNoRoomKey = "offline.autoNoRoom"
    static let autoNoRoomShowKey = "offline.autoNoRoomShow"
    static let autoNextKey = "offline.autoNext"
    static let autoDropKey = "offline.autoDrop"
    private static let dayMs: Int64 = 24 * 60 * 60 * 1000
    private static let autoLog = Logger(subsystem: "app.tendshelf.ios", category: "autodownload")

    // MARK: Marks

    /// The ids of books and episodes fetched as the next item: the only ones this path may remove.
    func marks() -> Set<String> { Set(defaults.stringArray(forKey: Self.autoBooksKey) ?? []) }

    private func setMarks(_ s: Set<String>) { defaults.set(Array(s).sorted(), forKey: Self.autoBooksKey) }

    func mark(_ id: String) { setMarks(marks().union([id])) }

    func unmark(_ id: String) {
        let now = marks()
        if now.contains(id) { setMarks(now.subtracting([id])) }
    }

    /// The marks of both automatic paths: the next item and the shows' "Download new episodes".
    func allMarks() -> Set<String> { marks().union(episodes.marks()) }

    /// True for a book or episode that was downloaded on its own and has not been made the person's own.
    func isAuto(_ id: String) -> Bool { allMarks().contains(id) }

    /// Written before the row; a mark without a row counts as nothing.
    func markFor(_ id: String, _ auto: AutoKind?) {
        switch auto {
        case nil:
            unmark(id)
            episodes.unmark(id)
            episodes.undecline(id)
        case .nextItem?:
            mark(id)
            defaults.removeObject(forKey: Self.autoNoRoomKey)
        case .show?:
            episodes.mark(id)
            defaults.removeObject(forKey: Self.autoNoRoomShowKey)
        }
    }

    /// True when the last automatic item was not fetched for lack of room; the Downloads page says so.
    func autoNoRoomNoted() -> Bool {
        defaults.bool(forKey: Self.autoNoRoomKey) || defaults.bool(forKey: Self.autoNoRoomShowKey)
    }

    /// Pressing Download on an automatic item makes it the person's own: no Wi-Fi rule, high priority, and what has not arrived
    /// starts again from the start (resume data is never kept, and the old request carries the Wi-Fi flags).
    func makePersons(_ id: String, _ row: OfflineBook, _ conn: Connection) async {
        unmark(id)
        episodes.unmark(id)
        store.setAuto(id, auto: false)
        guard row.state != .ready, row.state != .stale else { return }
        for file in store.files(id) where file.step.name != FileStep.done.name {
            let key = Self.key(id, file.cacheKey)
            await transport.cancel(key: key)
            store.setArrived(id, file.cacheKey, length: 0, bytes: 0, name: "")
            store.setStep(id, file.cacheKey, .queued, failures: 0)
        }
        for file in store.files(id) where file.step.name == FileStep.queued.name { start(file, with: conn) }
    }

    // MARK: Settings

    /// The Download the next book switch changed in Settings: kept at once; off removes the unfinished automatic ones.
    func setAutoDownload(_ on: Bool) {
        let s = keptSettings()
        keep(s.doCopy(
            speed: s.speed, skipBackS: s.skipBackS, skipForwardS: s.skipForwardS, autoRewind: s.autoRewind,
            nextInSeries: s.nextInSeries, rewindSteps: s.rewindSteps, skipQuiet: s.skipQuiet, autoDownload: on, night: s.night))
        if !on { Task { await autoSync() } }
    }

    // MARK: The next book, on its own (decision 0035)

    /// The player is near the end of [book] but the network is metered: ask again at the next allowed moment.
    func autoWant(_ book: String) { defaults.set(book, forKey: Self.autoWantKey) }

    func autoForget() { defaults.removeObject(forKey: Self.autoWantKey) }

    /// Applies the switch to what is unfinished (off removes it) and tidies the server's queue.
    func autoSync() async {
        await tidyQueue()
        let rows = store.all()
        let kept = marks().filter { m in rows.contains { $0.bookId == m } }
        if kept != marks() { setMarks(kept) }
        if !keptSettings().autoDownload {
            for row in OfflineKt.autoUnfinished(rows: rows, marks: kept) { await remove(row.bookId, byPerson: false) }
            for key in [Self.autoWantKey, Self.autoNoRoomKey, Self.autoNextKey] { defaults.removeObject(forKey: key) }
        }
        episodes.pruneMarks(rows)
    }

    /// The book to start when the server could not be asked at the end of [ended]: the remembered next book, only when it is
    /// whole on the phone. Cleared when used, so it is never offered twice.
    func offlineNext(_ ended: String) -> String? {
        let next = OfflineKt.offlineNextFor(stored: defaults.string(forKey: Self.autoNextKey), ended: ended) {
            KotlinBoolean(bool: self.playable($0))
        }
        if next != nil { defaults.removeObject(forKey: Self.autoNextKey) }
        return next
    }

    /// [book] started without the server's say; the server's queue still holds it until `tidyQueue` succeeds.
    func autoDrop(_ book: String) { defaults.set(book, forKey: Self.autoDropKey) }

    /// Takes a book that was started from the phone off the server's queue, once a connection works.
    func tidyQueue() async {
        guard let book = defaults.string(forKey: Self.autoDropKey), !book.isEmpty, let conn = connection() else { return }
        do {
            let status = try await api(conn.origin).queueRemove(token: conn.token, bookId: book)
            if OfflineKt.queueDeleteDone(status: Int32(status)) {
                // Only if it still names this book: a newer one must not be lost.
                if defaults.string(forKey: Self.autoDropKey) == book { defaults.removeObject(forKey: Self.autoDropKey) }
                Self.autoLog.info("next book: queue tidied (\(status))")
            } else {
                Self.autoLog.info("next book: queue tidy answered \(status), later")
            }
        } catch {
            Self.autoLog.info("next book: queue tidy not sent")
        }
    }

    /// Asks the server for the item after [ended] and, if the rules allow, downloads it on Wi-Fi.
    func autoFetch(_ ended: String) async {
        guard let conn = connection(), keptSettings().autoDownload, unmetered() else { return }
        guard OfflineKt.autoUnfinished(rows: store.all(), marks: marks()).isEmpty else { return }
        let service = api(conn.origin)
        let next: String?
        do {
            next = try await service.queuePeek(token: conn.token, ended: ended)
        } catch {
            Self.autoLog.info("next book: peek failed")
            return
        }
        // Remembered before anything else, so a book the person already has counts too (the plane case).
        defaults.removeObject(forKey: Self.autoWantKey)
        if let next { defaults.set(OfflineKt.encodeAutoNext(ended: ended, next: next), forKey: Self.autoNextKey) } else {
            defaults.removeObject(forKey: Self.autoNextKey)
        }
        guard let next, store.get(id: next) == nil else { return }
        if PodcastKt.isEpisodeId(id: next) {
            _ = await addEpisode(next, conn, auto: .nextItem)
            return
        }
        do {
            let text = try await service.manifestText(token: conn.token, bookId: next)
            let apiPaths = ApiPaths(server: conn.origin.text)
            guard let manifest = PlayerParseKt.parseManifest(body: text, paths: apiPaths), manifest.id == next else { return }
            switch OfflineKt.autoDownloadPlan(m: manifest, paths: apiPaths, freeBytes: free(false), keptAutoBytes: OfflineKt.autoBytes(rows: store.all(), marks: marks())) {
            case let go as AutoPlanGo:
                let mimes = Dictionary(manifest.files.map { ($0.id, $0.mime) }, uniquingKeysWith: { first, _ in first })
                await enqueue(
                    next, conn, service, plan: go.plan, mimes: mimes, text: text, title: manifest.title, author: manifest.author,
                    durationMs: manifest.durationMs, kind: OfflineKt.KIND_BOOK, showId: "", publishedAt: 0, place: manifest.progress,
                    artwork: { await service.cover(token: conn.token, bookId: next) }, chapters: nil, auto: .nextItem)
            case is AutoPlanNoRoom:
                defaults.set(true, forKey: Self.autoNoRoomKey)
            default:
                Self.autoLog.info("next book: skipped")
            }
        } catch {
            Self.autoLog.info("next book: not fetched")
        }
    }

    // MARK: Coming to the front

    /// Foreground work after `reconcile`: the switch and the queue, the book that was wanted while the network was metered, the
    /// daily check, and the shows' check (at most hourly).
    func foreground() async {
        await autoSync()
        if let want = defaults.string(forKey: Self.autoWantKey), !want.isEmpty { await autoFetch(want) }
        await dailyCheck()
        await episodes.syncIfDue()
    }

    /// At most once a day for each book (decision 0019, point 10): same files, new words; changed files, old; gone, removed.
    /// Books only: an episode has no book answer to compare, and the show check looks after it.
    func dailyCheck() async {
        guard let conn = connection() else { return }
        let whole = store.all().filter { ($0.state == .ready || $0.state == .stale) && $0.kind != OfflineKt.KIND_EPISODE }
        guard !whole.isEmpty else { return }
        let service = api(conn.origin)
        let apiPaths = ApiPaths(server: conn.origin.text)
        let at = now()
        for row in whole {
            if at - store.checkedAt(row.bookId) < Self.dayMs { continue }
            do {
                let text = try await service.manifestText(token: conn.token, bookId: row.bookId)
                guard let fresh = PlayerParseKt.parseManifest(body: text, paths: apiPaths), fresh.id == row.bookId else { continue }
                let old = store.manifest(id: row.bookId).flatMap { PlayerParseKt.parseManifest(body: $0, paths: apiPaths) }
                switch OfflineKt.checkVerdict(status: 200, old: old, new: fresh) {
                case .same: store.touchChecked(row.bookId, at: at)
                case .update: store.setManifest(row.bookId, text, at: at)
                case .stale:
                    store.setState(id: row.bookId, s: .stale)
                    store.touchChecked(row.bookId, at: at)
                default: break
                }
            } catch let e as ApiError {
                if OfflineKt.checkVerdict(status: Int32(e.status), old: nil, new: nil) == .remove { await remove(row.bookId, byPerson: false) }
            } catch {}
            changed(row.bookId)
        }
    }
}
