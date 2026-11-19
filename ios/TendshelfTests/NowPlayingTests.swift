import MediaPlayer
import XCTest
@preconcurrency import TendshelfShared
@testable import Tendshelf

@MainActor
final class NowPlayingTests: XCTestCase {
    private func snapshot(playing: Bool, pos: Int64 = 0, dur: Int64 = 600_000, speed: Double = 1.0) -> PlayerSnapshot {
        PlayerSnapshot(
            bookId: "b1", positionMs: pos, durationMs: dur, playing: playing, buffering: false, ended: false, speed: speed,
            chapterIdx: 0, error: "", ask: nil, rewoundS: 0, rewoundSeq: 0, sleep: SleepInfo.companion.OFF)
    }

    private var chapters: [Chapter] {
        [Chapter(idx: 0, startMs: 0, endMs: 100_000, title: "One"), Chapter(idx: 1, startMs: 100_000, endMs: 600_000, title: "Two")]
    }

    private func lines(live: Bool = false, episode: String? = nil, pos: Int64 = 0, now: String = "") -> NowLines {
        NowLinesKt.nowLines(
            live: live, title: "Book", author: "An Author", episode: episode, chapters: chapters, positionMs: pos,
            liveNow: now, liveWord: "Live")
    }

    func testBookShowsChapterBookAuthorAndBookTime() {
        let info = NowPlaying.info(snapshot(playing: true, pos: 30_000, speed: 1.25), lines: lines(pos: 30_000), live: false, art: nil)
        XCTAssertEqual(info[MPMediaItemPropertyTitle] as? String, "One")
        XCTAssertEqual(info[MPMediaItemPropertyAlbumTitle] as? String, "Book")
        XCTAssertEqual(info[MPMediaItemPropertyArtist] as? String, "An Author")
        XCTAssertEqual(info[MPNowPlayingInfoPropertyElapsedPlaybackTime] as? Double, 30)
        XCTAssertEqual(info[MPMediaItemPropertyPlaybackDuration] as? Double, 600)
        XCTAssertEqual(info[MPNowPlayingInfoPropertyPlaybackRate] as? Double, 1.25)
        XCTAssertEqual(info[MPNowPlayingInfoPropertyDefaultPlaybackRate] as? Double, 1.25)
        XCTAssertEqual(info[MPNowPlayingInfoPropertyIsLiveStream] as? Bool, false)
    }

    func testChapterChangeChangesTheTitle() {
        let info = NowPlaying.info(snapshot(playing: true, pos: 150_000), lines: lines(pos: 150_000), live: false, art: nil)
        XCTAssertEqual(info[MPMediaItemPropertyTitle] as? String, "Two")
        XCTAssertEqual(info[MPNowPlayingInfoPropertyElapsedPlaybackTime] as? Double, 150)
    }

    func testRateIsZeroWhenPaused() {
        let info = NowPlaying.info(snapshot(playing: false, pos: 1000, speed: 1.5), lines: lines(), live: false, art: nil)
        XCTAssertEqual(info[MPNowPlayingInfoPropertyPlaybackRate] as? Double, 0)
        XCTAssertEqual(info[MPNowPlayingInfoPropertyDefaultPlaybackRate] as? Double, 1.5)
    }

    func testEpisodeShowsEpisodeTitleAndShow() {
        let info = NowPlaying.info(snapshot(playing: true), lines: lines(episode: "The Episode"), live: false, art: nil)
        XCTAssertEqual(info[MPMediaItemPropertyTitle] as? String, "The Episode")
        XCTAssertEqual(info[MPMediaItemPropertyArtist] as? String, "Book")
    }

    func testStationIsLiveWithNoTimeAndTheTitleLine() {
        let info = NowPlaying.info(snapshot(playing: true, dur: 0), lines: lines(live: true, now: "A Song"), live: true, art: nil)
        XCTAssertEqual(info[MPNowPlayingInfoPropertyIsLiveStream] as? Bool, true)
        XCTAssertNil(info[MPMediaItemPropertyPlaybackDuration])
        XCTAssertNil(info[MPNowPlayingInfoPropertyElapsedPlaybackTime])
        XCTAssertEqual(info[MPMediaItemPropertyArtist] as? String, "A Song")
        let quiet = NowPlaying.info(snapshot(playing: true, dur: 0), lines: lines(live: true), live: true, art: nil)
        XCTAssertEqual(quiet[MPMediaItemPropertyArtist] as? String, "Live")
    }

    func testArtworkIsIncludedWhenGiven() {
        let image = UIGraphicsImageRenderer(size: CGSize(width: 4, height: 4)).image { _ in }
        let info = NowPlaying.info(snapshot(playing: true), lines: lines(), live: false, art: image)
        XCTAssertNotNil(info[MPMediaItemPropertyArtwork])
    }
}
