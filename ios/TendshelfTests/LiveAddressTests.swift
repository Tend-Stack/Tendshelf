import AVFoundation
import XCTest
@preconcurrency import TendshelfShared
@testable import Tendshelf

/// A station plays from a one-hour live address AVPlayer fetches itself (decision 0049, I2r).

/// A player API that answers only what a station start asks: the card, no art, no title, and the mint from a script.
@MainActor
final class FakeLiveAPI: PlayerAPI {
    var mints: [(token: String, id: String, origin: Origin)] = []
    /// What each mint answers, in order; the last one repeats.
    var answers: [Result<URL, ApiError>] = []

    func liveAddress(token: String, id: String, origin: Origin) async throws -> URL {
        mints.append((token, id, origin))
        return try answers[min(mints.count, answers.count) - 1].get()
    }

    func radioStation(token: String, id: String) async throws -> StationCard {
        StationCard(id: id, name: "Jazz FM", codec: "mp3", genre: "", country: "", favourite: true, lastPlayedAt: 0, problem: "", artUrl: "")
    }

    func radioArt(token: String, id: String, artPath: String) async -> Data? { nil }
    func radioNow(token: String, id: String) async -> String? { nil }
    func settings(token: String) async throws -> PlayerSettings { throw ApiError(status: 0) }
    func manifest(token: String, bookId: String) async throws -> BookManifest { throw ApiError(status: 0) }
    func cover(token: String, bookId: String) async -> Data? { nil }
    func episode(token: String, id: String) async throws -> EpisodeDoc { throw ApiError(status: 0) }
    func chapters(token: String, path: String, durationMs: Int64) async -> [Chapter] { [] }
    func podcastArt(token: String, artPath: String) async -> Data? { nil }
    func progress(token: String, bookId: String) async throws -> RemoteProgress? { nil }
    func queueNext(token: String, ended: String) async throws -> String? { nil }
    func putSpeed(token: String, bookId: String, speed: Double) async throws {}
    func batch(token: String, saves: [Save], deviceId: String) async throws -> [BatchItemResult] { [] }
}

@MainActor
final class LiveAddressTests: XCTestCase {
    private let origin = Origin.companion.parse(input: "https://books.example")!
    private let station = "rs_01HZZZ"
    private var good: URL { URL(string: "https://books.example/cast/" + String(repeating: "A", count: 43) + "/live")! }

    override func setUp() {
        StubProtocol.seen = []
        StubProtocol.handler = nil
    }

    // MARK: The mint call and the address check

    func testMintIsABearerPostWithNoCookie() async throws {
        let path = "/cast/" + String(repeating: "B", count: 43) + "/live"
        StubProtocol.handler = { request in
            let response = HTTPURLResponse(url: request.url!, statusCode: 200, httpVersion: "HTTP/1.1", headerFields: [:])!
            return (response, Data("{\"path\":\"\(path)\",\"expiresAt\":1}".utf8))
        }
        let api = PlayerApi(origin: origin, client: ServerClient(protocolClasses: [StubProtocol.self]))
        let url = try await api.liveAddress(token: "tok-1", id: station, origin: origin)
        XCTAssertEqual(url.absoluteString, "https://books.example" + path)
        let request = try XCTUnwrap(StubProtocol.seen.first)
        XCTAssertEqual(request.httpMethod, "POST")
        XCTAssertEqual(request.url?.absoluteString, "https://books.example/api/v1/radio/live/\(station)/address")
        XCTAssertEqual(request.value(forHTTPHeaderField: "Authorization"), "Bearer tok-1")
        XCTAssertNil(request.value(forHTTPHeaderField: "Cookie"))
    }

    func testMintRefusesAnAnswerThatIsNotTheLiveShape() async {
        for path in ["/cast/x/live", "//evil.test/cast/\(String(repeating: "C", count: 43))/live", "https://evil.test/x"] {
            StubProtocol.handler = { request in
                (HTTPURLResponse(url: request.url!, statusCode: 200, httpVersion: "HTTP/1.1", headerFields: [:])!, Data("{\"path\":\"\(path)\"}".utf8))
            }
            let api = PlayerApi(origin: origin, client: ServerClient(protocolClasses: [StubProtocol.self]))
            do {
                _ = try await api.liveAddress(token: "t", id: station, origin: origin)
                XCTFail("accepted \(path)")
            } catch {
                XCTAssertEqual(error as? ApiError, ApiError(status: 0))
            }
        }
    }

    func testMintStatusBecomesAnApiError() async {
        StubProtocol.handler = { request in
            (HTTPURLResponse(url: request.url!, statusCode: 503, httpVersion: "HTTP/1.1", headerFields: ["Retry-After": "7"])!, Data())
        }
        let api = PlayerApi(origin: origin, client: ServerClient(protocolClasses: [StubProtocol.self]))
        do {
            _ = try await api.liveAddress(token: "t", id: station, origin: origin)
            XCTFail("a 503 passed")
        } catch {
            XCTAssertEqual(error as? ApiError, ApiError(status: 503, retryAfterS: 7))
        }
    }

    func testLiveUrlShape() {
        let id = String(repeating: "D", count: 43)
        XCTAssertNotNil(PlayerApi.liveURL(origin: origin, path: "/cast/\(id)/live"))
        for bad in [
            "/cast/x/live", "/cast/\(id)/art", "//evil.test/cast/\(id)/live", "https://evil.test/cast/\(id)/live",
            "/cast/\(id)/live?x=1", "/cast/\(id)/live#x", "/cast/\(id)/live\n", "/cast/\(id)/live/",
        ] {
            XCTAssertNil(PlayerApi.liveURL(origin: origin, path: bad), bad)
        }
    }

    // MARK: The player

    private func player(_ api: FakeLiveAPI) -> (AudioPlayer, Box) {
        let conn = Connection(origin: origin, token: "tok-9", deviceId: "dev-1")
        let p = AudioPlayer(connection: { conn }, makeApi: { _ in api }, loader: MediaLoader(connection: { conn }, configuration: stubConfiguration()))
        let box = Box()
        p.makeLiveItem = { url in
            box.items.append(url)
            // A file that does not exist: the item exists to be seen, never to play.
            return AVPlayerItem(url: URL(fileURLWithPath: "/nonexistent/silence.mp3"))
        }
        p.onFailed = { box.failures.append($0) }
        return (p, box)
    }

    @MainActor final class Box {
        var items: [URL] = []
        var failures: [String] = []
    }

    func testSuccessMakesAnItemFromOriginAndPath() async {
        let api = FakeLiveAPI()
        api.answers = [.success(good)]
        let (p, box) = player(api)
        p.start(station, atMs: nil)
        let made = await waitFor { box.items.count == 1 }
        XCTAssertTrue(made)
        XCTAssertEqual(box.items.first, good)
        XCTAssertEqual(api.mints.first?.token, "tok-9")
        XCTAssertEqual(api.mints.first?.id, station)
        XCTAssertFalse(p.player.allowsExternalPlayback)
    }

    func testMint404IsBookAndNoItem() async {
        let api = FakeLiveAPI()
        api.answers = [.failure(ApiError(status: 404))]
        let (p, box) = player(api)
        p.start(station, atMs: nil)
        let failed = await waitFor { box.failures.contains("book") }
        XCTAssertTrue(failed, "\(box.failures)")
        XCTAssertTrue(box.items.isEmpty)
    }

    func testMint401IsSignedOut() async {
        let api = FakeLiveAPI()
        api.answers = [.failure(ApiError(status: 401))]
        let (p, box) = player(api)
        p.start(station, atMs: nil)
        let failed = await waitFor { box.failures.contains("signedOut") }
        XCTAssertTrue(failed, "\(box.failures)")
        XCTAssertTrue(box.items.isEmpty)
    }

    func testMint503MintsAgainAfterTheWait() async {
        let api = FakeLiveAPI()
        api.answers = [.failure(ApiError(status: 503, retryAfterS: 2)), .success(good)]
        let (p, box) = player(api)
        p.start(station, atMs: nil)
        let made = await waitFor(10) { box.items.count == 1 }
        XCTAssertTrue(made, "mints \(api.mints.count)")
        XCTAssertEqual(api.mints.count, 2)
        XCTAssertTrue(box.failures.isEmpty)
    }

    func testAnItemFailureMintsAgain() async {
        let api = FakeLiveAPI()
        api.answers = [.success(good)]
        let (p, box) = player(api)
        p.start(station, atMs: nil)
        _ = await waitFor { box.items.count == 1 }
        let item = p.player.currentItem
        NotificationCenter.default.post(name: AVPlayerItem.failedToPlayToEndTimeNotification, object: item)
        let again = await waitFor(10) { api.mints.count == 2 && box.items.count == 2 }
        XCTAssertTrue(again, "mints \(api.mints.count), items \(box.items.count)")
    }
}
