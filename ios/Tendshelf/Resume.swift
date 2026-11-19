import Foundation
import OSLog
@preconcurrency import TendshelfShared

/// Smart rewind and the check for a place elsewhere, on every start of sound after a break (port of `Resume.kt`). It is fed
/// from the player's sound state, not from `play()`, so a resume after a call counts too. The page is not involved.
@MainActor
final class Resume {
    private static let log = Logger(subsystem: "app.tendshelf.ios", category: "resume")

    private let clock: ResumeClock
    private let settings: () -> PlayerSettings?
    /// The loaded book's id, or "" when there is none or it is a station (a station has no place to rewind to).
    private let book: () -> String
    private let position: () -> Int64

    /// Seeks to a book time; the player's own seek.
    var seekTo: ((Int64) -> Void)?
    /// Called with the seconds stepped back, after the seek.
    var onRewound: ((Int) -> Void)?
    /// Called when the break was a minute or more and the book is the same: look at the server's place.
    var checkRemote: ((String) -> Void)?

    init(
        now: @escaping () -> Int64, settings: @escaping () -> PlayerSettings?, book: @escaping () -> String,
        position: @escaping () -> Int64
    ) {
        clock = ResumeClock(now: { KotlinLong(value: now()) })
        self.settings = settings
        self.book = book
        self.position = position
    }

    /// The sound changed. Buffering with the play button on is not a pause.
    func sound(_ state: AudioPlayer.Sound) {
        switch state {
        case .playing: resumed()
        case .paused: clock.stopped(hasBook: !book().isEmpty, buffering: false)
        case .waiting: break
        }
    }

    /// A book was loaded. [writtenAt] is when its stored place was saved; nil with [atGiven] or no place.
    func bookLoaded(writtenAt: Int64?, atGiven: Bool) {
        clock.bookLoaded(placeWrittenAt: writtenAt.map { KotlinLong(value: $0) }, atGiven: atGiven)
    }

    func clear() { clock.clear() }

    private func resumed() {
        // A station (or nothing loaded) never rewinds or asks (invariant 6).
        if book().isEmpty {
            clock.clear()
            return
        }
        let s = settings()
        guard let step = clock.resumed(steps: s?.rewindSteps ?? SmartRewindKt.DEFAULT_REWIND, enabled: s?.autoRewind ?? true) else { return }
        if step.rewindMs > 0 {
            seekTo?(max(0, position() - step.rewindMs))
            Self.log.info("rewound \(step.rewindMs / 1000) s")
            onRewound?(Int(step.rewindMs / 1000))
        }
        let id = book()
        if step.checkRemote, !id.isEmpty { checkRemote?(id) }
    }
}

/// "You were at {time} on {device}. Continue there?" (port of `AskPrompt.kt` without the notification: on iPhone the page shows
/// it, and while the app is hidden it waits, because `PushDiff.shown()` repeats it on return). While it is open the book's
/// saves are held, not sent. Nothing moves until it is answered.
@MainActor
final class AskState {
    private let writer: ProgressWriter
    private let seekTo: (Int64) -> Void
    private let changed: () -> Void

    private(set) var open: Ask?

    init(writer: ProgressWriter, seekTo: @escaping (Int64) -> Void, changed: @escaping () -> Void) {
        self.writer = writer
        self.seekTo = seekTo
        self.changed = changed
    }

    var isOpen: Bool { open != nil }

    func show(_ ask: Ask) {
        if open != nil { return }
        open = ask
        writer.heldBook = ask.bookId
        changed()
    }

    /// "Stay here" ([keepHere]): this place wins on the server. "Continue there": go to the other place.
    func answer(keepHere: Bool) {
        guard let ask = open else { return }
        open = nil
        if keepHere {
            writer.release(bookId: ask.bookId, force: true)
        } else {
            writer.dropHeld(bookId: ask.bookId)
            seekTo(ask.positionMs)
        }
        changed()
    }

    /// Sign-out: the question and its held rows go with the person.
    func discard() {
        open = nil
        writer.heldBook = nil
    }

    /// The book changed or the question is moot: the held place goes out plain, newest wins.
    func dismissPlain() {
        guard let ask = open else { return }
        open = nil
        writer.release(bookId: ask.bookId, force: false)
        changed()
    }
}
