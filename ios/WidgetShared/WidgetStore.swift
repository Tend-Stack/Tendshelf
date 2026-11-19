import Foundation

/// The app group folder the app writes and the widget reads: `widget/state.json` and `widget/cover.jpg`. A file, not
/// `UserDefaults(suiteName:)`, so the privacy manifest needs no second required-reason entry.
nonisolated enum WidgetStore {
    static let group = "group.app.tendshelf.ios"
    static let maxStateBytes = 64 * 1024
    static let maxCoverBytes = 512 * 1024

    /// `<group>/widget`, created and kept out of backups; nil when the group is not available.
    static func folder(container: URL? = FileManager.default.containerURL(forSecurityApplicationGroupIdentifier: group)) -> URL? {
        guard var url = container?.appendingPathComponent("widget", isDirectory: true) else { return nil }
        do {
            try FileManager.default.createDirectory(at: url, withIntermediateDirectories: true)
            var values = URLResourceValues()
            values.isExcludedFromBackup = true
            try url.setResourceValues(values)
        } catch {
            return nil
        }
        return url
    }

    static func read(folder: URL? = WidgetStore.folder()) -> WidgetSnapshot? {
        guard let url = folder?.appendingPathComponent("state.json"),
              let data = try? Data(contentsOf: url), data.count <= maxStateBytes,
              let snapshot = try? JSONDecoder().decode(WidgetSnapshot.self, from: data),
              snapshot.v == WidgetSnapshot.version
        else { return nil }
        return snapshot
    }

    @discardableResult
    static func write(_ snapshot: WidgetSnapshot, folder: URL? = WidgetStore.folder()) -> Bool {
        guard let url = folder?.appendingPathComponent("state.json"), let data = try? JSONEncoder().encode(snapshot) else { return false }
        return (try? data.write(to: url, options: .atomic)) != nil
    }

    /// Nil deletes the cover; a cover over the cap is not kept.
    static func writeCover(_ jpeg: Data?, folder: URL? = WidgetStore.folder()) {
        guard let url = coverURL(folder: folder) else { return }
        guard let jpeg, jpeg.count <= maxCoverBytes else {
            try? FileManager.default.removeItem(at: url)
            return
        }
        try? jpeg.write(to: url, options: .atomic)
    }

    static func coverURL(folder: URL? = WidgetStore.folder()) -> URL? {
        folder?.appendingPathComponent("cover.jpg")
    }

    /// Nothing of a person stays: the state and the cover go.
    static func clear(folder: URL? = WidgetStore.folder()) {
        guard let folder else { return }
        try? FileManager.default.removeItem(at: folder.appendingPathComponent("state.json"))
        try? FileManager.default.removeItem(at: folder.appendingPathComponent("cover.jpg"))
    }
}
