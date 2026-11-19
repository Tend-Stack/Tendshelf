import XCTest
@testable import Tendshelf

@MainActor
final class OfflinePathsTests: XCTestCase {
    private func temp() -> OfflinePaths {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent("paths-\(UUID().uuidString)", isDirectory: true)
        addTeardownBlock { try? FileManager.default.removeItem(at: root) }
        return OfflinePaths(root: root.appendingPathComponent("offline", isDirectory: true))
    }

    func testPrepareExcludesFromBackupAndProtects() throws {
        let p = temp()
        try p.prepare()
        let values = try p.root.resourceValues(forKeys: [.isExcludedFromBackupKey])
        XCTAssertEqual(values.isExcludedFromBackup, true)
        let attributes = try FileManager.default.attributesOfItem(atPath: p.root.path)
        // The simulator may not report a protection class; where it does, it is the first-unlock one.
        if let protection = attributes[.protectionKey] as? FileProtectionType {
            XCTAssertEqual(protection, .completeUntilFirstUserAuthentication)
        }
    }

    func testPathsAreInsideTheRootForGoodIds() {
        let p = temp()
        XCTAssertEqual(p.audioFile("bk_1", "f_1.mp3")?.path, p.root.path + "/audio/bk_1/f_1.mp3")
        XCTAssertEqual(p.cover("bk_1")?.lastPathComponent, "bk_1.jpg")
        XCTAssertEqual(p.chapters("pe_1")?.lastPathComponent, "pe_1.json")
        XCTAssertEqual(p.text("bk_1")?.lastPathComponent, "bk_1")
        XCTAssertEqual(p.database.lastPathComponent, "offline.sqlite")
    }

    func testBadIdsAndNamesAreRefused() {
        let p = temp()
        for id in ["../x", "a/b", "", "a b", "x.y", String(repeating: "a", count: 65)] {
            XCTAssertNil(p.audio(id), id)
            XCTAssertNil(p.cover(id), id)
            XCTAssertNil(p.chapters(id), id)
            XCTAssertNil(p.text(id), id)
        }
        for name in ["", "a/b", "../x", "..", "."] {
            XCTAssertNil(p.audioFile("bk_1", name), name)
        }
    }
}
