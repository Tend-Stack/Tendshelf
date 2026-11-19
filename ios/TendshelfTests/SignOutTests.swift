import XCTest
@preconcurrency import TendshelfShared
@testable import Tendshelf

@MainActor
final class SignOutTests: XCTestCase {
    func testTokenIsGoneBeforeRevokeRuns() async {
        let r = Rig(self)
        r.signedIn()
        var sawToken: Bool?
        var sawResets = -1
        r.api.onRevoke = {
            sawToken = r.store.hasToken() || r.store.current() != nil
            sawResets = r.host.resets
        }
        r.signOut.run(reason: "bye")
        XCTAssertFalse(r.store.hasToken(), "the token goes before anything is awaited")
        await eventually("revoke") { r.api.revokes == ["tok-1"] }
        XCTAssertEqual(sawToken, false)
        XCTAssertEqual(sawResets, 1, "web data is wiped before the revoke")
    }

    func testStartPageShowsTheAddressAndTheReason() async {
        let r = Rig(self)
        r.signedIn()
        r.signOut.run(reason: "bye")
        await eventually("start") { r.host.loads.last == BundledPages.startUrl }
        XCTAssertEqual(r.pages.state, StartState(address: r.origin.text, note: "bye"))
        XCTAssertEqual(r.store.origin, r.origin, "the address is kept")
        XCTAssertNil(r.store.current())
        XCTAssertNil(r.store.userId)
    }

    func testFlowIsResetAndDoesNotReconnectByItself() async {
        let r = Rig(self)
        r.signedIn()
        r.flow.beginConnect(r.origin)
        XCTAssertTrue(r.flow.codePending)
        r.signOut.run(reason: nil)
        XCTAssertFalse(r.flow.codePending)
        XCTAssertEqual(r.flow.state, .noserver)
        await eventually("start") { r.host.loads.last == BundledPages.startUrl }
        let loads = r.host.loads.count
        r.flow.pageFinished(r.origin.text + "/anything")
        XCTAssertEqual(r.host.loads.count, loads, "a late page does not start a connect")
    }

    func testLogoutSeenIsClearedByARun() async {
        let r = Rig(self)
        r.signedIn()
        r.signOut.logoutSeen = true
        r.signOut.run(reason: nil)
        XCTAssertFalse(r.signOut.logoutSeen)
    }

    func testNoRevokeWhenThereWasNoToken() async {
        let r = Rig(self)
        r.store.origin = r.origin
        r.signOut.run(reason: nil)
        await eventually("start") { r.host.loads.last == BundledPages.startUrl }
        XCTAssertEqual(r.api.revokes, [])
    }
}
