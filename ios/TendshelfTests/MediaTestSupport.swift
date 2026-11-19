import AVFoundation
import XCTest
@preconcurrency import TendshelfShared
@testable import Tendshelf

/// What the media stub answers: a status, headers, and the body in pieces sent [interval] seconds apart. `hold` keeps the
/// answer open without ever finishing it (a cancelled task is then visible in `MediaStub.cancelled`).
nonisolated struct MediaScript {
    var status = 200
    var headers: [String: String] = [:]
    var pieces: [Data] = []
    var interval = 0.0
    var hold = false
}

/// A stand-in for the network that can stream slowly. State is static (a `URLProtocol` is made by the system) and locked.
nonisolated final class MediaStub: URLProtocol, @unchecked Sendable {
    private static let lock = NSLock()
    nonisolated(unsafe) private static var _seen: [URLRequest] = []
    nonisolated(unsafe) private static var _cancelled = 0
    nonisolated(unsafe) private static var _handler: (@Sendable (URLRequest) -> MediaScript)?
    private var stopped = false

    static var seen: [URLRequest] { lock.withLock { _seen } }
    static var cancelled: Int { lock.withLock { _cancelled } }
    static func reset(_ handler: (@Sendable (URLRequest) -> MediaScript)?) {
        lock.withLock {
            _seen = []
            _cancelled = 0
            _handler = handler
        }
    }

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        let handler = Self.lock.withLock { () -> (@Sendable (URLRequest) -> MediaScript)? in
            Self._seen.append(request)
            return Self._handler
        }
        guard let script = handler?(request) else {
            client?.urlProtocol(self, didFailWithError: URLError(.cannotConnectToHost))
            return
        }
        let response = HTTPURLResponse(url: request.url!, statusCode: script.status, httpVersion: "HTTP/1.1", headerFields: script.headers)!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        send(script.pieces[...], script)
    }

    private func send(_ rest: ArraySlice<Data>, _ script: MediaScript) {
        if stopped { return }
        guard let first = rest.first else {
            if !script.hold { client?.urlProtocolDidFinishLoading(self) }
            return
        }
        client?.urlProtocol(self, didLoad: first)
        let next = rest.dropFirst()
        if script.interval > 0 {
            DispatchQueue.global().asyncAfter(deadline: .now() + script.interval) { [self] in send(next, script) }
        } else {
            send(next, script)
        }
    }

    override func stopLoading() {
        stopped = true
        Self.lock.withLock { Self._cancelled += 1 }
    }
}

/// A loading request that records what the loader did with it.
nonisolated final class FakeMediaRequest: MediaRequest, @unchecked Sendable {
    private let lock = NSLock()
    let offset: Int64
    let length: Int
    let toEnd: Bool
    let wantsInfo: Bool
    private var _info: (type: String, length: Int64, ranges: Bool)?
    private var _data = Data()
    private var _finished = false
    private var _error: Error?

    init(offset: Int64 = 0, length: Int = 0, toEnd: Bool = false, wantsInfo: Bool = false) {
        self.offset = offset
        self.length = length
        self.toEnd = toEnd
        self.wantsInfo = wantsInfo
    }

    var info: (type: String, length: Int64, ranges: Bool)? { lock.withLock { _info } }
    var data: Data { lock.withLock { _data } }
    var finished: Bool { lock.withLock { _finished } }
    var error: Error? { lock.withLock { _error } }
    var done: Bool { lock.withLock { _finished || _error != nil } }

    func setInfo(type: String, length: Int64, ranges: Bool) { lock.withLock { _info = (type, length, ranges) } }
    func respond(_ data: Data) { lock.withLock { _data.append(data) } }
    func finish() { lock.withLock { _finished = true } }
    func fail(_ error: Error) { lock.withLock { _error = error } }
}

/// Polls (off the main actor's way) until [condition] holds or [seconds] pass.
@MainActor func waitFor(_ seconds: Double = 5, _ condition: @MainActor () -> Bool) async -> Bool {
    let end = Date().addingTimeInterval(seconds)
    while Date() < end {
        if condition() { return true }
        try? await Task.sleep(nanoseconds: 10_000_000)
    }
    return condition()
}

/// A configuration whose only protocol is the media stub.
func stubConfiguration() -> URLSessionConfiguration {
    let config = ServerClient.ephemeralConfiguration()
    config.protocolClasses = [MediaStub.self]
    return config
}

/// A synthetic mono 16-bit WAV of [seconds] (a quiet 440 Hz tone).
func makeWav(seconds: Double, rate: Int = 22050) -> Data {
    let frames = Int(Double(rate) * seconds)
    var pcm = Data(capacity: frames * 2)
    for i in 0..<frames {
        let v = Int16(8000 * sin(2 * Double.pi * 440 * Double(i) / Double(rate)))
        pcm.append(contentsOf: [UInt8(truncatingIfNeeded: v), UInt8(truncatingIfNeeded: v >> 8)])
    }
    func le32(_ x: Int) -> [UInt8] { (0..<4).map { UInt8((x >> (8 * $0)) & 0xff) } }
    func le16(_ x: Int) -> [UInt8] { (0..<2).map { UInt8((x >> (8 * $0)) & 0xff) } }
    var out = Data("RIFF".utf8)
    out.append(contentsOf: le32(36 + pcm.count))
    out.append(Data("WAVEfmt ".utf8))
    out.append(contentsOf: le32(16) + le16(1) + le16(1) + le32(rate) + le32(rate * 2) + le16(2) + le16(16))
    out.append(Data("data".utf8))
    out.append(contentsOf: le32(pcm.count))
    out.append(pcm)
    return out
}

/// A synthetic ADTS AAC stream of [seconds] (a sine), made with the system encoder.
func makeAdts(seconds: Double) throws -> Data {
    let url = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString + ".aac")
    defer { try? FileManager.default.removeItem(at: url) }
    var pcm = AudioStreamBasicDescription(
        mSampleRate: 44100, mFormatID: kAudioFormatLinearPCM,
        mFormatFlags: kAudioFormatFlagIsFloat | kAudioFormatFlagIsPacked, mBytesPerPacket: 4, mFramesPerPacket: 1,
        mBytesPerFrame: 4, mChannelsPerFrame: 1, mBitsPerChannel: 32, mReserved: 0)
    var aac = AudioStreamBasicDescription(
        mSampleRate: 44100, mFormatID: kAudioFormatMPEG4AAC, mFormatFlags: 0, mBytesPerPacket: 0, mFramesPerPacket: 1024,
        mBytesPerFrame: 0, mChannelsPerFrame: 1, mBitsPerChannel: 0, mReserved: 0)
    var file: ExtAudioFileRef?
    guard ExtAudioFileCreateWithURL(url as CFURL, kAudioFileAAC_ADTSType, &aac, nil, AudioFileFlags.eraseFile.rawValue, &file) == noErr,
          let file else { throw URLError(.cannotCreateFile) }
    guard ExtAudioFileSetProperty(file, kExtAudioFileProperty_ClientDataFormat, UInt32(MemoryLayout.size(ofValue: pcm)), &pcm) == noErr
    else { throw URLError(.cannotCreateFile) }
    let chunk = 4096
    var done = 0
    let total = Int(44100 * seconds)
    while done < total {
        let n = min(chunk, total - done)
        var samples = (0..<n).map { Float(0.25 * sin(2 * Double.pi * 440 * Double(done + $0) / 44100)) }
        let status = samples.withUnsafeMutableBytes { raw -> OSStatus in
            var list = AudioBufferList(
                mNumberBuffers: 1,
                mBuffers: AudioBuffer(mNumberChannels: 1, mDataByteSize: UInt32(n * 4), mData: raw.baseAddress))
            return ExtAudioFileWrite(file, UInt32(n), &list)
        }
        guard status == noErr else { throw URLError(.cannotCreateFile) }
        done += n
    }
    ExtAudioFileDispose(file)
    return try Data(contentsOf: url)
}

/// [data] cut into pieces of [size] bytes.
func pieces(_ data: Data, _ size: Int) -> [Data] {
    stride(from: 0, to: data.count, by: size).map { data.subdata(in: $0..<min($0 + size, data.count)) }
}

/// A 206 answer for [body] starting at byte [from] of [total].
nonisolated func partialScript(_ body: Data, from: Int, total: Int) -> MediaScript {
    MediaScript(
        status: 206,
        headers: ["Content-Range": "bytes \(from)-\(from + body.count - 1)/\(total)", "Content-Type": "audio/mpeg"],
        pieces: [body])
}
