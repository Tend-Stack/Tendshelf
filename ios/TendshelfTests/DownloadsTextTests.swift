import XCTest
@preconcurrency import TendshelfShared
@testable import Tendshelf

/// I3d: the text follows the audio, waits for the web view's session, is checked daily, and goes with the book.
@MainActor
final class DownloadsTextTests: XCTestCase {
    private let book = DownloadsRig.book
    private var revision = "r1"
    private var signedIn = true

    override func setUp() {
        StubProtocol.seen = []
        StubProtocol.handler = nil
        revision = "r1"
        signedIn = true
    }

    private func serve() {
        let id = book
        StubProtocol.handler = { [self] req in
            let path = req.url!.path
            func reply(_ status: Int, _ body: String, _ type: String = "text/html") -> (HTTPURLResponse, Data) {
                var headers = ["Content-Type": type]
                if status == 302 { headers["Location"] = "https://books.example/login" }
                return (HTTPURLResponse(url: req.url!, statusCode: status, httpVersion: "HTTP/1.1", headerFields: headers)!, Data(body.utf8))
            }
            if !signedIn && !path.hasPrefix("/static/") { return reply(302, "") }
            if path == "/offline/books/\(id)/manifest" {
                let json = "{\"bookId\":\"\(id)\",\"editionId\":\"e1\",\"revision\":\"\(revision)\",\"title\":\"Text title\",\"author\":\"Au\","
                    + "\"bytes\":10,\"parts\":[\"/hx/books/\(id)/read/chapters/0\"],\"images\":[],\"assets\":[\"/static/a.js\"]}"
                return reply(200, json, "application/json")
            }
            return reply(200, "x")
        }
    }

    private func state(_ r: DownloadsRig) -> String? { r.store.text(book)?.state }

    private func waitFor(_ r: DownloadsRig, _ want: String) async {
        for _ in 0..<200 where state(r) != want { try? await Task.sleep(nanoseconds: 20_000_000) }
        XCTAssertEqual(state(r), want)
    }

    func testTextFollowsTheAudioAndGoesWithTheBook() async {
        serve()
        let r = DownloadsRig(self)
        await r.downloads.add(book)
        await waitFor(r, "ready")
        XCTAssertTrue(r.downloads.readable(book))
        XCTAssertEqual(r.store.text(book)?.revision, "r1")
        XCTAssertNotNil(r.downloads.text.lookup(OfflineRouteShell(bookId: book, key: "/books/\(book)/read")))
        await r.downloads.remove(book, byPerson: true)
        XCTAssertNil(r.downloads.text.lookup(OfflineRouteShell(bookId: book, key: "/books/\(book)/read")))
        XCTAssertFalse(FileManager.default.fileExists(atPath: r.paths.text(book)!.path))
    }

    func testNoSessionKeepsTheTextWaitingUntilAServerPageLoads() async {
        signedIn = false
        serve()
        let r = DownloadsRig(self)
        await r.downloads.add(book)
        await waitFor(r, "signin")
        XCTAssertFalse(r.downloads.readable(book))
        signedIn = true
        await r.downloads.pageLoaded()
        XCTAssertEqual(state(r), "ready")
    }

    func testBookWithNoTextRightIsNone() async {
        StubProtocol.handler = { req in (HTTPURLResponse(url: req.url!, statusCode: 404, httpVersion: "HTTP/1.1", headerFields: nil)!, Data()) }
        let r = DownloadsRig(self)
        await r.downloads.add(book)
        await waitFor(r, "none")
        XCTAssertFalse(r.downloads.readable(book))
    }

    func testTextOnlyBookIsReadyOnceItsTextIsWhole() async {
        serve()
        let r = DownloadsRig(self)
        r.api.manifest = DownloadsRig.manifestJson(download: true, files: [])
        await r.downloads.add(book)
        let row = r.store.get(id: book)
        XCTAssertEqual(row?.state, .ready)
        XCTAssertEqual(row?.title, "Text title")
        XCTAssertEqual(state(r), "ready")
        XCTAssertFalse(r.downloads.hasAudio(book))
        XCTAssertTrue(r.transport.started.isEmpty)
    }

    func testTextOnlyBookWhoseTextFailsLeavesNothing() async {
        StubProtocol.handler = { [self] req in
            let path = req.url!.path
            if path.hasSuffix("/manifest") {
                let json = "{\"bookId\":\"\(book)\",\"revision\":\"r1\",\"bytes\":1,\"parts\":[\"/hx/books/\(book)/read/chapters/0\"],\"images\":[],\"assets\":[]}"
                return (HTTPURLResponse(url: req.url!, statusCode: 200, httpVersion: "HTTP/1.1", headerFields: nil)!, Data(json.utf8))
            }
            return (HTTPURLResponse(url: req.url!, statusCode: 500, httpVersion: "HTTP/1.1", headerFields: nil)!, Data())
        }
        let r = DownloadsRig(self)
        r.api.manifest = DownloadsRig.manifestJson(download: true, files: [])
        await r.downloads.add(book)
        XCTAssertNil(r.store.get(id: book))
        XCTAssertEqual(r.notices.count, 1)
    }

    func testDailyCheckFetchesAgainForANewRevisionAndDropsAGoneText() async {
        serve()
        let r = DownloadsRig(self)
        await r.downloads.add(book)
        await waitFor(r, "ready")
        revision = "r2"
        await r.downloads.checkText(book)
        XCTAssertEqual(r.store.text(book)?.revision, "r2")
        StubProtocol.handler = { req in (HTTPURLResponse(url: req.url!, statusCode: 404, httpVersion: "HTTP/1.1", headerFields: nil)!, Data()) }
        await r.downloads.checkText(book)
        XCTAssertEqual(state(r), "none")
        XCTAssertNotNil(r.store.get(id: book), "a book with audio stays")
    }
}
