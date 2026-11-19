import Foundation
@preconcurrency import TendshelfShared

/// Refuses every redirect: the answer is the 3xx itself, and nothing is followed.
private final class RedirectRefuser: NSObject, URLSessionTaskDelegate {
    nonisolated func urlSession(
        _ session: URLSession,
        task: URLSessionTask,
        willPerformHTTPRedirection response: HTTPURLResponse,
        newRequest request: URLRequest,
        completionHandler: @escaping @Sendable (URLRequest?) -> Void
    ) {
        completionHandler(nil)
    }
}

/// What the connect flow asks of the server: `ServerClient` in the app, a fake in the tests.
protocol ServerAPI: AnyObject {
    func health(_ origin: Origin) async -> Health
    func identity(_ origin: Origin) async -> Identity?
    func signInOriginsBody(_ origin: Origin) async -> String?
    func swap(_ origin: Origin, code: String, verifier: String, name: String, bearer: String?) async -> Swap
    func me(_ origin: Origin, token: String) async -> Me
    func revoke(_ origin: Origin, token: String) async
}

/// The shell's own HTTP (invariant 4): /healthz, the code swap, /me and revoke. One ephemeral session: no cookies, no
/// cache, redirects refused, answers read up to 4096 bytes. The token goes in an `Authorization` header only on
/// `/api/v1/` paths. The WebView's cookie jar is never touched.
final class ServerClient: ServerAPI {
    /// What one call got: status and at most 4096 bytes of body, or the TLS-failure flag.
    struct Reply {
        let status: Int
        let body: String
        var certificate = false
        /// True when the body was cut at the limit.
        var truncated = false
    }

    static let bodyLimit = 4096

    private let session: URLSession
    private let refuser = RedirectRefuser()

    /// What one data call got: status, the body (at most `limit` bytes), and the `Retry-After` seconds when sent.
    struct DataReply {
        let status: Int
        let body: Data
        /// True when the answer was longer than the limit (the body then holds the first `limit` bytes).
        let tooLarge: Bool
        let retryAfterS: Int?
    }

    /// No cookies, no cache, no waiting for connectivity: the settings of every native session (this one and the media
    /// loader's). Redirects are refused by each session's own delegate.
    nonisolated static func ephemeralConfiguration() -> URLSessionConfiguration {
        let config = URLSessionConfiguration.ephemeral
        config.httpCookieStorage = nil
        config.httpShouldSetCookies = false
        config.httpCookieAcceptPolicy = .never
        config.urlCache = nil
        config.requestCachePolicy = .reloadIgnoringLocalCacheData
        config.waitsForConnectivity = false
        return config
    }

    /// [protocolClasses] is for tests (a `URLProtocol` stub in front of the network).
    init(protocolClasses: [AnyClass] = []) {
        let config = Self.ephemeralConfiguration()
        if !protocolClasses.isEmpty { config.protocolClasses = protocolClasses }
        session = URLSession(configuration: config, delegate: refuser, delegateQueue: nil)
    }

    func health(_ origin: Origin) async -> Health {
        guard let r = await call(origin.healthUrl(), "GET", nil, nil, 5) else { return .unreachable }
        return AuthAnswersKt.healthOf(status: Int32(r.status), body: r.body, certificate: r.certificate)
    }

    /// The Tendshelf identity at [origin] (`app`, `id`), or nil when nothing answered 200 with JSON. Never sends a credential.
    func identity(_ origin: Origin) async -> Identity? {
        guard let r = await call(origin.healthUrl(), "GET", nil, nil, 5), !r.certificate, r.status == 200 else { return nil }
        return ApiJson.shared.identity(body: r.body)
    }

    /// The body of `GET /login/oidc/origins` (public, no credential), or nil when nothing answered 200.
    func signInOriginsBody(_ origin: Origin) async -> String? {
        guard let r = await call("\(origin.text)/login/oidc/origins", "GET", nil, nil, 5) else { return nil }
        return r.status == 200 && !r.certificate ? r.body : nil
    }

    /// Exchange a code for a device. [bearer] is the token already held, when the phone is reconnecting.
    func swap(_ origin: Origin, code: String, verifier: String, name: String, bearer: String?) async -> Swap {
        let json = ApiJson.shared.swapRequest(code: code, verifier: verifier, name: name)
        guard let r = await call("\(origin.text)/api/v1/auth/token", "POST", json, bearer, 15) else { return SwapUnreachable.shared }
        return AuthAnswersKt.swapOf(status: Int32(r.status), body: r.body)
    }

    func me(_ origin: Origin, token: String) async -> Me {
        guard let r = await call(origin.meUrl(), "GET", nil, token, 10) else { return MeUnreachable.shared }
        return AuthAnswersKt.meOf(status: Int32(r.status), body: r.body)
    }

    /// Best effort: the device is revoked on the server, and a failure changes nothing here.
    func revoke(_ origin: Origin, token: String) async {
        _ = await call("\(origin.text)/api/v1/auth/token", "DELETE", nil, token, 5)
    }

    /// One request. Nil means no answer at all (network failure); a TLS failure is a reply with `certificate` set.
    func call(_ urlText: String, _ method: String, _ json: String?, _ bearer: String?, _ timeout: TimeInterval) async -> Reply? {
        let limit = Self.bodyLimit
        do {
            guard let r = try await callData(urlText, method, json, bearer, timeout, limit: limit) else { return nil }
            return Reply(status: r.status, body: String(decoding: r.body, as: UTF8.self), truncated: r.tooLarge)
        } catch let error as URLError where Self.isCertificateFailure(error.code) {
            return Reply(status: 0, body: "", certificate: true)
        } catch {
            return nil
        }
    }

    /// One request that keeps its bytes: reads chunks and stops one byte past [limit], so `tooLarge` is exact. Nil means no
    /// answer at all; a TLS failure throws its `URLError` (`call` turns that into a reply).
    func callData(
        _ urlText: String, _ method: String, _ json: String?, _ bearer: String?, _ timeout: TimeInterval, limit: Int
    ) async throws -> DataReply? {
        guard let url = URL(string: urlText) else { return nil }
        var request = URLRequest(url: url, cachePolicy: .reloadIgnoringLocalCacheData, timeoutInterval: timeout)
        request.httpMethod = method
        request.httpShouldHandleCookies = false
        request.setValue("application/json", forHTTPHeaderField: "Accept")
        // The token is for the API of the pinned origin only: never for a page, never for another path.
        if let bearer, url.path.hasPrefix("/api/v1/") {
            request.setValue("Bearer \(bearer)", forHTTPHeaderField: "Authorization")
        }
        if let json {
            request.httpBody = Data(json.utf8)
            request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        }
        do {
            let (bytes, response) = try await session.bytes(for: request)
            guard let http = response as? HTTPURLResponse else { return nil }
            var body = [UInt8]()
            var tooLarge = false
            for try await byte in bytes {
                if body.count >= limit {
                    tooLarge = true
                    break
                }
                body.append(byte)
            }
            let wait = http.value(forHTTPHeaderField: "Retry-After").flatMap { Int($0.trimmingCharacters(in: .whitespaces)) }
            return DataReply(status: http.statusCode, body: Data(body), tooLarge: tooLarge, retryAfterS: wait)
        } catch let error as URLError where Self.isCertificateFailure(error.code) {
            throw error
        } catch {
            return nil
        }
    }

    static func isCertificateFailure(_ code: URLError.Code) -> Bool {
        switch code {
        case .serverCertificateHasBadDate, .serverCertificateUntrusted, .serverCertificateHasUnknownRoot,
             .serverCertificateNotYetValid, .secureConnectionFailed:
            return true
        default:
            return false
        }
    }
}
