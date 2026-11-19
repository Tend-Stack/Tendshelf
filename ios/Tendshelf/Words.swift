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
        static let changeServerTitle = "change_server_title"
        static let changeServerBody = "change_server_body"
        static let changeServerContinue = "change_server_continue"
        static let linkOnly = "link_only"
        static let linkConnected = "link_connected"
        static let linkTitle = "link_title"
        static let linkBody = "link_body"
        static let linkConnect = "link_connect"
        static let expired = "expired"
        static let otherPerson = "other_person"
        static let signedOut = "signed_out"
        static let downloadNotAllowed = "download_not_allowed"
        static let downloadNoRoom = "download_no_room"
        static let downloadNothing = "download_nothing"
        static let downloadFailed = "download_failed"
        static let downloadsTitle = "downloads_title"
        static let downloadsLead = "downloads_lead"
        static let downloadsEmpty = "downloads_empty"
        static let downloadsEmptyHow = "downloads_empty_how"
        static let downloadsActive = "downloads_active"
        static let downloadsReady = "downloads_ready"
        static let downloadsPercent = "downloads_percent"
        static let downloadsWorking = "downloads_working"
        static let downloadsWaiting = "downloads_waiting"
        static let downloadsPausedRow = "downloads_paused_row"
        static let downloadsFailedRow = "downloads_failed_row"
        static let downloadsStaleRow = "downloads_stale_row"
        static let downloadsPlay = "downloads_play"
        static let downloadsRead = "downloads_read"
        static let downloadsPause = "downloads_pause"
        static let downloadsRemove = "downloads_remove"
        static let downloadsLibrary = "downloads_library"
        static let downloadsAutoRow = "downloads_auto_row"
        static let downloadsAutoWaiting = "downloads_auto_waiting"
        static let downloadsAutoNoRoom = "downloads_auto_no_room"
        static let downloadsPodcasts = "downloads_podcasts"
        static let downloadsPodcastsLead = "downloads_podcasts_lead"
        static let downloadsPodcastOnRow = "downloads_podcast_on_row"
        static let downloadsPodcastOffRow = "downloads_podcast_off_row"
        static let downloadsPodcastTurnOn = "downloads_podcast_turn_on"
        static let downloadsPodcastTurnOff = "downloads_podcast_turn_off"
        static let offlineTitle = "offline_title"
        static let offlineBody = "offline_body"
        static let offlineRetry = "offline_retry"

        static let all = [
            startTitle, startBody, startLabel, startButton, startHint, startServer, startInvited,
            joinLabel, joinHint, joinButton, addressOnly, unreachable, certificate, notTendshelf, oldServer,
            httpWarningTitle, httpWarningBody, httpWarningPublic, httpWarningContinue, httpWarningCancel,
            changeServerTitle, changeServerBody, changeServerContinue, linkOnly, linkConnected, linkTitle, linkBody,
            linkConnect, expired, otherPerson, signedOut,
            downloadNotAllowed, downloadNoRoom, downloadNothing, downloadFailed, downloadsTitle, downloadsLead, downloadsEmpty,
            downloadsEmptyHow, downloadsActive, downloadsReady, downloadsPercent, downloadsWorking, downloadsWaiting,
            downloadsPausedRow, downloadsFailedRow, downloadsStaleRow, downloadsPlay, downloadsRead, downloadsPause,
            downloadsRemove, downloadsLibrary, downloadsAutoRow, downloadsAutoWaiting, downloadsAutoNoRoom, downloadsPodcasts,
            downloadsPodcastsLead, downloadsPodcastOnRow, downloadsPodcastOffRow, downloadsPodcastTurnOn,
            downloadsPodcastTurnOff, offlineTitle, offlineBody, offlineRetry,
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
