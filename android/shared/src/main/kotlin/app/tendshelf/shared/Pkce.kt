package app.tendshelf.shared

import kotlin.io.encoding.Base64
import kotlin.io.encoding.ExperimentalEncodingApi

/** PKCE S256, RFC 7636: the caller supplies the random bytes and the SHA-256, so this stays pure. */
@OptIn(ExperimentalEncodingApi::class)
object Pkce {
    private val url = Base64.UrlSafe.withPadding(Base64.PaddingOption.ABSENT)

    /** 32 random bytes as base64url with no padding: 43 characters. */
    fun verifier(random32: ByteArray): String {
        require(random32.size == 32)
        return url.encode(random32)
    }

    /** base64url(SHA-256(ASCII verifier)), no padding. */
    fun challenge(sha256OfVerifierAscii: ByteArray): String {
        require(sha256OfVerifierAscii.size == 32)
        return url.encode(sha256OfVerifierAscii)
    }
}
