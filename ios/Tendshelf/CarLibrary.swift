import Foundation
@preconcurrency import TendshelfShared

/// One row of a CarPlay list: a folder to open, or something that plays.
enum CarRow: Equatable {
    case folder(id: String, title: String)
    case book(BookCard)
    case episode(EpisodeCard)
    case station(StationCard)
    /// A book on Home: the card, how far in the person is, and the series line when it is a series row.
    case homeBook(HomeBook)
}

/// One titled section of the Home list (a `CPListSection` in the car).
struct CarSection: Equatable {
    let title: String
    let rows: [CarRow]
}

/// What a node shows: rows, or plain words (a failure, or an empty list) for the empty-view of the template.
enum CarList: Equatable {
    case rows([CarRow])
    case sections([CarSection])
    case message(String)
}

/// The car words. They are Android's `car_*` strings; the keys sit here so this package does not touch `Words.Key` (`CarLibraryTests` checks each exists).
enum CarWords {
    static let keys = [
        "car_home", "car_library", "car_home_continue", "car_home_series", "car_home_new", "car_home_because", "car_home_episodes",
        "car_series_line", "car_not_started", "car_recent", "car_by_title", "car_by_author",
        "car_up_next", "car_shelves", "car_downloaded", "car_podcasts", "car_podcasts_new", "car_podcasts_shows",
        "car_radio", "car_radio_stations", "car_radio_recent", "car_sign_in", "car_no_connection", "car_not_found", "car_nothing_here",
    ]
    static func text(_ key: String) -> String { words(key) }
}

/// One winner of the work-against-the-clock race of `CarLibrary.load`.
@MainActor
private final class CarRace {
    private var taken = false
    func claim() -> Bool {
        if taken { return false }
        taken = true
        return true
    }
}

/// Why a node could not be listed: each has plain words.
private enum CarFailure: Error {
    case signIn, noConnection, notFound
}

/// What the car browses (decision 0048, I4c): the four tabs over the server's own lists, a port of Android's `LibraryTree.load`
/// (`LibraryTree.kt` app:277-300). Every call answers within [budget] or with plain words; the car never waits on a spinner.
/// Lists go through the device token to the pinned origin only. The downloaded lists are the shared `carMine` family.
@MainActor
final class CarLibrary {
    static let budget: Duration = .seconds(5)

    private let api: (Origin) -> CarAPI
    private let connection: () -> Connection?
    private let offline: () -> [OfflineBook]
    private let userId: () -> String?
    private let coverData: (String) -> Data?
    private let budgetTime: Duration

    /// [offline] is the person's stored rows, [coverData] a stored cover (a test replaces them).
    init(
        api: @escaping (Origin) -> CarAPI, connection: @escaping () -> Connection?, offline: @escaping () -> [OfflineBook],
        userId: @escaping () -> String?, coverData: @escaping (String) -> Data? = { _ in nil }, budget: Duration = CarLibrary.budget
    ) {
        self.api = api
        self.connection = connection
        self.offline = offline
        self.userId = userId
        self.coverData = coverData
        budgetTime = budget
    }

    /// The node's list, or plain words. Never throws.
    func load(_ nodeId: String) async -> CarList {
        guard let node = LibraryTreeKt.parseNode(id: nodeId), Self.isFolder(node) else { return .message(CarWords.text("car_not_found")) }
        let budget = budgetTime
        // The work and a timer race; the first to finish answers once (both run on the main actor, so the flag needs no lock).
        return await withCheckedContinuation { (answer: CheckedContinuation<CarList, Never>) in
            let race = CarRace()
            let work = Task { @MainActor in
                let list = await self.run(node)
                if race.claim() { answer.resume(returning: list) }
            }
            Task { @MainActor in
                try? await Task.sleep(for: budget)
                if race.claim() {
                    work.cancel()
                    // Home, unlike the other lists, answers its offline sections when the network is late.
                    let stored = node is NodeHome ? self.offlineHome() : []
                    answer.resume(returning: stored.isEmpty ? .message(CarWords.text("car_no_connection")) : .sections(stored))
                }
            }
        }
    }

    /// A node that lists children: Root and the leaves (a book, an episode, a station) do not.
    private static func isFolder(_ node: Node) -> Bool {
        switch node {
        case is NodeRoot, is NodeBook, is NodeEpisode, is NodeStation: return false
        default: return true
        }
    }

    private func run(_ node: Node) async -> CarList {
        do {
            guard connection() != nil else { throw CarFailure.signIn } // signed out: words, even for the tabs
            if node is NodeHome {
                let sections = try await homeSections()
                return sections.isEmpty ? .message(CarWords.text("car_nothing_here")) : .sections(sections)
            }
            let rows = try await rows(node)
            return rows.isEmpty ? .message(CarWords.text("car_nothing_here")) : .rows(rows)
        } catch CarFailure.signIn {
            return .message(CarWords.text("car_sign_in"))
        } catch CarFailure.notFound {
            return .message(CarWords.text("car_not_found"))
        } catch {
            return .message(CarWords.text("car_no_connection"))
        }
    }

    private func rows(_ node: Node) async throws -> [CarRow] {
        switch node {
        case is NodeContinue:
            return try await online({ [self] in Self.cards(carContinueOffline()) }) { api, t in Self.cards(try await api.continueList(token: t)) }
        case is NodeUpNext:
            return try await online(nil) { api, t in Self.cards(try await api.queue(token: t)) }
        case is NodeLibrary:
            var out: [CarRow] = [
                .folder(id: LibraryTreeKt.nodeId(n: NodeContinue.shared), title: CarWords.text("car_home_continue")),
                .folder(id: LibraryTreeKt.nodeId(n: NodeLibraryNotStarted.shared), title: CarWords.text("car_not_started")),
                .folder(id: LibraryTreeKt.nodeId(n: NodeLibraryRecent.shared), title: CarWords.text("car_recent")),
                .folder(id: LibraryTreeKt.nodeId(n: NodeLibraryTitle.shared), title: CarWords.text("car_by_title")),
                .folder(id: LibraryTreeKt.nodeId(n: NodeLibraryAuthor.shared), title: CarWords.text("car_by_author")),
            ]
            if await hasShelves() { out.append(.folder(id: LibraryTreeKt.nodeId(n: NodeShelves.shared), title: CarWords.text("car_shelves"))) }
            if await answers({ api, t in _ = try await api.podcastShows(token: t) }) {
                out.append(.folder(id: LibraryTreeKt.nodeId(n: NodePodcasts.shared), title: CarWords.text("car_podcasts")))
            }
            if await answers({ api, t in _ = try await api.radioStations(token: t) }) {
                out.append(.folder(id: LibraryTreeKt.nodeId(n: NodeRadio.shared), title: CarWords.text("car_radio")))
            }
            return out
        case is NodeLibraryNotStarted:
            return try await online(nil) { api, t in Self.cards(try await api.books(token: t, sort: "title", show: "not_started")) }
        case is NodeLibraryRecent:
            return try await online(nil) { api, t in Self.cards(try await api.books(token: t, sort: "added", show: nil)) }
        case is NodeLibraryTitle:
            return try await online(nil) { api, t in Self.cards(try await api.books(token: t, sort: "title", show: nil)) }
        case is NodeLibraryAuthor:
            return try await online(nil) { api, t in Self.cards(try await api.books(token: t, sort: "author", show: nil)) }
        case is NodeShelves:
            let shelves = try await online(nil) { api, t in try await api.shelves(token: t) }
            return shelves.map { .folder(id: LibraryTreeKt.nodeId(n: NodeShelf(id: $0.id)), title: $0.name) }
        case is NodePodcasts:
            return [
                .folder(id: LibraryTreeKt.nodeId(n: NodePodcastNew.shared), title: CarWords.text("car_podcasts_new")),
                .folder(id: LibraryTreeKt.nodeId(n: NodePodcastDownloaded.shared), title: CarWords.text("car_downloaded")),
                .folder(id: LibraryTreeKt.nodeId(n: NodePodcastShows.shared), title: CarWords.text("car_podcasts_shows")),
            ]
        case is NodePodcastNew:
            return try await online(nil) { api, t in try await api.podcastNew(token: t).map { .episode($0) } }
        case is NodePodcastDownloaded:
            return downloadedEpisodes().map { .episode($0) }
        case is NodePodcastShows:
            return try await online(nil) { api, t in
                try await api.podcastShows(token: t).map { .folder(id: LibraryTreeKt.nodeId(n: NodeShow(id: $0.id)), title: PodcastKt.showRowTitle(s: $0)) }
            }
        case let show as NodeShow:
            return try await online(nil) { api, t in try await api.showEpisodes(token: t, showId: show.id).map { .episode($0) } }
        case is NodeRadio:
            return [
                .folder(id: LibraryTreeKt.nodeId(n: NodeRadioStations.shared), title: CarWords.text("car_radio_stations")),
                .folder(id: LibraryTreeKt.nodeId(n: NodeRadioRecent.shared), title: CarWords.text("car_radio_recent")),
            ]
        case is NodeRadioStations:
            return try await online(nil) { api, t in try await api.radioStations(token: t).map { .station($0) } }
        case is NodeRadioRecent:
            return try await online(nil) { api, t in try await api.radioRecent(token: t).map { .station($0) } }
        case let shelf as NodeShelf:
            return try await online(nil) { api, t in Self.cards(try await api.shelfBooks(token: t, shelfId: shelf.id)) }
        case is NodeDownloaded:
            // The tab also lists downloaded episodes: it is the one place that works with no connection.
            return Self.cards(carDownloadedBooks()) + downloadedEpisodes().map { .episode($0) }
        default:
            throw CarFailure.notFound
        }
    }

    private static func cards(_ books: [BookCard]) -> [CarRow] { books.map { .book($0) } }

    // MARK: Home

    /// The Home sections. Online: one request. An old server (404) gives Continue listening from `/continue`; no network gives the stored ones.
    private func homeSections() async throws -> [CarSection] {
        try await online({ [self] in offlineHome() }) { api, t in
            do {
                return Self.sections(try await api.home(token: t))
            } catch let e as ApiError where e.status == 404 {
                let books = try await api.continueList(token: t).prefix(Int(CarHomeKt.homeCap(k: .continue_)))
                let rows = books.map { HomeRowBook(book: HomeBook(card: $0, positionMs: 0, series: nil)) }
                return Self.sections(CarHome(sections: [HomeSection(kind: .continue_, seed: "", rows: rows)]))
            }
        }
    }

    /// Continue listening and Downloaded from the person's stored rows.
    private func offlineHome() -> [CarSection] {
        Self.sections(CarHomeKt.carHomeOffline(mine: mine()) { [coverData] in KotlinBoolean(bool: coverData($0) != nil) })
    }

    private static func sections(_ home: CarHome) -> [CarSection] {
        home.sections.map { section in
            let key = CarHomeKt.homeTitleKey(k: section.kind)
            let title = section.kind == .because ? words(key, section.seed) : words(key)
            let rows: [CarRow] = section.rows.compactMap { row in
                if let b = row as? HomeRowBook { return .homeBook(b.book) }
                if let e = row as? HomeRowEpisode { return .episode(e.card) }
                return nil
            }
            return CarSection(title: title, rows: rows)
        }
    }

    /// Whether the person has a shelf; a refused token still signs out, any other failure leaves the row out.
    private func hasShelves() async -> Bool {
        do {
            return !(try await online(nil) { api, t in try await api.shelves(token: t) }).isEmpty
        } catch {
            return false
        }
    }

    // MARK: Server calls

    /// Runs a server call. A refused token is "sign in"; no answer is [offline] when there is one; anything else is "no connection".
    private func online<T>(_ offline: (() -> T)?, _ call: (CarAPI, String) async throws -> T) async throws -> T {
        guard let conn = connection() else { throw CarFailure.signIn }
        do {
            return try await call(api(conn.origin), conn.token)
        } catch let e as ApiError {
            if e.status == 401 { throw CarFailure.signIn }
            if e.status == 0, let offline { return offline() }
            throw CarFailure.noConnection
        } catch is URLError {
            if let offline { return offline() }
            throw CarFailure.noConnection
        } catch is CancellationError {
            throw CancellationError()
        } catch {
            throw CarFailure.noConnection
        }
    }

    /// Whether a list answers: Podcasts and Radio are rows only where the server has them on for this person. A refused token still signs out.
    private func answers(_ call: (CarAPI, String) async throws -> Void) async -> Bool {
        do {
            try await online(nil, call)
            return true
        } catch CarFailure.signIn {
            return false // the next call (the shelves) says it in words
        } catch {
            return false
        }
    }

    // MARK: Pictures

    /// The picture of a row: a stored cover first (no request), else the server's, for the pinned origin only. Nil when there is none.
    func picture(for row: CarRow) async -> Data? {
        let card = Self.card(of: row)
        if let card, let stored = coverData(card.id) { return stored }
        guard let conn = connection() else { return nil }
        let api = api(conn.origin)
        switch row {
        case .folder: return nil
        case .book, .homeBook: return card?.hasCover == true ? await api.cover(token: conn.token, bookId: card!.id) : nil
        case .episode(let e): return e.showId.isEmpty ? nil : await api.showArt(token: conn.token, showId: e.showId)
        case .station(let s): return s.artUrl.isEmpty ? nil : await api.radioArt(token: conn.token, id: s.id, artPath: s.artUrl)
        }
    }

    /// The book behind a row, when it is one (a `.homeBook` row is a book as far as its picture goes).
    private static func card(of row: CarRow) -> BookCard? {
        switch row {
        case .book(let b): return b
        case .homeBook(let h): return h.card
        default: return nil
        }
    }

    // MARK: Downloaded (no network, the signed-in person's only)

    private func mine() -> [OfflineBook] { LibraryTreeKt.carMine(all: offline(), userId: userId()) }

    private func carContinueOffline() -> [BookCard] { LibraryTreeKt.carContinueOffline(mine: mine()) { [coverData] in KotlinBoolean(bool: coverData($0) != nil) } }

    private func carDownloadedBooks() -> [BookCard] { LibraryTreeKt.carDownloadedBooks(mine: mine()) { [coverData] in KotlinBoolean(bool: coverData($0) != nil) } }

    private func downloadedEpisodes() -> [EpisodeCard] {
        EpisodeDownloadsKt.readyEpisodes(rows: mine()).map {
            // The row as the car lists it (`asEpisodeCard` in the shared module): the show's name is the author column.
            EpisodeCard(
                id: $0.bookId, showId: $0.showId, title: $0.title, show: $0.author, durationMs: $0.durationMs, publishedAt: $0.publishedAt,
                positionMs: $0.placeMs, played: $0.played, favourite: false)
        }
    }
}
