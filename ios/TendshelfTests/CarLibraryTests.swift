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

    private func gate(_ token: String) async throws {
        tokens.append(token)
        if let delay { try await Task.sleep(for: delay) }
        if let failure { throw failure }
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
        for node in ["continue", "queue", "shelves", "downloaded"] {
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

    func testPodcastsOffLeavesTheFolderOutAndRadioOnShowsItsRow() async {
        let api = FakeCarAPI()
        api.shelfList = [ShelfCard(id: "s1", name: "Mine", books: 2)]
        api.stations = []
        let list = await library(api).load("shelves")
        XCTAssertEqual(list, .rows([.folder(id: "radio", title: "Radio"), .folder(id: "shelf:s1", title: "Mine")]))
        api.shows = []
        let both = await library(api).load("shelves")
        XCTAssertEqual(both, .rows([.folder(id: "podcasts", title: "Podcasts"), .folder(id: "radio", title: "Radio"), .folder(id: "shelf:s1", title: "Mine")]))
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
