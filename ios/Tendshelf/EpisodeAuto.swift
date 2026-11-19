import BackgroundTasks
import Foundation
import OSLog
@preconcurrency import TendshelfShared

/// "Download new episodes" of the followed shows (decision 0048, I3c; port of `EpisodeAuto.kt` without the notification, which is
/// Android-only: the iPhone app posts none, so there is nothing to prime and no list of what was announced). It runs three ways:
/// when the app comes to the front (`syncIfDue`, at most hourly), while something plays (`syncSoon`), and as a
/// `BGAppRefreshTask` asked for no earlier than 6 hours. The Wi-Fi rule is the requests' own network flags (invariant 4);
/// the check also asks the network first. A person's own download is never removed by a rule.
@MainActor
final class EpisodeAuto {
    nonisolated static let taskId = "app.tendshelf.ios.episodes"
    private static let log = Logger(subsystem: "app.tendshelf.ios", category: "episodes")
    private static let declinedMax = 300
    /// The engine the background task runs on (set by the first `Downloads`).
    private(set) static weak var current: EpisodeAuto?

    private let d: Downloads
    private var syncing = false
    /// Set by tests: the earliest start asked of the system.
    var scheduleHook: ((Date?) -> Void)?

    init(_ downloads: Downloads) {
        d = downloads
        Self.current = self
    }

    private var defaults: UserDefaults { d.defaults }
    private func list(_ key: String) -> [String] { defaults.stringArray(forKey: key) ?? [] }
    private func put(_ key: String, _ values: [String]) { defaults.set(values, forKey: key) }

    // MARK: The shows that ask for it, and the episodes fetched for them

    func enabled() -> Set<String> { Set(list("offline.episodes.shows")) }
    func anyOn() -> Bool { !enabled().isEmpty }

    /// The ids of episodes fetched on a show's behalf: the only ones a rule may remove.
    func marks() -> Set<String> { Set(list("offline.episodes.marks")) }

    func mark(_ id: String) { put("offline.episodes.marks", Array(marks().union([id])).sorted()) }

    func unmark(_ id: String) {
        let now = marks()
        if now.contains(id) { put("offline.episodes.marks", Array(now.subtracting([id])).sorted()) }
    }

    /// A mark without a row counts as nothing (the mark is written before the row).
    func pruneMarks(_ rows: [OfflineBook]) {
        let now = marks()
        let kept = now.filter { m in rows.contains { $0.bookId == m } }
        if kept != now { put("offline.episodes.marks", Array(kept).sorted()) }
    }

    /// Episodes the person removed on purpose: never fetched again by a rule. Newest last, capped.
    func declined() -> [String] { list("offline.episodes.declined") }

    func decline(_ id: String) {
        put("offline.episodes.declined", Array((declined().filter { $0 != id } + [id]).suffix(Self.declinedMax)))
    }

    func undecline(_ id: String) {
        let now = declined()
        if now.contains(id) { put("offline.episodes.declined", now.filter { $0 != id }) }
    }

    // MARK: Shows

    /// The shows as the last check saw them, for the Downloads page when the server cannot be reached.
    func knownShows() -> [ShowCard] {
        (defaults.string(forKey: "offline.episodes.showsCache") ?? "").split(separator: "\n").compactMap { line in
            let p = line.split(separator: "\t", omittingEmptySubsequences: false).map(String.init)
            guard p.count >= 3, !p[0].isEmpty else { return nil }
            return ShowCard(id: p[0], title: p[2], author: "", hasArt: false, newCount: 0, keepLatest: Int32(p[1]) ?? PodcastKt.DEFAULT_KEEP_LATEST)
        }
    }

    private func saveShows(_ shows: [ShowCard]) {
        let text = shows.map { "\($0.id)\t\($0.keepLatest)\t\($0.title.replacingOccurrences(of: "\t", with: " ").replacingOccurrences(of: "\n", with: " "))" }
            .joined(separator: "\n")
        defaults.set(text, forKey: "offline.episodes.showsCache")
    }

    /// What the Downloads page lists.
    func showRows() -> [AutoShowRow] {
        let on = enabled()
        return knownShows().map { AutoShowRow(id: $0.id, title: $0.title, keepLatest: $0.keepLatest, on: on.contains($0.id)) }
    }

    /// Reads the followed shows so the page can list them (also with no show switched on yet). True when the list changed.
    @discardableResult
    func refreshShows() async -> Bool {
        guard let conn = d.connection() else { return false }
        let before = defaults.string(forKey: "offline.episodes.showsCache")
        guard let shows = try? await d.api(conn.origin).podcastShows(token: conn.token) else { return false }
        saveShows(shows)
        return defaults.string(forKey: "offline.episodes.showsCache") != before
    }

    /// The person switched a show's choice. On: the job is asked for and a check runs now. Off: what is unfinished goes, what is
    /// whole stays and becomes the person's own.
    func setShow(_ id: String, on: Bool) async {
        guard !id.isEmpty, BridgeProtocolKt.validId(s: id) else { return }
        let now = enabled()
        put("offline.episodes.shows", Array(on ? now.union([id]) : now.subtracting([id])).sorted())
        if on {
            schedule()
            await sync()
        } else {
            await d.releaseShow(id)
            if !anyOn() { cancel() }
            await d.autoSync()
        }
    }

    // MARK: The system's refresh task

    /// Registers the handler; called once at launch, before the app finishes launching.
    nonisolated static func register() {
        BGTaskScheduler.shared.register(forTaskWithIdentifier: taskId, using: nil) { task in
            guard let task = task as? BGAppRefreshTask else { return }
            let box = RefreshBox(task)
            Task { @MainActor in handle(box) }
        }
    }

    private static func handle(_ box: RefreshBox) {
        let engine = current
        engine?.schedule()
        let work = Task { @MainActor in
            await engine?.sync()
            box.complete(true)
        }
        box.task.expirationHandler = {
            work.cancel()
            box.complete(false)
        }
    }

    /// Asked for no earlier than 6 hours from now; the system times it. Idempotent: a new request replaces the old one.
    func schedule() {
        let request = BGAppRefreshTaskRequest(identifier: Self.taskId)
        request.earliestBeginDate = Date(timeIntervalSinceNow: 6 * 60 * 60)
        scheduleHook?(request.earliestBeginDate)
        try? BGTaskScheduler.shared.submit(request)
    }

    func cancel() {
        BGTaskScheduler.shared.cancel(taskRequestWithIdentifier: Self.taskId)
    }

    /// Sign-out, another person, another server: the choices, marks and the task go with the downloads.
    func wipe() {
        cancel()
        for key in defaults.dictionaryRepresentation().keys where key.hasPrefix("offline.episodes.") { defaults.removeObject(forKey: key) }
    }

    // MARK: A check

    /// The foreground path: a check when a show asks for one and the last was over an hour ago.
    func syncIfDue() async {
        guard anyOn() else { return }
        schedule()
        let last = defaults.double(forKey: "offline.episodes.checkedAt")
        if Double(d.now()) - last >= Double(EpisodeDownloadsKt.EPISODE_CHECK_MS) { await sync() }
    }

    /// The playing path: the same check, off the player's thread of control.
    func syncSoon() {
        guard anyOn() else { return }
        Task { await syncIfDue() }
    }

    /// Asks the server for the chosen shows' episodes, fetches what the rules want on Wi-Fi, and removes what they name.
    func sync() async {
        guard anyOn(), !syncing else { return }
        syncing = true
        defer { syncing = false }
        guard let conn = d.connection() else { return }
        let service = d.api(conn.origin)
        let shows: [ShowCard]
        do {
            shows = try await service.podcastShows(token: conn.token)
        } catch {
            Self.log.info("episodes: shows not read")
            return
        }
        saveShows(shows)
        let followed = Set(shows.map(\.id))
        // A show the person stopped following has nothing left to ask for.
        for id in enabled() where !followed.contains(id) { await setShow(id, on: false) }
        let mine = shows.filter { enabled().contains($0.id) }
        let keep = Dictionary(uniqueKeysWithValues: mine.map { ($0.id, $0.keepLatest) })
        let wifi = d.unmetered()
        for show in mine {
            if Task.isCancelled { return }
            guard let cards = try? await service.showEpisodes(token: conn.token, showId: show.id) else {
                Self.log.info("episodes: a show was not read")
                continue
            }
            for card in cards where card.played { d.markPlayed(card.id) }
            await d.tidyEpisodes(keep)
            if EpisodeDownloadsKt.mayAutoDownloadEpisodes(anyShowOn: true, unmetered: wifi) {
                let have = Set(d.store.all().map(\.bookId))
                let wanted = EpisodeDownloadsKt.autoEpisodeWanted(cards: cards, have: have, declined: Set(declined()), keep: show.keepLatest)
                for card in wanted.prefix(Int(EpisodeDownloadsKt.EPISODE_BATCH)) { _ = await d.addEpisode(card.id, conn, auto: .show) }
            }
        }
        await d.tidyEpisodes(keep)
        await d.autoSync()
        defaults.set(Double(d.now()), forKey: "offline.episodes.checkedAt")
    }
}

extension Downloads {
    /// Removes the automatic episodes the rules name (played, beyond the latest N, over the quota); never the playing one.
    func tidyEpisodes(_ keep: [String: Int32]) async {
        let gone = EpisodeDownloadsKt.autoEpisodeEvictions(
            rows: store.all(), marks: episodes.marks(), otherMarks: marks(), keep: keep.mapValues { KotlinInt(int: $0) })
        for id in gone where id != playingNow { await remove(id, byPerson: false) }
    }

    /// A show's automatic downloads were switched off: what is not finished is dropped; what is whole stays and becomes the
    /// person's own.
    func releaseShow(_ showId: String) async {
        for row in store.all() where row.kind == OfflineKt.KIND_EPISODE && row.showId == showId && episodes.marks().contains(row.bookId) {
            if row.state == .ready || row.state == .stale {
                episodes.unmark(row.bookId)
                store.setAuto(row.bookId, auto: false)
            } else {
                await remove(row.bookId, byPerson: false)
            }
        }
    }
}

/// The system's task, finished exactly once whichever of the work and the time limit comes first.
nonisolated private final class RefreshBox: @unchecked Sendable {
    let task: BGAppRefreshTask
    private let lock = NSLock()
    private var done = false

    init(_ task: BGAppRefreshTask) { self.task = task }

    func complete(_ success: Bool) {
        lock.lock()
        defer { lock.unlock() }
        guard !done else { return }
        done = true
        task.setTaskCompleted(success: success)
    }
}
