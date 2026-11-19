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
        let handle = loader.register(url: "https://books.example/api/v1/stream/f1", origin: origin, mime: "audio/wav", live: false)
        let result = await play(handle: handle, loader: loader, mime: "audio/wav", until: 0.5)
        XCTAssertTrue(result.playing, result.status)
        XCTAssertTrue(result.reached, result.status)
        XCTAssertTrue(MediaStub.seen.allSatisfy { $0.value(forHTTPHeaderField: "Authorization") == "Bearer tok" })
    }

    /// The live spike (I2 plan, step 11): an endless-looking ADTS stream with no length, in small pieces, plays.
    func testLiveAdtsStreamWithNoLengthPlays() async throws {
        let aac = try makeAdts(seconds: 10)
        MediaStub.reset { _ in
            MediaScript(status: 200, headers: ["Content-Type": "audio/aac"], pieces: pieces(aac, 4096), interval: 0.05)
        }
        let conn = Connection(origin: origin, token: "tok", deviceId: "d")
        let loader = MediaLoader(connection: { conn }, configuration: stubConfiguration())
        let handle = loader.register(url: "https://books.example/api/v1/radio/live/rs_1", origin: origin, mime: "audio/aac", live: true)
        let result = await play(handle: handle, loader: loader, mime: "audio/aac", until: 0.5)
        XCTAssertTrue(result.playing, result.status)
        XCTAssertTrue(result.reached, result.status)
    }

    /// A live MP3 station with no mime override (the type comes from the answer's Content-Type), as the player opens one.
    func testLiveMp3StreamWithNoOverridePlays() async {
        let tone = Data(base64Encoded: Self.toneMp3)!
        var stream = Data()
        for _ in 0..<10 { stream.append(tone) }
        let bytes = stream
        MediaStub.reset { _ in
            MediaScript(status: 200, headers: ["Content-Type": "audio/mpeg"], pieces: pieces(bytes, 1024), interval: 0.02)
        }
        let conn = Connection(origin: origin, token: "tok", deviceId: "d")
        let loader = MediaLoader(connection: { conn }, configuration: stubConfiguration())
        let handle = loader.register(url: "https://books.example/api/v1/radio/live/rs_1", origin: origin, mime: "", live: true)
        let result = await play(handle: handle, loader: loader, mime: "", until: 0.5)
        XCTAssertTrue(result.playing, "\(result.status) requests \(MediaStub.seen.count)")
        XCTAssertTrue(result.reached, result.status)
    }

    /// Two seconds of 16 kbps mono MP3 made with ffmpeg (a 440 Hz tone), so the test needs no encoder.
    private static let toneMp3 = "//MgxAAIMEaceUwAAAE5wAF69evXr/osMxLBuBMCYjk9eDgIAgCGD4fP5TlwfXSYl8G/VP/zIsQGCih+mAGaOADBghBTlBjgDVgBqDrypiLdCAwtShCD0Hv48NhtyoKs5YKgqv3ZxjiQ//MgxAUJ+FZUAd4AAMFQQjAJAsMDEAIwHQbjCrCOND8YMwsggygFBiRcJt3djVez/963Bf/zIMQECHhKJADn9EAYw0q0WtJhGAKABZqQw0IGqR4E3siorYWV///R/979/us9JkfvJpP/8yDECQjoTiAA5/RADUAJgDYAsa5yQwg+MNCWbQmjsYN/9P/Gf+lF1VeowFAHbTRYcgCM//MixAwJiEogAAf0CAGgBs1wYlIBcEaFM1hVFbFv/u/2f+t////1Kt3IJMm5hW5hqRJgCoD/8yDEDQhISiQABz4EMGoCkcoKAokIW2kNHgK//f/kf/XV1uPGRrCuVrSgxgBgAqaLAQ3A//MgxBIH4EYoAAc+BOAhUanVkV4ez///f/7V3lBJl3HKZMNRVMATAIDTkSlEDATyBrbRWf/zIMQZCCBKJAAHPgTrwr///p/+5dYx4ypV1ys6TGMAMAHTQ7iaIFAQKXTqxayP///1//T/8yLEHwiwRigABz4Ef///sf/W7kAmbcEoEw1CSYAmATGmCl0ZzQCBrbRWeGN///rd/5DWof/zIMQkCIBGJADn9EBjMFLWFZ0kMYAYARmg1FYACAeUknVi1kf///3f+xXeUAmBHg1yQyr/8yDEKQfQRigAPz4gUtiYAuAcGp5nVJ1xAcBadFZ69r//2+7/0tbiRg2CfqlZ0kMYBQFJ//MgxDAIeE4gAAf0CP6V5B9ApJOrLrI9n//9X/0////s3lAJgQ4LMoMu0u6YAeAaGjUm/v/zIsQ1CIBGKAAHsgicUMhS40VtDHf//7/9dNXKrEjAjAV1QVdxf4wA4A3NFCOgjhB0Bziw//MgxDsISEYkAAf0CPWR7P/R/3/+1d3IBMCtBhkUlBSzpgCYCMaZiiPnNHF0WXQ7LuCH///zIMRACGBGJAAH9Ai//T/93+n+81VfY2dtS7KRQFAAjAGAFk1UxQkOwVCAy6X6lo8Ofr7/8yDERQkASiAAB/QIj7df/w1p//1/sf/LHJ9Ta+oLtJEgQAFMAXAWjUs1L86pYHAV1O7L//MixEgKsEYcAOf0QLghNf93X8l/2qd22V/+lfxiRgUQJumCsYusYAUAhme5INB8Go/NdjX/8yDERQrgShwA5/RAZPM///xv/sXdaImBUglSVyxS6pgBICIZ3ihqnuYii5UZtHXf//7v//MgxEAIIEYkAAfyCPyK7jDRgZAKWhqoMDQAAwAwBVNBsToD+TQBNdh6lW///+rb+mnR///zIMRGCChGJAAH8gj/r2XeURMCxBEkrliluTABwEgzdtGZPE5IlyozTHXf//1P/9LLGVH/8yLETAnoRiAAB/IIgWgIOlasYt0YAMAlmajI+R2HpFOTGqVj///9//k65lDJga4JAgOUFP/zIMRMCDhGJAAH8ggYABmAEgLRnOamqeayDrOoZpiZn//9k3/7av/n4xo7zrSRWMXOMAD/8yDEUgggRiQAB/IIgE8zApKWOZFMZyY1VYa///r/65L///9l5lGTA3wQBCpTUGAAZgA4//MgxFgI0EYgAAfyCAvGZ/qwZ1sISWtQzTEzP/p/1u7f0P/neRo8LPSRXUXOMAAAUTKuE//zIsRbCShGJADf8kAiP00vnZlVUga///xn/or3ApgY4F8kkukuaAQFIygVNhPckvXalNbZ//MgxF4I+EYgAAfyCK///0f+qu4y4wMgCvS5XUXOAgCmZKQnMHaaczsyqqQf///V/8j////zIMRhCCBGJADf8ED/svMBTA6wLhA5TUKAAYBAZDJuVo896R5cKI1ibjv//6n9v6buMuP/8yDEZwegSiQAL/AkAzAI1LldReYEAKpj3SfsbqqVPrEqpB//r/7//TX/7zstPPekzl0l//MixG8IsEYkAAfwCOUGgKxjc6g6a6KUulEbgfM///1v/8mq9gYwNABpTNXUXmCgCuYwMof/8yDEdAhwRiAAL/Akxmqqs6M1dDBr/1/6//t////Z/+87OnpqiZy6S8oXAVjFG1Fkw0WB//MgxHkH2EYkAAfwCHimbhMz///W7/0K7jPH6JaFquhCABBYBpMSyXRgAAOqHN1j1DUDLv/zIMSACAhGJADf8ED//+j/7v9F9ww9MmSaYKX1EYCwYYSo+hUAUWS8UduB93//9Tv/If//8yLEhgi4RiQAL/AkQu8oz9BNElXQhAAhQBpMISXYhEAOpvPTHqGoZ///2f9iv///1fcMJv/zIMSLB9hGJADf8EAGqTyYKghHQFgw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"
}
