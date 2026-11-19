import XCTest
@preconcurrency import TendshelfShared
@testable import Tendshelf

@MainActor
final class MediaLoaderTests: XCTestCase {
    private let origin = Origin.companion.parse(input: "https://books.example")!
    private let streamUrl = "https://books.example/api/v1/stream/f1"

    private func loader(retryScale: Double = 1, connection: Connection? = nil) -> MediaLoader {
        let conn = connection ?? Connection(origin: origin, token: "tok-9", deviceId: "dev-1")
        return MediaLoader(connection: { conn }, configuration: stubConfiguration(), retryScale: retryScale)
    }

    override func setUp() { MediaStub.reset(nil) }

    func testBearerAndRangeSentAndNoCookie() async {
        MediaStub.reset { _ in partialScript(Data(repeating: 1, count: 100), from: 0, total: 1000) }
        let l = loader()
        let handle = l.register(url: streamUrl, origin: origin, mime: "audio/mpeg", live: false)
        let req = FakeMediaRequest(offset: 0, length: 100, wantsInfo: true)
        l.begin(req, url: handle)
        let done = await waitFor { req.done }
        XCTAssertTrue(done)
        XCTAssertTrue(req.finished)
        let seen = MediaStub.seen
        XCTAssertEqual(seen.count, 1)
        XCTAssertEqual(seen[0].value(forHTTPHeaderField: "Authorization"), "Bearer tok-9")
        XCTAssertEqual(seen[0].value(forHTTPHeaderField: "Range"), "bytes=0-99")
        XCTAssertNil(seen[0].value(forHTTPHeaderField: "Cookie"))
        XCTAssertEqual(req.data.count, 100)
        XCTAssertEqual(req.info?.length, 1000)
        XCTAssertEqual(req.info?.ranges, true)
    }

    func testInfoOnlyAsksTwoBytes() async {
        MediaStub.reset { _ in partialScript(Data([1, 2]), from: 0, total: 5000) }
        let l = loader()
        let handle = l.register(url: streamUrl, origin: origin, mime: "audio/mpeg", live: false)
        let req = FakeMediaRequest(wantsInfo: true)
        l.begin(req, url: handle)
        _ = await waitFor { req.done }
        XCTAssertEqual(MediaStub.seen.first?.value(forHTTPHeaderField: "Range"), "bytes=0-1")
        XCTAssertEqual(req.info?.length, 5000)
        XCTAssertTrue(req.finished)
        XCTAssertTrue(req.data.isEmpty)
    }

    func testUnknownHandleForeignUrlAndChangedOriginMakeNoRequest() async {
        MediaStub.reset { _ in partialScript(Data([1]), from: 0, total: 1) }
        let l = loader()
        let unknown = FakeMediaRequest(length: 10)
        l.begin(unknown, url: URL(string: "tsmedia://m/99")!)
        _ = await waitFor { unknown.done }
        XCTAssertNotNil(unknown.error)

        let foreign = l.register(url: "https://evil.example/api/v1/stream/f1", origin: origin, mime: "audio/mpeg", live: false)
        let r2 = FakeMediaRequest(length: 10)
        l.begin(r2, url: foreign)
        _ = await waitFor { r2.done }
        XCTAssertNotNil(r2.error)
        XCTAssertEqual(l.lastFailure(foreign)?.otherServer, true)

        let other = Origin.companion.parse(input: "https://other.example")!
        let moved = loader(connection: Connection(origin: other, token: "t", deviceId: "d"))
        let h3 = moved.register(url: streamUrl, origin: origin, mime: "audio/mpeg", live: false)
        let r3 = FakeMediaRequest(length: 10)
        moved.begin(r3, url: h3)
        _ = await waitFor { r3.done }
        XCTAssertNotNil(r3.error)
        XCTAssertEqual(moved.lastFailure(h3)?.otherServer, true)
        XCTAssertTrue(MediaStub.seen.isEmpty)
    }

    func testBusyTwiceThenPartialPlaysOn() async {
        let count = Counter()
        MediaStub.reset { _ in
            if count.next() <= 2 { return MediaScript(status: 503, headers: ["Retry-After": "1"]) }
            return partialScript(Data(repeating: 7, count: 50), from: 0, total: 50)
        }
        let l = loader(retryScale: 0.001)
        let handle = l.register(url: streamUrl, origin: origin, mime: "audio/mpeg", live: false)
        let req = FakeMediaRequest(length: 50)
        l.begin(req, url: handle)
        _ = await waitFor { req.done }
        XCTAssertTrue(req.finished)
        XCTAssertEqual(req.data.count, 50)
        XCTAssertEqual(MediaStub.seen.count, 3)
    }

    func testUnauthorizedFailsAtOnce() async {
        MediaStub.reset { _ in MediaScript(status: 401) }
        let l = loader()
        let handle = l.register(url: streamUrl, origin: origin, mime: "audio/mpeg", live: false)
        let req = FakeMediaRequest(length: 50)
        l.begin(req, url: handle)
        _ = await waitFor { req.done }
        XCTAssertNotNil(req.error)
        XCTAssertEqual(l.lastFailure(handle)?.status, 401)
        XCTAssertEqual(MediaStub.seen.count, 1)
    }

    func testFullAnswerToOffsetRequestDropsTheStart() async {
        var whole = Data()
        for i in 0..<5000 { whole.append(UInt8(i % 251)) }
        let body = whole
        MediaStub.reset { _ in MediaScript(status: 200, headers: ["Content-Length": "5000"], pieces: [body]) }
        let l = loader()
        let handle = l.register(url: streamUrl, origin: origin, mime: "audio/mpeg", live: false)
        let req = FakeMediaRequest(offset: 4096, length: 904)
        l.begin(req, url: handle)
        _ = await waitFor { req.done }
        XCTAssertEqual(MediaStub.seen.first?.value(forHTTPHeaderField: "Range"), "bytes=4096-4999")
        XCTAssertEqual(req.data, whole.subdata(in: 4096..<5000))
    }

    func testLiveBusyRecordsRetryAfter() async {
        MediaStub.reset { _ in MediaScript(status: 503, headers: ["Retry-After": "7"]) }
        let l = loader()
        let handle = l.register(url: "https://books.example/api/v1/radio/live/rs_1", origin: origin, mime: "audio/mpeg", live: true)
        let req = FakeMediaRequest(toEnd: true, wantsInfo: true)
        l.begin(req, url: handle)
        _ = await waitFor { req.done }
        XCTAssertNotNil(req.error)
        let f = l.lastFailure(handle)
        XCTAssertEqual(f?.status, 503)
        XCTAssertEqual(f?.retryAfterS, 7)
        XCTAssertEqual(MediaStub.seen.count, 1)
        XCTAssertNil(MediaStub.seen[0].value(forHTTPHeaderField: "Range"))
    }

    func testForgetAllCancelsTasks() async {
        MediaStub.reset { _ in MediaScript(status: 206, headers: ["Content-Range": "bytes 0-9/100"], pieces: [Data(count: 10)], hold: true) }
        let l = loader()
        let handle = l.register(url: streamUrl, origin: origin, mime: "audio/mpeg", live: false)
        let req = FakeMediaRequest(length: 100)
        l.begin(req, url: handle)
        _ = await waitFor { req.data.count == 10 }
        l.forgetAll()
        let cancelled = await waitFor { MediaStub.cancelled >= 1 }
        XCTAssertTrue(cancelled)
        // The handle is gone: a new request for it fails without a request.
        let again = FakeMediaRequest(length: 10)
        let before = MediaStub.seen.count
        l.begin(again, url: handle)
        _ = await waitFor { again.done }
        XCTAssertNotNil(again.error)
        XCTAssertEqual(MediaStub.seen.count, before)
    }
}

/// A counter that a stub's closure (any thread) can use.
nonisolated final class Counter: @unchecked Sendable {
    private let lock = NSLock()
    private var n = 0
    func next() -> Int { lock.withLock { n += 1; return n } }
    var peek: Int { lock.withLock { n } }
}
