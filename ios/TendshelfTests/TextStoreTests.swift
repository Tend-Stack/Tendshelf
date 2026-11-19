import XCTest
@preconcurrency import TendshelfShared
@testable import Tendshelf

/// I3d: the stored text of a downloaded book. The network is a `URLProtocol` stub; the files are real, in a temporary folder.
@MainActor
final class TextStoreTests: XCTestCase {
    let origin = Origin.companion.parse(input: "http://10.0.0.5:8095")!
    var root: URL!
    var paths: OfflinePaths!

    override func setUp() {
        root = FileManager.default.temporaryDirectory.appendingPathComponent("ts-text-" + UUID().uuidString, isDirectory: true)
        paths = OfflinePaths(root: root)
        StubProtocol.seen = []
        StubProtocol.handler = nil
    }

    override func tearDown() {
        try? FileManager.default.removeItem(at: root)
    }

    func manifest(parts: [String] = ["/hx/books/b1/read/chapters/0"], assets: [String] = ["/static/a.js"], images: [String] = []) -> TextManifest {
        TextManifest(
            bookId: "b1", editionId: "e1", revision: "r1", title: "T", author: "A", bytes: 10, parts: parts, images: images, assets: assets)
    }

    func store() -> TextStore {
        let s = TextStore(paths: paths, protocolClasses: [StubProtocol.self])
        s.cookieHeader = { _ in "sid=abc" }
        return s
    }

    func ok(_ req: URLRequest, _ body: String, type: String = "text/html", csp: String? = nil) -> (HTTPURLResponse, Data) {
        var headers = ["Content-Type": type]
        if let csp { headers["Content-Security-Policy"] = csp }
        return (HTTPURLResponse(url: req.url!, statusCode: 200, httpVersion: "HTTP/1.1", headerFields: headers)!, Data(body.utf8))
    }

    func testFetchSendsCookieWithShellMarkToPagesNotToStaticFiles() async {
        StubProtocol.handler = { [self] req in ok(req, "x:" + req.url!.path, csp: req.url!.path.hasSuffix("/read") ? "script-src 'self'" : nil) }
        let result = await store().fetch(origin, manifest(), freeBytes: 1 << 40)
        XCTAssertEqual(result, .ready)
        for req in StubProtocol.seen {
            let cookie = req.value(forHTTPHeaderField: "Cookie")
            if req.url!.path.hasPrefix("/static/") {
                XCTAssertNil(cookie, req.url!.path)
            } else {
                XCTAssertEqual(cookie, "sid=abc; ts_shell=ios", req.url!.path)
            }
        }
        // The shell is stored last, and its stored answer keeps the CSP.
        XCTAssertEqual(StubProtocol.seen.last?.url?.path, "/offline/books/b1/read")
        let found = store().lookup(OfflineRouteShell(bookId: "b1", key: "/books/b1/read"))
        XCTAssertEqual(found?.csp, "script-src 'self'")
        XCTAssertEqual(try? String(contentsOf: found!.file, encoding: .utf8), "x:/offline/books/b1/read")
        XCTAssertNotNil(store().lookup(OfflineRoutePart(bookId: "b1", key: "/hx/books/b1/read/chapters/0")))
        XCTAssertNotNil(store().lookup(OfflineRouteStatic(key: "/static/a.js")))
        XCTAssertNil(store().lookup(OfflineRouteStatic(key: "/static/b.js")))
    }

    func testRedirectIsSignin() async {
        StubProtocol.handler = { req in
            (HTTPURLResponse(url: req.url!, statusCode: 302, httpVersion: "HTTP/1.1", headerFields: ["Location": "http://10.0.0.5:8095/login"])!, Data())
        }
        let result = await store().fetch(origin, manifest(), freeBytes: 1 << 40)
        XCTAssertEqual(result, .signin)
        XCTAssertNil(store().lookup(OfflineRouteShell(bookId: "b1", key: "/books/b1/read")))
        let answer = await store().manifest(origin, "b1")
        guard case .not(.signin) = answer else { return XCTFail("a redirect at the manifest is sign-in") }
    }

    func testForeignAddressInTheManifestFailsAndFetchesNothing() async {
        StubProtocol.handler = { [self] req in ok(req, "x") }
        for m in [manifest(parts: ["/hx/books/b2/read/chapters/0"]), manifest(assets: ["/other/x.js"]), manifest(parts: ["http://evil.example/hx/books/b1/read/chapters/0"])] {
            let result = await store().fetch(origin, m, freeBytes: 1 << 40)
            XCTAssertEqual(result, .failed)
        }
        XCTAssertTrue(StubProtocol.seen.isEmpty)
    }

    func testNoRoomFailsBeforeAnyRequest() async {
        StubProtocol.handler = { [self] req in ok(req, "x") }
        let result = await store().fetch(origin, manifest(), freeBytes: 1000)
        XCTAssertEqual(result, .failed)
        XCTAssertTrue(StubProtocol.seen.isEmpty)
    }

    func testMissingPartFailsAndNoShellIsStored() async {
        StubProtocol.handler = { [self] req in
            req.url!.path.contains("/chapters/") ? (HTTPURLResponse(url: req.url!, statusCode: 500, httpVersion: "HTTP/1.1", headerFields: nil)!, Data()) : ok(req, "x")
        }
        let result = await store().fetch(origin, manifest(), freeBytes: 1 << 40)
        XCTAssertEqual(result, .failed)
        XCTAssertNil(store().lookup(OfflineRouteShell(bookId: "b1", key: "/books/b1/read")))
    }

    func testManifestAnswers() async {
        let json = "{\"bookId\":\"b1\",\"editionId\":\"e1\",\"revision\":\"r9\",\"title\":\"T\",\"bytes\":5,\"parts\":[],\"images\":[],\"assets\":[]}"
        StubProtocol.handler = { [self] req in ok(req, json, type: "application/json") }
        let answer = await store().manifest(origin, "b1")
        guard case .ok(let m) = answer else { return XCTFail("manifest") }
        XCTAssertEqual(m.revision, "r9")
        XCTAssertEqual(StubProtocol.seen.first?.url?.path, "/offline/books/b1/manifest")
        StubProtocol.handler = { req in (HTTPURLResponse(url: req.url!, statusCode: 404, httpVersion: "HTTP/1.1", headerFields: nil)!, Data()) }
        guard case .code(404) = await store().manifest(origin, "b1") else { return XCTFail("404") }
    }

    func testRemoveAndPrune() async {
        StubProtocol.handler = { [self] req in ok(req, req.url!.path.hasSuffix("/read") ? "<script src=\"/static/a.js\"></script>" : "x") }
        let s = store()
        let result = await s.fetch(origin, manifest(assets: ["/static/a.js", "/static/old.js"]), freeBytes: 1 << 40)
        XCTAssertEqual(result, .ready)
        s.prune(keep: [])
        XCTAssertNotNil(s.lookup(OfflineRouteStatic(key: "/static/a.js")), "named by the stored shell")
        XCTAssertNil(s.lookup(OfflineRouteStatic(key: "/static/old.js")))
        s.remove("b1")
        XCTAssertNil(s.lookup(OfflineRouteShell(bookId: "b1", key: "/books/b1/read")))
    }
}
