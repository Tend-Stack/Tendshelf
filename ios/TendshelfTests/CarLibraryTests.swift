import XCTest
@preconcurrency import TendshelfShared
@testable import Tendshelf

/// A scripted server for the car lists.
final class FakeCarAPI: CarAPI, @unchecked Sendable {
    var failure: Error?
    var delay: Duration?
    var continueBooks: [BookCard] = []
    var queueBooks: [BookCard] = []
    var shelfList: [ShelfCard] = []
    var shelfBookList: [BookCard] = []
    var shows: [ShowCard]?
    var stations: [StationCard]?
    var newEpisodes: [EpisodeCard] = []
    var tokens: [String] = []
    var homeResult: CarHome?
    var homeFailure: Error?
    var listed: [(String, String?)] = []

    private func gate(_ token: String, skipFailure: Bool = false) async throws {
        tokens.append(token)
        if let delay { try await Task.sleep(for: delay) }
        if !skipFailure, let failure { throw failure }
    }

    func home(token: String) async throws -> CarHome {
        try await gate(token, skipFailure: true)
        if let homeFailure { throw homeFailure }
        if let failure { throw failure }
        return homeResult ?? CarHome(sections: [])
    }
    func books(token: String, sort: String, show: String?) async throws -> [BookCard] {
        try await gate(token)
        listed.append((sort, show))
        return continueBooks
    }
    func continueList(token: String) async throws -> [BookCard] { try await gate(token); return continueBooks }
    func queue(token: String) async throws -> [BookCard] { try await gate(token); return queueBooks }
    func shelves(token: String) async throws -> [ShelfCard] { try await gate(token); return shelfList }
    func shelfBooks(token: String, shelfId: String) async throws -> [BookCard] { try await gate(token); return shelfBookList }
    func podcastNew(token: String) async throws -> [EpisodeCard] { try await gate(token); return newEpisodes }
    func podcastShows(token: String) async throws -> [ShowCard] {
        try await gate(token)
        guard let shows else { throw ApiError(status: 404) }
        return shows
    }
    func showEpisodes(token: String, showId: String) async throws -> [EpisodeCard] { try await gate(token); return [] }
    func radioStations(token: String) async throws -> [StationCard] {
        try await gate(token)
        guard let stations else { throw ApiError(status: 404) }
        return stations
    }
    func radioRecent(token: String) async throws -> [StationCard] { try await gate(token); return [] }
    func cover(token: String, bookId: String) async -> Data? { Data([1]) }
    func showArt(token: String, showId: String) async -> Data? { nil }
    func radioArt(token: String, id: String, artPath: String) async -> Data? { nil }
}

@MainActor
final class CarLibraryTests: XCTestCase {
    private let origin = Origin.companion.parse(input: "https://books.example")!

    private func book(_ id: String, _ title: String = "T") -> BookCard {
        BookCard(id: id, title: title, author: "A", durationMs: 1000, hasCover: false)
    }

    private func row(_ id: String, title: String, place: Int64 = 0, at: Int64 = 0, user: String = "u") -> OfflineBook {
        OfflineBook(
            bookId: id, userId: user, title: title, author: "A", durationMs: 1000, sizeBytes: 1, state: .ready, placeMs: place, placeAt: at,
            kind: OfflineKt.KIND_BOOK, showId: "", publishedAt: 0, played: false)
    }

    private func library(_ api: FakeCarAPI, signedIn: Bool = true, rows: [OfflineBook] = [], budget: Duration = .seconds(5)) -> CarLibrary {
        let conn = Connection(origin: origin, token: "tok", deviceId: "dev")
        return CarLibrary(api: { _ in api }, connection: { signedIn ? conn : nil }, offline: { rows }, userId: { "u" }, budget: budget)
    }

    func testEveryCarWordExists() {
        for key in CarWords.keys { XCTAssertNotNil(Words.shared[key], "missing word: \(key)") }
    }

    func testSignedOutSaysSignInOnEveryTab() async {
        let lib = library(FakeCarAPI(), signedIn: false)
        for node in ["home", "queue", "library", "downloaded"] {
            let list = await lib.load(node)
            XCTAssertEqual(list, .message(CarWords.text("car_sign_in")), node)
        }
    }

    func testContinueListsTheServersBooksWithTheToken() async {
        let api = FakeCarAPI()
        api.continueBooks = [book("b1", "One")]
        let list = await library(api).load("continue")
        XCTAssertEqual(list, .rows([.book(book("b1", "One"))]))
        XCTAssertEqual(api.tokens, ["tok"])
    }

    func testContinueFallsBackToDownloadsWhenOffline() async {
        let api = FakeCarAPI()
        api.failure = URLError(.notConnectedToInternet)
        let rows = [row("a", title: "A", place: 5, at: 10), row("b", title: "B"), row("c", title: "C", place: 7, at: 30)]
        let list = await library(api, rows: rows).load("continue")
        guard case .rows(let out) = list else { return XCTFail("\(list)") }
        XCTAssertEqual(out.compactMap { if case .book(let b) = $0 { b.id } else { nil } }, ["c", "a"])
    }

    func testNoAnswerOnUpNextHasNoOfflineList() async {
        let api = FakeCarAPI()
        api.failure = URLError(.notConnectedToInternet)
        let list = await library(api).load("queue")
        XCTAssertEqual(list, .message(CarWords.text("car_no_connection")))
    }

    func testARefusedTokenSaysSignIn() async {
        let api = FakeCarAPI()
        api.failure = ApiError(status: 401)
        let list = await library(api).load("continue")
        XCTAssertEqual(list, .message(CarWords.text("car_sign_in")))
    }

    func testAnyOtherFailureSaysNoConnection() async {
        let api = FakeCarAPI()
        api.failure = ApiError(status: 500)
        let list = await library(api).load("queue")
        XCTAssertEqual(list, .message(CarWords.text("car_no_connection")))
    }

    private var libraryBookRows: [CarRow] {
        [
            .folder(id: "continue", title: "Continue listening"), .folder(id: "library-not-started", title: "Not started"),
            .folder(id: "library-recent", title: "Recently added"), .folder(id: "library-title", title: "By title"),
            .folder(id: "library-author", title: "By author"),
        ]
    }

    func testLibraryWithPodcastsOffAndNoShelvesListsTheFiveBookRows() async {
        let list = await library(FakeCarAPI()).load("library")
        XCTAssertEqual(list, .rows(libraryBookRows))
    }

    func testLibraryAddsShelvesPodcastsAndRadioWhereTheyExist() async {
        let api = FakeCarAPI()
        api.shelfList = [ShelfCard(id: "s1", name: "Mine", books: 2)]
        api.stations = []
        let radio = await library(api).load("library")
        XCTAssertEqual(radio, .rows(libraryBookRows + [.folder(id: "shelves", title: "Shelves"), .folder(id: "radio", title: "Radio")]))
        api.shows = []
        let both = await library(api).load("library")
        XCTAssertEqual(
            both, .rows(libraryBookRows + [.folder(id: "shelves", title: "Shelves"), .folder(id: "podcasts", title: "Podcasts"), .folder(id: "radio", title: "Radio")]))
    }

    func testShelvesListsOnlyShelves() async {
        let api = FakeCarAPI()
        api.shelfList = [ShelfCard(id: "s1", name: "Mine", books: 2)]
        api.shows = []
        api.stations = []
        let list = await library(api).load("shelves")
        XCTAssertEqual(list, .rows([.folder(id: "shelf:s1", title: "Mine")]))
    }

    func testTheFourListNodesAskForTheirSortAndShow() async {
        let api = FakeCarAPI()
        api.continueBooks = [book("b1")]
        let lib = library(api)
        for node in ["library-not-started", "library-recent", "library-title", "library-author"] {
            let list = await lib.load(node)
            XCTAssertEqual(list, .rows([.book(book("b1"))]), node)
        }
        XCTAssertEqual(api.listed.map { $0.0 }, ["title", "added", "title", "author"])
        let shows: [String?] = ["not_started", nil, nil, nil]
        XCTAssertEqual(api.listed.map { $0.1 }, shows)
    }

    // MARK: Home

    private func homeBook(_ id: String, position: Int64 = 0) -> HomeBook { HomeBook(card: book(id), positionMs: position, series: nil) }

    private func section(_ kind: HomeKind, _ seed: String, _ ids: [String]) -> HomeSection {
        HomeSection(kind: kind, seed: seed, rows: ids.map { HomeRowBook(book: homeBook($0)) as HomeRow })
    }

    private func titles(_ list: CarList) -> [String] {
        guard case .sections(let s) = list else { return ["not sections: \(list)"] }
        return s.map { $0.title }
    }

    func testHomeOnlineGivesTheSectionsInOrderWithTheSeedInTheBecauseTitle() async {
        let api = FakeCarAPI()
        let episode = EpisodeCard(
            id: "pe_1", showId: "sh", title: "Pilot", show: "S", durationMs: 1, publishedAt: 0, positionMs: 0, played: false, favourite: false)
        let episodes: [HomeRow] = [HomeRowEpisode(card: episode)]
        let all: [HomeSection] = [
            section(.continue_, "", ["a"]), section(.series, "", ["b"]), section(.theNew, "", ["c"]),
            section(.because, "Dune", ["d"]), HomeSection(kind: .episodes, seed: "", rows: episodes),
        ]
        api.homeResult = CarHome(sections: all)
        let list = await library(api).load("home")
        XCTAssertEqual(titles(list), [
            "Continue listening", "Your series in progress", "New on the shelf", "Because you listened to Dune", "New episodes",
        ])
    }

    func testHomeWithNoNetworkGivesContinueAndDownloadedFromThisPersonsRows() async {
        let api = FakeCarAPI()
        api.homeFailure = URLError(.notConnectedToInternet)
        let rows = [row("a", title: "A", place: 5, at: 10), row("b", title: "B"), row("x", title: "X", place: 9, at: 99, user: "someone")]
        let list = await library(api, rows: rows).load("home")
        XCTAssertEqual(titles(list), ["Continue listening", "Downloaded"])
        guard case .sections(let s) = list else { return XCTFail("\(list)") }
        XCTAssertEqual(s[0].rows.count, 1)
        XCTAssertEqual(s[1].rows.count, 1)
    }

    func testHomeOn404GivesOnlyContinueListening() async {
        let api = FakeCarAPI()
        api.homeFailure = ApiError(status: 404)
        api.continueBooks = [book("a"), book("b")]
        let list = await library(api).load("home")
        XCTAssertEqual(titles(list), ["Continue listening"])
    }

    func testHomeSlowerThanTheBudgetAnswersTheOfflineSectionsNotWords() async {
        let api = FakeCarAPI()
        api.delay = .seconds(2)
        let list = await library(api, rows: [row("a", title: "A", place: 5, at: 10), row("b", title: "B")], budget: .milliseconds(200)).load("home")
        XCTAssertEqual(titles(list), ["Continue listening", "Downloaded"])
    }

    func testHomeSlowWithNothingDownloadedSaysNoConnection() async {
        let api = FakeCarAPI()
        api.delay = .seconds(2)
        let list = await library(api, budget: .milliseconds(200)).load("home")
        XCTAssertEqual(list, .message(CarWords.text("car_no_connection")))
    }

    func testLeavesAndUnknownIdsAreNotFound() async {
        let lib = library(FakeCarAPI())
        for id in ["book:x", "nope", "episode:x", "station:x", "root", "episode:pe_x", "shelf:a/b"] {
            let list = await lib.load(id)
            XCTAssertEqual(list, .message(CarWords.text("car_not_found")), id)
        }
    }

    func testAnEmptyListSaysNothingHere() async {
        let list = await library(FakeCarAPI()).load("queue")
        XCTAssertEqual(list, .message(CarWords.text("car_nothing_here")))
    }

    func testDownloadedTabListsOnlyThePersonsRowsByTitle() async {
        let rows = [row("1", title: "banana"), row("2", title: "Apple"), row("3", title: "other", user: "someone")]
        let list = await library(FakeCarAPI(), rows: rows).load("downloaded")
        guard case .rows(let out) = list else { return XCTFail("\(list)") }
        XCTAssertEqual(out.compactMap { if case .book(let b) = $0 { b.id } else { nil } }, ["2", "1"])
    }

    func testASlowServerAnswersWithinTheBudget() async {
        let api = FakeCarAPI()
        api.delay = .seconds(6)
        let started = ContinuousClock.now
        let list = await library(api, budget: .seconds(5)).load("continue")
        XCTAssertEqual(list, .message(CarWords.text("car_no_connection")))
        XCTAssertLessThan(ContinuousClock.now - started, .milliseconds(5500))
    }
}
