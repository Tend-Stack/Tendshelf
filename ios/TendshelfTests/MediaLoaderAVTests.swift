import AVFoundation
import XCTest
@preconcurrency import TendshelfShared
@testable import Tendshelf

/// The loader against the real AVFoundation in the simulator: a synthetic file served by the stub plays through a handle.
@MainActor
final class MediaLoaderAVTests: XCTestCase {
    private let origin = Origin.companion.parse(input: "https://books.example")!

    private func play(handle: URL, loader: MediaLoader, mime: String, until seconds: Double, wait: Double = 20) async -> (playing: Bool, reached: Bool, status: String) {
        let asset = AVURLAsset(url: handle, options: mime.isEmpty ? nil : [AVURLAssetOverrideMIMETypeKey: mime])
        asset.resourceLoader.setDelegate(loader, queue: loader.queue)
        let item = AVPlayerItem(asset: asset)
        let player = AVPlayer(playerItem: item)
        player.play()
        let saw = Counter()
        let reached = await waitFor(wait) {
            if player.timeControlStatus == .playing { _ = saw.next() }
            return player.currentTime().seconds >= seconds
        }
        let status = "item \(item.status.rawValue) error \(String(describing: item.error)) control \(player.timeControlStatus.rawValue)"
        player.pause()
        player.replaceCurrentItem(with: nil)
        return (saw.peek > 0, reached, status)
    }

    func testWavServedThroughAHandlePlays() async {
        let wav = makeWav(seconds: 3)
        MediaStub.reset { req in
            let total = wav.count
            var from = 0
            var to = total - 1
            if let r = req.value(forHTTPHeaderField: "Range"), r.hasPrefix("bytes=") {
                let parts = r.dropFirst(6).split(separator: "-", omittingEmptySubsequences: false)
                from = Int(parts[0]) ?? 0
                if parts.count > 1, let e = Int(parts[1]) { to = min(e, total - 1) }
            }
            let body = wav.subdata(in: from..<(to + 1))
            return MediaScript(
                status: 206,
                headers: ["Content-Range": "bytes \(from)-\(to)/\(total)", "Content-Type": "audio/wav"],
                pieces: pieces(body, 16 * 1024))
        }
        let conn = Connection(origin: origin, token: "tok", deviceId: "d")
        let loader = MediaLoader(connection: { conn }, configuration: stubConfiguration())
        let handle = loader.register(url: "https://books.example/api/v1/stream/f1", origin: origin, mime: "audio/wav")
        let result = await play(handle: handle, loader: loader, mime: "audio/wav", until: 0.5)
        XCTAssertTrue(result.playing, result.status)
        XCTAssertTrue(result.reached, result.status)
        XCTAssertTrue(MediaStub.seen.allSatisfy { $0.value(forHTTPHeaderField: "Authorization") == "Bearer tok" })
    }
}
