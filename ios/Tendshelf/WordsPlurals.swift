import Foundation

/// `<plurals>` of `strings.xml` (decision 0048, I4b): `one` and `other` only, as Android's English words use them.
extension Words {
    /// Reads every `<plurals name=...>` into name -> quantity -> unescaped text.
    static func parsePlurals(_ xml: Data) -> [String: [String: String]] {
        let reader = PluralReader()
        let parser = XMLParser(data: xml)
        parser.delegate = reader
        parser.parse()
        return reader.values
    }

    static let sharedPlurals: [String: [String: String]] = {
        guard let url = Bundle(for: PluralMarker.self).url(forResource: "strings", withExtension: "xml", subdirectory: "ShellAssets"),
              let data = try? Data(contentsOf: url)
        else { return [:] }
        return parsePlurals(data)
    }()

    private final class PluralMarker {}

    private final class PluralReader: NSObject, XMLParserDelegate {
        var values: [String: [String: String]] = [:]
        private var name: String?
        private var quantity: String?
        private var text = ""

        func parser(_ parser: XMLParser, didStartElement elementName: String, namespaceURI: String?, qualifiedName qName: String?, attributes attributeDict: [String: String] = [:]) {
            if elementName == "plurals" {
                name = attributeDict["name"]
            } else if elementName == "item", name != nil {
                quantity = attributeDict["quantity"]
                text = ""
            }
        }

        func parser(_ parser: XMLParser, foundCharacters string: String) {
            if quantity != nil { text += string }
        }

        func parser(_ parser: XMLParser, didEndElement elementName: String, namespaceURI: String?, qualifiedName qName: String?) {
            if elementName == "item", let n = name, let q = quantity {
                values[n, default: [:]][q] = Words.unescape(text)
                quantity = nil
            } else if elementName == "plurals" {
                name = nil
            }
        }
    }
}

/// The plural of [key] for [n]: `one` when n is 1, else `other`, with `%1$d` filled. A missing key returns the key and asserts in DEBUG.
func wordsPlural(_ key: String, _ n: Int) -> String {
    guard let forms = Words.sharedPlurals[key], let text = n == 1 ? forms["one"] ?? forms["other"] : forms["other"] else {
        assertionFailure("missing plural: \(key)")
        return key
    }
    return Words.fill(text, [String(n)])
}
