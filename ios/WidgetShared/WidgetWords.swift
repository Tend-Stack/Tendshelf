import Foundation

/// The few words the widget extension needs without reading `strings.xml` (the extension links no shared code and no bundled
/// assets). `WidgetWordsTests` holds each equal to Android's `strings.xml`.
nonisolated enum WidgetWords {
    static let label = "Tendshelf"
    static let description = "Your book, with play and skip buttons."
    static let previewTitle = "Your book"
    static let previewChapter = "Chapter 3"
    static let signedOut = "Sign in to listen. Tap to open Tendshelf."
    static let noBook = "Pick a book to listen. Tap to open Tendshelf."
    static let play = "Play"
    static let pause = "Pause"
    static let back = "Back"
    static let forward = "Forward"
}
