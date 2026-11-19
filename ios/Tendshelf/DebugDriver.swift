#if DEBUG
import MediaPlayer
import WebKit

/// Debug builds only (the whole file is compiled out of Release; `ios/tools` checks the binary for these strings).
/// Lets a headless simulator run fill the forms a person would: launch arguments `-TSTestAddress <address>`,
/// `-TSTestLogin <user>:<password>`, `-TSTestSignOut 1`, `-TSTestAutoYes 1`. It types only into the page's own fields and
/// taps its own buttons; the app's native code is exercised unchanged. Progress goes to stdout as `TSDRIVE ...` lines.
/// I2b: `-TSTestStart <id>` calls `Playback.start` once a page on the server has loaded; `-TSTestScript "pause;seek 20000;speed 1.50"`
/// sends those bridge messages from the page (`window.tsNative.postMessage`), six seconds apart; a probe line each two seconds
/// shows the player's snapshot, the lock-screen card (`MPNowPlayingInfoCenter`) and the last message the page received.
@MainActor
enum DebugDriver {
    static weak var playback: Playback?
    static weak var downloads: Downloads?
    private static var started = false
    private static var downloadStarted = false
    private static var downloadProbe: Timer?
    private static var probe: Timer?

    private static var args: UserDefaults { .standard }
    private static var done = Set<String>()

    /// True when the plain-http alert should answer Continue by itself.
    static var autoYes: Bool { args.bool(forKey: "TSTestAutoYes") }

    static func pageFinished(_ web: WKWebView) {
        guard let url = web.url else { return }
        let path = url.path
        let key = "\(url.absoluteString)"
        report(web, path)
        if url.scheme == BundledPages.scheme, path == "/offline", let after = args.string(forKey: "TSTestRetry").flatMap(Double.init) {
            // I3b: tap "Try again" on the offline page after [after] seconds.
            DispatchQueue.main.asyncAfter(deadline: .now() + after) {
                run(web, "var f=document.querySelector('form[action=\"/connect\"]'); if(f){f.requestSubmit();}")
            }
        }
        startPlayback(web, url)
        startDownload(url)
        tap(web, url)
        startRowsProbe(web)
        if path == "/start", args.string(forKey: "TSTestAddress") != nil, !done.contains("choose") {
            // The first-run page offers two buttons; "I have a server" opens the address form.
            done.insert("choose")
            run(web, "location.href='tsbundle://app/server'")
        } else if path == "/server", let address = args.string(forKey: "TSTestAddress"), !done.contains("address") {
            done.insert("address")
            run(web, "var a=document.getElementById('address'); if(a){a.value=\(literal(address)); a.form.requestSubmit();}")
        } else if path == "/login", let login = args.string(forKey: "TSTestLogin"), !done.contains(key) {
            done.insert(key)
            let parts = login.split(separator: ":", maxSplits: 1).map(String.init)
            guard parts.count == 2 else { return }
            run(web, "document.getElementById('username').value=\(literal(parts[0])); document.getElementById('password').value=\(literal(parts[1])); document.getElementById('username').form.requestSubmit();")
        } else if path.hasPrefix("/app/connect"), !done.contains("consent") {
            done.insert("consent")
            run(web, "var b=document.querySelector('main form button[type=submit]'); if(b){b.click();}")
        } else if path == "/", let go = args.string(forKey: "TSTestGo"), !done.contains("go") {
            // I3b: open one page of the server, such as /me/downloads, once the home page has loaded.
            done.insert("go")
            run(web, "location.href=\(literal(go))")
        } else if path == "/", args.bool(forKey: "TSTestSignOut"), !done.contains("tome") {
            done.insert("tome")
            run(web, "location.href='/me'")
        } else if path == "/me", args.bool(forKey: "TSTestSignOut"), !done.contains("signout") {
            done.insert("signout")
            run(web, "var f=document.querySelector('form[action=\"/logout\"]'); if(f){f.requestSubmit();}")
        }
    }

    /// I3c: `-TSTestProbe 1` prints the player's state and every download row each two seconds from the first page on.
    private static func startRowsProbe(_ web: WKWebView) {
        guard args.bool(forKey: "TSTestProbe"), probe == nil, let downloads else { return }
        probe = Timer.scheduledTimer(withTimeInterval: 2, repeats: true) { _ in
            MainActor.assumeIsolated {
                if let playback { probeNow(web, playback) }
                let rows = downloads.rows().map { r -> String in
                    let v = downloads.view(r.bookId)
                    return "\(r.bookId)=\(r.state.name)/\(v.pct)/auto=\(downloads.isAuto(r.bookId))"
                }
                print("TSDRIVE rows \(rows) unmetered=\(downloads.unmetered())")
            }
        }
    }

    /// I3c: `-TSTestTap "/play;/autoshow"` taps, on the app's own bundled pages (Downloads, offline), the first link whose address
    /// starts with each prefix, once each, in order, on a page load where it exists. The app's native code runs unchanged.
    private static func tap(_ web: WKWebView, _ url: URL) {
        guard url.scheme == BundledPages.scheme, let taps = args.string(forKey: "TSTestTap") else { return }
        let list = taps.split(separator: ";").map(String.init)
        guard let index = list.indices.first(where: { !done.contains("tap\($0)") }) else { return }
        let script = "var a=document.querySelector('a[href^=\"\(list[index])\"]'); if(a){a.click(); true}else{false}"
        web.evaluateJavaScript(script) { value, _ in
            if (value as? Bool) == true {
                done.insert("tap\(index)")
                print("TSDRIVE tapped \(list[index])")
            }
        }
    }

    /// I3b: `-TSTestDownload <id>` adds that book once a page on the server has loaded, and prints `dl=<state>/<pct>` each second.
    private static func startDownload(_ url: URL) {
        guard let id = args.string(forKey: "TSTestDownload"), !downloadStarted, url.scheme?.hasPrefix("http") == true,
              url.path != "/login", !url.path.hasPrefix("/app/connect"), let downloads else { return }
        downloadStarted = true
        Task { await downloads.add(id) }
        downloadProbe = Timer.scheduledTimer(withTimeInterval: 1, repeats: true) { _ in
            MainActor.assumeIsolated {
                let v = downloads.view(id)
                print("TSDRIVE dl=\(v.state)/\(v.pct) files=\(downloads.store.files(id).map { "\($0.step.name):\($0.bytes)" })")
            }
        }
    }

    private static func startPlayback(_ web: WKWebView, _ url: URL) {
        guard let id = args.string(forKey: "TSTestStart"), !started, url.scheme?.hasPrefix("http") == true,
              url.path != "/login", !url.path.hasPrefix("/app/connect"), let playback else { return }
        started = true
        // Collect what the page receives, so the probe can show the bar would follow.
        run(web, "window.__tsLog=[]; if(window.tsNative){window.tsNative.addEventListener('message',e=>{window.__tsLog.push(e.data);});}")
        playback.start(id, atMs: nil)
        probe = Timer.scheduledTimer(withTimeInterval: 2, repeats: true) { _ in
            MainActor.assumeIsolated { probeNow(web, playback) }
        }
        let steps = (args.string(forKey: "TSTestScript") ?? "").split(separator: ";").map { $0.trimmingCharacters(in: .whitespaces) }
        // A step is a bridge message, sent six seconds after the one before; `wait N` adds N seconds; `signout` is the page's
        // own Sign out (the me page's form).
        var at = 8.0
        for step in steps {
            if step.hasPrefix("wait "), let n = Double(step.dropFirst(5)) {
                at += n
                continue
            }
            DispatchQueue.main.asyncAfter(deadline: .now() + at) {
                print("TSDRIVE script step \(step)")
                if step == "signout" {
                    run(web, "location.href='/me'")
                    DispatchQueue.main.asyncAfter(deadline: .now() + 4) {
                        run(web, "var f=document.querySelector('form[action=\"/logout\"]'); if(f){f.requestSubmit();}")
                    }
                } else {
                    run(web, "window.tsNative.postMessage(\(literal(step)))")
                }
            }
            at += 6
        }
    }

    private static func probeNow(_ web: WKWebView, _ playback: Playback) {
        let s = playback.snapshot()
        let info = MPNowPlayingInfoCenter.default().nowPlayingInfo ?? [:]
        let title = (info[MPMediaItemPropertyTitle] as? String) ?? "-"
        let artist = (info[MPMediaItemPropertyArtist] as? String) ?? "-"
        let elapsed = (info[MPNowPlayingInfoPropertyElapsedPlaybackTime] as? Double).map { String(format: "%.1f", $0) } ?? "-"
        let rate = (info[MPNowPlayingInfoPropertyPlaybackRate] as? Double).map { String($0) } ?? "-"
        let art = info[MPMediaItemPropertyArtwork] != nil
        let dl = args.string(forKey: "TSTestDownload").flatMap { id in downloads.map { d in d.view(id) } }.map { "\($0.state)/\($0.pct)" } ?? "-"
        web.evaluateJavaScript("(window.__tsLog||[]).length + ' ' + ((window.__tsLog||[]).filter(m=>m.indexOf('\"state\"')>0).slice(-1)[0]||'')") { value, _ in
            let last = String(describing: value ?? "nil").prefix(220)
            print("TSDRIVE probe book=\(s.bookId) pos=\(s.positionMs) dur=\(s.durationMs) playing=\(s.playing) buf=\(s.buffering) speed=\(s.speed) ch=\(s.chapterIdx) err=\(s.error) ask=\(s.ask.map { "\($0.deviceLabel)@\($0.positionMs)" } ?? "-") rewound=\(s.rewoundS)/\(s.rewoundSeq) sleep=\(s.sleep.mode.name)/\(s.sleep.leftMs)/\(s.sleep.fading) rows=\(playback.writer.rowCount()) dl=\(dl) card[title=\(title) artist=\(artist) elapsed=\(elapsed) rate=\(rate) art=\(art)] page=\(last)")
        }
    }

    private static func report(_ web: WKWebView, _ path: String) {
        web.evaluateJavaScript("document.documentElement.getAttribute('data-shell')") { value, _ in
            print("TSDRIVE finished \(web.url?.scheme ?? "")://\(web.url?.host ?? "")\(path) data-shell=\(String(describing: value ?? "nil"))")
        }
    }

    private static func run(_ web: WKWebView, _ script: String) {
        web.evaluateJavaScript(script) { _, error in
            if let error { print("TSDRIVE script error \(error.localizedDescription)") }
        }
    }

    private static func literal(_ text: String) -> String {
        let data = (try? JSONSerialization.data(withJSONObject: [text])) ?? Data("[\"\"]".utf8)
        let json = String(decoding: data, as: UTF8.self)
        return String(json.dropFirst().dropLast())
    }
}
#endif
