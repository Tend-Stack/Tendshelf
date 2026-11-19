import Foundation
import OSLog
@preconcurrency import TendshelfShared

/// The download engine of the iPhone app (decision 0048, I3b; port of `Offline.kt` without Media3). It fetches a book's
/// manifest, checks the right and the room, keeps the answer and the cover, and creates one background task for every file at
/// once, so a book never waits for the app to wake between files. The shared rules decide: `downloadPlan`, `episodePlan`,
/// `fileComplete`, `bookStateOf`, `downloadRetryDelayMs`, `downloadPercent`. Invariant 3: a row is READY only when every file
/// arrived whole. Invariant 1: the token is only ever an `Authorization` header of a request to the pinned origin.
@MainActor
final class Downloads: DownloadEvents {
    private static let log = Logger(subsystem: "app.tendshelf.ios", category: "downloads")
    static let wipePendingKey = "offline.wipePending"

    let store: SqliteOfflineStore
    let paths: OfflinePaths
    let transport: DownloadTransport
    let api: (Origin) -> PlayerAPI
    let connection: () -> Connection?
    let userId: () -> String?
    let free: (Bool) -> Int64
    let notice: (String) -> Void
    let now: () -> Int64
    let defaults: UserDefaults
    private let retryScale: Double
    /// True while the active network is Wi-Fi-like: satisfied, not expensive, not constrained (decision 0048, I3c).
    let unmetered: () -> Bool
    /// The automatic episode rules of the followed shows (`EpisodeAuto.swift`).
    private(set) lazy var episodes = EpisodeAuto(self)
    /// The book or episode the player has now: a rule never removes it.
    var playingNow = ""

    /// A book's row or files changed (its id): the Downloads page and the book page's button follow.
    var onChange: ((String) -> Void)?

    private var floors: [String: Int32] = [:]
    private var retries: [String: Task<Void, Never>] = [:]
    private var adding: Set<String> = []
    private var wiping: Task<Void, Never>?

    /// [free] gets true for a person's own download (the room that counts as important), false for an automatic one.
    /// [retryScale] shortens the waits of the retry rule in tests.
    init(
        store: SqliteOfflineStore, paths: OfflinePaths, transport: DownloadTransport, api: @escaping (Origin) -> PlayerAPI,
        connection: @escaping () -> Connection?, userId: @escaping () -> String? = { nil }, free: @escaping (Bool) -> Int64,
        notice: @escaping (String) -> Void, now: @escaping () -> Int64 = { Int64(Date().timeIntervalSince1970 * 1000) },
        defaults: UserDefaults = .standard, retryScale: Double = 1, unmetered: @escaping () -> Bool = { UnmeteredMonitor.shared.value }
    ) {
        self.store = store
        self.paths = paths
        self.transport = transport
        self.api = api
        self.connection = connection
        self.userId = userId
        self.free = free
        self.notice = notice
        self.now = now
        self.defaults = defaults
        self.retryScale = retryScale
        self.unmetered = unmetered
        transport.events = self
        // Files a kill left between "moved" and "recorded" are not trusted: their task is restarted by `reconcile`.
        try? FileManager.default.removeItem(at: paths.incoming)
        if defaults.bool(forKey: Self.wipePendingKey) { wipe() }
    }

    /// The bytes a download may use: "important" for a person's own, "opportunistic" for an automatic one.
    static func freeBytes(important: Bool) -> Int64 {
        let url = OfflinePaths.defaultRoot().deletingLastPathComponent()
        let keys: Set<URLResourceKey> = [.volumeAvailableCapacityForImportantUsageKey, .volumeAvailableCapacityForOpportunisticUsageKey]
        let values = try? url.resourceValues(forKeys: keys)
        return important ? (values?.volumeAvailableCapacityForImportantUsage ?? 0) : (values?.volumeAvailableCapacityForOpportunisticUsage ?? 0)
    }

    // MARK: Keys

    nonisolated static func key(_ book: String, _ cacheKey: String) -> String { "\(book)|\(cacheKey)" }

    nonisolated static func split(_ key: String) -> (book: String, cacheKey: String)? {
        guard let bar = key.firstIndex(of: "|") else { return nil }
        let book = String(key[..<bar])
        let cache = String(key[key.index(after: bar)...])
        return book.isEmpty || cache.isEmpty ? nil : (book, cache)
    }

    // MARK: Add

    /// Fetches the manifest, checks the right and the room, keeps the answer and the cover, and queues every file.
    func add(_ id: String) async {
        guard BridgeProtocolKt.validId(s: id), let conn = connection(), !adding.contains(id) else { return }
        adding.insert(id)
        defer { adding.remove(id) }
        await wiping?.value
        if PodcastKt.isEpisodeId(id: id) {
            _ = await addEpisode(id, conn, auto: nil)
            return
        }
        if let existing = store.get(id: id), existing.state != .failed, existing.state != .stale {
            if isAuto(id) { await makePersons(id, existing, conn) }
            changed(id)
            return
        }
        let service = api(conn.origin)
        do {
            let text = try await service.manifestText(token: conn.token, bookId: id)
            let apiPaths = ApiPaths(server: conn.origin.text)
            guard let manifest = PlayerParseKt.parseManifest(body: text, paths: apiPaths), manifest.id == id else {
                return refuse(Words.Key.downloadFailed)
            }
            switch OfflineKt.downloadPlan(m: manifest, paths: apiPaths, freeBytes: free(true)) {
            case let ok as PlanOk:
                let mimes = Dictionary(manifest.files.map { ($0.id, $0.mime) }, uniquingKeysWith: { first, _ in first })
                await enqueue(
                    id, conn, service, plan: ok, mimes: mimes, text: text, title: manifest.title, author: manifest.author,
                    durationMs: manifest.durationMs, kind: OfflineKt.KIND_BOOK, showId: "", publishedAt: 0,
                    place: manifest.progress, artwork: { await service.cover(token: conn.token, bookId: id) }, chapters: nil)
            case is PlanNotAllowed: refuse(Words.Key.downloadNotAllowed)
            case is PlanNoRoom: refuse(Words.Key.downloadNoRoom)
            default: refuse(Words.Key.downloadNothing)
            }
        } catch let e as ApiError {
            Self.log.warning("download not started: server answered \(e.status)")
            refuse(e.status == 403 ? Words.Key.downloadNotAllowed : Words.Key.downloadFailed)
        } catch {
            refuse(Words.Key.downloadFailed)
        }
    }

    /// One episode: the playback document (so it starts with no network), the picture, the chapters and the file.
    /// Returns false when nothing was queued.
    @discardableResult
    func addEpisode(_ id: String, _ conn: Connection, auto: AutoKind?) async -> Bool {
        if let existing = store.get(id: id), existing.state != .failed, existing.state != .stale {
            // Pressing Download on an automatic episode makes it the person's own.
            if auto == nil, isAuto(id) { await makePersons(id, existing, conn) }
            changed(id)
            return false
        }
        let service = api(conn.origin)
        do {
            let text = try await service.episodeText(token: conn.token, id: id)
            guard let doc = PlayerParseKt.parseEpisode(body: text), doc.id == id else {
                if auto == nil { refuse(Words.Key.downloadFailed) }
                return false
            }
            let apiPaths = ApiPaths(server: conn.origin.text)
            let plan: Plan
            if auto != nil {
                switch EpisodeDownloadsKt.autoEpisodePlan(
                    doc: doc, paths: apiPaths, freeBytes: free(false), keptAutoBytes: OfflineKt.autoBytes(rows: store.all(), marks: allMarks()))
                {
                case let go as AutoPlanGo: plan = go.plan
                case is AutoPlanNoRoom:
                    defaults.set(true, forKey: auto == .nextItem ? Self.autoNoRoomKey : Self.autoNoRoomShowKey)
                    return false
                default: return false
                }
            } else {
                plan = EpisodeDownloadsKt.episodePlan(doc: doc, paths: apiPaths, freeBytes: free(true))
            }
            switch plan {
            case let ok as PlanOk:
                let chapters: (() async -> String?)? = doc.chaptersUrl.isEmpty ? nil : { await service.chaptersText(token: conn.token, path: doc.chaptersUrl) }
                await enqueue(
                    id, conn, service, plan: ok, mimes: [id: doc.mime], text: text, title: doc.title, author: doc.show,
                    durationMs: doc.durationMs, kind: OfflineKt.KIND_EPISODE, showId: doc.showId, publishedAt: doc.publishedAt,
                    place: doc.progress, artwork: { await service.podcastArt(token: conn.token, artPath: doc.artUrl) }, chapters: chapters,
                    auto: auto)
                return true
            case is PlanNoRoom: refuse(Words.Key.downloadNoRoom)
            default: refuse(Words.Key.downloadNothing)
            }
        } catch let e as ApiError {
            Self.log.warning("episode not started: server answered \(e.status)")
            if auto == nil { refuse(e.status == 404 || e.status == 403 ? Words.Key.downloadNotAllowed : Words.Key.downloadFailed) }
        } catch {
            if auto == nil { refuse(Words.Key.downloadFailed) }
        }
        return false
    }

    func refuse(_ key: String) {
        notice(words(key))
    }

    /// Keeps the cover, the chapters, the row and the files, then makes one task for every file.
    func enqueue(
        _ id: String, _ conn: Connection, _ service: PlayerAPI, plan: PlanOk, mimes: [String: String], text: String,
        title: String, author: String, durationMs: Int64, kind: String, showId: String, publishedAt: Int64,
        place: RemoteProgress?, artwork: () async -> Data?, chapters: (() async -> String?)?, auto: AutoKind? = nil
    ) async {
        // A failed or stale book being added again: the old tasks and files go first.
        await dropFiles(id)
        do {
            try paths.prepare()
        } catch {
            return refuse(Words.Key.downloadFailed)
        }
        store.reopen()
        if let bytes = await artwork(), let cover = paths.cover(id) { write(bytes, to: cover) }
        if let chapters, let json = await chapters(), let file = paths.chapters(id) { write(Data(json.utf8), to: file) }
        floors[id] = nil
        // The mark is written before the row; a mark without a row counts as nothing.
        markFor(id, auto)
        let isEpisode = kind == OfflineKt.KIND_EPISODE
        let row = OfflineBook(
            bookId: id, userId: userId() ?? "", title: title, author: author, durationMs: durationMs, sizeBytes: plan.total,
            state: .queued, placeMs: place?.positionMs ?? 0, placeAt: place?.clientUpdatedAt ?? 0, kind: kind, showId: showId,
            publishedAt: publishedAt, played: isEpisode && place?.finished == true)
        store.put(row, text, at: now())
        let files = plan.files.map {
            OfflineFile(
                bookId: id, cacheKey: $0.cacheKey, url: $0.url, planned: isEpisode ? 0 : $0.size, mime: mimes[$0.fileId] ?? "",
                auto: auto != nil)
        }
        store.putFiles(id, files)
        for file in files { start(file, with: conn) }
        changed(id)
    }

    private func write(_ data: Data, to url: URL) {
        try? FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
        try? data.write(to: url, options: [.atomic, .completeFileProtectionUntilFirstUserAuthentication])
    }

    // MARK: Tasks

    /// One background task for [file]: a GET with the bearer, 60 s of silence allowed. A person's download may use any
    /// network; an automatic one never an expensive or constrained network (invariant 4).
    func start(_ file: OfflineFile, with conn: Connection, resume: Data? = nil) {
        guard conn.origin.matches(url: file.url), let url = URL(string: file.url) else {
            store.setStep(file.bookId, file.cacheKey, .failed, failures: file.failures)
            return
        }
        var request = URLRequest(url: url)
        request.httpMethod = "GET"
        request.setValue("Bearer \(conn.token)", forHTTPHeaderField: "Authorization")
        request.timeoutInterval = 60
        request.allowsExpensiveNetworkAccess = !file.auto
        request.allowsConstrainedNetworkAccess = !file.auto
        transport.start(key: Self.key(file.bookId, file.cacheKey), request: request, resumeData: resume)
    }

    private func restart(_ file: OfflineFile) {
        guard let conn = connection() else { return }
        start(file, with: conn)
    }

    // MARK: Remove

    /// Drops the book's tasks, bytes, row and cover. [byPerson] is kept for the automatic rules of I3c.
    func remove(_ id: String, byPerson: Bool) async {
        if byPerson, PodcastKt.isEpisodeId(id: id) { episodes.decline(id) }
        await dropFiles(id)
        unmark(id)
        episodes.unmark(id)
        defaults.removeObject(forKey: Self.autoNoRoomKey)
        defaults.removeObject(forKey: Self.autoNoRoomShowKey)
        store.remove(id: id)
        if let cover = paths.cover(id) { try? FileManager.default.removeItem(at: cover) }
        if let chapters = paths.chapters(id) { try? FileManager.default.removeItem(at: chapters) }
        if let text = paths.text(id) { try? FileManager.default.removeItem(at: text) }
        floors[id] = nil
        changed(id)
    }

    /// Cancels the book's tasks and deletes its audio folder; the row stays.
    func dropFiles(_ id: String) async {
        let files = store.files(id)
        for file in files {
            let key = Self.key(id, file.cacheKey)
            retries[key]?.cancel()
            retries[key] = nil
        }
        if let folder = paths.audio(id) { try? FileManager.default.removeItem(at: folder) }
        for file in files { await transport.cancel(key: Self.key(id, file.cacheKey)) }
    }

    // MARK: DownloadEvents

    func progressed(key: String, written: Int64, expected: Int64) {
        guard let (book, cache) = Self.split(key), let file = store.files(book).first(where: { $0.cacheKey == cache }) else { return }
        guard file.step.name == FileStep.queued.name || file.step.name == FileStep.running.name else { return }
        store.setArrived(book, cache, length: expected, bytes: written, name: "")
        if file.step.name == FileStep.queued.name { store.setStep(book, cache, .running, failures: file.failures) }
        recompute(book)
    }

    func arrived(key: String, status: Int, length: Int64, mime: String?, staged: URL, bytes: Int64) {
        guard let (book, cache) = Self.split(key), let row = store.get(id: book),
              let file = store.files(book).first(where: { $0.cacheKey == cache }), (200..<300).contains(status)
        else {
            try? FileManager.default.removeItem(at: staged)
            return
        }
        retries[key]?.cancel()
        retries[key] = nil
        let whole: Bool
        if row.kind == OfflineKt.KIND_EPISODE {
            whole = EpisodeDownloadsKt.episodeFileComplete(contentLength: length, cachedBytes: bytes, downloadedBytes: bytes)
        } else {
            whole = OfflineKt.fileComplete(planned: file.planned, compat: cache.hasSuffix(":c"), contentLength: length, cachedBytes: bytes)
        }
        let type = file.mime.isEmpty ? (mime ?? "") : file.mime
        let name = OfflineKt.offlineFileName(cacheKey: cache, mime: type)
        guard whole, !name.isEmpty, let destination = paths.audioFile(book, name) else {
            try? FileManager.default.removeItem(at: staged)
            store.setArrived(book, cache, length: length, bytes: bytes, name: "")
            store.setStep(book, cache, .broken, failures: file.failures)
            recompute(book)
            return
        }
        do {
            let fm = FileManager.default
            try fm.createDirectory(at: destination.deletingLastPathComponent(), withIntermediateDirectories: true)
            try? fm.removeItem(at: destination)
            try fm.moveItem(at: staged, to: destination)
        } catch {
            try? FileManager.default.removeItem(at: staged)
            store.setStep(book, cache, .broken, failures: file.failures)
            recompute(book)
            return
        }
        store.setArrived(book, cache, length: length, bytes: bytes, name: name)
        store.setStep(book, cache, .done, failures: file.failures)
        recompute(book)
    }

    func failed(key: String, status: Int, retryAfterS: Int, resumeData: Data?) {
        guard let (book, cache) = Self.split(key), store.get(id: book) != nil,
              let file = store.files(book).first(where: { $0.cacheKey == cache }) else { return }
        let tries = file.failures
        if let resumeData, tries < Int(OfflineKt.DOWNLOAD_MAX_TRIES) {
            // The resume data embeds the request, so it is used once, from memory, and never kept.
            store.setStep(book, cache, .queued, failures: tries + 1)
            if let conn = connection() { start(file, with: conn, resume: resumeData) }
            recompute(book)
            return
        }
        if let delay = OfflineKt.downloadRetryDelayMs(status: Int32(status), failuresBefore: Int32(tries), retryAfterS: Int32(retryAfterS)) {
            store.setStep(book, cache, .queued, failures: tries + 1)
            retries[key]?.cancel()
            let wait = UInt64(max(0, Double(delay.int64Value) * retryScale * 1_000_000))
            retries[key] = Task { [weak self] in
                try? await Task.sleep(nanoseconds: wait)
                guard !Task.isCancelled, let self else { return }
                retries[key] = nil
                guard let file = store.files(book).first(where: { $0.cacheKey == cache }), file.step.name == FileStep.queued.name else { return }
                restart(file)
            }
        } else {
            store.setStep(book, cache, .failed, failures: tries)
        }
        recompute(book)
    }

    /// Folds the files' steps into the book's one state.
    private func recompute(_ id: String) {
        guard let row = store.get(id: id) else { return }
        let files = store.files(id)
        guard !files.isEmpty else { return }
        let steps = files.map(\.step)
        if let next = OfflineKt.bookStateOf(steps: steps, expected: Int32(files.count), previous: row.state, paused: false), next != row.state {
            store.setState(id: id, s: next)
        }
        // A feed's announced size is often wrong: a finished episode shows what arrived.
        if row.kind == OfflineKt.KIND_EPISODE, store.get(id: id)?.state == .ready, let file = files.first {
            store.setSize(id: id, bytes: EpisodeDownloadsKt.episodeSize(contentLength: file.length, cachedBytes: file.bytes))
        }
        changed(id)
    }

    func changed(_ id: String) {
        onChange?(id)
    }

    // MARK: Reading

    /// How the page sees the book: none, a live percent that never goes back, or a full bar.
    func view(_ id: String) -> DlView {
        let row = store.get(id: id)
        let files = store.files(id).map {
            FileProgress(planned: $0.planned, contentLength: $0.length, bytes: $0.bytes, done: $0.step.name == FileStep.done.name)
        }
        let p = OfflineKt.downloadPercent(files: files, previous: floors[id] ?? 0)
        floors[id] = max(floors[id] ?? 0, p.pct)
        return OfflineKt.dlView(row: row, p: p)
    }

    func rows() -> [OfflineBook] { store.all() }

    /// The room a person's own download may still use (the page's `dl room`).
    func freeBytes() -> Int64 { free(true) }

    func hasOtherPerson(_ userId: String) -> Bool {
        store.all().contains { $0.userId != userId }
    }

    /// A row the player may start from the phone: finished and not removed.
    func playable(_ id: String) -> Bool { playableRow(id) != nil }

    func playableRow(_ id: String) -> OfflineBook? {
        guard let row = store.get(id: id), row.state == .ready || row.state == .stale else { return nil }
        return row
    }

    /// Whether the book's audio files are on disk (the page offers Play only then).
    func hasAudio(_ id: String) -> Bool {
        let files = store.files(id)
        return !files.isEmpty && files.allSatisfy { localFile(id, $0.cacheKey) != nil }
    }

    func coverData(_ id: String) -> Data? {
        paths.cover(id).flatMap { try? Data(contentsOf: $0) }
    }

    /// The stored file of [cacheKey], when it is marked arrived and is really there.
    func localFile(_ bookId: String, _ cacheKey: String) -> URL? {
        guard let file = store.files(bookId).first(where: { $0.cacheKey == cacheKey }), file.step.name == FileStep.done.name,
              let url = paths.audioFile(bookId, file.name), FileManager.default.fileExists(atPath: url.path) else { return nil }
        return url
    }

    // MARK: Reconcile

    /// At launch and when the app comes to the front: a file the system no longer holds a task for starts again from the
    /// start (resume data is memory only), and a book that has no files at all failed.
    func reconcile() async {
        let held = await transport.runningKeys()
        for row in store.all() where row.state == .queued || row.state == .downloading {
            let files = store.files(row.bookId)
            if files.isEmpty {
                store.setState(id: row.bookId, s: .failed)
                changed(row.bookId)
                continue
            }
            for file in files where file.step.name == FileStep.queued.name || file.step.name == FileStep.running.name {
                let key = Self.key(file.bookId, file.cacheKey)
                if held.contains(key) || retries[key] != nil { continue }
                restart(file)
            }
        }
        // A task that belongs to no file is cancelled.
        for key in held {
            guard let (book, cache) = Self.split(key), store.files(book).contains(where: { $0.cacheKey == cache }) else {
                await transport.cancel(key: key)
                continue
            }
        }
    }

    // MARK: Wipe

    /// Sign-out, another person, another server: every downloaded byte, row and background task goes. The rows go at once, so
    /// nothing can be offered or played; the default makes the rest durable, so a wipe a kill interrupted is finished at launch.
    @discardableResult
    func wipe() -> Task<Void, Never> {
        defaults.set(true, forKey: Self.wipePendingKey)
        for key in defaults.dictionaryRepresentation().keys where key.hasPrefix("offline.") && key != Self.wipePendingKey {
            defaults.removeObject(forKey: key)
        }
        episodes.wipe()
        store.clear()
        floors = [:]
        for task in retries.values { task.cancel() }
        retries = [:]
        let previous = wiping
        let task = Task { [weak self] in
            await previous?.value
            await self?.finishWipe()
        }
        wiping = task
        return task
    }

    private func finishWipe() async {
        await transport.cancelAll()
        store.close()
        try? FileManager.default.removeItem(at: paths.root)
        for key in defaults.dictionaryRepresentation().keys where key.hasPrefix("offline.") { defaults.removeObject(forKey: key) }
        defaults.set(false, forKey: Self.wipePendingKey)
        Self.log.info("offline: wiped")
    }
}

extension Downloads: DownloadsControl {}

/// Why a download is automatic: the next item of the queue (decision 0035) or a followed show's new episode (decision 0038).
enum AutoKind { case nextItem, show }
