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

    func testBeforeWipeGetsTheConnectionWhileTheTokenIsStillThere() async {
        let r = Rig(self)
        r.signedIn()
        var seen: Connection?
        var tokenPresent = false
        r.signOut.beforeWipe = { conn in
            seen = conn
            tokenPresent = r.store.current() != nil
        }
        r.signOut.run(reason: nil)
        XCTAssertEqual(seen?.token, "tok-1")
        XCTAssertEqual(seen?.deviceId, "dev-1")
        XCTAssertTrue(tokenPresent, "the hook runs before the token is cleared")
        XCTAssertNil(r.store.current())
    }

    /// The order the app composes in `ShellController`: the player stops, the downloads are wiped, and only then the revoke goes.
    func testTheDownloadsWipeRunsAfterThePlayerStopsAndBeforeTheRevoke() async {
        let r = Rig(self)
        r.signedIn()
        let d = DownloadsRig(self)
        await d.downloads.add(DownloadsRig.book)
        XCTAssertEqual(d.store.all().count, 1)
        var order: [String] = []
        var wipe: Task<Void, Never>?
        r.signOut.beforeWipe = { _ in
            order.append("player")
            wipe = d.downloads.wipe()
            order.append("wipe")
            XCTAssertTrue(d.store.all().isEmpty, "the rows go at once")
        }
        r.api.onRevoke = { order.append("revoke") }
        r.signOut.run(reason: nil)
        await eventually("revoke") { r.api.revokes == ["tok-1"] }
        await wipe?.value
        XCTAssertEqual(order, ["player", "wipe", "revoke"])
        XCTAssertEqual(d.transport.cancelledAll, 1)
        XCTAssertFalse(FileManager.default.fileExists(atPath: d.paths.root.path))
    }

    func testLeavingAServerAlsoRunsTheHook() async {
        let r = Rig(self)
        r.signedIn()
        var seen: String?
        r.signOut.beforeWipe = { seen = $0?.token }
        r.signOut.leave(oldOrigin: r.origin) {}
        XCTAssertEqual(seen, "tok-1")
    }
}

/// What sign-out does to the outbox: the last rows are sent once with the connection that is going, then every row goes.
@MainActor
final class SignOutOutboxTests: XCTestCase {
    func testRowsAreFlushedOnceWithTheCapturedConnectionThenCleared() async {
        let r = WriterRig(self)
        let captured = r.conn
        r.api.batchError = ApiError(status: 0)
        r.writer.saveNow("tick")
        await eventually("first try") { r.api.batches.count == 1 }
        r.api.batchError = nil
        r.conn = nil // the token is already gone when the flush runs
        await r.writer.flushAndClear(connection: captured)
        XCTAssertEqual(r.api.batches.count, 2, "one more try, with the held connection")
        XCTAssertEqual(r.api.tokens.last, "tok-1")
        XCTAssertEqual(r.store.count(), 0, "and nothing is left")
    }

    func testAFailedFlushStillClears() async {
        let r = WriterRig(self)
        let captured = r.conn
        r.api.batchError = ApiError(status: 0)
        r.writer.saveNow("tick")
        await r.writer.flushAndClear(connection: captured)
        XCTAssertEqual(r.store.count(), 0)
    }

    func testRowsOfAnotherDeviceAreNeverSent() async {
        let r = WriterRig(self)
        r.api.batchError = ApiError(status: 0)
        r.writer.saveNow("tick")
        await eventually("try") { r.api.batches.count == 1 }
        r.api.batchError = nil
        let other = Connection(origin: r.conn!.origin, token: "tok-9", deviceId: "dev-9")
        await r.writer.flushAndClear(connection: other)
        XCTAssertEqual(r.api.batches.count, 1)
        XCTAssertEqual(r.store.count(), 0)
    }

    func testNoFlushIsStartedWhileClosing() async {
        let r = WriterRig(self)
        let captured = r.conn
        r.writer.saveNow("tick")
        await r.writer.flushAndClear(connection: captured)
        r.writer.flush()
        await settle(60)
        XCTAssertEqual(r.store.count(), 0)
    }
}
