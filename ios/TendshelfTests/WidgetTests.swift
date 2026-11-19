import XCTest
@preconcurrency import TendshelfShared
@testable import Tendshelf

/// The widget side of the app (decision 0048, I4b): the file it draws, what the feed writes, what the buttons do.
@MainActor
final class WidgetFeedTests: XCTestCase {
    private final class Sink {
        var snapshots: [WidgetSnapshot] = []
        var covers: [Data?] = []
        var clears = 0
        var reloads = 0
    }

    private let sink = Sink()
    private lazy var feed = WidgetFeed(writer: WidgetWriter(
        write: { [sink] in sink.snapshots.append($0) }, writeCover: { [sink] in sink.covers.append($0) },
        clear: { [sink] in sink.clears += 1 }, reload: { [sink] in sink.reloads += 1 }))

    private func input(
        id: String = "b1", title: String = "A Book", chapter: String = "Chapter 3", playing: Bool = false, show: String = "",
        live: Bool = false, back: Int32 = 30, forward: Int32 = 30
    ) -> WidgetInput {
        WidgetInput(
            signedIn: true, bookId: id, title: title, chapter: chapter, playing: playing, skipBackS: back, skipForwardS: forward,
            show: show, live: live)
    }

    private func jpeg(_ side: CGFloat) -> Data {
        let image = UIGraphicsImageRenderer(size: CGSize(width: side, height: side)).image { ctx in
            UIColor.red.setFill()
            ctx.fill(CGRect(x: 0, y: 0, width: side, height: side))
        }
        return image.jpegData(compressionQuality: 0.9)!
    }

    func testBookSmallHidesChapterAndSkipMediumShowsThem() {
        feed.update(input(), art: nil)
        let s = sink.snapshots[0]
        XCTAssertEqual(s.kind, "book")
        XCTAssertEqual(s.itemId, "b1")
        XCTAssertEqual(s.title, "A Book")
        XCTAssertEqual(s.chapter, "Chapter 3")
        XCTAssertEqual(s.small, WidgetLookOut(showCover: true, showChapter: false, showSkip: false))
        XCTAssertEqual(s.medium, WidgetLookOut(showCover: true, showChapter: true, showSkip: true))
        XCTAssertEqual(s.labels.back, "Back 30 seconds")
        XCTAssertEqual(s.labels.forward, "Forward 30 seconds")
        XCTAssertEqual(s.labels.open, "Open A Book")
        XCTAssertEqual(sink.reloads, 1)
    }

    func testSingularSecondLabel() {
        feed.update(input(back: 1, forward: 1), art: nil)
        XCTAssertEqual(sink.snapshots[0].labels.back, "Back 1 second")
    }

    func testEpisodeShowsItsShowBeneathTheTitle() {
        feed.update(input(id: "ep_1", title: "The Episode", chapter: "", show: "The Show"), art: nil)
        XCTAssertEqual(sink.snapshots[0].chapter, "The Show")
        XCTAssertTrue(sink.snapshots[0].medium.showChapter)
    }

    func testStationHasNoSkip() {
        feed.update(input(id: "rs_1", title: "Radio One", chapter: "", show: "Live", live: true), art: nil)
        XCTAssertFalse(sink.snapshots[0].medium.showSkip)
        XCTAssertEqual(sink.snapshots[0].chapter, "Live")
    }

    func testChapterlessBookRepeatsNoLine() {
        feed.update(input(title: "A Book", chapter: "A Book"), art: nil)
        XCTAssertFalse(sink.snapshots[0].medium.showChapter)
    }

    func testTwoEqualUpdatesWriteOnce() {
        feed.update(input(), art: nil)
        feed.update(input(), art: nil)
        XCTAssertEqual(sink.snapshots.count, 1)
        XCTAssertEqual(sink.reloads, 1)
        feed.update(input(playing: true), art: nil)
        XCTAssertEqual(sink.snapshots.count, 2)
    }

    func testNewItemRewritesTheCoverScaledTo360() throws {
        feed.update(input(id: "b1"), art: jpeg(1200))
        feed.update(input(id: "b1", playing: true), art: jpeg(1200))
        XCTAssertEqual(sink.covers.count, 1)
        let first = try XCTUnwrap(sink.covers[0])
        XCTAssertEqual(try XCTUnwrap(UIImage(data: first)).size.width, 360)
        feed.update(input(id: "b2"), art: nil)
        XCTAssertEqual(sink.covers.count, 2)
        XCTAssertNil(sink.covers[1])
    }

    func testACoverThatArrivesLaterIsWrittenAndReloads() {
        feed.update(input(), art: nil)
        feed.update(input(), art: jpeg(100))
        XCTAssertEqual(sink.covers.count, 2)
        XCTAssertNotNil(sink.covers[1])
        XCTAssertEqual(sink.reloads, 2)
    }

    func testSignedOutClearsAndWritesTheSignedOutLine() {
        feed.update(input(), art: jpeg(100))
        feed.signedOut()
        XCTAssertEqual(sink.clears, 1)
        let s = sink.snapshots.last!
        XCTAssertEqual(s.kind, "signedOut")
        XCTAssertEqual(s.itemId, "")
        XCTAssertEqual(s.title, "")
        XCTAssertEqual(s.line, WidgetWords.signedOut)
    }

    func testNoBookState() {
        feed.update(WidgetInput(signedIn: true, bookId: "", title: "", chapter: "", playing: false, skipBackS: 30, skipForwardS: 30, show: "", live: false), art: nil)
        XCTAssertEqual(sink.snapshots[0].kind, "noBook")
        XCTAssertEqual(sink.snapshots[0].line, WidgetWords.noBook)
    }
}

final class WidgetSnapshotTests: XCTestCase {
    private func folder() throws -> URL {
        let url = FileManager.default.temporaryDirectory.appendingPathComponent("widget-test-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: url, withIntermediateDirectories: true)
        addTeardownBlock { try? FileManager.default.removeItem(at: url) }
        return url
    }

    private var sample: WidgetSnapshot {
        WidgetSnapshot(
            kind: "book", itemId: "b1", title: "T", chapter: "C", playing: true,
            small: WidgetLookOut(showCover: true, showChapter: false, showSkip: false),
            medium: WidgetLookOut(showCover: true, showChapter: true, showSkip: true), line: "",
            labels: WidgetLabels(play: "Play", pause: "Pause", back: "Back", forward: "Forward", open: "Open T"))
    }

    func testRoundTrip() throws {
        let dir = try folder()
        XCTAssertTrue(WidgetStore.write(sample, folder: dir))
        XCTAssertEqual(WidgetStore.read(folder: dir), sample)
    }

    func testWrongVersionReadsNil() throws {
        let dir = try folder()
        var future = sample
        future.v = 2
        WidgetStore.write(future, folder: dir)
        XCTAssertNil(WidgetStore.read(folder: dir))
    }

    func testOversizedFileReadsNil() throws {
        let dir = try folder()
        var big = sample
        big.title = String(repeating: "x", count: 65 * 1024)
        WidgetStore.write(big, folder: dir)
        XCTAssertNil(WidgetStore.read(folder: dir))
    }

    func testCoverCapAndClear() throws {
        let dir = try folder()
        WidgetStore.writeCover(Data(repeating: 1, count: 1000), folder: dir)
        XCTAssertTrue(FileManager.default.fileExists(atPath: WidgetStore.coverURL(folder: dir)!.path))
        WidgetStore.writeCover(Data(repeating: 1, count: 513 * 1024), folder: dir)
        XCTAssertFalse(FileManager.default.fileExists(atPath: WidgetStore.coverURL(folder: dir)!.path))
        WidgetStore.writeCover(Data(repeating: 1, count: 10), folder: dir)
        WidgetStore.write(sample, folder: dir)
        WidgetStore.clear(folder: dir)
        XCTAssertNil(WidgetStore.read(folder: dir))
        XCTAssertFalse(FileManager.default.fileExists(atPath: WidgetStore.coverURL(folder: dir)!.path))
    }

    /// The ad hoc signed simulator build must hold the app group (plan I4b, first guess).
    func testTheAppGroupContainerIsAvailableToTheApp() {
        XCTAssertNotNil(WidgetStore.folder(), "the app group container is nil: the entitlement is not honoured")
    }

    func testMissingGroupIsNil() {
        XCTAssertNil(WidgetStore.folder(container: nil))
        XCTAssertNil(WidgetStore.read(folder: nil))
    }
}

@MainActor
final class WidgetCommandTests: XCTestCase {
    private var calls: [String] = []
    private var loadedNow = false
    private var playingNow = false
    private var connected: Origin? = Origin.companion.parse(input: "https://example.test")
    private var last: String? = "b9"

    private var commander: WidgetCommander {
        WidgetCommander(
            loaded: { self.loadedNow }, playing: { self.playingNow }, connectedOrigin: { self.connected }, lastItem: { self.last },
            pause: { self.calls.append("pause") }, play: { self.calls.append("play") },
            skip: { self.calls.append("skip \($0)") }, start: { self.calls.append("start \($0)") },
            load: { self.calls.append("load \($0.absoluteString)") })
    }

    func testToggleWithNothingLoadedStartsTheLastItem() {
        commander.run(.toggle)
        XCTAssertEqual(calls, ["start b9"])
    }

    func testToggleLoadedPausesOrPlays() {
        loadedNow = true
        playingNow = true
        commander.run(.toggle)
        playingNow = false
        commander.run(.toggle)
        XCTAssertEqual(calls, ["pause", "play"])
    }

    func testNothingLoadedAndNoLastItemOrSignedOutDoesNothing() {
        last = ""
        commander.run(.toggle)
        last = "b9"
        connected = nil
        commander.run(.toggle)
        commander.run(.forward)
        XCTAssertEqual(calls, [])
    }

    func testSkipsLoadedAndNotLoaded() {
        loadedNow = true
        commander.run(.back)
        commander.run(.forward)
        loadedNow = false
        commander.run(.back)
        XCTAssertEqual(calls, ["skip false", "skip true", "start b9"])
    }

    func testOpenWithAnInvalidIdDoesNothing() {
        commander.run(.open("../x"))
        commander.run(.open(""))
        connected = nil
        commander.run(.open("b1"))
        XCTAssertEqual(calls, [])
    }

    func testOpenLoadsTheRightPage() {
        commander.run(.open("b1"))
        commander.run(.open("rs_abc"))
        XCTAssertEqual(calls.count, 2)
        XCTAssertTrue(calls[0].contains("shell=ios") && calls[0].contains("%2Fbooks%2Fb1"), calls[0])
        XCTAssertTrue(calls[1].contains("%2Fradio"), calls[1])
        let episode = PodcastKt.isEpisodeId(id: "ep_1") ? "ep_1" : ""
        if !episode.isEmpty {
            calls = []
            commander.run(.open(episode))
            XCTAssertTrue(calls[0].contains("%2Fpodcasts%2Fepisodes%2Fep_1"), calls[0])
        }
    }
}

@MainActor
final class WidgetWordsTests: XCTestCase {
    func testWordsEqualAndroidsStringsXml() throws {
        let words = Words.shared
        XCTAssertEqual(WidgetWords.label, words["widget_label"])
        XCTAssertEqual(WidgetWords.description, words["widget_description"])
        XCTAssertEqual(WidgetWords.previewTitle, words["widget_preview_title"])
        XCTAssertEqual(WidgetWords.previewChapter, words["widget_preview_chapter"])
        XCTAssertEqual(WidgetWords.signedOut, words["widget_signed_out"])
        XCTAssertEqual(WidgetWords.noBook, words["widget_no_book"])
        XCTAssertEqual(WidgetWords.play, words["widget_play"])
        XCTAssertEqual(WidgetWords.pause, words["widget_pause"])
        XCTAssertEqual(WidgetWords.back, words["widget_back_preview"])
        XCTAssertEqual(WidgetWords.forward, words["widget_forward_preview"])
    }

    func testPlurals() {
        let xml = """
        <resources><plurals name="p"><item quantity="one">%1$d second</item><item quantity="other">%1$d seconds</item></plurals></resources>
        """
        XCTAssertEqual(Words.parsePlurals(Data(xml.utf8)), ["p": ["one": "%1$d second", "other": "%1$d seconds"]])
        XCTAssertEqual(wordsPlural("widget_back", 1), "Back 1 second")
        XCTAssertEqual(wordsPlural("widget_back", 15), "Back 15 seconds")
        XCTAssertEqual(wordsPlural("widget_forward", 0), "Forward 0 seconds")
    }
}
