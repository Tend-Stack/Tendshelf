import XCTest
@preconcurrency import TendshelfShared
@testable import Tendshelf

@MainActor
final class ConnectFlowTests: XCTestCase {
    private let token43 = String(repeating: "a", count: 43)

    private func link(_ kind: String) -> URL {
        URL(string: "tendshelf://open?link=https://books.example/\(kind)/\(token43)")!
    }

    func testCodeIsTakenOnlyWhilePending() async {
        let r = Rig(self)
        r.store.origin = r.origin
        r.api.swapAnswer = SwapExpired.shared
        XCTAssertFalse(r.flow.codePending)
        r.flow.onCode("code-0")
        try? await Task.sleep(nanoseconds: 50_000_000)
        XCTAssertEqual(r.api.swaps.count, 0)
        r.flow.beginConnect(r.origin)
        XCTAssertTrue(r.flow.codePending)
        r.flow.onCode("code-1")
        await eventually("swap") { r.api.swaps.count == 1 }
        XCTAssertEqual(r.api.swaps[0].code, "code-1")
    }

    func testVerifierIsClearedAfterOneSwapAndMatchesTheChallenge() async throws {
        let r = Rig(self)
        r.store.origin = r.origin
        r.api.swapAnswer = SwapExpired.shared
        r.flow.beginConnect(r.origin)
        let connect = r.host.loads.last!.absoluteString
        r.flow.onCode("c1")
        XCTAssertFalse(r.flow.codePending)
        r.flow.onCode("c2")
        await eventually("swap") { r.api.swaps.count == 1 }
        try? await Task.sleep(nanoseconds: 50_000_000)
        XCTAssertEqual(r.api.swaps.count, 1)
        XCTAssertEqual(r.api.swaps[0].verifier.count, 43)
        XCTAssertTrue(connect.contains(PkceSwift.challenge(for: r.api.swaps[0].verifier)), connect)
        XCTAssertNil(r.api.swaps[0].bearer)
    }

    func testDoneSavesTheTokenAndOpensHomeAsIos() async {
        let r = Rig(self)
        r.store.origin = r.origin
        r.api.swapAnswer = SwapDone(answer: SwapAnswer(token: "tok-new", deviceId: "dev-9", userId: "usr-9"))
        r.flow.beginConnect(r.origin)
        r.flow.onCode("c")
        await eventually("home") { r.store.hasToken() && r.host.loads.last?.absoluteString.contains("shell=ios") == true }
        XCTAssertEqual(r.store.current()?.token, "tok-new")
        XCTAssertEqual(r.store.current()?.deviceId, "dev-9")
        XCTAssertEqual(r.flow.state, .connected)
    }

    func testConflictWithAHeldTokenSignsOut() async {
        let r = Rig(self)
        r.signedIn()
        r.api.swapAnswer = SwapConflict.shared
        r.flow.beginConnect(r.origin)
        r.flow.onCode("c")
        await eventually("signed out") { !r.store.hasToken() && r.host.loads.last == BundledPages.startUrl }
        XCTAssertEqual(r.pages.state.note, words(Words.Key.otherPerson))
        XCTAssertEqual(r.pages.state.address, r.origin.text)
        XCTAssertEqual(r.api.swaps[0].bearer, "tok-1")
        await eventually("revoke") { r.api.revokes == ["tok-1"] }
    }

    func testExpiredShowsTheStartPage() async {
        let r = Rig(self)
        r.store.origin = r.origin
        r.api.swapAnswer = SwapExpired.shared
        r.flow.beginConnect(r.origin)
        r.flow.onCode("c")
        await eventually("start") { r.host.loads.last == BundledPages.startUrl }
        XCTAssertEqual(r.pages.state.note, words(Words.Key.expired))
    }

    func testLaunchWithRevokedTokenSignsOut() async {
        let r = Rig(self)
        r.signedIn()
        r.api.meAnswer = MeUnauthorized.shared
        r.flow.launch()
        await eventually("signed out") { !r.store.hasToken() }
        await eventually("start page") { r.host.loads.last == BundledPages.startUrl }
        XCTAssertEqual(r.pages.state.note, words(Words.Key.signedOut))
    }

    func testLaunchWithNetworkFailureKeepsThePerson() async {
        let r = Rig(self)
        r.signedIn()
        r.api.meAnswer = MeUnreachable.shared
        r.flow.launch()
        await eventually("me asked") { r.api.meCalls == 1 }
        try? await Task.sleep(nanoseconds: 50_000_000)
        XCTAssertTrue(r.store.hasToken())
        XCTAssertEqual(r.flow.state, .connected)
        XCTAssertEqual(r.api.revokes, [])
        XCTAssertTrue(r.host.loads.last?.absoluteString.contains("shell=ios") == true)
    }

    func testLoginPageWithATokenChecksMeAndSignsOutOn401() async {
        let r = Rig(self)
        r.signedIn()
        r.api.meAnswer = MeUnauthorized.shared
        r.flow.pageFinished(r.origin.text + "/login")
        await eventually("signed out") { !r.store.hasToken() }
    }

    func testChangeOfServerAsksFirst() async {
        let r = Rig(self)
        r.signedIn()
        r.host.confirmAnswer = false
        r.flow.onAddress("https://other.example")
        await eventually("question") { r.host.confirms.count == 1 }
        XCTAssertEqual(r.host.confirms[0].title, words(Words.Key.changeServerTitle))
        XCTAssertTrue(r.store.hasToken(), "cancel keeps the sign-in")
        XCTAssertEqual(r.store.origin, r.origin)
    }

    func testChangeOfServerConfirmedRevokesTheOldDeviceAndStartsTheNewSignIn() async {
        let r = Rig(self)
        r.signedIn()
        r.flow.onAddress("https://other.example")
        await eventually("new sign-in") { r.host.loads.last?.absoluteString.contains("other.example") == true }
        XCTAssertFalse(r.store.hasToken())
        XCTAssertEqual(r.store.origin?.text, "https://other.example")
        XCTAssertEqual(r.host.resets, 1)
        await eventually("revoke") { r.api.revokes == ["tok-1"] }
    }

    func testNestedTendshelfLinkIsRefused() async {
        let r = Rig(self)
        let inner = "tendshelf://open?link=https://books.example/invite/\(token43)"
        let encoded = inner.addingPercentEncoding(withAllowedCharacters: .alphanumerics)!
        r.flow.openLink(URL(string: "tendshelf://open?link=\(encoded)")!)
        XCTAssertEqual(r.host.confirms.count, 0)
        XCTAssertEqual(r.api.healthCalls, 0)
        XCTAssertEqual(r.host.loads.last, BundledPages.joinUrl)
        XCTAssertEqual(r.pages.joinState.note, words(Words.Key.linkOnly))
    }

    func testOtherSchemesAndHostsAreIgnored() async {
        let r = Rig(self)
        r.flow.openLink(URL(string: "https://books.example/invite/\(token43)")!)
        r.flow.openLink(URL(string: "tendshelf://elsewhere?link=https://books.example/invite/\(token43)")!)
        XCTAssertEqual(r.host.confirms.count, 0)
        XCTAssertEqual(r.host.loads.count, 0)
    }

    func testAnInvitationLinkAsksBeforeAnyRequest() async {
        let r = Rig(self)
        r.host.confirmAnswer = false
        r.flow.openLink(link("invite"))
        XCTAssertEqual(r.host.confirms.count, 1)
        XCTAssertTrue(r.host.confirms[0].message.contains("https://books.example"))
        try? await Task.sleep(nanoseconds: 50_000_000)
        XCTAssertEqual(r.api.healthCalls, 0)
    }

    func testAnInvitationLinkConfirmedOpensTheInvitationPage() async {
        let r = Rig(self)
        r.flow.openLink(link("invite"))
        await eventually("invite page") { r.host.loads.last?.absoluteString.contains("invite") == true }
        XCTAssertTrue(r.host.loads.last!.absoluteString.contains("shell=ios"))
        XCTAssertFalse(r.flow.codePending)
    }

    func testAPairingLinkHoldsAVerifierAndTheSameLinkAgainIsIgnored() async {
        let r = Rig(self)
        r.flow.openLink(link("pair"))
        await eventually("pair page") { r.host.loads.last?.absoluteString.contains("pair") == true }
        XCTAssertTrue(r.flow.codePending)
        let asked = r.host.confirms.count
        r.flow.openLink(link("pair"))
        XCTAssertEqual(r.host.confirms.count, asked)
    }

    func testALinkToTheConnectedServerIsRefusedPolitely() async {
        let r = Rig(self)
        r.signedIn()
        r.flow.openLink(link("invite"))
        XCTAssertEqual(r.host.notices, [words(Words.Key.linkConnected)])
        XCTAssertEqual(r.host.confirms.count, 0)
    }
}
