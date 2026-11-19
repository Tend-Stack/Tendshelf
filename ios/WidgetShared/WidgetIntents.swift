import AppIntents
import Foundation

/// What a widget button asks of the player.
nonisolated enum WidgetCommand: Sendable, Equatable {
    case toggle, back, forward
    case open(String)
}

/// Where the intents go: the app sets the handler at launch. `AudioPlaybackIntent` runs in the app's process (iOS 17), so the
/// handler is the real `Playback`. In the extension's own process the handler is nil and nothing happens.
@MainActor
enum WidgetCommands {
    static var handler: ((WidgetCommand) -> Void)?
}

struct TogglePlayIntent: AudioPlaybackIntent {
    static let title: LocalizedStringResource = "Play or pause"
    static let isDiscoverable = false

    init() {}

    @MainActor
    func perform() async throws -> some IntentResult {
        WidgetCommands.handler?(.toggle)
        return .result()
    }
}

struct SkipIntent: AudioPlaybackIntent {
    static let title: LocalizedStringResource = "Skip"
    static let isDiscoverable = false

    @Parameter(title: "Forward") var forward: Bool

    init() {}
    init(forward: Bool) { self.forward = forward }

    @MainActor
    func perform() async throws -> some IntentResult {
        WidgetCommands.handler?(forward ? .forward : .back)
        return .result()
    }
}

/// Opens the app and loads the item's page. No address scheme: `tendshelf:` carries only a link.
struct OpenItemIntent: AppIntent {
    static let title: LocalizedStringResource = "Open"
    static let isDiscoverable = false
    static let openAppWhenRun = true

    @Parameter(title: "Item") var itemId: String

    init() {}
    init(itemId: String) { self.itemId = itemId }

    @MainActor
    func perform() async throws -> some IntentResult {
        WidgetCommands.handler?(.open(itemId))
        return .result()
    }
}
