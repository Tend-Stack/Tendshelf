import CryptoKit
import Foundation
@preconcurrency import TendshelfShared

/// PKCE S256 (RFC 7636) for the connect flow: the shared module encodes, Security and CryptoKit supply the bytes.
enum PkceSwift {
    /// A fresh verifier from 32 random bytes: 43 characters.
    static func newVerifier() -> String {
        var bytes = [UInt8](repeating: 0, count: 32)
        let status = SecRandomCopyBytes(kSecRandomDefault, bytes.count, &bytes)
        precondition(status == errSecSuccess, "no random bytes")
        return Pkce.shared.verifier(random32: Data(bytes).kotlin)
    }

    /// base64url(SHA-256(ASCII verifier)).
    static func challenge(for verifier: String) -> String {
        let digest = SHA256.hash(data: Data(verifier.utf8))
        return Pkce.shared.challenge(sha256OfVerifierAscii: Data(digest).kotlin)
    }
}

extension Data {
    var kotlin: KotlinByteArray {
        let array = KotlinByteArray(size: Int32(count))
        for (i, byte) in enumerated() {
            array.set(index: Int32(i), value: Int8(bitPattern: byte))
        }
        return array
    }

    init(kotlin array: KotlinByteArray) {
        var bytes = [UInt8]()
        bytes.reserveCapacity(Int(array.size))
        for i in 0..<array.size {
            bytes.append(UInt8(bitPattern: array.get(index: i)))
        }
        self.init(bytes)
    }
}
