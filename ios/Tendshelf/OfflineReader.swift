import Foundation
import WebKit
@preconcurrency import TendshelfShared

/// The iPhone's offline reader origin `tsread://app` (decision 0048, I3d). WKWebView cannot answer at the server's own address,
/// so a stored reader page opens here. It answers GET only from the signed-in person's stored text, through the shared
/// `offlineRoute`, and one POST, `/reading/<id>`, which is kept on the phone (`reading_out`) until the server's own page takes
/// it through the bridge (`rd`). It sends nothing to the network, never gets `window.tsNative` and is never a bundled page.
final class OfflineReader: NSObject, WKURLSchemeHandler {
    static let scheme = "tsread"
    static let host = "app"
    /// The most bytes of one reading place.
    static let maxBody = 64 * 1024

    private var text: TextStore?
    private var store: SqliteOfflineStore?
    private var origin: () -> Origin? = { nil }
    private var userId: () -> String? = { nil }
    private let now: () -> Int64

    /// Made empty (the web view's configuration needs the handler before the download engine exists); [configure] gives it the
    /// store. Until then every address is a 404.
    init(now: @escaping () -> Int64 = { Int64(Date().timeIntervalSince1970 * 1000) }) {
        self.now = now
    }

    func configure(text: TextStore, store: SqliteOfflineStore, origin: @escaping () -> Origin?, userId: @escaping () -> String?) {
        self.text = text
        self.store = store
        self.origin = origin
        self.userId = userId
    }

    /// The reader address of a server path and query (`/books/<id>/read?ch=2`), for a page that loads offline.
    static func url(path: String, query: String?) -> URL? {
        URL(string: "\(scheme)://\(host)" + path + (query.map { "?" + $0 } ?? ""))
    }

    // MARK: WKURLSchemeHandler

    func webView(_ webView: WKWebView, start urlSchemeTask: any WKURLSchemeTask) {
        let request = urlSchemeTask.request
        let reply = answer(request)
        let url = request.url ?? URL(string: "\(Self.scheme)://\(Self.host)/")!
        var headers = ["Content-Type": reply.type, "Content-Length": String(reply.body.count), "Cache-Control": "no-store"]
        if let csp = reply.csp { headers["Content-Security-Policy"] = csp }
        let response = HTTPURLResponse(url: url, statusCode: reply.status, httpVersion: "HTTP/1.1", headerFields: headers)!
        urlSchemeTask.didReceive(response)
        urlSchemeTask.didReceive(reply.body)
        urlSchemeTask.didFinish()
    }

    func webView(_ webView: WKWebView, stop urlSchemeTask: any WKURLSchemeTask) {}

    // MARK: Answers

    struct Reply {
        let status: Int
        let type: String
        let body: Data
        var csp: String?
    }

    private static let notFound = Reply(status: 404, type: "text/plain; charset=utf-8", body: Data("Not stored on this phone.".utf8))

    func answer(_ request: URLRequest) -> Reply {
        guard let url = request.url, url.scheme == Self.scheme, url.host?.lowercased() == Self.host, url.port == nil, url.user == nil,
              let text, let origin = origin(), let parts = URLComponents(url: url, resolvingAgainstBaseURL: false) else { return Self.notFound }
        let path = parts.percentEncodedPath
        let query = parts.percentEncodedQuery
        let address = origin.text + path + (query.map { "?" + $0 } ?? "")
        switch request.httpMethod ?? "GET" {
        case "GET":
            guard let route = OfflineRoutesKt.offlineRoute(url: address, origin: origin), let stored = text.lookup(route),
                  let data = try? Data(contentsOf: stored.file) else { return Self.notFound }
            return Reply(status: 200, type: stored.type, body: data, csp: stored.csp)
        case "POST":
            return place(request, path: path, query: query)
        default:
            return Self.notFound
        }
    }

    /// `POST /reading/<id>` for a book whose text is ready: kept for the current person, answered as applied.
    private func place(_ request: URLRequest, path: String, query: String?) -> Reply {
        let seg = path.split(separator: "/", omittingEmptySubsequences: false).map(String.init)
        guard query == nil, seg.count == 3, seg[0].isEmpty, seg[1] == "reading", BridgeProtocolKt.validId(s: seg[2]),
              let store, let user = userId(), store.text(seg[2])?.state == TextResult.ready.rawValue,
              let body = Self.body(of: request), let form = String(data: body, encoding: .utf8) else { return Self.notFound }
        store.addReading(userId: user, bookId: seg[2], form: form, at: now())
        let at = URLComponents(string: "?" + form)?.queryItems?.first { $0.name == "client_updated_at" }?.value.flatMap { Int64($0) } ?? 0
        return Reply(status: 200, type: "application/json", body: Data("{\"applied\":true,\"reading\":{\"clientUpdatedAt\":\(at)}}".utf8))
    }

    /// The request's body (`httpBody`, or the stream a web view gives a custom scheme), at most [maxBody] bytes; nil for more.
    private static func body(of request: URLRequest) -> Data? {
        if let data = request.httpBody { return data.count <= maxBody ? data : nil }
        guard let stream = request.httpBodyStream else { return Data() }
        stream.open()
        defer { stream.close() }
        var out = Data()
        var buffer = [UInt8](repeating: 0, count: 4096)
        while stream.hasBytesAvailable {
            let n = stream.read(&buffer, maxLength: buffer.count)
            if n <= 0 { break }
            out.append(buffer, count: n)
            if out.count > maxBody { return nil }
        }
        return out
    }
}
