import Foundation
import OSLog
@preconcurrency import TendshelfShared

/// The book ended, possibly with the screen off (port of `NextBook.kt`, with the phone fallback of I3c). A background
/// task covers the hand-off (the iPhone's analogue of the 30 s wake lock). `finished` is written and sent first; only when
/// that went out is the queue asked, once. The ask is never repeated: a lost answer costs one queued book, a retry could
/// skip a second. No page is involved. A sleep timer ends the book without any start.
@MainActor
final class NextBook {
    private static let log = Logger(subsystem: "app.tendshelf.ios", category: "nextbook")

    private let writer: ProgressWriter
    private let connection: () -> Connection?
    private let makeApi: (Origin) -> PlayerAPI
    private let background: BackgroundWork
    private let start: (String) -> Void
    private let once = EndOnce()
    /// How long the background task stays after the hand-off: the next book's manifest fetch runs on.
    var lingerSeconds: Double = 8
    /// The phone fallback (`NextBook.kt:84-95`, I3c): the remembered next book when it is whole here, cleared when used; and a
    /// note that it started without the server's say, so the server's queue is tidied later.
    var offlineNext: ((String) -> String?)?
    var autoDrop: ((String) -> Void)?

    init(
        writer: ProgressWriter, connection: @escaping () -> Connection?, makeApi: @escaping (Origin) -> PlayerAPI,
        background: BackgroundWork, start: @escaping (String) -> Void
    ) {
        self.writer = writer
        self.connection = connection
        self.makeApi = makeApi
        self.background = background
        self.start = start
    }

    /// A new book began; its end is again a hand-off.
    func reset() { once.reset() }

    /// On the player's end. [stillEnded] is checked again before the next book starts. Returns the hand-off so a test can wait.
    @discardableResult
    func ended(bookId: String, positionMs: Int64, speed: Double, askQueue: Bool, stillEnded: @escaping () -> Bool) -> Task<Void, Never>? {
        guard once.claim(id: bookId) else { return nil }
        let id = background.begin("nextbook")
        let listened = writer.drainListened()
        let conn = connection()
        return Task { @MainActor [weak self] in
            guard let self else { return }
            await handOff(conn, bookId, positionMs, speed, listened, askQueue, stillEnded)
            if lingerSeconds > 0 { try? await Task.sleep(nanoseconds: UInt64(lingerSeconds * 1_000_000_000)) }
            background.end(id)
        }
    }

    private func handOff(
        _ conn: Connection?, _ bookId: String, _ positionMs: Int64, _ speed: Double, _ listened: Int64, _ askQueue: Bool,
        _ stillEnded: () -> Bool
    ) async {
        let sent = await writer.finishAndFlush(bookId: bookId, positionMs: positionMs, speed: speed, listened: listened)
        if !askQueue {
            Self.log.info("a sleep timer ended this book, queue not asked")
            return
        }
        guard sent, let conn else {
            Self.log.info("finished not sent, nothing popped")
            fromPhone(bookId, stillEnded)
            return
        }
        let next: String?
        do {
            next = try await makeApi(conn.origin).queueNext(token: conn.token, ended: bookId)
        } catch {
            Self.log.warning("no answer from the queue, not asked again")
            fromPhone(bookId, stillEnded)
            return
        }
        guard let next else {
            Self.log.info("no next book")
            return
        }
        // Signed out or changed server while waiting: nothing starts.
        guard connection()?.deviceId == conn.deviceId, stillEnded() else { return }
        Self.log.info("next book: starting")
        start(next)
    }

    /// The ask gave no answer: start the remembered book if it is whole here, and remember to take it off the queue later.
    private func fromPhone(_ bookId: String, _ stillEnded: () -> Bool) {
        guard let next = offlineNext?(bookId) else {
            Self.log.info("next book: nothing on the phone to start")
            return
        }
        Self.log.info("next book: starting from the phone")
        autoDrop?(next)
        if stillEnded() { start(next) }
    }
}
