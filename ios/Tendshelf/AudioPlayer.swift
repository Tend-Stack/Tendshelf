@preconcurrency import AVFoundation
import Foundation
import OSLog
@preconcurrency import TendshelfShared

/// The player: port of `BookEngine.kt` + `BookPlayer.kt` + `PlayerRemote.snapshot`. One `AVQueuePlayer` with one item per
/// playable file (the current one and the next), book time = `BookTimeline`. Items load through `MediaLoader`, so the token
/// never rides in a URL (invariant 1). A newer `start` wins over an older one still fetching (`seq`, as `BookEngine.kt:152`).
/// Logs hold kinds and counts only.
@MainActor
final class AudioPlayer {
    private static let log = Logger(subsystem: "app.tendshelf.ios", category: "player")
    /// The play-to-end margin below which a periodic tick is not trusted to be "at the end".
    private static let tickMs: Int64 = 500

    // MARK: Collaborators and hooks

    private let connection: @Sendable () -> Connection?
    private let makeApi: (Origin) -> PlayerAPI
    let loader: MediaLoader
    let player = AVQueuePlayer()

    var onChange: (() -> Void)?
    var onTick: (() -> Void)?
    var onEnded: (() -> Void)?
    var onBookStarted: ((String, Int64?, Bool) -> Void)?
    var onBookChanging: (() -> Void)?
    var onFailed: ((String) -> Void)?
    /// The place this device has not sent yet (I2c); nil until then.
    var pendingFor: (String) -> Save? = { _ in nil }
    /// Asked before sound starts: the audio session is set up for the kind of item ([live] for a station).
    var willPlay: ((Bool) -> Void)?
    /// The chapter shown changed (book time moved across a chapter).
    var onChapterChanged: (() -> Void)?
    /// The sound started or stopped (from `timeControlStatus`): playing, paused, or waiting while a play is wanted.
    var onSound: ((Sound) -> Void)?
    /// A seek was made by a command (the writer settles a place after a seek while paused).
    var onSeek: (() -> Void)?
    /// The player left its ended state (a replay): the end is a hand-off again.
    var onLeftEnded: (() -> Void)?

    enum Sound { case playing, paused, waiting }
    var liveWord = "Live"

    // MARK: State

    private var seq = 0
    private(set) var bookId = ""
    private(set) var error = ""
    private(set) var ended = false {
        didSet { if oldValue, !ended { onLeftEnded?() } }
    }
    private(set) var settings: PlayerSettings?
    private(set) var title = ""
    private(set) var author = ""
    private(set) var episodeTitle: String?
    private(set) var chapters: [Chapter] = []
    private(set) var art: Data?
    private(set) var live = false
    private(set) var liveNow = ""
    private(set) var speed = 1.0
    private var station: StationCard?
    private var timeline: BookTimeline?
    private var durationMs: Int64 = 0
    private var files: [AudioFile] = []
    private var isEpisode = false
    private var showSettings: ShowSettings?
    private var rules: SkipRules?
    private var ruleDurationMs: Int64 = 0
    private var outroDone = false
    private var liveAttempt = 0
    private var liveRetry: Task<Void, Never>?
    private var liveConnect: Task<Void, Never>?
    private var liveNowTask: Task<Void, Never>?
    private var preparing = false
    private var wantsPlay = false
    /// True from a play request until a pause, a failure or the end: the person wants sound.
    var playRequested: Bool { wantsPlay }
    private var shownChapter = -1
    /// File index of each queued item.
    private var fileOf: [ObjectIdentifier: Int] = [:]
    private var reported = Set<ObjectIdentifier>()
    private var observers: [NSKeyValueObservation] = []
    private var itemObservers: [ObjectIdentifier: NSKeyValueObservation] = [:]
    private var tokens: [NSObjectProtocol] = []
    private var timeObserver: Any?

    init(connection: @escaping @Sendable () -> Connection?, makeApi: @escaping (Origin) -> PlayerAPI, loader: MediaLoader? = nil) {
        self.connection = connection
        self.makeApi = makeApi
        self.loader = loader ?? MediaLoader(connection: connection)
        player.automaticallyWaitsToMinimizeStalling = true
        player.actionAtItemEnd = .advance
        // A station's address is never handed to an AirPlay video receiver (decision 0049); AirPlay audio still routes through the session.
        player.allowsExternalPlayback = false
        observers.append(player.observe(\.timeControlStatus) { [weak self] _, _ in
            Task { @MainActor in self?.statusChanged() }
        })
        observers.append(player.observe(\.currentItem) { [weak self] _, _ in
            Task { @MainActor in self?.currentItemChanged() }
        })
        let center = NotificationCenter.default
        tokens.append(center.addObserver(forName: AVPlayerItem.didPlayToEndTimeNotification, object: nil, queue: .main) { [weak self] n in
            let item = n.object as? AVPlayerItem
            MainActor.assumeIsolated { self?.didPlayToEnd(item) }
        })
        tokens.append(center.addObserver(forName: AVPlayerItem.failedToPlayToEndTimeNotification, object: nil, queue: .main) { [weak self] n in
            let item = n.object as? AVPlayerItem
            MainActor.assumeIsolated { self?.itemFailed(item) }
        })
        timeObserver = player.addPeriodicTimeObserver(forInterval: CMTime(value: Self.tickMs, timescale: 1000), queue: .main) { [weak self] _ in
            MainActor.assumeIsolated { self?.tick() }
        }
    }

    // MARK: Starting

    /// Starts [id]: a book, an episode or a station. [atMs] is a place the person chose (not for a station).
    func start(_ id: String, atMs: Int64?) {
        seq += 1
        let mine = seq
        guard let conn = connection() else {
            fail("signedOut")
            return
        }
        if RadioKt.isStationId(id: id) {
            Task { await startStation(id, conn, mine) }
        } else if PodcastKt.isEpisodeId(id: id) {
            Task { await startEpisode(id, atMs, conn, mine) }
        } else {
            Task { await startBook(id, atMs, conn, mine) }
        }
    }

    private func failure(_ error: Error, live: Bool) -> String {
        let status = (error as? ApiError)?.status ?? -1
        if status < 0 { return "book" } // an answer that was not a book at all
        return PlayFailureKt.playFailureKind(status: Int32(status), otherServer: false, live: live)
    }

    private func startBook(_ id: String, _ atMs: Int64?, _ conn: Connection, _ mine: Int) async {
        let api = makeApi(conn.origin)
        do {
            let settings = try await api.settings(token: conn.token)
            guard seq == mine else { return }
            let manifest = try await api.manifest(token: conn.token, bookId: id)
            guard seq == mine else { return }
            let art = await api.cover(token: conn.token, bookId: manifest.id)
            guard seq == mine else { return }
            begin(conn, manifest, settings, art, atMs, pendingFor(manifest.id))
        } catch {
            Self.log.warning("could not start the book: status \((error as? ApiError)?.status ?? -1)")
            if seq == mine { fail(failure(error, live: false)) }
        }
    }

    private func begin(_ conn: Connection, _ manifest: BookManifest, _ settings: PlayerSettings, _ art: Data?, _ atMs: Int64?, _ pending: Save?) {
        guard manifest.files.contains(where: { $0.playable }) else {
            fail("book")
            return
        }
        onBookChanging?()
        leaveLive()
        resetItems()
        bookId = manifest.id
        error = ""
        ended = false
        isEpisode = false
        showSettings = nil
        rules = nil
        self.settings = settings
        files = manifest.files
        timeline = BookTimeline(files: manifest.files)
        durationMs = manifest.durationMs
        chapters = manifest.chapters
        title = manifest.title
        author = manifest.author
        episodeTitle = nil
        self.art = art
        shownChapter = -1
        speed = manifest.speed
        player.defaultRate = Float(speed)
        let given = (atMs ?? -1) >= 0
        onBookStarted?(manifest.id, PositionKt.placeWrittenAt(p: manifest.progress, pending: pending)?.int64Value, given)
        let place = PositionKt.startPlace(p: manifest.progress, atMs: atMs.map { KotlinLong(value: $0) }, pending: pending)
        load(atBookMs: place, play: true)
        Self.log.info("started book: \(manifest.files.count) files, \(manifest.chapters.count) chapters")
        changed()
    }

    private func startEpisode(_ id: String, _ atMs: Int64?, _ conn: Connection, _ mine: Int) async {
        let api = makeApi(conn.origin)
        do {
            let settings = try await api.settings(token: conn.token)
            guard seq == mine else { return }
            let doc = try await api.episode(token: conn.token, id: id)
            guard seq == mine else { return }
            var art: Data?
            if !doc.artUrl.isEmpty { art = await api.podcastArt(token: conn.token, artPath: doc.artUrl) }
            guard seq == mine else { return }
            beginEpisode(conn, doc, settings, art, atMs, pendingFor(doc.id))
            guard !doc.chaptersUrl.isEmpty else { return }
            let list = await api.chapters(token: conn.token, path: doc.chaptersUrl, durationMs: doc.durationMs)
            if !list.isEmpty, seq == mine {
                chapters = list
                changed()
            }
        } catch {
            Self.log.warning("could not start the episode: status \((error as? ApiError)?.status ?? -1)")
            if seq == mine { fail(failure(error, live: false)) }
        }
    }

    private func beginEpisode(_ conn: Connection, _ doc: EpisodeDoc, _ settings: PlayerSettings, _ art: Data?, _ atMs: Int64?, _ pending: Save?) {
        guard let url = ApiPaths(server: conn.origin.text).podcastUrl(path: doc.audioUrl, width: 0) else {
            fail("book")
            return
        }
        onBookChanging?()
        leaveLive()
        resetItems()
        bookId = doc.id
        error = ""
        ended = false
        isEpisode = true
        self.settings = settings
        showSettings = doc.settings
        ruleDurationMs = doc.durationMs
        rules = PodcastKt.skipRules(settings: doc.settings, durationMs: doc.durationMs)
        outroDone = false
        files = []
        timeline = nil
        durationMs = doc.durationMs
        chapters = []
        title = doc.show
        author = ""
        episodeTitle = doc.title
        self.art = art
        shownChapter = -1
        speed = PodcastKt.episodeSpeed(settings: doc.settings, personal: settings.speed)
        player.defaultRate = Float(speed)
        let given = (atMs ?? -1) >= 0
        onBookStarted?(doc.id, PositionKt.placeWrittenAt(p: doc.progress, pending: pending)?.int64Value, given)
        let place = PositionKt.startPlace(p: doc.progress, atMs: atMs.map { KotlinLong(value: $0) }, pending: pending)
        let from = given ? place : (rules?.startAt(placeMs: place) ?? place)
        let handle = loader.register(url: url, origin: conn.origin, mime: doc.mime)
        let item = makeItem(handle, mime: doc.mime.hasPrefix("audio/") ? doc.mime : nil, file: 0)
        willPlay?(false)
        wantsPlay = true
        preparing = true
        player.insert(item, after: nil)
        seekWhenReady(item, ms: from)
        Self.log.info("started episode")
        changed()
    }

    private func startStation(_ id: String, _ conn: Connection, _ mine: Int) async {
        let api = makeApi(conn.origin)
        do {
            let card = try await api.radioStation(token: conn.token, id: id)
            guard seq == mine else { return }
            let art = await api.radioArt(token: conn.token, id: id, artPath: card.artUrl)
            guard seq == mine else { return }
            beginStation(card, art)
        } catch {
            Self.log.warning("could not start the station: status \((error as? ApiError)?.status ?? -1)")
            if seq == mine { fail(failure(error, live: true)) }
        }
    }

    private func beginStation(_ card: StationCard, _ art: Data?) {
        onBookChanging?()
        leaveLive()
        resetItems()
        bookId = card.id
        error = ""
        ended = false
        isEpisode = false
        showSettings = nil
        rules = nil
        files = []
        timeline = nil
        chapters = []
        durationMs = 0
        station = card
        live = true
        liveNow = ""
        liveAttempt = 0
        title = RadioKt.stationRowTitle(s: card)
        author = ""
        episodeTitle = nil
        self.art = art
        speed = 1.0
        player.defaultRate = 1.0
        onBookStarted?(card.id, nil, true)
        connectLive()
        Self.log.info("started station")
        changed()
    }

    // MARK: Items

    /// A handle's item: pitch kept for speech, the asset loaded through the loader's queue.
    private func makeItem(_ handle: URL, mime: String?, file: Int) -> AVPlayerItem {
        var options: [String: Any] = [:]
        if let mime, !mime.isEmpty, mime != "audio/x-m4b" { options[AVURLAssetOverrideMIMETypeKey] = mime }
        let asset = AVURLAsset(url: handle, options: options.isEmpty ? nil : options)
        asset.resourceLoader.setDelegate(loader, queue: loader.queue)
        let item = AVPlayerItem(asset: asset)
        item.audioTimePitchAlgorithm = .timeDomain
        watch(item, file: file)
        return item
    }

    /// Records [item] as file [file] and follows its status.
    private func watch(_ item: AVPlayerItem, file: Int) {
        let id = ObjectIdentifier(item)
        fileOf[id] = file
        itemObservers[id] = item.observe(\.status) { [weak self, weak item] _, _ in
            Task { @MainActor in
                guard let item else { return }
                self?.itemStatusChanged(item)
            }
        }
    }

    private func fileItem(_ index: Int, _ conn: Connection) -> AVPlayerItem? {
        guard files.indices.contains(index), BridgeProtocolKt.validId(s: files[index].id) else { return nil }
        let f = files[index]
        let url = ApiPaths(server: conn.origin.text).streamUrl(fileId: f.id, compat: f.needsCompat)
        let handle = loader.register(url: url, origin: conn.origin, mime: f.mime)
        return makeItem(handle, mime: f.needsCompat ? nil : f.mime, file: index)
    }

    /// Drops every queued item and the loader's handles (a new item, stop).
    private func resetItems() {
        player.pause()
        player.removeAllItems()
        loader.forgetAll()
        fileOf.removeAll()
        itemObservers.removeAll()
        reported.removeAll()
    }

    /// Rebuilds the queue for book time [bookMs]: the file it lies in, and the next playable one.
    private func load(atBookMs bookMs: Int64, play: Bool) {
        guard let conn = connection() else {
            fail("signedOut")
            return
        }
        guard let timeline, let at = timeline.target(bookMs: bookMs) ?? timeline.target(bookMs: 0) else {
            fail("book")
            return
        }
        resetItems()
        guard let first = fileItem(at.fileIndex, conn) else {
            fail("book")
            return
        }
        player.insert(first, after: nil)
        if let next = timeline.next(after: at.fileIndex), let following = fileItem(next, conn) {
            player.insert(following, after: first)
        }
        willPlay?(false)
        wantsPlay = play
        preparing = play
        ended = false
        seekWhenReady(first, ms: at.inFileMs, play: play)
    }

    /// Seeks [item] to [ms] once it is ready, then plays (when asked): a seek before the item is ready is lost.
    private func seekWhenReady(_ item: AVPlayerItem, ms: Int64, play: Bool = true) {
        guard ms > 0 else {
            if play { player.play() }
            return
        }
        pendingSeek = (ObjectIdentifier(item), ms, play)
        if item.status == .readyToPlay { applyPendingSeek(item) }
    }

    private var pendingSeek: (item: ObjectIdentifier, ms: Int64, play: Bool)?

    private func applyPendingSeek(_ item: AVPlayerItem) {
        guard let pending = pendingSeek, pending.item == ObjectIdentifier(item) else { return }
        pendingSeek = nil
        let tolerance = CMTime(value: 250, timescale: 1000)
        item.seek(to: CMTime(value: pending.ms, timescale: 1000), toleranceBefore: tolerance, toleranceAfter: tolerance) { [weak self] _ in
            Task { @MainActor in
                guard let self else { return }
                if pending.play, self.wantsPlay { self.player.play() }
                self.changed()
            }
        }
    }

    // MARK: Station

    private func connectLive() {
        guard let card = station else { return }
        liveRetry?.cancel()
        guard let conn = connection() else {
            fail("signedOut")
            return
        }
        player.removeAllItems()
        loader.forgetAll()
        fileOf.removeAll()
        itemObservers.removeAll()
        reported.removeAll()
        guard BridgeProtocolKt.validId(s: card.id) else {
            fail("book")
            return
        }
        // A station plays from a one-hour live address AVPlayer fetches itself (decision 0049); every connect asks for a new one.
        // The address is a credential: never logged, never kept beyond the item.
        let mine = seq
        let api = makeApi(conn.origin)
        liveConnect?.cancel()
        liveConnect = Task { [weak self] in
            let url: URL
            do {
                url = try await api.liveAddress(token: conn.token, id: card.id, origin: conn.origin)
            } catch {
                guard let self, !Task.isCancelled, self.seq == mine, self.station?.id == card.id else { return }
                let e = error as? ApiError
                self.liveBroke(status: e?.status ?? 0, retryAfterS: e?.retryAfterS ?? 0, otherServer: false)
                return
            }
            guard let self, !Task.isCancelled, self.seq == mine, self.station?.id == card.id else { return }
            let item = self.makeLiveItem(url)
            self.willPlay?(true)
            self.wantsPlay = true
            self.preparing = true
            self.player.removeAllItems()
            self.watch(item, file: 0)
            self.player.insert(item, after: nil)
            self.player.play()
            self.scheduleNow(after: 2)
        }
    }

    /// The station's item: a plain URL item with no cookies and no type override (a station's type is what its answer says).
    /// Tests replace this to see the address without a network.
    var makeLiveItem: (URL) -> AVPlayerItem = { url in
        let options: [String: Any] = [AVURLAssetHTTPCookiesKey: [HTTPCookie]()]
        let item = AVPlayerItem(asset: AVURLAsset(url: url, options: options))
        item.audioTimePitchAlgorithm = .timeDomain
        return item
    }

    private func leaveLive() {
        liveConnect?.cancel()
        liveRetry?.cancel()
        liveNowTask?.cancel()
        station = nil
        live = false
        liveAttempt = 0
    }

    /// A station's stream broke or ended: try again by the shared rule, or give up with the right message.
    private func liveBroke(status: Int, retryAfterS: Int, otherServer: Bool) {
        if otherServer {
            Self.log.warning("station: the address is not the connected server")
            fail(PlayFailureKt.playFailureKind(status: Int32(status), otherServer: true, live: true))
            return
        }
        guard let wait = RadioKt.liveRetryDelayMs(code: Int32(status), attempt: Int32(liveAttempt), retryAfterS: Int32(retryAfterS)) else {
            Self.log.warning("station: giving up (status \(status))")
            fail(PlayFailureKt.playFailureKind(status: Int32(status), otherServer: false, live: true))
            return
        }
        liveAttempt += 1
        Self.log.info("station: trying again in \(wait.int64Value / 1000) s (status \(status))")
        liveRetry?.cancel()
        liveRetry = Task { [weak self] in
            try? await Task.sleep(nanoseconds: UInt64(wait.int64Value) * 1_000_000)
            guard !Task.isCancelled else { return }
            self?.connectLive()
        }
    }

    private func scheduleNow(after seconds: Double) {
        liveNowTask?.cancel()
        liveNowTask = Task { [weak self] in
            try? await Task.sleep(nanoseconds: UInt64(seconds * 1_000_000_000))
            guard !Task.isCancelled else { return }
            await self?.askNow()
        }
    }

    private func askNow() async {
        guard let card = station, wantsPlay, let conn = connection() else { return }
        let text = await makeApi(conn.origin).radioNow(token: conn.token, id: card.id)
        guard station?.id == card.id else { return }
        if let text, text != liveNow {
            liveNow = text
            changed()
            onChapterChanged?()
        }
        if wantsPlay { scheduleNow(after: Double(RadioKt.RADIO_NOW_POLL_MS) / 1000) }
    }

    // MARK: Commands

    func play() {
        if live {
            guard player.currentItem == nil else {
                wantsPlay = true
                player.play()
                changed()
                return
            }
            liveAttempt = 0
            error = ""
            connectLive()
            changed()
            return
        }
        guard !bookId.isEmpty else { return }
        if !error.isEmpty, timeline != nil || isEpisode {
            // A failed item plays again from the same place.
            error = ""
            reloadHere(play: true)
            return
        }
        willPlay?(false)
        wantsPlay = true
        player.play()
        changed()
    }

    func pause() {
        wantsPlay = false
        preparing = false
        if live {
            liveRetry?.cancel()
            liveNowTask?.cancel()
            // A station has no place: pausing lets go of the stream, playing joins it anew.
            player.pause()
            player.removeAllItems()
            loader.forgetAll()
            fileOf.removeAll()
            itemObservers.removeAll()
        } else {
            player.pause()
        }
        changed()
    }

    /// Book time [bookMs]; for an episode its own time. Ignored for a station.
    func seek(_ bookMs: Int64) {
        guard !live, !bookId.isEmpty else { return }
        if let timeline {
            let target = timeline.target(bookMs: bookMs)
            let current = player.currentItem.flatMap { fileOf[ObjectIdentifier($0)] }
            if let target, let current, target.fileIndex == current, let item = player.currentItem {
                seekItem(item, ms: target.inFileMs)
            } else if target == nil, let last = timeline.playable.last, let current, current == last, let item = player.currentItem {
                seekItem(item, ms: files[last].durationMs)
            } else {
                load(atBookMs: target == nil ? durationMs : bookMs, play: wantsPlay || player.timeControlStatus != .paused)
            }
        } else if let item = player.currentItem {
            seekItem(item, ms: max(0, bookMs))
        }
        changed()
        onChapterChanged?()
        onSeek?()
    }

    private func seekItem(_ item: AVPlayerItem, ms: Int64) {
        ended = false
        let tolerance = CMTime(value: 250, timescale: 1000)
        item.seek(to: CMTime(value: ms, timescale: 1000), toleranceBefore: tolerance, toleranceAfter: tolerance) { [weak self] _ in
            Task { @MainActor in self?.changed() }
        }
    }

    func skipBack() {
        guard !live else { return }
        let s = Int64(settings?.skipBackS ?? 30) * 1000
        seek(max(0, positionMs - s))
    }

    func skipForward() {
        guard !live else { return }
        let s = Int64(settings?.skipForwardS ?? 30) * 1000
        let to = positionMs + s
        seek(durationMs > 0 ? min(to, durationMs) : to)
    }

    func nextChapter() {
        guard !live, let to = PositionKt.nextChapterStart(chapters: chapters, bookMs: positionMs) else { return }
        seek(to.int64Value)
    }

    func previousChapter() {
        guard !live else { return }
        seek(PositionKt.previousChapterStart(chapters: chapters, bookMs: positionMs))
    }

    /// Speed is `defaultRate`, so a paused player keeps it and `play()` starts at it. Not for a station.
    func setSpeed(_ value: Double) {
        guard !live else { return }
        speed = value
        player.defaultRate = Float(value)
        if player.timeControlStatus != .paused { player.rate = Float(value) }
        changed()
    }

    func setVolume(_ value: Float) {
        player.volume = value
    }

    /// Everything stops and is forgotten: `BookEngine.kt:513-528` plus the loader's handles.
    func stopAndClear() {
        seq += 1
        onBookChanging?()
        leaveLive()
        resetItems()
        pendingSeek = nil
        bookId = ""
        error = ""
        ended = false
        wantsPlay = false
        preparing = false
        isEpisode = false
        showSettings = nil
        rules = nil
        files = []
        timeline = nil
        chapters = []
        title = ""
        author = ""
        episodeTitle = nil
        art = nil
        durationMs = 0
        shownChapter = -1
        changed()
    }

    func fail(_ kind: String) {
        error = kind
        wantsPlay = false
        preparing = false
        player.pause()
        changed()
        onFailed?(kind)
    }

    /// A failed item plays again at the same place: rebuild it.
    private func reloadHere(play: Bool) {
        let place = positionMs
        if timeline != nil {
            load(atBookMs: place, play: play)
        } else if isEpisode, let conn = connection() {
            let id = bookId
            let at = place
            let api = makeApi(conn.origin)
            seq += 1
            let mine = seq
            Task {
                do {
                    let doc = try await api.episode(token: conn.token, id: id)
                    guard seq == mine else { return }
                    let paths = ApiPaths(server: conn.origin.text)
                    guard let url = paths.podcastUrl(path: doc.audioUrl, width: 0) else { return }
                    resetItems()
                    let handle = loader.register(url: url, origin: conn.origin, mime: doc.mime)
                    let item = makeItem(handle, mime: doc.mime.hasPrefix("audio/") ? doc.mime : nil, file: 0)
                    wantsPlay = play
                    preparing = play
                    player.insert(item, after: nil)
                    seekWhenReady(item, ms: at, play: play)
                    changed()
                } catch {
                    fail(failure(error, live: false))
                }
            }
        }
    }

    // MARK: Snapshot

    private var currentFile: Int? {
        player.currentItem.flatMap { fileOf[ObjectIdentifier($0)] }
    }

    /// Book time now (for a station 0).
    var positionMs: Int64 {
        guard !live, let item = player.currentItem else { return 0 }
        let t = item.currentTime()
        let ms = t.isNumeric ? Int64(max(0, t.seconds) * 1000) : 0
        if let timeline, let f = currentFile { return timeline.bookMs(fileIndex: f, itemMs: ms) }
        return ms
    }

    var duration: Int64 {
        if live { return 0 }
        if timeline == nil, let item = player.currentItem, item.duration.isNumeric, item.duration.seconds > 0 {
            return Int64(item.duration.seconds * 1000)
        }
        return durationMs
    }

    var playing: Bool { player.timeControlStatus == .playing }

    /// The speed the player takes at play (a station has one).
    var rate: Double { Double(player.defaultRate) }

    /// A place worth saving: an item is ready and no start seek is waiting, so `positionMs` is the real place and not 0.
    var hasPlace: Bool {
        guard !bookId.isEmpty, pendingSeek == nil, let item = player.currentItem else { return false }
        return item.status == .readyToPlay
    }

    var chapterIdx: Int {
        if live { return -1 }
        return Int(Position.shared.chapterAt(chapters: chapters, bookMs: positionMs)?.idx ?? -1)
    }

    func snapshot() -> PlayerSnapshot {
        PlayerSnapshot(
            bookId: bookId,
            positionMs: positionMs,
            durationMs: duration,
            playing: playing,
            buffering: player.timeControlStatus == .waitingToPlayAtSpecifiedRate || (preparing && error.isEmpty && !ended),
            ended: ended,
            speed: Double(player.defaultRate),
            chapterIdx: Int32(chapterIdx),
            error: error,
            ask: nil,
            rewoundS: 0,
            rewoundSeq: 0,
            sleep: SleepInfo.companion.OFF
        )
    }

    // MARK: Events

    private func changed() { onChange?() }

    private func statusChanged() {
        if player.timeControlStatus == .playing {
            preparing = false
            liveAttempt = 0
            if !error.isEmpty { error = "" }
        }
        let sound: Sound
        switch player.timeControlStatus {
        case .playing: sound = .playing
        case .waitingToPlayAtSpecifiedRate: sound = wantsPlay ? .waiting : .paused
        default: sound = .paused
        }
        if sound != lastSound {
            lastSound = sound
            onSound?(sound)
        }
        changed()
    }

    private var lastSound = Sound.paused

    /// The queue moved to its next file: queue the one after it.
    private func currentItemChanged() {
        guard let timeline, let item = player.currentItem, let file = fileOf[ObjectIdentifier(item)] else {
            changed()
            return
        }
        if let conn = connection(), let next = timeline.next(after: file),
           !player.items().contains(where: { fileOf[ObjectIdentifier($0)] == next }),
           let following = fileItem(next, conn) {
            player.insert(following, after: player.items().last)
        }
        // Items that have played are gone; forget their tables.
        let alive = Set(player.items().map { ObjectIdentifier($0) })
        fileOf = fileOf.filter { alive.contains($0.key) }
        itemObservers = itemObservers.filter { alive.contains($0.key) }
        changed()
        checkChapter()
    }

    private func itemStatusChanged(_ item: AVPlayerItem) {
        switch item.status {
        case .readyToPlay:
            applyPendingSeek(item)
        case .failed:
            itemFailed(item)
        default:
            break
        }
        changed()
    }

    private func didPlayToEnd(_ item: AVPlayerItem?) {
        guard let item, fileOf[ObjectIdentifier(item)] != nil else { return }
        if live {
            liveBroke(status: 0, retryAfterS: 0, otherServer: false)
            return
        }
        // Only the last queued file ends the book; the others hand over to the next one.
        guard player.items().last === item || player.items().count <= 1 else { return }
        if let timeline, let file = fileOf[ObjectIdentifier(item)], timeline.next(after: file) != nil { return }
        ended = true
        wantsPlay = false
        changed()
        onEnded?()
    }

    private func itemFailed(_ item: AVPlayerItem?) {
        guard let item, fileOf[ObjectIdentifier(item)] != nil, !reported.contains(ObjectIdentifier(item)) else { return }
        reported.insert(ObjectIdentifier(item))
        if live {
            // A station's item fetched its own address: the HTTP status, when AVFoundation kept one, is in the error log.
            let code = item.errorLog()?.events.last?.errorStatusCode ?? 0
            let status = (400...599).contains(code) ? code : 0
            Self.log.warning("station error: status \(status)")
            liveBroke(status: status, retryAfterS: 0, otherServer: false)
            return
        }
        let handle = (item.asset as? AVURLAsset)?.url
        let seen = handle.flatMap { loader.lastFailure($0) }
        Self.log.warning("player error: status \(seen?.status ?? 0)")
        fail(PlayFailureKt.playFailureKind(status: Int32(seen?.status ?? 0), otherServer: seen?.otherServer ?? false, live: false))
    }

    private func tick() {
        guard player.timeControlStatus == .playing else { return }
        checkChapter()
        checkOutro()
        onTick?()
    }

    private func checkChapter() {
        let idx = chapterIdx
        if idx != shownChapter {
            shownChapter = idx
            onChapterChanged?()
            changed()
        }
    }

    /// An episode inside its show's outro counts as over (port of `BookEngine.kt:317-329`).
    private func checkOutro() {
        guard showSettings != nil, !outroDone, playing, let item = player.currentItem else { return }
        let real: Int64 = item.duration.isNumeric ? Int64(max(0, item.duration.seconds) * 1000) : 0
        if real > 0, real != ruleDurationMs, let shown = showSettings {
            ruleDurationMs = real
            rules = PodcastKt.skipRules(settings: shown, durationMs: real)
        }
        if real > 0, rules?.inOutro(positionMs: positionMs) == true {
            outroDone = true
            seekItem(item, ms: real)
        }
    }
}
