import XCTest
@preconcurrency import TendshelfShared
@testable import Tendshelf

@MainActor
final class TokenStoreTests: XCTestCase {
    private var suite = ""
    private var service = ""
    private var defaults: UserDefaults!

    override func setUp() {
        let id = UUID().uuidString
        suite = "tendshelf.tests.\(id)"
        service = "app.tendshelf.ios.tests.\(id)"
        defaults = UserDefaults(suiteName: suite)
        let (suite, service) = (self.suite, self.service)
        addTeardownBlock {
            let d = UserDefaults(suiteName: suite)!
            TokenStore(defaults: d, service: service).clearToken()
            d.removePersistentDomain(forName: suite)
        }
    }

    private func store() -> TokenStore { TokenStore(defaults: defaults, service: service) }

    func testRoundTrip() {
        let s = store()
        let o = Origin.companion.parse(input: "https://books.example")!
        s.origin = o
        XCTAssertNil(s.current())
        XCTAssertFalse(s.hasToken())
        s.save(token: "tok-1", deviceId: "dev-1", userId: "usr-1")
        XCTAssertTrue(s.hasToken())
        let c = s.current()
        XCTAssertEqual(c?.token, "tok-1")
        XCTAssertEqual(c?.deviceId, "dev-1")
        XCTAssertEqual(c?.origin, o)
        XCTAssertEqual(s.userId, "usr-1")
        // A second store over the same storage sees the same token.
        XCTAssertEqual(store().current()?.token, "tok-1")
    }

    func testTokenIsNotInDefaults() {
        let s = store()
        s.origin = Origin.companion.parse(input: "https://books.example")
        s.save(token: "secret-token", deviceId: "d", userId: "u")
        let dump = defaults.persistentDomain(forName: suite).map { "\($0)" } ?? ""
        XCTAssertFalse(dump.contains("secret-token"))
    }

    func testReinstallMarkDeletesTheItem() {
        let first = store()
        first.origin = Origin.companion.parse(input: "https://books.example")
        first.save(token: "old", deviceId: "d", userId: "u")
        // A reinstall: the defaults are gone, the Keychain item stays.
        defaults.removePersistentDomain(forName: suite)
        let fresh = store()
        fresh.origin = Origin.companion.parse(input: "https://books.example")
        XCTAssertFalse(fresh.hasToken())
        XCTAssertNil(fresh.current())
        XCTAssertNotNil(defaults.object(forKey: "installed"))
    }

    func testClearToken() {
        let s = store()
        s.origin = Origin.companion.parse(input: "https://books.example")
        s.save(token: "tok", deviceId: "d", userId: "u")
        s.clearToken()
        XCTAssertFalse(s.hasToken())
        XCTAssertNil(s.current())
        XCTAssertNil(s.userId)
        // The address stays, so the start page can fill it in.
        XCTAssertEqual(s.origin?.text, "https://books.example")
    }

    func testHttpWarnedPerOrigin() {
        let s = store()
        let a = Origin.companion.parse(input: "http://10.0.0.2:8095")!
        let b = Origin.companion.parse(input: "http://10.0.0.3:8095")!
        XCTAssertFalse(s.httpWarned(a))
        s.markHttpWarned(a)
        XCTAssertTrue(s.httpWarned(a))
        XCTAssertFalse(s.httpWarned(b))
    }
}
