import CarPlay
import XCTest
@preconcurrency import TendshelfShared
@testable import Tendshelf

@MainActor
final class CarPlayItemsTests: XCTestCase {
    func testModelsPerRowKind() {
        let episode = EpisodeCard(
            id: "pe_1", showId: "sh", title: " Pilot ", show: "The Show", durationMs: 60_000, publishedAt: 0, positionMs: 0, played: false, favourite: false)
        let station = StationCard(id: "rs_1", name: "Jazz FM", codec: "mp3", genre: "Jazz", country: "US", favourite: true, lastPlayedAt: 0, problem: "", artUrl: "")
        let rows: [CarRow] = [
            .folder(id: "shelf:s1", title: "Mine"),
            .book(BookCard(id: "b1", title: "Dune", author: "Herbert", durationMs: 1, hasCover: true)),
            .book(BookCard(id: "b2", title: "Anon", author: "", durationMs: 1, hasCover: false)),
            .episode(episode),
            .station(station),
        ]
        let models = CarPlay.items(rows, max: 10)
        XCTAssertEqual(models[0], CarItemModel(id: "shelf:s1", text: "Mine", detail: nil, isFolder: true))
        XCTAssertEqual(models[1], CarItemModel(id: "book:b1", text: "Dune", detail: "Herbert", isFolder: false))
        XCTAssertEqual(models[2].detail, nil)
        XCTAssertEqual(models[3].id, "episode:pe_1")
        XCTAssertEqual(models[3].text, "Pilot")
        XCTAssertEqual(models[3].detail, PodcastKt.episodeSubline(e: episode))
        XCTAssertEqual(models[4], CarItemModel(id: "station:rs_1", text: "Jazz FM", detail: RadioKt.stationSubline(s: station), isFolder: false))
    }

    func testTabsAreHomeUpNextLibraryDownloaded() {
        XCTAssertEqual(CarPlay.tabs.map { $0.node }, ["home", "queue", "library", "downloaded"])
        XCTAssertEqual(CarPlay.tabs.map { $0.words }, ["car_home", "car_up_next", "car_library", "car_downloaded"])
    }

    func testHomeBookDetailAndProgress() {
        let card = BookCard(id: "b1", title: "Dune", author: "Herbert", durationMs: 60 * 60_000, hasCover: false)
        let series = HomeBook(card: card, positionMs: 30 * 60_000, series: SeriesNote(name: "Saga", index: 2, total: 5))
        let plain = HomeBook(card: card, positionMs: 0, series: nil)
        let models = CarPlay.items([.homeBook(series), .homeBook(plain)], max: 10)
        XCTAssertEqual(models[0].detail, "Book 2 of 5 \u{B7} Saga")
        XCTAssertEqual(models[0].progress, 0.5)
        XCTAssertEqual(models[1].detail, "Herbert")
        XCTAssertNil(models[1].progress)
    }

    func testSectionModelsFitTheCar() {
        let sizes = [6, 4, 8, 4, 4]
        let sections = sizes.enumerated().map { i, n in
            CarSection(title: "S\(i)", rows: (0..<n).map { .folder(id: "f\(i)-\($0)", title: "t") })
        }
        XCTAssertEqual(CarPlay.sectionModels(sections, maxItems: 12, maxSections: 5).map { $0.1.count }, [4, 2, 2, 2, 2])
        XCTAssertEqual(CarPlay.sectionModels(sections, maxItems: 26, maxSections: 2).map { $0.0 }, ["S0", "S1"])
    }

    func testTheListIsCapped() {
        let rows = (0..<30).map { CarRow.folder(id: "shelf:\($0)", title: "S\($0)") }
        XCTAssertEqual(CarPlay.items(rows, max: 12).count, 12)
        XCTAssertEqual(CarPlay.items(rows, max: 100).count, 30)
        XCTAssertEqual(CarPlay.items([], max: 12).count, 0)
        XCTAssertGreaterThan(CPListTemplate.maximumItemCount, 0)
    }

    func testTapsPlayOnlyLeaves() {
        XCTAssertEqual(CarPlay.playable("book:b1"), "b1")
        XCTAssertEqual(CarPlay.playable("episode:pe_1"), "pe_1")
        XCTAssertEqual(CarPlay.playable("station:rs_1"), "rs_1")
        XCTAssertNil(CarPlay.playable("shelf:s1"))
        XCTAssertNil(CarPlay.playable("continue"))
        XCTAssertNil(CarPlay.playable("nope"))
    }

    func testTheImageCacheKeepsTheLatest64() {
        var cache = CarImageCache(capacity: 64)
        let image = UIGraphicsImageRenderer(size: CGSize(width: 2, height: 2)).image { _ in }
        for i in 0..<70 { cache.store(image, for: "k\(i)") }
        XCTAssertNil(cache.image(for: "k0"))
        XCTAssertNil(cache.image(for: "k5"))
        XCTAssertNotNil(cache.image(for: "k6"))
        XCTAssertNotNil(cache.image(for: "k69"))
        _ = cache.image(for: "k6") // used: it stays when the next one comes
        cache.store(image, for: "new")
        XCTAssertNotNil(cache.image(for: "k6"))
        XCTAssertNil(cache.image(for: "k7"))
    }

    func testPicturesAreScaledToTheRowSize() {
        let big = UIGraphicsImageRenderer(size: CGSize(width: 2000, height: 1000)).pngData { ctx in
            UIColor.red.setFill()
            ctx.fill(CGRect(x: 0, y: 0, width: 2000, height: 1000))
        }
        let scaled = CarPlay.scaled(big)
        XCTAssertNotNil(scaled)
        XCTAssertLessThanOrEqual(scaled!.size.width, CPListItem.maximumImageSize.width)
        XCTAssertNil(CarPlay.scaled(Data([1, 2, 3])))
    }

    func testTheCarPlaySceneIsDeclaredAndItsClassExists() {
        let scenes = Bundle.main.object(forInfoDictionaryKey: "UIApplicationSceneManifest") as? [String: Any]
        let manifest = scenes?["UISceneConfigurations"] as? [String: [[String: Any]]]
        let entry = manifest?["CPTemplateApplicationSceneSessionRoleApplication"]?.first
        XCTAssertEqual(entry?["UISceneConfigurationName"] as? String, "CarPlay")
        let name = entry?["UISceneDelegateClassName"] as? String
        XCTAssertNotNil(name)
        XCTAssertNotNil(NSClassFromString(name ?? ""), "the scene delegate class named in Info.plist must exist")
        XCTAssertNil(manifest?["UIWindowSceneSessionRoleApplication"], "the SwiftUI phone window keeps its default scene")
    }
}
