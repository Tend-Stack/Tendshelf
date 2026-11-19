import XCTest
@preconcurrency import TendshelfShared
@testable import Tendshelf

/// I3c: the next book on its own, the remembered book for an offline hand-off, the queue tidy, and the daily check.
@MainActor
final class AutoDownloadTests: XCTestCase {
    private let next = "bk_01NEXTBOOK00"

    private func nextManifest(download: Bool = true) -> String {
        DownloadsRig.manifestJson(download: download, files: [("f_01NEXTONE", 100)]).replacingOccurrences(of: DownloadsRig.book, with: next)
    }

    /// A rig whose switch is on and whose server names [next] after the book that ended.
    private func rig(autoOn: Bool = true) -> DownloadsRig {
        let r = DownloadsRig(self)
        r.api.peek = next
        r.api.manifests[next] = nextManifest()
        if autoOn { r.downloads.setAutoDownload(true) }
        return r
    }

    private func finish(_ r: DownloadsRig, _ id: String, _ cache: String) {
        r.arrive(cache, size: 100, book: id)
    }

    func testTheSwitchIsKeptAndOffByDefault() {
        let r = DownloadsRig(self)
        XCTAssertFalse(r.downloads.keptSettings().autoDownload)
        r.downloads.setAutoDownload(true)
        XCTAssertTrue(r.downloads.keptSettings().autoDownload)
        XCTAssertEqual(r.downloads.keptSettings().speed, 1.0)
    }

    func testAnAutomaticBookNeverMovesAByteOnAnExpensiveNetworkAndIsLowPriority() async {
        let r = rig()
        await r.downloads.autoFetch("bk_01ENDED")
        XCTAssertEqual(r.transport.started.count, 1)
        let request = r.transport.started[0].request
        XCTAssertFalse(request.allowsExpensiveNetworkAccess)
        XCTAssertFalse(request.allowsConstrainedNetworkAccess)
        XCTAssertEqual(BackgroundTransport.priority(for: request), URLSessionTask.lowPriority)
        XCTAssertEqual(request.value(forHTTPHeaderField: "Authorization"), "Bearer tok-1")
        XCTAssertTrue(r.downloads.isAuto(next))
        XCTAssertTrue(r.store.files(next).allSatisfy(\.auto))
        XCTAssertEqual(r.store.get(id: next)?.state.name, "QUEUED", "held for Wi-Fi means queued")
        XCTAssertEqual(r.defaults.string(forKey: Downloads.autoNextKey), "bk_01ENDED\n\(next)")
        let own = DownloadsRig(self)
        await own.downloads.add(DownloadsRig.book)
        XCTAssertEqual(BackgroundTransport.priority(for: own.transport.started[0].request), URLSessionTask.highPriority)
    }

    func testNothingIsAskedOrMovedOnAMeteredNetworkOrWithTheSwitchOff() async {
        let r = rig()
        r.unmetered = false
        await r.downloads.autoFetch("bk_01ENDED")
        XCTAssertTrue(r.transport.started.isEmpty)
        XCTAssertFalse(r.api.calls.contains { $0.hasPrefix("peek") })
        let off = rig(autoOn: false)
        await off.downloads.autoFetch("bk_01ENDED")
        XCTAssertTrue(off.transport.started.isEmpty)
        XCTAssertFalse(off.api.calls.contains { $0.hasPrefix("peek") })
    }

    func testTheTickAsksOnlyNearTheEndAndAtMostOncePerAsk() async {
        let r = rig()
        var now: Int64 = 1_000
        let auto = AutoNext(r.downloads, clock: { now })
        let hour: Int64 = 3_600_000
        auto.tick(book: "bk_01ENDED", positionMs: 0, durationMs: 10 * hour, playing: true)
        await auto.lastWork?.value
        XCTAssertFalse(r.api.calls.contains { $0.hasPrefix("peek") }, "far from the end")
        now += 61_000
        auto.tick(book: "bk_01ENDED", positionMs: 9 * hour + 1, durationMs: 10 * hour, playing: false)
        await auto.lastWork?.value
        XCTAssertFalse(r.api.calls.contains { $0.hasPrefix("peek") }, "paused")
        auto.tick(book: "bk_01ENDED", positionMs: 9 * hour + 1, durationMs: 10 * hour, playing: true)
        await auto.lastWork?.value
        XCTAssertEqual(r.api.calls.filter { $0.hasPrefix("peek") }.count, 1)
        now += 10_000
        auto.tick(book: "bk_01ENDED", positionMs: 9 * hour + 2, durationMs: 10 * hour, playing: true)
        now += 120_000
        auto.tick(book: "bk_01ENDED", positionMs: 9 * hour + 3, durationMs: 10 * hour, playing: true)
        await auto.lastWork?.value
        XCTAssertEqual(r.api.calls.filter { $0.hasPrefix("peek") }.count, 1, "asked again only after 15 minutes")
    }

    func testAMeteredNearEndIsRememberedAndFetchedAtTheNextForeground() async {
        let r = rig()
        r.unmetered = false
        let auto = AutoNext(r.downloads, clock: { 5_000 })
        auto.tick(book: "bk_01ENDED", positionMs: 9, durationMs: 10, playing: true)
        await auto.lastWork?.value
        XCTAssertEqual(r.defaults.string(forKey: Downloads.autoWantKey), "bk_01ENDED")
        r.unmetered = true
        await r.downloads.foreground()
        XCTAssertEqual(r.transport.started.count, 1)
        XCTAssertNil(r.defaults.string(forKey: Downloads.autoWantKey))
    }

    func testTheSwitchOffRemovesUnfinishedAutomaticBooksOnly() async {
        let r = rig()
        await r.downloads.autoFetch("bk_01ENDED")
        // A person's own, unfinished, is never touched by a rule.
        await r.downloads.add(DownloadsRig.book)
        r.downloads.setAutoDownload(false)
        await r.downloads.autoSync()
        XCTAssertNil(r.store.get(id: next))
        XCTAssertNotNil(r.store.get(id: DownloadsRig.book))
        XCTAssertFalse(r.downloads.isAuto(next))
    }

    func testAFinishedAutomaticBookStaysWhenTheSwitchGoesOff() async {
        let r = rig()
        await r.downloads.autoFetch("bk_01ENDED")
        finish(r, next, "f_01NEXTONE")
        XCTAssertEqual(r.store.get(id: next)?.state.name, "READY")
        r.downloads.setAutoDownload(false)
        await r.downloads.autoSync()
        XCTAssertNotNil(r.store.get(id: next))
    }

    func testTheQuotaStopsAnAutomaticBook() async {
        let r = rig()
        r.freeBytes = 150 // the spare room of an automatic book is 2 GiB
        await r.downloads.autoFetch("bk_01ENDED")
        XCTAssertTrue(r.transport.started.isEmpty)
        XCTAssertTrue(r.downloads.autoNoRoomNoted())
        XCTAssertNil(r.store.get(id: next))
    }

    func testThePressedDownloadMakesAnAutomaticBookThePersonsOwn() async {
        let r = rig()
        await r.downloads.autoFetch("bk_01ENDED")
        r.api.manifest = nextManifest()
        await r.downloads.add(next)
        XCTAssertFalse(r.downloads.isAuto(next))
        XCTAssertEqual(r.transport.cancelled, [Downloads.key(next, "f_01NEXTONE")])
        XCTAssertEqual(r.transport.started.count, 2, "started again")
        XCTAssertTrue(r.transport.started[1].request.allowsExpensiveNetworkAccess)
        XCTAssertNil(r.transport.started[1].resume)
        XCTAssertTrue(r.store.files(next).allSatisfy { !$0.auto })
    }

    // MARK: Offline hand-off

    func testTheRememberedBookIsOfferedOnceAndOnlyWhenWhole() async {
        let r = rig()
        await r.downloads.autoFetch("bk_01ENDED")
        XCTAssertNil(r.downloads.offlineNext("bk_01ENDED"), "not whole yet")
        r.defaults.set("bk_01ENDED\n\(next)", forKey: Downloads.autoNextKey)
        finish(r, next, "f_01NEXTONE")
        XCTAssertNil(r.downloads.offlineNext("bk_01OTHER"), "remembered for another book")
        XCTAssertEqual(r.downloads.offlineNext("bk_01ENDED"), next)
        XCTAssertNil(r.downloads.offlineNext("bk_01ENDED"), "cleared when used")
    }

    func testTheQueueIsTidiedOnceAConnectionWorks() async {
        let r = rig()
        r.downloads.autoDrop(next)
        r.api.removeStatus = 500
        await r.downloads.tidyQueue()
        XCTAssertEqual(r.defaults.string(forKey: Downloads.autoDropKey), next, "tried again later")
        r.api.removeStatus = 204
        await r.downloads.tidyQueue()
        XCTAssertNil(r.defaults.string(forKey: Downloads.autoDropKey))
        r.downloads.autoDrop(next)
        r.api.removeStatus = 404
        await r.downloads.tidyQueue()
        XCTAssertNil(r.defaults.string(forKey: Downloads.autoDropKey), "gone already")
        XCTAssertEqual(r.api.removed, [next, next, next])
    }

    func testAWipeTakesEveryAutomaticKeyWithIt() async {
        let r = rig()
        await r.downloads.autoFetch("bk_01ENDED")
        await r.downloads.wipe().value
        XCTAssertTrue(r.defaults.dictionaryRepresentation().keys.filter { $0.hasPrefix("offline.") }.allSatisfy { $0 == Downloads.wipePendingKey })
    }

    // MARK: The daily check

    private func ready(_ r: DownloadsRig) async {
        await r.downloads.add(DownloadsRig.book)
        r.arrive("f_01FILEONE", size: 100)
        r.arrive("f_01FILETWO", size: 200)
        XCTAssertEqual(r.store.get(id: DownloadsRig.book)?.state.name, "READY")
    }

    private let day: Int64 = 86_400_000

    func testDailyCheckSameFilesSameWordsOnlyNotesTheCheck() async {
        let r = DownloadsRig(self)
        await ready(r)
        r.clock = day + 5
        await r.downloads.dailyCheck()
        XCTAssertEqual(r.store.checkedAt(DownloadsRig.book), day + 5)
        XCTAssertEqual(r.store.get(id: DownloadsRig.book)?.state.name, "READY")
        let calls = r.api.calls.count
        r.clock = day + 10
        await r.downloads.dailyCheck()
        XCTAssertEqual(r.api.calls.count, calls, "at most once a day")
    }

    func testDailyCheckNewWordsUpdateTheStoredAnswerButOldFilesStayReady() async {
        let r = DownloadsRig(self)
        await ready(r)
        r.api.manifest = r.api.manifest.replacingOccurrences(of: "\"title\":\"Parts\"", with: "\"title\":\"Parts, new edition\"")
        r.clock = day + 5
        await r.downloads.dailyCheck()
        XCTAssertEqual(r.store.get(id: DownloadsRig.book)?.state.name, "READY")
        XCTAssertTrue(r.store.manifest(id: DownloadsRig.book)?.contains("new edition") == true)
    }

    func testDailyCheckChangedFilesMakeTheBookStale() async {
        let r = DownloadsRig(self)
        await ready(r)
        r.api.manifest = DownloadsRig.manifestJson(download: true, files: [("f_01FILEONE", 100), ("f_01FILETWO", 999)])
        r.clock = day + 5
        await r.downloads.dailyCheck()
        XCTAssertEqual(r.store.get(id: DownloadsRig.book)?.state.name, "STALE")
        XCTAssertTrue(r.downloads.playable(DownloadsRig.book), "stale still plays")
    }

    func testDailyCheck404RemovesTheBookAndAFaultChangesNothing() async {
        let r = DownloadsRig(self)
        await ready(r)
        r.clock = day + 5
        r.api.error = ApiError(status: 500)
        await r.downloads.dailyCheck()
        XCTAssertNotNil(r.store.get(id: DownloadsRig.book))
        r.api.error = ApiError(status: 404)
        await r.downloads.dailyCheck()
        XCTAssertNil(r.store.get(id: DownloadsRig.book))
    }
}
