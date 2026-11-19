import XCTest
@preconcurrency import TendshelfShared
@testable import Tendshelf

/// A stand-in for the network: records every request and answers from a closure.
final class StubProtocol: URLProtocol, @unchecked Sendable {
    nonisolated(unsafe) static var seen: [URLRequest] = []
    nonisolated(unsafe) static var handler: ((URLRequest) -> (HTTPURLResponse, Data))?

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        Self.seen.append(request)
        guard let (response, data) = Self.handler?(request) else {
            client?.urlProtocol(self, didFailWithError: URLError(.cannotConnectToHost))
            return
        }
        if (300..<400).contains(response.statusCode), let location = response.value(forHTTPHeaderField: "Location"), let target = URL(string: location) {
            // Like a real stack: offer the redirect, and if the delegate refuses it the 3xx itself is the answer.
            client?.urlProtocol(self, wasRedirectedTo: URLRequest(url: target), redirectResponse: response)
        }
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: data)
        client?.urlProtocolDidFinishLoading(self)
    }

    override func stopLoading() {}
}

@MainActor
final class ServerClientTests: XCTestCase {
    private let origin = Origin.companion.parse(input: "https://books.example")!

    override func setUp() {
        StubProtocol.seen = []
        StubProtocol.handler = nil
    }

    private func reply(_ url: URL, _ status: Int, headers: [String: String] = [:]) -> HTTPURLResponse {
        HTTPURLResponse(url: url, statusCode: status, httpVersion: "HTTP/1.1", headerFields: headers)!
    }

    func testRedirectIsRefused() async {
        StubProtocol.handler = { [self] req in
            if req.url?.path == "/healthz" {
                return (reply(req.url!, 302, headers: ["Location": "https://evil.example/healthz"]), Data())
            }
            return (reply(req.url!, 200), Data("{\"status\":\"ok\"}".utf8))
        }
        let client = ServerClient(protocolClasses: [StubProtocol.self])
        let r = await client.call(origin.healthUrl(), "GET", nil, nil, 5)
        XCTAssertEqual(r?.status, 302)
        XCTAssertFalse(StubProtocol.seen.contains { $0.url?.host == "evil.example" })
        let health = await client.health(origin)
        XCTAssertEqual(health, .nottendshelf)
    }

    func testBodyIsCutAt4096Bytes() async {
        StubProtocol.handler = { [self] req in
            (reply(req.url!, 200), Data(repeating: UInt8(ascii: "a"), count: 4097))
        }
        let client = ServerClient(protocolClasses: [StubProtocol.self])
        let r = await client.call(origin.healthUrl(), "GET", nil, nil, 5)
        XCTAssertEqual(r?.body.utf8.count, 4096)
        XCTAssertEqual(r?.truncated, true)
    }

    func testBodyOfExactlyTheLimitIsNotCut() async {
        StubProtocol.handler = { [self] req in
            (reply(req.url!, 200), Data(repeating: UInt8(ascii: "a"), count: 4096))
        }
        let client = ServerClient(protocolClasses: [StubProtocol.self])
        let r = await client.call(origin.healthUrl(), "GET", nil, nil, 5)
        XCTAssertEqual(r?.body.utf8.count, 4096)
        XCTAssertEqual(r?.truncated, false)
    }

    func testNoCookieHeaderIsSent() async {
        StubProtocol.handler = { [self] req in (reply(req.url!, 200), Data("{}".utf8)) }
        HTTPCookieStorage.shared.setCookie(HTTPCookie(properties: [
            .domain: "books.example", .path: "/", .name: "session", .value: "x",
        ])!)
        defer { HTTPCookieStorage.shared.cookies?.forEach(HTTPCookieStorage.shared.deleteCookie) }
        let client = ServerClient(protocolClasses: [StubProtocol.self])
        _ = await client.health(origin)
        _ = await client.me(origin, token: "tok")
        XCTAssertFalse(StubProtocol.seen.isEmpty)
        for req in StubProtocol.seen {
            XCTAssertNil(req.value(forHTTPHeaderField: "Cookie"))
        }
    }

    func testBearerOnlyOnApiPaths() async {
        StubProtocol.handler = { [self] req in (reply(req.url!, 200), Data("{}".utf8)) }
        let client = ServerClient(protocolClasses: [StubProtocol.self])
        _ = await client.call("https://books.example/healthz", "GET", nil, "tok", 5)
        _ = await client.call("https://books.example/me/devices", "GET", nil, "tok", 5)
        _ = await client.me(origin, token: "tok")
        await client.revoke(origin, token: "tok")
        let byPath = Dictionary(grouping: StubProtocol.seen, by: { $0.url?.path ?? "" })
        XCTAssertNil(byPath["/healthz"]?.first?.value(forHTTPHeaderField: "Authorization"))
        XCTAssertNil(byPath["/me/devices"]?.first?.value(forHTTPHeaderField: "Authorization"))
        XCTAssertEqual(byPath["/api/v1/me"]?.first?.value(forHTTPHeaderField: "Authorization"), "Bearer tok")
        let revoke = byPath["/api/v1/auth/token"]?.first
        XCTAssertEqual(revoke?.httpMethod, "DELETE")
        XCTAssertEqual(revoke?.value(forHTTPHeaderField: "Authorization"), "Bearer tok")
    }

    func testNoAnswerIsUnreachable() async {
        StubProtocol.handler = nil
        let client = ServerClient(protocolClasses: [StubProtocol.self])
        let health = await client.health(origin)
        XCTAssertEqual(health, .unreachable)
    }
}
