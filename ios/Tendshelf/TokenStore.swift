import Foundation
import Security
@preconcurrency import TendshelfShared

/// The server and device a held token belongs to.
nonisolated struct Connection: Equatable {
    let origin: Origin
    let token: String
    let deviceId: String
}

/// The device token lives only in the Keychain (invariant 3), readable after the first unlock so playback can use it
/// while the phone is locked, and never copied to another device. Everything that is not a secret is in `UserDefaults`.
/// The token is sent only by native HTTP; nothing here logs it or hands it to the WebView.
nonisolated final class TokenStore: @unchecked Sendable {
    static let service = "app.tendshelf.ios"
    private static let account = "device-token"

    private let defaults: UserDefaults
    private let service: String

    /// [service] and [defaults] are parameters so tests can use their own Keychain item and suite.
    init(defaults: UserDefaults = .standard, service: String = TokenStore.service) {
        self.defaults = defaults
        self.service = service
        // The Keychain outlives a deletion of the app; the defaults do not. A missing `installed` mark means this is a
        // fresh install, so a token left by an earlier one is dropped, never trusted.
        if defaults.object(forKey: "installed") == nil {
            deleteItem()
            defaults.set(true, forKey: "installed")
        }
    }

    /// The server the person chose; kept after sign-out so the start page can fill it in. It is not a secret.
    var origin: Origin? {
        get { defaults.string(forKey: "origin").flatMap { Origin.companion.parse(input: $0) } }
        set {
            // The pinned install id belongs to one address; another address is another server.
            let same = newValue != nil && newValue?.text == defaults.string(forKey: "origin")
            if let newValue { defaults.set(newValue.text, forKey: "origin") } else { defaults.removeObject(forKey: "origin") }
            if !same { defaults.removeObject(forKey: "serverId") }
        }
    }

    /// The `/healthz` install id seen when this server was connected; nil for a server that sent none.
    var serverId: String? {
        get { defaults.string(forKey: "serverId") }
        set {
            if let newValue, !newValue.isEmpty { defaults.set(newValue, forKey: "serverId") } else { defaults.removeObject(forKey: "serverId") }
        }
    }

    var userId: String? { defaults.string(forKey: "userId") }

    func hasToken() -> Bool {
        switch readItem() {
        case .found, .unreadable: return true
        case .missing: return false
        }
    }

    /// The held connection, or nil. A Keychain that is busy or locked is "not now": the token stays and the next call
    /// tries again (the iPhone twin of `tokenIsLost`). Only an item that exists but can never be read again is dropped.
    func current() -> Connection? {
        guard let o = origin, let device = defaults.string(forKey: "deviceId") else { return nil }
        switch readItem() {
        case .found(let token): return Connection(origin: o, token: token, deviceId: device)
        case .unreadable, .missing: return nil
        }
    }

    func save(token: String, deviceId: String, userId: String) {
        writeItem(token)
        saveIds(deviceId: deviceId, userId: userId)
    }

    /// Rebinding keeps the token; only the device and person it belongs to are refreshed.
    func saveIds(deviceId: String, userId: String) {
        defaults.set(deviceId, forKey: "deviceId")
        defaults.set(userId, forKey: "userId")
    }

    func clearToken() {
        deleteItem()
        defaults.removeObject(forKey: "deviceId")
        defaults.removeObject(forKey: "userId")
    }

    func httpWarned(_ o: Origin) -> Bool { defaults.bool(forKey: "httpWarned.\(o.text)") }
    func markHttpWarned(_ o: Origin) { defaults.set(true, forKey: "httpWarned.\(o.text)") }

    // MARK: Keychain

    private enum Item {
        case found(String)
        /// The item exists but this read failed (locked, busy) or its bytes are not text it can use.
        case unreadable
        case missing
    }

    private var query: [String: Any] {
        [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: Self.account,
        ]
    }

    private func readItem() -> Item {
        var q = query
        q[kSecReturnData as String] = true
        q[kSecMatchLimit as String] = kSecMatchLimitOne
        var out: CFTypeRef?
        let status = SecItemCopyMatching(q as CFDictionary, &out)
        if status == errSecItemNotFound { return .missing }
        guard status == errSecSuccess else { return .unreadable }
        guard let data = out as? Data, let text = String(data: data, encoding: .utf8), !text.isEmpty else {
            // Readable but empty or not text: it can never work again, so it counts as signed out.
            deleteItem()
            return .missing
        }
        return .found(text)
    }

    private func writeItem(_ token: String) {
        deleteItem()
        var q = query
        q[kSecValueData as String] = Data(token.utf8)
        q[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
        SecItemAdd(q as CFDictionary, nil)
    }

    private func deleteItem() {
        SecItemDelete(query as CFDictionary)
    }
}
