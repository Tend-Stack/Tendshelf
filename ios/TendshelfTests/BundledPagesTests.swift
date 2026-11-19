import XCTest
@testable import Tendshelf

@MainActor
final class BundledPagesTests: XCTestCase {
    private func text(_ r: BundledReply) -> String { String(decoding: r.body, as: UTF8.self) }

    func testEscapingOfTheAddressAndNote() throws {
        let pages = BundledPages()
        pages.state = StartState(address: "a\"<b>&'c", note: "n\"<&'")
        let r = pages.reply(for: "/server")
        XCTAssertEqual(r.status, 200)
        let html = text(r)
        XCTAssertTrue(html.contains("value=\"a&quot;&lt;b&gt;&amp;&#39;c\""))
        XCTAssertTrue(html.contains("n&quot;&lt;&amp;&#39;"))
        XCTAssertFalse(html.contains("<b>"))
        XCTAssertFalse(html.contains("{{"))
    }

    func testJoinPageNoteIsEscaped() {
        let pages = BundledPages()
        pages.joinState = StartState(note: "<i>x</i>")
        let html = text(pages.reply(for: "/join"))
        XCTAssertTrue(html.contains("&lt;i&gt;x&lt;/i&gt;"))
        XCTAssertFalse(html.contains("{{"))
    }

    func testStartPicksChooseWithoutAddressOrNote() {
        let pages = BundledPages()
        let choose = text(pages.reply(for: "/start"))
        XCTAssertTrue(choose.contains("href=\"/server\""))
        XCTAssertFalse(choose.contains("<form"))
        XCTAssertFalse(choose.contains("{{"))

        pages.state = StartState(address: "books.example")
        XCTAssertTrue(text(pages.reply(for: "/start")).contains("<form"))
        pages.state = StartState(address: "", note: "Nope")
        XCTAssertTrue(text(pages.reply(for: "/start")).contains("Nope"))
    }

    func testThemeFollowsTheTraitCollection() {
        let pages = BundledPages()
        pages.dark = { true }
        XCTAssertTrue(text(pages.reply(for: "/start")).contains("data-theme=\"dark\""))
        pages.dark = { false }
        XCTAssertTrue(text(pages.reply(for: "/start")).contains("data-theme=\"light\""))
    }

    func testAssets() {
        let pages = BundledPages()
        let css = pages.reply(for: "/assets/tokens.css")
        XCTAssertEqual(css.status, 200)
        XCTAssertTrue(css.mime.hasPrefix("text/css"))
        XCTAssertEqual(pages.reply(for: "/assets/start.css").status, 200)
        XCTAssertTrue(text(pages.reply(for: "/assets/fonts.css")).contains("/assets/fonts/"))
    }

    func testUnknownAssetIs404() {
        let pages = BundledPages()
        for path in ["/assets/nope.css", "/assets/strings.xml", "/assets/start.html", "/assets/../strings.xml",
                     "/assets/fonts/nope.woff2", "/assets/", "/nothing", "/connect"] {
            XCTAssertEqual(pages.reply(for: path).status, 404, path)
        }
    }
}
