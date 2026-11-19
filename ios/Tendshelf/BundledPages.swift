import UIKit
import WebKit

/// What the start page shows: the address to fill in and, after a failure, one sentence of plain words.
struct StartState: Equatable {
    var address = ""
    var note: String?
}

/// One answer of the bundled site.
struct BundledReply {
    let status: Int
    let mime: String
    let body: Data
}

/// The bundled pages, served from `tsbundle://app` (decision 0048): `/start`, `/server`, `/join` and the `/assets/` folder
/// (the server's own stylesheets and fonts, copied at build time into `ShellAssets/`). The same templates as the Android
/// app, filled with HTML-escaped words exactly as `StartPages.kt` does. The pages hold no script at all.
final class BundledPages: NSObject, WKURLSchemeHandler {
    static let scheme = "tsbundle"
    static let host = "app"
    static var startUrl: URL { URL(string: "tsbundle://app/start")! }
    static var joinUrl: URL { URL(string: "tsbundle://app/join")! }

    var state = StartState()
    /// The join page's note (its field is always empty: a pasted link is never put back on a page).
    var joinState = StartState()
    /// "dark" or "light" for the page's `data-theme`; the controller supplies the trait collection's answer.
    var dark: () -> Bool = { UITraitCollection.current.userInterfaceStyle == .dark }

    private let assets: URL?

    init(assets: URL? = Bundle(for: BundledPages.self).url(forResource: "ShellAssets", withExtension: nil)) {
        self.assets = assets
    }

    // MARK: WKURLSchemeHandler

    func webView(_ webView: WKWebView, start urlSchemeTask: any WKURLSchemeTask) {
        let url = urlSchemeTask.request.url
        let reply = reply(for: url?.path ?? "")
        let response = HTTPURLResponse(
            url: url ?? Self.startUrl,
            statusCode: reply.status,
            httpVersion: "HTTP/1.1",
            headerFields: ["Content-Type": reply.mime, "Content-Length": String(reply.body.count), "Cache-Control": "no-store"]
        )!
        urlSchemeTask.didReceive(response)
        urlSchemeTask.didReceive(reply.body)
        urlSchemeTask.didFinish()
    }

    func webView(_ webView: WKWebView, stop urlSchemeTask: any WKURLSchemeTask) {}

    // MARK: Answers

    /// The answer for a path of the bundled site; anything else is a plain 404.
    func reply(for path: String) -> BundledReply {
        switch path {
        case "/start":
            return state.address.isEmpty && state.note == nil ? page("choose.html", choose()) : page("start.html", start())
        case "/server":
            return page("start.html", start())
        case "/join":
            return page("join.html", join())
        default:
            if path.hasPrefix("/assets/"), let file = asset(String(path.dropFirst("/assets/".count))) {
                return file
            }
            return notFound
        }
    }

    private var notFound: BundledReply { BundledReply(status: 404, mime: "text/plain; charset=utf-8", body: Data("Not found".utf8)) }

    /// A file copied into the assets folder: only stylesheets and `fonts/*.woff2` that are really there, never the
    /// templates or `strings.xml`, and never a name that climbs out of the folder.
    private func asset(_ name: String) -> BundledReply? {
        guard let assets, !name.isEmpty, !name.contains(".."), !name.hasPrefix("/") else { return nil }
        let mime: String
        switch (name as NSString).pathExtension {
        case "css": mime = "text/css; charset=utf-8"
        case "woff2": mime = "font/woff2"
        default: return nil
        }
        let components = name.split(separator: "/", omittingEmptySubsequences: false)
        let allowedFolder = components.count == 1 || (components.count == 2 && components[0] == "fonts")
        guard allowedFolder else { return nil }
        guard let data = try? Data(contentsOf: assets.appendingPathComponent(name)) else { return nil }
        return BundledReply(status: 200, mime: mime, body: data)
    }

    private func choose() -> [String: String] {
        [
            "TITLE": Self.esc(words(Words.Key.startTitle)),
            "SERVER": Self.esc(words(Words.Key.startServer)),
            "INVITED": Self.esc(words(Words.Key.startInvited)),
            // The camera hint does not apply on the iPhone (no QR scanning in I1).
            "SCAN": "",
        ]
    }

    private func join() -> [String: String] {
        let note = joinState.note.map { "<p class=\"start__note\" role=\"alert\">\(Self.esc($0))</p>" } ?? ""
        return [
            "TITLE": Self.esc(words(Words.Key.startInvited)),
            "NOTE": note,
            "LABEL": Self.esc(words(Words.Key.joinLabel)),
            "HINT": Self.esc(words(Words.Key.joinHint)),
            "BUTTON": Self.esc(words(Words.Key.joinButton)),
            "SERVER": Self.esc(words(Words.Key.startServer)),
        ]
    }

    private func start() -> [String: String] {
        let s = state
        let note = s.note.map { "<p class=\"start__note\" role=\"alert\">\(Self.esc($0))</p>" } ?? ""
        return [
            "TITLE": Self.esc(words(Words.Key.startTitle)),
            "BODY": Self.esc(words(Words.Key.startBody)),
            "NOTE": note,
            "LABEL": Self.esc(words(Words.Key.startLabel)),
            "HINT": Self.esc(words(Words.Key.startHint)),
            "BUTTON": Self.esc(words(Words.Key.startButton)),
            "ADDRESS": Self.esc(s.address),
            "INVITED": Self.esc(words(Words.Key.startInvited)),
        ]
    }

    private func page(_ template: String, _ values: [String: String]) -> BundledReply {
        guard let assets, let raw = try? String(contentsOf: assets.appendingPathComponent(template), encoding: .utf8) else {
            return notFound
        }
        var html = raw.replacingOccurrences(of: "{{THEME}}", with: dark() ? "dark" : "light")
        for (key, value) in values {
            html = html.replacingOccurrences(of: "{{\(key)}}", with: value)
        }
        return BundledReply(status: 200, mime: "text/html; charset=utf-8", body: Data(html.utf8))
    }

    /// The same five replacements as `StartPages.esc`.
    static func esc(_ s: String) -> String {
        var out = ""
        for ch in s {
            switch ch {
            case "&": out += "&amp;"
            case "<": out += "&lt;"
            case ">": out += "&gt;"
            case "\"": out += "&quot;"
            case "'": out += "&#39;"
            default: out.append(ch)
            }
        }
        return out
    }
}
