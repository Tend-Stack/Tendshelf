import Foundation
@preconcurrency import TendshelfShared

/// Makes the next book ready on Wi-Fi while this one plays (decision 0035; port of `AutoNext.kt`, decision 0048, I3c). Main actor
/// only. It looks at the position at most once a minute, and only while the player is playing. The clock is monotonic
/// (`ProcessInfo.systemUptime`), so a changed wall clock cannot hold or hurry it.
@MainActor
final class AutoNext {
    private let downloads: Downloads
    private let clock: () -> Int64
    private var lastTick: Int64 = 0
    private var asked = ""
    private var askedAt: Int64 = 0
    /// Tests wait for the work a tick started.
    private(set) var lastWork: Task<Void, Never>?

    init(_ downloads: Downloads, clock: @escaping () -> Int64 = { Int64(ProcessInfo.processInfo.systemUptime * 1000) + 1 }) {
        self.downloads = downloads
        self.clock = clock
    }

    /// A new book started: forget what was asked for the old one, and the pending wish.
    func reset() {
        lastTick = 0
        asked = ""
        askedAt = 0
        downloads.autoForget()
    }

    func tick(book: String, positionMs: Int64, durationMs: Int64, playing: Bool) {
        guard playing, !book.isEmpty else { return }
        let now = clock()
        if lastTick != 0, now - lastTick < OfflineKt.AUTO_TICK_MS { return }
        lastTick = now
        let downloads = downloads
        // Holds and releases follow the switch even when this book is not near its end.
        var work: [@MainActor () async -> Void] = [{ await downloads.autoSync() }]
        downloads.episodes.syncSoon() // at most once an hour; only while some show asks for downloads
        let ago: KotlinLong? = asked == book ? KotlinLong(value: now - askedAt) : nil
        switch OfflineKt.autoStep(
            enabled: downloads.keptSettings().autoDownload, near: OfflineKt.nearEnd(positionMs: positionMs, durationMs: durationMs),
            unmetered: downloads.unmetered(), askedAgoMs: ago)
        {
        case .want:
            downloads.autoWant(book)
        case .ask:
            asked = book
            askedAt = now
            work.append { await downloads.autoFetch(book) }
        default:
            break
        }
        lastWork = Task { @MainActor in
            for step in work { await step() }
        }
    }
}
