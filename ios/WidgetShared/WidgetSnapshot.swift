import Foundation

/// What one widget size draws, decided by the app with the shared `widgetLook` (decision 0048, I4b). The widget only draws.
nonisolated struct WidgetLookOut: Codable, Equatable, Sendable {
    var showCover: Bool
    var showChapter: Bool
    var showSkip: Bool
}

/// The spoken labels of the buttons, filled by the app (plurals and the title need the shared words).
nonisolated struct WidgetLabels: Codable, Equatable, Sendable {
    var play: String
    var pause: String
    var back: String
    var forward: String
    var open: String
}

/// The one file the widget reads: titles and looks only, never a credential or an address.
nonisolated struct WidgetSnapshot: Codable, Equatable, Sendable {
    static let version = 1
    static let signedOutKind = "signedOut"
    static let noBookKind = "noBook"
    static let bookKind = "book"

    var v: Int = WidgetSnapshot.version
    /// "signedOut", "noBook" or "book".
    var kind: String
    var itemId: String
    var title: String
    var chapter: String
    var playing: Bool
    var small: WidgetLookOut
    var medium: WidgetLookOut
    /// The plain line of the signed-out and no-book states.
    var line: String
    var labels: WidgetLabels
}
