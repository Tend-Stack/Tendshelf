import CarPlay
import OSLog
import UIKit
@preconcurrency import TendshelfShared

/// One row of a CarPlay list as plain values: what `CarPlay.items` decides, before any `CPListItem` exists (the tests read these).
struct CarItemModel: Equatable {
    let id: String
    let text: String
    let detail: String?
    let isFolder: Bool
    /// How far in a Continue listening row is (0 to 1); nil for any other row.
    var progress: Double? = nil
}

/// A small least-recently-used cache of scaled pictures, so a list that is shown again does not fetch its covers again.
struct CarImageCache {
    private var order: [String] = []
    private var images: [String: UIImage] = [:]
    let capacity: Int

    init(capacity: Int = 64) { self.capacity = capacity }

    /// The picture for [key], marking it as the most recently used.
    mutating func image(for key: String) -> UIImage? {
        guard let image = images[key] else { return nil }
        touch(key)
        return image
    }

    mutating func store(_ image: UIImage, for key: String) {
        images[key] = image
        touch(key)
        while order.count > capacity {
            let oldest = order.removeFirst()
            images.removeValue(forKey: oldest)
        }
    }

    private mutating func touch(_ key: String) {
        if let i = order.firstIndex(of: key) { order.remove(at: i) }
        order.append(key)
    }

    mutating func clear() {
        order = []
        images = [:]
    }
}

/// The CarPlay audio app (decision 0048, I4c): a tab bar of Home, Up next, Library and Downloaded over `CarLibrary`, a tap that plays through
/// the one `Playback` the page drives, and the system's Now Playing screen on the lock-screen info and remote commands that already exist.
@MainActor
final class CarPlay: NSObject, CPTabBarTemplateDelegate {
    static let shared = CarPlay()
    private static let log = Logger(subsystem: "app.tendshelf.ios", category: "carplay")

    /// The tabs: node id, words key and the SF Symbol, in Android's order.
    static let tabs: [(node: String, words: String, symbol: String)] = [
        ("home", "car_home", "house"),
        ("queue", "car_up_next", "list.bullet"),
        ("library", "car_library", "books.vertical"),
        ("downloaded", "car_downloaded", "arrow.down.circle"),
    ]
    /// The pictures fetched for the first rows only.
    static let imageRows = 24

    private var controller: CPInterfaceController?
    private var tabTemplates: [CPListTemplate] = []
    private var images = CarImageCache()
    /// Overridden by tests; the real one reads what the shell holds.
    var library: () -> CarLibrary = { CarPlay.makeLibrary() }

    // MARK: Pure parts

    /// The rows as list models, at most [max] of them; the cut uses the shared `slice` rule.
    static func items(_ rows: [CarRow], max: Int) -> [CarItemModel] {
        let range = LibraryTreeKt.slice(total: Int32(rows.count), p: Page(page: 0, size: Int32(max)))
        guard range.first <= range.last else { return [] }
        return rows[Int(range.first)...Int(range.last)].map { row in
            switch row {
            case .folder(let id, let title):
                return CarItemModel(id: id, text: title, detail: nil, isFolder: true)
            case .book(let b):
                return CarItemModel(id: LibraryTreeKt.nodeId(n: NodeBook(id: b.id)), text: b.title, detail: b.author.isEmpty ? nil : b.author, isFolder: false)
            case .homeBook(let h):
                let detail: String
                if let series = h.series {
                    detail = words("car_series_line", "\(series.index)", "\(series.total)", series.name)
                } else {
                    detail = h.card.author
                }
                return CarItemModel(
                    id: LibraryTreeKt.nodeId(n: NodeBook(id: h.card.id)), text: h.card.title, detail: detail.isEmpty ? nil : detail,
                    isFolder: false, progress: CarHomeKt.homeProgress(b: h)?.doubleValue)
            case .episode(let e):
                let line = PodcastKt.episodeSubline(e: e)
                return CarItemModel(id: LibraryTreeKt.nodeId(n: NodeEpisode(id: e.id)), text: PodcastKt.episodeRowTitle(e: e), detail: line.isEmpty ? nil : line, isFolder: false)
            case .station(let s):
                let line = RadioKt.stationSubline(s: s)
                return CarItemModel(id: LibraryTreeKt.nodeId(n: NodeStation(id: s.id)), text: RadioKt.stationRowTitle(s: s), detail: line.isEmpty ? nil : line, isFolder: false)
            }
        }
    }

    /// The Home sections as list models: the shared `fitSections` cut (every section keeps two rows before any gets more) and the section cap.
    static func sectionModels(_ sections: [CarSection], maxItems: Int, maxSections: Int) -> [(String, [CarItemModel])] {
        let kept = Array(sections.filter { !$0.rows.isEmpty }.prefix(max(0, maxSections)))
        let sizes = CarHomeKt.fitSections(sizes: kept.map { KotlinInt(int: Int32($0.rows.count)) }, max: Int32(maxItems))
        return zip(kept, sizes).map { section, size in (section.title, items(section.rows, max: size.intValue)) }
    }

    /// The id the player starts for a leaf node id (`book:x`, `episode:x`, `station:x`), nil for a folder or a bad id.
    static func playable(_ nodeId: String) -> String? {
        LibraryTreeKt.playableId(n: LibraryTreeKt.parseNode(id: nodeId))
    }

    private static func makeLibrary() -> CarLibrary {
        let shell = ShellController.shared
        let store = TokenStore()
        let client = ServerClient()
        return CarLibrary(
            api: { PlayerApi(origin: $0, client: client) }, connection: { store.current() },
            offline: { shell.downloads.store.all() }, userId: { store.userId },
            coverData: { shell.downloads.coverData($0) })
    }

    // MARK: Scene

    /// The car screen connected: the tab bar is the root, and the first tab loads at once.
    func connect(_ interfaceController: CPInterfaceController) {
        controller = interfaceController
        images.clear()
        tabTemplates = Self.tabs.map { tab in
            let template = CPListTemplate(title: CarWords.text(tab.words), sections: [])
            template.tabTitle = CarWords.text(tab.words)
            template.tabImage = UIImage(systemName: tab.symbol) ?? UIImage()
            Self.spin(template)
            return template
        }
        let bar = CPTabBarTemplate(templates: tabTemplates)
        bar.delegate = self
        interfaceController.setRootTemplate(bar, animated: false, completion: nil)
        configureNowPlaying()
        fill(tabTemplates[0], node: Self.tabs[0].node)
    }

    func disconnect() {
        controller = nil
        tabTemplates = []
        images.clear()
    }

    /// Signed out on the phone: the car goes back to its root and the lists load again (they say "sign in" in words).
    func signedOut() {
        images.clear()
        guard let controller, !tabTemplates.isEmpty else { return }
        controller.popToRootTemplate(animated: false, completion: nil)
        for (i, template) in tabTemplates.enumerated() { fill(template, node: Self.tabs[i].node) }
    }

    nonisolated func tabBarTemplate(_ tabBarTemplate: CPTabBarTemplate, didSelect selectedTemplate: CPTemplate) {
        MainActor.assumeIsolated {
            guard let list = selectedTemplate as? CPListTemplate, let i = tabTemplates.firstIndex(of: list) else { return }
            fill(list, node: Self.tabs[i].node)
        }
    }

    // MARK: Lists

    /// The system's spinner while a list is empty and loading (iOS 18.4 and later; earlier ones show the empty view).
    private static func spin(_ template: CPListTemplate) {
        if #available(iOS 18.4, *) { template.showsSpinnerWhileEmpty = true }
    }

    /// Loads [node] and puts its rows (or its words) into [template].
    private func fill(_ template: CPListTemplate, node: String) {
        let library = library()
        Task { @MainActor in
            let list = await library.load(node)
            show(list, in: template)
        }
    }

    private func show(_ list: CarList, in template: CPListTemplate) {
        switch list {
        case .message(let text):
            template.emptyViewTitleVariants = [text]
            template.updateSections([])
        case .rows(let rows):
            let models = Self.items(rows, max: CPListTemplate.maximumItemCount)
            let listItems = zip(rows, models).map { row, model in listItem(row, model) }
            template.updateSections([CPListSection(items: listItems)])
            loadImages(Array(zip(rows, listItems).prefix(Self.imageRows)))
        case .sections(let sections):
            // Rows are cut the same way as the models, so the pairs line up; pictures go to the first rows across all sections.
            let kept = Array(sections.filter { !$0.rows.isEmpty }.prefix(max(0, CPListTemplate.maximumSectionCount)))
            let sizes = CarHomeKt.fitSections(sizes: kept.map { KotlinInt(int: Int32($0.rows.count)) }, max: Int32(CPListTemplate.maximumItemCount))
            let cut = zip(kept, sizes).map { CarSection(title: $0.title, rows: Array($0.rows.prefix($1.intValue))) }
            let models = Self.sectionModels(sections, maxItems: CPListTemplate.maximumItemCount, maxSections: CPListTemplate.maximumSectionCount)
            var pairs: [(CarRow, CPListItem)] = []
            var built: [CPListSection] = []
            for (section, shown) in zip(cut, models) {
                let items = zip(section.rows, shown.1).map { row, model in listItem(row, model) }
                pairs += zip(section.rows, items)
                built.append(CPListSection(items: items, header: section.title, sectionIndexTitle: nil))
            }
            template.updateSections(built)
            loadImages(Array(pairs.prefix(Self.imageRows)))
        }
    }

    private func listItem(_ row: CarRow, _ model: CarItemModel) -> CPListItem {
        let item = CPListItem(text: model.text, detailText: model.detail, image: nil, accessoryImage: nil, accessoryType: model.isFolder ? .disclosureIndicator : .none)
        if let p = model.progress { item.playbackProgress = CGFloat(p) }
        if let cached = images.image(for: Self.imageKey(row)) { item.setImage(cached) }
        item.handler = { [weak self] _, completion in
            MainActor.assumeIsolated {
                if model.isFolder {
                    self?.push(folder: model)
                    completion()
                } else {
                    self?.play(model.id)
                    completion()
                }
            }
        }
        return item
    }

    private func push(folder model: CarItemModel) {
        let template = CPListTemplate(title: model.text, sections: [])
        Self.spin(template)
        controller?.pushTemplate(template, animated: true, completion: nil)
        fill(template, node: model.id)
    }

    /// A tap on something that plays: the same `Playback` the page drives, then the system's Now Playing screen.
    private func play(_ nodeId: String) {
        guard let id = Self.playable(nodeId) else { return }
        ShellController.shared.playback.start(id, atMs: nil)
        guard let controller, !(controller.topTemplate is CPNowPlayingTemplate) else { return }
        controller.pushTemplate(CPNowPlayingTemplate.shared, animated: true, completion: nil)
    }

    // MARK: Pictures

    private static func imageKey(_ row: CarRow) -> String {
        switch row {
        case .folder(let id, _): return "folder:\(id)"
        case .book(let b): return "book:\(b.id)"
        case .homeBook(let h): return "book:\(h.card.id)"
        case .episode(let e): return "show:\(e.showId)"
        case .station(let s): return "station:\(s.id)"
        }
    }

    /// Covers and pictures for the first rows: from the server for the signed-in person, the stored cover when there is no network.
    private func loadImages(_ rows: [(CarRow, CPListItem)]) {
        let library = library()
        Task { @MainActor in
            for (row, item) in rows {
                let key = Self.imageKey(row)
                if images.image(for: key) != nil { continue }
                guard let data = await library.picture(for: row), let image = Self.scaled(data) else { continue }
                images.store(image, for: key)
                item.setImage(image)
            }
        }
    }

    /// The picture scaled down to what a CarPlay row shows.
    static func scaled(_ data: Data) -> UIImage? {
        guard let image = UIImage(data: data) else { return nil }
        let target = CPListItem.maximumImageSize
        guard image.size.width > 0, image.size.height > 0 else { return nil }
        let scale = min(target.width / image.size.width, target.height / image.size.height, 1)
        let size = CGSize(width: max(1, (image.size.width * scale).rounded()), height: max(1, (image.size.height * scale).rounded()))
        return UIGraphicsImageRenderer(size: size).image { _ in image.draw(in: CGRect(origin: .zero, size: size)) }
    }

    // MARK: Now Playing

    /// The system's Now Playing screen reads the lock-screen info and remote commands the player already keeps; the buttons are the rate only.
    private func configureNowPlaying() {
        let now = CPNowPlayingTemplate.shared
        now.isUpNextButtonEnabled = false
        now.isAlbumArtistButtonEnabled = false
        let rate = CPNowPlayingPlaybackRateButton { _ in
            MainActor.assumeIsolated {
                let playback = ShellController.shared.playback!
                playback.setSpeed(LibraryTreeKt.speedCycle(current: playback.snapshot().speed))
            }
        }
        now.updateNowPlayingButtons([rate])
    }
}
