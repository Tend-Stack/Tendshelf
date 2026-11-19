import XCTest
@testable import Tendshelf

@MainActor
final class WordsTests: XCTestCase {
    func testEveryKeyUsedInTheAppExists() {
        XCTAssertFalse(Words.shared.isEmpty, "strings.xml was not copied into the app")
        for key in Words.Key.all {
            XCTAssertNotNil(Words.shared[key], "missing word: \(key)")
        }
    }

    func testUnescape() {
        XCTAssertEqual(Words.unescape("Couldn\\'t say \\\"hi\\\"\\nnext \\@home \\\\ \\u00b7"), "Couldn't say \"hi\"\nnext @home \\ \u{b7}")
    }

    func testPositionalArguments() {
        XCTAssertEqual(Words.fill("From %1$s to %2$s, %3$d left", ["a", "b", "3"]), "From a to b, 3 left")
    }

    func testParseReadsStringElementsOnly() {
        let xml = """
        <resources>
          <string name="a">Don\\'t</string>
          <string name="b">x &amp; y</string>
          <plurals name="p"><item quantity="one">%d one</item></plurals>
        </resources>
        """
        XCTAssertEqual(Words.parse(Data(xml.utf8)), ["a": "Don't", "b": "x & y"])
    }

    func testIosWordsExistAndNoKeyIsInBothFiles() throws {
        for key in ["ios_scan_button", "ios_scan_hint", "ios_scan_cancel", "ios_scan_denied", "ios_scan_settings", "ios_scan_no_camera"] {
            XCTAssertNotNil(Words.shared[key], key)
        }
        XCTAssertEqual(words(Words.Key.iosScanButton), "Scan a code")
        XCTAssertTrue(words(Words.Key.iosScanDenied).contains("can't use the camera"))
        let common = Words.load("strings")
        let ios = Words.load("strings-ios")
        XCTAssertFalse(ios.isEmpty)
        XCTAssertTrue(Set(common.keys).isDisjoint(with: Set(ios.keys)), "a word is in both files")
        XCTAssertTrue(ios.keys.allSatisfy { $0.hasPrefix("ios_") })
    }

    func testFilledWordFromTheFile() {
        // The app's own start words come through the same path the pages use.
        XCTAssertEqual(words(Words.Key.startButton), "Connect")
        XCTAssertTrue(words(Words.Key.unreachable).contains("Couldn't reach"))
    }
}
