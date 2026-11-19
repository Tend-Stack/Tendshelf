import CryptoKit
import Foundation
import OSLog
@preconcurrency import TendshelfShared

/// How a text download ended. `none` is "no right to this text" (a 404 at the manifest), not a failure.
enum TextResult: String { case ready, none, signin, failed }

/// A stored answer: the file, and the headers the server sent that matter.
struct StoredText {
    let file: URL
    let type: String
    let csp: String?
}

/// The text of the downloaded books (decision 0048, I3d; port of `TextStore.kt`). Files under `offline/text/<bookId>/` and the
/// shared `offline/static/`: each answer is a file named for the hash of its address, plus `index.json` (the shared `TextIndex`)
/// mapping address to file, `Content-Type` and `Content-Security-Policy`. Fetching uses the web view's session cookie for the
/// connected origin's host only, built for the request, and nothing else: it is never kept, logged or stored, and this class
/// never POSTs. The session has no cookie store and follows no redirect (a redirect means the session is gone).
final class TextStore {
    private static let log = Logger(subsystem: "app.tendshelf.ios", category: "text")
    static let maxAnswer = 32 * 1024 * 1024
    static let spare: Int64 = 50 * 1024 * 1024

    let paths: OfflinePaths
    private let session: URLSession
    private let refuser = RedirectRefuser()

    /// The `Cookie` header for a request to [origin]: the session cookies the web view holds for that host. Set by the
    /// controller; empty when there is none.
    var cookieHeader: (Origin) async -> String = { _ in "" }

    /// [protocolClasses] is for tests (a `URLProtocol` stub in front of the network).
    init(paths: OfflinePaths, protocolClasses: [AnyClass] = []) {
        self.paths = paths
        let config = ServerClient.ephemeralConfiguration()
        config.timeoutIntervalForRequest = 30
        if !protocolClasses.isEmpty { config.protocolClasses = protocolClasses }
        session = URLSession(configuration: config, delegate: refuser, delegateQueue: nil)
    }

    // MARK: Reading

    private static func name(_ key: String) -> String {
        "s" + SHA256.hash(data: Data(key.utf8)).map { String(format: "%02x", $0) }.joined()
    }

    /// The stored answer for [route], or nil: a book with no stored text, or an address not stored.
    func lookup(_ route: OfflineRoute) -> StoredText? {
        switch route {
        case let r as OfflineRouteShell: return find(paths.text(r.bookId), r.key)
        case let r as OfflineRoutePart: return find(paths.text(r.bookId), r.key)
        case let r as OfflineRouteImage: return find(paths.text(r.bookId), r.key)
        case let r as OfflineRouteStatic: return find(paths.staticDir, r.key)
        default: return nil
        }
    }

    private func find(_ dir: URL?, _ key: String) -> StoredText? {
        guard let dir, let entry = readIndex(dir).get(key: key) else { return nil }
        let file = dir.appendingPathComponent(entry.file)
        guard !entry.file.isEmpty, !entry.file.contains("/"), FileManager.default.fileExists(atPath: file.path) else { return nil }
        return StoredText(file: file, type: entry.type, csp: entry.csp.isEmpty ? nil : entry.csp)
    }

    private func readIndex(_ dir: URL) -> TextIndex {
        guard let text = try? String(contentsOf: dir.appendingPathComponent("index.json"), encoding: .utf8) else { return TextIndex() }
        return TextIndex.companion.parse(text: text)
    }

    private func writeIndex(_ dir: URL, _ index: TextIndex) throws {
        try Data(index.toJson().utf8).write(to: dir.appendingPathComponent("index.json"), options: .atomic)
    }

    // MARK: Removing

    func remove(_ book: String) {
        if let dir = paths.text(book) { try? FileManager.default.removeItem(at: dir) }
    }

    /// Static files no stored shell names any more (a new build replaced them).
    func prune(keep: [String]) {
        var used = Set(keep)
        let fm = FileManager.default
        let root = paths.root.appendingPathComponent("text", isDirectory: true)
        let books = (try? fm.contentsOfDirectory(at: root, includingPropertiesForKeys: nil)) ?? []
        let named = try? NSRegularExpression(pattern: "/static/[A-Za-z0-9._/-]+")
        for dir in books {
            let index = readIndex(dir)
            for key in index.keys where key.hasPrefix("/books/") {
                guard let entry = index.get(key: key), let html = try? String(contentsOf: dir.appendingPathComponent(entry.file), encoding: .utf8) else { continue }
                let range = NSRange(html.startIndex..., in: html)
                for m in named?.matches(in: html, range: range) ?? [] {
                    if let r = Range(m.range, in: html) { used.insert(String(html[r])) }
                }
            }
        }
        let dir = paths.staticDir
        let index = readIndex(dir)
        var changed = false
        for key in index.keys where !used.contains(key) {
            if let entry = index.get(key: key) { try? fm.removeItem(at: dir.appendingPathComponent(entry.file)) }
            index.remove(key: key)
            changed = true
        }
        if changed { try? writeIndex(dir, index) }
    }

    // MARK: Fetching

    private struct Reply {
        let code: Int
        let body: Data
        let type: String
        let csp: String?
    }

    private struct TooLarge: Error {}

    /// The session cookie of the web view for [origin], plus the shell mark: the stored pages are the app's own variant, so
    /// `ts_shell=ios` goes with the session even if the jar lost it.
    private func cookie(_ origin: Origin) async -> String {
        let jar = await cookieHeader(origin)
        if jar.range(of: "(^|; )ts_shell=", options: .regularExpression) != nil { return jar }
        return (jar.isEmpty ? "" : jar + "; ") + "ts_shell=ios"
    }

    /// One GET on the server's origin. [withCookie] false for static files. A redirect is returned, never followed.
    private func get(_ origin: Origin, _ path: String, withCookie: Bool, limit: Int = TextStore.maxAnswer) async throws -> Reply {
        let text = origin.text + path
        guard origin.matches(url: text), let url = URL(string: text) else { throw URLError(.badURL) }
        var request = URLRequest(url: url, cachePolicy: .reloadIgnoringLocalCacheData, timeoutInterval: 30)
        request.httpShouldHandleCookies = false
        if withCookie { request.setValue(await cookie(origin), forHTTPHeaderField: "Cookie") }
        let (bytes, response) = try await session.bytes(for: request)
        guard let http = response as? HTTPURLResponse else { throw URLError(.badServerResponse) }
        guard (200...299).contains(http.statusCode) else { return Reply(code: http.statusCode, body: Data(), type: "", csp: nil) }
        if http.expectedContentLength > Int64(limit) { throw TooLarge() }
        var body = Data()
        for try await byte in bytes {
            if body.count >= limit { throw TooLarge() }
            body.append(byte)
        }
        return Reply(
            code: http.statusCode, body: body, type: http.value(forHTTPHeaderField: "Content-Type") ?? "application/octet-stream",
            csp: http.value(forHTTPHeaderField: "Content-Security-Policy"))
    }

    /// The manifest, or the reason there is none.
    enum ManifestAnswer {
        case ok(TextManifest)
        case not(TextResult)
        case code(Int)
    }

    func manifest(_ origin: Origin, _ book: String) async -> ManifestAnswer {
        let reply: Reply
        do {
            reply = try await get(origin, "/offline/books/\(book)/manifest", withCookie: true, limit: 4 * 1024 * 1024)
        } catch {
            return .not(.failed)
        }
        switch reply.code {
        case 404, 403: return .code(404)
        case 300...399: return .not(.signin)
        case 200:
            guard let text = String(data: reply.body, encoding: .utf8), let m = StoredJsonKt.parseTextManifestOrNull(text: text) else { return .not(.failed) }
            return .ok(m)
        default: return .not(.failed)
        }
    }

    /// Stores everything [m] lists, the contents sheet and then the shell last (a book with no shell cannot be opened). A
    /// redirect means the session is gone. The room is checked against the manifest's own size estimate first.
    func fetch(_ origin: Origin, _ m: TextManifest, freeBytes: Int64) async -> TextResult {
        guard BridgeProtocolKt.validId(s: m.bookId), freeBytes >= m.bytes + Self.spare else { return .failed }
        // Whatever the server lists must be an address the app would answer for this book; nothing else is fetched.
        guard OfflineRoutesKt.textFetchList(m: m, origin: origin) != nil, let dir = paths.text(m.bookId) else { return .failed }
        let fm = FileManager.default
        let sdir = paths.staticDir
        do {
            try fm.createDirectory(at: dir, withIntermediateDirectories: true)
            try fm.createDirectory(at: sdir, withIntermediateDirectories: true)
            let keep = readIndex(dir)
            let fresh = TextIndex()
            for key in m.parts + m.images {
                let r = try await get(origin, key, withCookie: true)
                if (300...399).contains(r.code) { return .signin }
                if r.code != 200 {
                    // A picture that cannot be read is skipped; a missing part would leave a hole in the book.
                    if m.images.contains(key) { continue }
                    return .failed
                }
                try put(dir, fresh, key, r)
            }
            let staticIndex = readIndex(sdir)
            for key in m.assets {
                if let entry = staticIndex.get(key: key), fm.fileExists(atPath: sdir.appendingPathComponent(entry.file).path) { continue }
                let r = try await get(origin, key, withCookie: false)
                if r.code != 200 { return .failed }
                try put(sdir, staticIndex, key, r)
                try writeIndex(sdir, staticIndex)
            }
            // Parts first into the index, then the two shell pages: a book with no shell cannot be opened.
            try writeIndex(dir, keep.mergedWith(fresh: fresh))
            for path in ["/books/\(m.bookId)/read?sheet=contents", "/books/\(m.bookId)/read"] {
                let r = try await get(origin, "/offline/books/\(m.bookId)/read" + (path.range(of: "?").map { String(path[$0.lowerBound...]) } ?? ""), withCookie: true)
                if (300...399).contains(r.code) { return .signin }
                if r.code != 200 { return .failed }
                let index = readIndex(dir)
                try put(dir, index, path, r)
                try writeIndex(dir, index)
            }
            return .ready
        } catch {
            Self.log.warning("text: \(String(describing: type(of: error)))")
            return .failed
        }
    }

    private func put(_ dir: URL, _ index: TextIndex, _ key: String, _ r: Reply) throws {
        let file = Self.name(key)
        try r.body.write(to: dir.appendingPathComponent(file), options: .atomic)
        index.set(key: key, entry: TextIndexEntry(file: file, type: r.type, csp: r.csp ?? ""))
    }
}
