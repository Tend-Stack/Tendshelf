import XCTest
@preconcurrency import TendshelfShared
@testable import Tendshelf

/// A window that records what the flow asked of it.
@MainActor
final class FakeHost: ShellHost {
    var loads: [URL] = []
    var confirms: [(title: String, message: String)] = []
    var notices: [String] = []
    var resets = 0
    var confirmAnswer = true

    func load(_ url: URL) { loads.append(url) }

    func confirm(title: String, message: String, ok: String, cancel: String, completion: @escaping (Bool) -> Void) {
        confirms.append((title, message))
        completion(confirmAnswer)
    }

    func notice(_ message: String) { notices.append(message) }
    func resetWeb() async { resets += 1 }
}

/// A server with scripted answers; records the calls.
@MainActor
final class FakeAPI: ServerAPI {
    var healthAnswer = Health.ok
    var swapAnswer: Swap = SwapUnreachable.shared
    var meAnswer: Me = MeOk(userId: "usr-1")
    var healthCalls = 0
    var swaps: [(code: String, verifier: String, bearer: String?)] = []
    var meCalls = 0
    var revokes: [String] = []
    /// Runs when a revoke arrives: tests that check the order of events look at the store here.
    var onRevoke: (() -> Void)?

    func health(_ origin: Origin) async -> Health {
        healthCalls += 1
        return healthAnswer
    }

    func identity(_ origin: Origin) async -> Identity? { nil }
    func signInOriginsBody(_ origin: Origin) async -> String? { nil }

    func swap(_ origin: Origin, code: String, verifier: String, name: String, bearer: String?) async -> Swap {
        swaps.append((code, verifier, bearer))
        return swapAnswer
    }

    func me(_ origin: Origin, token: String) async -> Me {
        meCalls += 1
        return meAnswer
    }

    func revoke(_ origin: Origin, token: String) async {
        revokes.append(token)
        onRevoke?()
    }
}

/// A flow with its own Keychain item and defaults suite, removed at teardown.
@MainActor
final class Rig {
    let store: TokenStore
    let host = FakeHost()
    let api = FakeAPI()
    let pages = BundledPages()
    let signOut: SignOut
    let flow: ConnectFlow
    let origin = Origin.companion.parse(input: "https://books.example")!

    init(_ test: XCTestCase) {
        let id = UUID().uuidString
        let suite = "tendshelf.tests.\(id)"
        let service = "app.tendshelf.ios.tests.\(id)"
        store = TokenStore(defaults: UserDefaults(suiteName: suite)!, service: service)
        test.addTeardownBlock {
            let d = UserDefaults(suiteName: suite)!
            TokenStore(defaults: d, service: service).clearToken()
            d.removePersistentDomain(forName: suite)
        }
        signOut = SignOut(store: store, api: api, host: host, pages: pages)
        flow = ConnectFlow(store: store, client: api, pages: pages, host: host, signOut: signOut, deviceName: "iPhone")
    }

    /// Signed in to [origin] as device dev-1.
    func signedIn() {
        store.origin = origin
        store.save(token: "tok-1", deviceId: "dev-1", userId: "usr-1")
    }
}

/// Waits for a condition that a `Task` in the code under test will make true.
@MainActor
func eventually(_ message: String = "", file: StaticString = #filePath, line: UInt = #line, _ condition: () -> Bool) async {
    for _ in 0..<200 {
        if condition() { return }
        try? await Task.sleep(nanoseconds: 10_000_000)
    }
    XCTFail("timed out: \(message)", file: file, line: line)
}
