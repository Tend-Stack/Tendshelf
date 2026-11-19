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

    func testFilledWordFromTheFile() {
        // The app's own start words come through the same path the pages use.
        XCTAssertEqual(words(Words.Key.startButton), "Connect")
        XCTAssertTrue(words(Words.Key.unreachable).contains("Couldn't reach"))
    }
}
