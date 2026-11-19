import XCTest
@preconcurrency import TendshelfShared
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

    // MARK: Downloads and offline (I3b)

    private func input(rows: [DownloadRow] = [], offline: Bool = false) -> DownloadsInput {
        DownloadsInput(
            rows: rows, shows: [], playingId: "", playing: false, unmetered: true, autoNoRoom: false, offline: offline,
            offlineAddress: "https://books.example", homeUrl: "https://books.example/", readBase: "")
    }

    private func row(_ id: String, state: OfflineState, pct: Int32 = 100) -> DownloadRow {
        let book = OfflineBook(
            bookId: id, userId: "u", title: "Odd <Book>", author: "Ann", durationMs: 1000, sizeBytes: 10, state: state, placeMs: 0,
            placeAt: 0, kind: OfflineKt.KIND_BOOK, showId: "", publishedAt: 0, played: false)
        return DownloadRow(book: book, view: DlView(state: state.name.lowercased(), pct: pct, known: true), hasCover: true, readable: false, hasAudio: true, auto: false)
    }

    func testDownloadsPageEmptyAndWithARow() {
        let pages = BundledPages()
        XCTAssertEqual(pages.reply(for: "/downloads").status, 404, "no engine yet, no page")
        pages.downloadsInput = { [unowned self] offline in input(rows: [], offline: offline) }
        let empty = text(pages.reply(for: "/downloads"))
        XCTAssertEqual(pages.reply(for: "/downloads").status, 200)
        XCTAssertTrue(empty.contains(words(Words.Key.downloadsEmpty)))
        XCTAssertFalse(empty.contains("{{"))
        XCTAssertFalse(empty.contains(words(Words.Key.offlineRetry)))

        pages.downloadsInput = { [unowned self] offline in input(rows: [row("bk_1", state: .ready)], offline: offline) }
        let full = text(pages.reply(for: "/downloads"))
        XCTAssertTrue(full.contains("Odd &lt;Book&gt;"))
        XCTAssertFalse(full.contains("Odd <Book>"))
        XCTAssertTrue(full.contains("/covers/bk_1"))
        XCTAssertTrue(full.contains("/play?id=bk_1"))
        XCTAssertTrue(full.contains("/remove?id=bk_1"))
    }

    func testOfflinePageHasTryAgainAndTheList() {
        let pages = BundledPages()
        pages.downloadsInput = { [unowned self] offline in input(rows: [row("bk_1", state: .ready)], offline: offline) }
        let html = text(pages.reply(for: "/offline"))
        XCTAssertTrue(html.contains(words(Words.Key.offlineRetry)))
        XCTAssertTrue(html.contains("https://books.example"))
        XCTAssertTrue(html.contains("Odd &lt;Book&gt;"))
    }

    func testCoversAreServedFromStoredFilesOnly() {
        let pages = BundledPages()
        pages.coverData = { $0 == "bk_1" ? Data([1, 2, 3]) : nil }
        let ok = pages.reply(for: "/covers/bk_1")
        XCTAssertEqual(ok.status, 200)
        XCTAssertEqual(ok.mime, "image/jpeg")
        XCTAssertEqual(ok.body, Data([1, 2, 3]))
        for path in ["/covers/x", "/covers/../a", "/covers/", "/covers/bk_1/extra", "/covers/a%2Fb"] {
            XCTAssertEqual(pages.reply(for: path).status, 404, path)
        }
    }

    func testDownloadsAddresses() {
        XCTAssertEqual(BundledPages.downloadsUrl.absoluteString, "tsbundle://app/downloads")
        XCTAssertEqual(BundledPages.offlineUrl.absoluteString, "tsbundle://app/offline")
    }
}
