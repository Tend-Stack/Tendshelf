import Foundation
@preconcurrency import TendshelfShared

/// What a widget button does (decision 0048, I4b; behaviour of `WidgetActions.kt`): the same player the page drives. With nothing
/// loaded (the app was quit) Play and the skips start the last item, at the place the server holds. Open loads the item's page,
/// only for a valid id and a connected person. Closures, so tests need no player.
@MainActor
struct WidgetCommander {
    var loaded: () -> Bool
    var playing: () -> Bool
    /// The connected server, or nil when signed out or not connected.
    var connectedOrigin: () -> Origin?
    var lastItem: () -> String?
    var pause: () -> Void
    var play: () -> Void
    var skip: (Bool) -> Void
    var start: (String) -> Void
    var load: (URL) -> Void

    func run(_ command: WidgetCommand) {
        switch command {
        case .toggle:
            switch WidgetKt.widgetPlayTap(playing: playing(), loaded: loaded()) {
            case .pause: pause()
            case .play: play()
            default: resumeLast()
            }
        case .back, .forward:
            if loaded() { skip(command == .forward) } else { resumeLast() }
        case .open(let id):
            guard !id.isEmpty, BridgeProtocolKt.validId(s: id), let origin = connectedOrigin() else { return }
            let address: String
            if RadioKt.isStationId(id: id) {
                address = origin.radioUrl(shell: .ios)
            } else if PodcastKt.isEpisodeId(id: id) {
                address = origin.episodeUrl(episodeId: id, shell: .ios)
            } else {
                address = origin.bookUrl(bookId: id, shell: .ios)
            }
            if let url = URL(string: address) { load(url) }
        }
    }

    private func resumeLast() {
        guard connectedOrigin() != nil, let id = lastItem(), !id.isEmpty, BridgeProtocolKt.validId(s: id) else { return }
        start(id)
    }
}
