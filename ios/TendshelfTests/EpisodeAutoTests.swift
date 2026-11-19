import XCTest
@preconcurrency import TendshelfShared
@testable import Tendshelf

/// I3c: "Download new episodes" of a followed show.
@MainActor
final class EpisodeAutoTests: XCTestCase {
    private let show = "ps_01SHOWAUTO01"

    private func ep(_ n: Int) -> String { "pe_01EPISODE00\(n)" }

    private func doc(_ id: String) -> String {
        "{\"id\":\"\(id)\",\"showId\":\"\(show)\",\"title\":\"Ep \(id)\",\"show\":\"Show\",\"artUrl\":\"\",\"durationMs\":1000,"
            + "\"audioUrl\":\"/x\",\"mime\":\"audio/mpeg\",\"bytes\":100,\"chaptersUrl\":\"\"}"
    }

    private func card(_ n: Int, played: Bool = false) -> EpisodeCard {
        EpisodeCard(
            id: ep(n), showId: show, title: "Ep \(n)", show: "Show", durationMs: 1000, publishedAt: Int64(n * 1000), positionMs: 0,
            played: played, favourite: false)
    }

    /// A show with three episodes, the newest first on the server's side; [keep] is its "latest N".
    private func rig(keep: Int32 = 2, played: Set<Int> = []) -> DownloadsRig {
        let r = DownloadsRig(self)
        r.api.shows = [ShowCard(id: show, title: "Show", author: "", hasArt: false, newCount: 0, keepLatest: keep)]
        r.api.episodesOf[show] = (1...3).map { card($0, played: played.contains($0)) }
        for n in 1...3 { r.api.episodeDocs[ep(n)] = doc(ep(n)) }
        return r
    }

    private func finish(_ r: DownloadsRig, _ id: String) {
        r.arrive(id, size: 100, book: id)
    }

    func testTurningAShowOnFetchesItsNewestEpisodesOnWifiWithTheAutomaticRules() async {
        let r = rig()
        await r.downloads.episodes.setShow(show, on: true)
        XCTAssertEqual(Set(r.store.all().map(\.bookId)), [ep(3), ep(2)], "the latest 2, not the oldest")
        XCTAssertEqual(r.transport.started.count, 2)
        for s in r.transport.started {
            XCTAssertFalse(s.request.allowsExpensiveNetworkAccess)
            XCTAssertFalse(s.request.allowsConstrainedNetworkAccess)
            XCTAssertEqual(BackgroundTransport.priority(for: s.request), URLSessionTask.lowPriority)
            XCTAssertEqual(s.request.value(forHTTPHeaderField: "Authorization"), "Bearer tok-1")
            XCTAssertNil(s.request.value(forHTTPHeaderField: "Cookie"))
        }
        XCTAssertEqual(r.downloads.episodes.marks(), [ep(3), ep(2)])
        XCTAssertTrue(r.downloads.isAuto(ep(3)))
        XCTAssertEqual(r.downloads.episodes.showRows().map(\.on), [true])
    }

    func testAMeteredNetworkFetchesNothing() async {
        let r = rig()
        r.unmetered = false
        await r.downloads.episodes.setShow(show, on: true)
        XCTAssertTrue(r.store.all().isEmpty)
        XCTAssertTrue(r.transport.started.isEmpty)
    }

    func testPlayedAndDeclinedEpisodesAreNeverFetched() async {
        let r = rig(keep: 3, played: [3])
        r.downloads.episodes.decline(ep(1))
        await r.downloads.episodes.setShow(show, on: true)
        XCTAssertEqual(Set(r.store.all().map(\.bookId)), [ep(2)])
    }

    func testRemovingAnEpisodeByHandDeclinesItAndKeepsItsSlot() async {
        let r = rig()
        await r.downloads.episodes.setShow(show, on: true)
        await r.downloads.remove(ep(3), byPerson: true)
        XCTAssertEqual(r.downloads.episodes.declined(), [ep(3)])
        r.clock += 2 * 3_600_000
        await r.downloads.episodes.sync()
        XCTAssertNil(r.store.get(id: ep(3)))
        XCTAssertNil(r.store.get(id: ep(1)), "an older episode is not pulled in behind it")
    }

    func testAPlayedAutomaticEpisodeIsRemovedButAPersonsOwnIsNot() async {
        let r = rig(keep: 3)
        await r.downloads.add(ep(1)) // the person's own
        await r.downloads.episodes.setShow(show, on: true)
        XCTAssertFalse(r.downloads.isAuto(ep(1)))
        for n in 1...3 { finish(r, ep(n)) }
        r.api.episodesOf[show] = (1...3).map { card($0, played: true) }
        r.clock += 2 * 3_600_000
        await r.downloads.episodes.sync()
        XCTAssertNotNil(r.store.get(id: ep(1)), "the person's own stays")
        XCTAssertNil(r.store.get(id: ep(2)))
        XCTAssertNil(r.store.get(id: ep(3)))
    }

    func testThePlayingEpisodeIsNeverRemovedByARule() async {
        let r = rig(keep: 3)
        await r.downloads.episodes.setShow(show, on: true)
        for n in 1...3 { finish(r, ep(n)) }
        r.downloads.playingNow = ep(2)
        r.api.episodesOf[show] = (1...3).map { card($0, played: true) }
        r.clock += 2 * 3_600_000
        await r.downloads.episodes.sync()
        XCTAssertNotNil(r.store.get(id: ep(2)))
        XCTAssertNil(r.store.get(id: ep(3)))
    }

    func testSwitchingAShowOffDropsWhatIsUnfinishedAndKeepsWhatIsWholeAsThePersonsOwn() async {
        let r = rig()
        await r.downloads.episodes.setShow(show, on: true)
        finish(r, ep(3))
        await r.downloads.episodes.setShow(show, on: false)
        XCTAssertNil(r.store.get(id: ep(2)), "unfinished goes")
        XCTAssertNotNil(r.store.get(id: ep(3)), "whole stays")
        XCTAssertFalse(r.downloads.isAuto(ep(3)))
        XCTAssertFalse(r.downloads.episodes.anyOn())
    }

    func testTheQuotaKeepsAnEpisodeOutWhenTheRoomIsShort() async {
        let r = rig()
        r.freeBytes = 1_000_000 // far below the 2 GiB an automatic download keeps free
        await r.downloads.episodes.setShow(show, on: true)
        XCTAssertTrue(r.store.all().isEmpty)
        XCTAssertTrue(r.downloads.autoNoRoomNoted())
    }

    func testThePressedDownloadMakesAnAutomaticEpisodeThePersonsOwn() async {
        let r = rig()
        await r.downloads.episodes.setShow(show, on: true)
        r.api.episode = doc(ep(3))
        await r.downloads.add(ep(3))
        XCTAssertFalse(r.downloads.isAuto(ep(3)))
        XCTAssertTrue(r.transport.cancelled.contains(Downloads.key(ep(3), ep(3))))
        let last = r.transport.started.last!
        XCTAssertEqual(last.key, Downloads.key(ep(3), ep(3)))
        XCTAssertTrue(last.request.allowsExpensiveNetworkAccess)
        XCTAssertNil(last.resume)
    }

    func testTheShowsCheckIsAtMostHourlyAndAPlayedMarkComesFromTheServer() async {
        let r = rig()
        await r.downloads.episodes.setShow(show, on: true)
        let calls = r.transport.started.count
        r.clock += 10 * 60_000
        r.api.episodesOf[show] = (1...3).map { card($0, played: $0 == 3) }
        await r.downloads.episodes.syncIfDue()
        XCTAssertNotEqual(r.store.get(id: ep(3))?.played, true, "within the hour nothing is asked")
        r.clock += 2 * 3_600_000
        await r.downloads.episodes.syncIfDue()
        XCTAssertNil(r.store.get(id: ep(3)), "played on the server, so the rule removes it")
        XCTAssertGreaterThanOrEqual(r.transport.started.count, calls)
    }

    func testRefreshShowsListsFollowedShowsForThePage() async {
        let r = rig()
        XCTAssertTrue(r.downloads.episodes.showRows().isEmpty)
        let changed = await r.downloads.episodes.refreshShows()
        XCTAssertTrue(changed)
        XCTAssertEqual(r.downloads.episodes.showRows().map(\.id), [show])
        XCTAssertEqual(r.downloads.episodes.showRows().first?.keepLatest, 2)
        r.api.showsError = ApiError(status: 0)
        let again = await r.downloads.episodes.refreshShows()
        XCTAssertFalse(again, "a server that does not answer changes nothing")
        XCTAssertEqual(r.downloads.episodes.showRows().map(\.id), [show])
    }

    func testAnEndedEpisodeIsMarkedPlayed() async {
        let r = rig()
        await r.downloads.add(ep(1))
        finish(r, ep(1))
        r.downloads.markPlayed(ep(1))
        XCTAssertEqual(r.store.get(id: ep(1))?.played, true)
    }
}
