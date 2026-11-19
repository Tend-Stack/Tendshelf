import WebKit
import XCTest
@preconcurrency import TendshelfShared
@testable import Tendshelf

/// I3d: the offline reader origin `tsread://app`, in a real `WKWebView`. These prove the unproven points of the plan: a CSP of
/// `'self'` and ES modules work on a custom scheme, a `fetch` POST reaches the handler, and what `sendBeacon` does.
@MainActor
final class OfflineReaderTests: XCTestCase {
    let origin = Origin.companion.parse(input: "http://10.0.0.5:8095")!
    var root: URL!
    var paths: OfflinePaths!
    var offline: SqliteOfflineStore!
    var text: TextStore!
    var reader: OfflineReader!
    var web: WKWebView!
    var user: String? = "u1"

    static let csp = "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'"
    static let shell = "<!doctype html><html data-shell=\"ios\"><head><meta charset=\"utf-8\"><title>reader</title>"
        + "<script type=\"module\" src=\"/static/m.js\"></script></head><body><p id=\"x\">shell</p></body></html>"
    static let module = """
    window.__stage = "module ran";
    const out = {};
    try {
      out.x = (await import("/static/n.js")).x;
      window.__stage = "imported";
      out.part = await (await fetch("/hx/books/b1/read/chapters/0")).text();
      out.miss = (await fetch("/static/missing.js")).status;
      out.other = (await fetch("/books/b2/read")).status;
      out.get = (await fetch("/reading/b1")).status;
      window.__stage = "posting";
      const res = await fetch("/reading/b1", { method: "POST", body: "sid=c1&client_updated_at=5&chars_read=3&_csrf=t" });
      out.post = await res.json();
      out.postOther = (await fetch("/reading/b9", { method: "POST", body: "sid=c1" })).status;
      out.big = (await fetch("/reading/b1", { method: "POST", body: "a".repeat(70000) })).status;
      try {
        out.beacon = navigator.sendBeacon("/reading/b1", "sid=c2&client_updated_at=6&chars_read=1");
      } catch (e) {
        out.beacon = String(e);
      }
      // What the reader does instead on this origin: a keepalive request.
      fetch("/reading/b1", { method: "POST", body: "sid=c3&client_updated_at=7&chars_read=1", keepalive: true });
      out.native = typeof window.tsNative;
    } catch (e) {
      out.error = String(e) + " at " + window.__stage;
    }
    window.__r = JSON.stringify(out);
    """

    override func setUp() async throws {
        root = FileManager.default.temporaryDirectory.appendingPathComponent("ts-reader-" + UUID().uuidString, isDirectory: true)
        paths = OfflinePaths(root: root)
        try paths.prepare()
        offline = SqliteOfflineStore(path: paths.database)
        text = TextStore(paths: paths, protocolClasses: [StubProtocol.self])
        text.cookieHeader = { _ in "sid=abc" }
        StubProtocol.seen = []
        StubProtocol.handler = { req in
            let path = req.url!.path
            var type = "text/html"
            var csp: String?
            var body = "<p>part</p>"
            if path.hasSuffix("/read") {
                body = Self.shell
                csp = Self.csp
            } else if path == "/static/m.js" {
                body = Self.module
                type = "text/javascript"
            } else if path == "/static/n.js" {
                body = "export const x = 7;"
                type = "text/javascript"
            }
            var headers = ["Content-Type": type]
            if let csp { headers["Content-Security-Policy"] = csp }
            return (HTTPURLResponse(url: req.url!, statusCode: 200, httpVersion: "HTTP/1.1", headerFields: headers)!, Data(body.utf8))
        }
        let m = TextManifest(
            bookId: "b1", editionId: "e1", revision: "r1", title: "T", author: "A", bytes: 10,
            parts: ["/hx/books/b1/read/chapters/0"], images: [], assets: ["/static/m.js", "/static/n.js"])
        let result = await text.fetch(origin, m, freeBytes: 1 << 40)
        XCTAssertEqual(result, .ready)
        offline.put(
            OfflineBook(
                bookId: "b1", userId: "u1", title: "T", author: "A", durationMs: 0, sizeBytes: 0, state: .ready, placeMs: 0, placeAt: 0,
                kind: OfflineKt.KIND_BOOK, showId: "", publishedAt: 0, played: false), "{}", at: 1)
        offline.setText("b1", state: "ready", revision: "r1", edition: "e1")
        reader = OfflineReader()
        reader.configure(text: text, store: offline, origin: { [origin] in origin }, userId: { [unowned self] in user })
        let config = WKWebViewConfiguration()
        config.setURLSchemeHandler(reader, forURLScheme: OfflineReader.scheme)
        web = WKWebView(frame: CGRect(x: 0, y: 0, width: 390, height: 700), configuration: config)
    }

    override func tearDown() async throws {
        web = nil
        offline.close()
        try? FileManager.default.removeItem(at: root)
    }

    private func result() async throws -> [String: Any] {
        for _ in 0..<150 {
            if let json = try await web.evaluateJavaScript("window.__r") as? String {
                return try XCTUnwrap(JSONSerialization.jsonObject(with: Data(json.utf8)) as? [String: Any])
            }
            try await Task.sleep(nanoseconds: 100_000_000)
        }
        let stage = try? await web.evaluateJavaScript("String(window.__stage)")
        XCTFail("the page's module never finished (stage: \(stage ?? "none")): CSP or module loading on the custom scheme failed")
        return [:]
    }

    func testStoredShellRunsItsModuleUnderItsCspAndAnswersOnlyWhatIsStored() async throws {
        web.load(URLRequest(url: URL(string: "tsread://app/books/b1/read")!))
        let out = try await result()
        XCTAssertNil(out["error"], "the module's own error")
        XCTAssertEqual(out["x"] as? Int, 7, "an ES module imported another one on tsread://app")
        XCTAssertEqual(out["part"] as? String, "<p>part</p>")
        XCTAssertEqual(out["miss"] as? Int, 404, "a static file that is not stored")
        XCTAssertEqual(out["other"] as? Int, 404, "another book's reader")
        XCTAssertEqual(out["get"] as? Int, 404, "GET /reading is not answered")
        XCTAssertEqual(out["native"] as? String, "undefined")
        let post = try XCTUnwrap(out["post"] as? [String: Any])
        XCTAssertEqual(post["applied"] as? Bool, true)
        XCTAssertEqual((post["reading"] as? [String: Any])?["clientUpdatedAt"] as? Int, 5)
        XCTAssertEqual(out["postOther"] as? Int, 404, "a book with no stored text keeps nothing")
        XCTAssertEqual(out["big"] as? Int, 404, "a body over 64 KiB")
        // The beacon is asynchronous; wait for it a moment and say what happened.
        try await Task.sleep(nanoseconds: 1_500_000_000)
        let rows = offline.readingRows(userId: "u1", limit: 100, bytes: 1 << 20)
        print("TSBEACON sent=\(out["beacon"] ?? "?") rows=\(rows.count) forms=\(rows.map(\.form))")
        XCTAssertTrue(rows.contains { $0.form.contains("sid=c1") && $0.bookId == "b1" })
        XCTAssertTrue(rows.contains { $0.form.contains("sid=c3") }, "a keepalive request reaches the handler")
        XCTAssertEqual(rows.first?.seq, 1)
    }

    func testAnswersAreNoStoreAndCarryTheStoredCsp() {
        let ok = reader.answer(URLRequest(url: URL(string: "tsread://app/books/b1/read")!))
        XCTAssertEqual(ok.status, 200)
        XCTAssertEqual(ok.csp, Self.csp)
        XCTAssertTrue(ok.type.hasPrefix("text/html"))
        for bad in ["tsread://other/books/b1/read", "tsread://app/books/b1/read?download=1", "tsread://app/hx/books/b1/read/chapters/9", "tsread://app/static/../x"] {
            XCTAssertEqual(reader.answer(URLRequest(url: URL(string: bad)!)).status, 404, bad)
        }
        var head = URLRequest(url: URL(string: "tsread://app/books/b1/read")!)
        head.httpMethod = "PUT"
        XCTAssertEqual(reader.answer(head).status, 404)
    }

    func testPostNeedsAPersonAndAServer() {
        var post = URLRequest(url: URL(string: "tsread://app/reading/b1")!)
        post.httpMethod = "POST"
        post.httpBody = Data("sid=c1".utf8)
        XCTAssertEqual(reader.answer(post).status, 200)
        user = nil
        XCTAssertEqual(reader.answer(post).status, 404)
        user = "u1"
        var stream = post
        stream.httpBody = nil
        stream.httpBodyStream = InputStream(data: Data("sid=c3".utf8))
        XCTAssertEqual(reader.answer(stream).status, 200)
        XCTAssertEqual(offline.readingRows(userId: "u1", limit: 100, bytes: 1 << 20).map(\.form), ["sid=c1", "sid=c3"])
        XCTAssertTrue(offline.readingRows(userId: "u2", limit: 100, bytes: 1 << 20).isEmpty)
    }

    func testRowsAreForgottenUpToTheSequence() {
        for n in 1...3 { offline.addReading(userId: "u1", bookId: "b1", form: "n=\(n)", at: Int64(n)) }
        offline.addReading(userId: "u2", bookId: "b1", form: "other", at: 9)
        XCTAssertEqual(offline.readingRows(userId: "u1", limit: 2, bytes: 1 << 20).map(\.form), ["n=1", "n=2"])
        XCTAssertEqual(offline.readingRows(userId: "u1", limit: 100, bytes: 5).map(\.form), ["n=1"], "the byte cap stops in order")
        offline.deleteReading(userId: "u1", upTo: 2)
        XCTAssertEqual(offline.readingRows(userId: "u1", limit: 100, bytes: 1 << 20).map(\.form), ["n=3"])
        XCTAssertEqual(offline.readingRows(userId: "u2", limit: 100, bytes: 1 << 20).count, 1)
        offline.clear()
        XCTAssertEqual(offline.readingCount(), 0)
    }
}
