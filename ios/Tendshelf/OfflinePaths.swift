import Foundation
@preconcurrency import TendshelfShared

/// Where the downloads live (invariant 2 of the I3 plan): one folder, `Application Support/offline/`, kept out of backups and
/// readable after the first unlock. One removal of the folder is the whole wipe. Every id passes the shared `validId` and every
/// file name is a plain name, so no answer is ever a path that leaves the folder; anything else is nil.
nonisolated struct OfflinePaths {
    let root: URL

    init(root: URL = OfflinePaths.defaultRoot()) {
        self.root = root
    }

    static func defaultRoot() -> URL {
        let support = (try? FileManager.default.url(for: .applicationSupportDirectory, in: .userDomainMask, appropriateFor: nil, create: true))
            ?? FileManager.default.temporaryDirectory
        return support.appendingPathComponent("offline", isDirectory: true)
    }

    /// The folder of a book's audio files.
    func audio(_ bookId: String) -> URL? {
        guard valid(bookId) else { return nil }
        return root.appendingPathComponent("audio", isDirectory: true).appendingPathComponent(bookId, isDirectory: true)
    }

    func audioFile(_ bookId: String, _ name: String) -> URL? {
        guard let folder = audio(bookId), plain(name) else { return nil }
        return folder.appendingPathComponent(name)
    }

    func cover(_ id: String) -> URL? {
        valid(id) ? root.appendingPathComponent("covers", isDirectory: true).appendingPathComponent(id + ".jpg") : nil
    }

    func chapters(_ id: String) -> URL? {
        valid(id) ? root.appendingPathComponent("chapters", isDirectory: true).appendingPathComponent(id + ".json") : nil
    }

    func text(_ bookId: String) -> URL? {
        valid(bookId) ? root.appendingPathComponent("text", isDirectory: true).appendingPathComponent(bookId, isDirectory: true) : nil
    }

    /// Where a finished download waits, under a name of its own, until it is checked and moved.
    var incoming: URL { root.appendingPathComponent("incoming", isDirectory: true) }
    var staticDir: URL { root.appendingPathComponent("static", isDirectory: true) }
    var database: URL { root.appendingPathComponent("offline.sqlite") }

    /// Creates the folder: protected until the first unlock, and not part of a backup.
    func prepare() throws {
        let fm = FileManager.default
        try fm.createDirectory(at: root, withIntermediateDirectories: true, attributes: [.protectionKey: FileProtectionType.completeUntilFirstUserAuthentication])
        try fm.setAttributes([.protectionKey: FileProtectionType.completeUntilFirstUserAuthentication], ofItemAtPath: root.path)
        var url = root
        var values = URLResourceValues()
        values.isExcludedFromBackup = true
        try url.setResourceValues(values)
    }

    private func valid(_ id: String) -> Bool { BridgeProtocolKt.validId(s: id) }

    private func plain(_ name: String) -> Bool { !name.isEmpty && !name.contains("/") && name != "." && name != ".." }
}
