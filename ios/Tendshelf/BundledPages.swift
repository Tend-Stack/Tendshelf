import UIKit
import WebKit
@preconcurrency import TendshelfShared

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
    static var downloadsUrl: URL { URL(string: "tsbundle://app/downloads")! }
    static var offlineUrl: URL { URL(string: "tsbundle://app/offline")! }

    var state = StartState()
    /// The join page's note (its field is always empty: a pasted link is never put back on a page).
    var joinState = StartState()
    /// "dark" or "light" for the page's `data-theme`; the controller supplies the trait collection's answer.
    var dark: () -> Bool = { UITraitCollection.current.userInterfaceStyle == .dark }
    /// What the Downloads and offline pages list; the argument is true for the offline page. Set by the controller (I3b).
    var downloadsInput: ((Bool) -> DownloadsInput)?
    /// A downloaded book's cover (a stored file); nil for none.
    var coverData: ((String) -> Data?)?
    /// The address the offline page says it could not reach.
    var offlineAddress = ""

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
        case "/downloads":
            return downloadsPage(offline: false)
        case "/offline":
            return downloadsPage(offline: true)
        default:
            if path.hasPrefix("/covers/") {
                let id = String(path.dropFirst("/covers/".count))
                guard BridgeProtocolKt.validId(s: id), let bytes = coverData?(id) else { return notFound }
                return BundledReply(status: 200, mime: "image/jpeg", body: bytes)
            }
            if path == "/assets/safe-area.css" {
                // The pages' CSP allows no inline style, so the rule that keeps them clear of the status bar is a file.
                let css = "body{padding-top:env(safe-area-inset-top);padding-bottom:env(safe-area-inset-bottom)}\n"
                return BundledReply(status: 200, mime: "text/css; charset=utf-8", body: Data(css.utf8))
            }
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

    /// The one list of downloaded books, drawn by the shared renderer (the same as Android's) with every value escaped and no
    /// script. The offline page adds the "can't reach your server" words and Try again above it.
    private func downloadsPage(offline: Bool) -> BundledReply {
        guard let input = downloadsInput?(offline) else { return notFound }
        return page("offline.html", DownloadsPageKt.downloadsPage(input: input, w: downloadsWords()))
    }

    /// The words raw, as `strings.xml` holds them; the shared renderer fills `%1$d` itself.
    private func downloadsWords() -> DownloadsWords {
        typealias K = Words.Key
        return DownloadsWords(
            title: words(K.downloadsTitle), lead: words(K.downloadsLead), empty: words(K.downloadsEmpty),
            emptyHow: words(K.downloadsEmptyHow), active: words(K.downloadsActive), ready: words(K.downloadsReady),
            percent: words(K.downloadsPercent), working: words(K.downloadsWorking), waiting: words(K.downloadsWaiting),
            pausedRow: words(K.downloadsPausedRow), failedRow: words(K.downloadsFailedRow), staleRow: words(K.downloadsStaleRow),
            play: words(K.downloadsPlay), read: words(K.downloadsRead), pause: words(K.downloadsPause),
            remove: words(K.downloadsRemove), library: words(K.downloadsLibrary), autoRow: words(K.downloadsAutoRow),
            autoWaiting: words(K.downloadsAutoWaiting), autoNoRoom: words(K.downloadsAutoNoRoom),
            podcasts: words(K.downloadsPodcasts), podcastsLead: words(K.downloadsPodcastsLead),
            podcastOnRow: words(K.downloadsPodcastOnRow), podcastOffRow: words(K.downloadsPodcastOffRow),
            turnOn: words(K.downloadsPodcastTurnOn), turnOff: words(K.downloadsPodcastTurnOff),
            offlineTitle: words(K.offlineTitle), offlineBody: words(K.offlineBody), offlineRetry: words(K.offlineRetry))
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
        // The window runs under the status bar and the home indicator, so the page keeps clear of them (the server's own
        // pages do the same with env()); the Android pages need no such rule.
        html = html.replacingOccurrences(of: "content=\"width=device-width, initial-scale=1\"", with: "content=\"width=device-width, initial-scale=1, viewport-fit=cover\"")
        html = html.replacingOccurrences(of: "</head>", with: "<link rel=\"stylesheet\" href=\"/assets/safe-area.css\"></head>")
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
