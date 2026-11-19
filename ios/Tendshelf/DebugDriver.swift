#if DEBUG
import WebKit

/// Debug builds only (the whole file is compiled out of Release; `ios/tools` checks the binary for these strings).
/// Lets a headless simulator run fill the forms a person would: launch arguments `-TSTestAddress <address>`,
/// `-TSTestLogin <user>:<password>`, `-TSTestSignOut 1`, `-TSTestAutoYes 1`. It types only into the page's own fields and
/// taps its own buttons; the app's native code is exercised unchanged. Progress goes to stdout as `TSDRIVE ...` lines.
enum DebugDriver {
    private static var args: UserDefaults { .standard }
    private static var done = Set<String>()

    /// True when the plain-http alert should answer Continue by itself.
    static var autoYes: Bool { args.bool(forKey: "TSTestAutoYes") }

    static func pageFinished(_ web: WKWebView) {
        guard let url = web.url else { return }
        let path = url.path
        let key = "\(url.absoluteString)"
        report(web, path)
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
        } else if path == "/", args.bool(forKey: "TSTestSignOut"), !done.contains("tome") {
            done.insert("tome")
            run(web, "location.href='/me'")
        } else if path == "/me", args.bool(forKey: "TSTestSignOut"), !done.contains("signout") {
            done.insert("signout")
            run(web, "var f=document.querySelector('form[action=\"/logout\"]'); if(f){f.requestSubmit();}")
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
