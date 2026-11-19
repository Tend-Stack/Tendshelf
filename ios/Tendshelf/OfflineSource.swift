import Foundation
@preconcurrency import TendshelfShared

/// What the player reads from the phone's own downloads (decision 0048, I3c; the `Offline` object the Android engine calls).
/// `Downloads` conforms; the tests use a fake. Invariant 5: a downloaded book or episode starts with no request.
@MainActor
protocol OfflineSource: AnyObject {
    /// The row of a book or episode that is whole on the phone (ready, or stale: the bytes are old but still play); else nil.
    func playableRow(_ id: String) -> OfflineBook?
    /// The stored answer of the server (a manifest for a book, the playback document for an episode).
    func manifestJson(_ id: String) -> String?
    /// The stored file of one cache key, when it arrived whole and is really on disk.
    func localFile(_ bookId: String, _ cacheKey: String) -> URL?
    func coverData(_ id: String) -> Data?
    func chaptersText(_ id: String) -> String?
    /// The last settings the server gave; the built-in ones before any.
    func keptSettings() -> PlayerSettings
    func keep(_ s: PlayerSettings)
    /// A place made on this phone is kept beside the row, so a start with no network resumes there.
    func notePlace(_ id: String, ms: Int64, at: Int64)
    /// An episode was played to the end: "remove after played" may now take it.
    func markPlayed(_ id: String)
    /// The book or episode the player has now: a rule never removes it.
    var playingNow: String { get set }
}

extension Downloads: OfflineSource {
    func manifestJson(_ id: String) -> String? { store.manifest(id: id) }

    func chaptersText(_ id: String) -> String? {
        paths.chapters(id).flatMap { try? String(contentsOf: $0, encoding: .utf8) }
    }

    func keptSettings() -> PlayerSettings {
        StoredJsonKt.decodeKeptSettings(text: defaults.string(forKey: Self.settingsKey))
    }

    func keep(_ s: PlayerSettings) {
        defaults.set(StoredJsonKt.encodeKeptSettings(s: s), forKey: Self.settingsKey)
    }

    func notePlace(_ id: String, ms: Int64, at: Int64) {
        store.notePlace(id: id, ms: ms, at: at)
    }

    func markPlayed(_ id: String) {
        guard let row = store.get(id: id), row.kind == OfflineKt.KIND_EPISODE, !row.played else { return }
        store.setPlayed(id: id, played: true)
        changed(id)
    }
}
