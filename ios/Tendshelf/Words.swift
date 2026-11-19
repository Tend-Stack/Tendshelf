import Foundation

/// The app's words. They live in one place for both phone apps: Android's `strings.xml`, copied into the app as
/// `ShellAssets/strings.xml` by the "Copy shell assets" phase (decision 0048). Plain words, no translation yet.
enum Words {
    /// Every key the Swift code asks for. `WordsTests` checks each one exists in `strings.xml`.
    enum Key {
        static let startTitle = "start_title"
        static let startBody = "start_body"
        static let startLabel = "start_label"
        static let startButton = "start_button"
        static let startHint = "start_hint"
        static let startServer = "start_server"
        static let startInvited = "start_invited"
        static let joinLabel = "join_label"
        static let joinHint = "join_hint"
        static let joinButton = "join_button"
        static let addressOnly = "address_only"
        static let unreachable = "unreachable"
        static let certificate = "certificate"
        static let notTendshelf = "not_tendshelf"
        static let oldServer = "old_server"
        static let httpWarningTitle = "http_warning_title"
        static let httpWarningBody = "http_warning_body"
        static let httpWarningPublic = "http_warning_public"
        static let httpWarningContinue = "http_warning_continue"
        static let httpWarningCancel = "http_warning_cancel"

        static let all = [
            startTitle, startBody, startLabel, startButton, startHint, startServer, startInvited,
            joinLabel, joinHint, joinButton, addressOnly, unreachable, certificate, notTendshelf, oldServer,
            httpWarningTitle, httpWarningBody, httpWarningPublic, httpWarningContinue, httpWarningCancel,
        ]
    }

    /// Reads `strings.xml` text into key and unescaped value. Only `<string>` elements count (plurals are not used yet).
    static func parse(_ xml: Data) -> [String: String] {
        let reader = Reader()
        let parser = XMLParser(data: xml)
        parser.delegate = reader
        parser.parse()
        return reader.values
    }

    /// Android's escapes: `\'`, `\"`, `\n`, `\t`, `\@`, `\?`, `\\` and `\uXXXX`.
    static func unescape(_ text: String) -> String {
        var out = ""
        var chars = text.makeIterator()
        while let ch = chars.next() {
            guard ch == "\\" else {
                out.append(ch)
                continue
            }
            guard let next = chars.next() else { break }
            switch next {
            case "n": out.append("\n")
            case "t": out.append("\t")
            case "u":
                var hex = ""
                for _ in 0..<4 {
                    if let h = chars.next() { hex.append(h) }
                }
                if let code = UInt32(hex, radix: 16), let scalar = Unicode.Scalar(code) {
                    out.unicodeScalars.append(scalar)
                }
            default: out.append(next)
            }
        }
        return out
    }

    /// `%1$s` and `%1$d` are replaced by the Nth argument, plainly.
    static func fill(_ text: String, _ args: [String]) -> String {
        var out = text
        for (i, arg) in args.enumerated() {
            out = out.replacingOccurrences(of: "%\(i + 1)$s", with: arg)
            out = out.replacingOccurrences(of: "%\(i + 1)$d", with: arg)
        }
        return out
    }

    static let shared: [String: String] = {
        guard let url = Bundle(for: Marker.self).url(forResource: "strings", withExtension: "xml", subdirectory: "ShellAssets"),
              let data = try? Data(contentsOf: url)
        else { return [:] }
        return parse(data)
    }()

    private final class Marker {}

    private final class Reader: NSObject, XMLParserDelegate {
        var values: [String: String] = [:]
        private var key: String?
        private var text = ""

        func parser(_ parser: XMLParser, didStartElement elementName: String, namespaceURI: String?, qualifiedName qName: String?, attributes attributeDict: [String: String] = [:]) {
            if elementName == "string" {
                key = attributeDict["name"]
                text = ""
            }
        }

        func parser(_ parser: XMLParser, foundCharacters string: String) {
            if key != nil { text += string }
        }

        func parser(_ parser: XMLParser, didEndElement elementName: String, namespaceURI: String?, qualifiedName qName: String?) {
            if elementName == "string", let k = key {
                values[k] = Words.unescape(text)
                key = nil
            }
        }
    }
}

/// The words for [key], with `%N$s` filled by [args]. A missing key returns the key and asserts in DEBUG.
func words(_ key: String, _ args: String...) -> String {
    guard let value = Words.shared[key] else {
        assertionFailure("missing word: \(key)")
        return key
    }
    return Words.fill(value, args)
}
