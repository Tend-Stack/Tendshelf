import XCTest
@preconcurrency import TendshelfShared
@testable import Tendshelf

/// The shared Kotlin module, called from Swift the way the app calls it.
@MainActor
final class SharedFrameworkTests: XCTestCase {
    func testVerifierIs43Characters() {
        let v = PkceSwift.newVerifier()
        XCTAssertEqual(v.count, 43)
        XCTAssertNotEqual(v, PkceSwift.newVerifier())
    }

    func testChallengeOfRfc7636AppendixB() {
        XCTAssertEqual(
            PkceSwift.challenge(for: "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"),
            "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
        )
    }

    func testOriginParse() {
        let o = Origin.companion.parse(input: "http://Tendshelf.home:8095/")
        XCTAssertEqual(o?.text, "http://tendshelf.home:8095")
        XCTAssertEqual(o?.plainHttp, true)
        XCTAssertNil(Origin.companion.parse(input: "tendshelf.home"))
        XCTAssertNil(Origin.companion.parse(input: "https://a.example/path"))
    }

    func testCandidates() {
        XCTAssertEqual(Origin.companion.candidates(typed: "tendshelf.home").map(\.text), ["https://tendshelf.home", "http://tendshelf.home"])
        XCTAssertEqual(Origin.companion.candidates(typed: "http://10.0.0.2:8095").map(\.text), ["http://10.0.0.2:8095"])
        XCTAssertTrue(Origin.companion.candidates(typed: "a b").isEmpty)
    }

    private let server = Origin.companion.parse(input: "https://books.example")!

    private func decide(_ url: String, mainFrame: Bool = true, pending: Bool = false, fromBundled: Bool) -> Nav {
        NavigationKt.decide(
            url: url, mainFrame: mainFrame, origin: server, codePending: pending, fromBundled: fromBundled,
            signInOrigins: [], bundled: BundledSite.companion.IOS, reader: nil
        )
    }

    func testBundledCommandFromBundledPage() {
        let nav = decide("tsbundle://app/connect?address=books.example", fromBundled: true)
        XCTAssertEqual((nav as? NavConnect)?.address, "books.example")
    }

    func testBundledCommandRefusedFromServerPage() {
        XCTAssertTrue(decide("tsbundle://app/connect?address=evil.example", fromBundled: false) is NavBlock)
        XCTAssertTrue(decide("tsbundle://app/connect?address=evil.example", mainFrame: false, fromBundled: true) is NavBlock)
    }

    func testConnectDoneWithPendingCode() {
        let nav = decide("https://books.example/app/connect/done?code=abc123", pending: true, fromBundled: false)
        XCTAssertEqual((nav as? NavTakeCode)?.code, "abc123")
    }

    func testConnectDoneWithoutPendingCode() {
        XCTAssertTrue(decide("https://books.example/app/connect/done?code=abc123", pending: false, fromBundled: false) is NavBlock)
    }

    func testOtherAddressLeavesTheWindow() {
        XCTAssertTrue(decide("https://elsewhere.example/", fromBundled: false) is NavExternal)
        XCTAssertTrue(decide("https://elsewhere.example/", mainFrame: false, fromBundled: false) is NavBlock)
    }
}
