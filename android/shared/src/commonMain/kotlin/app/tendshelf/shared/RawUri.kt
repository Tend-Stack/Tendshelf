package app.tendshelf.shared

/**
 * The parts of a URI that origin matching and link handoff read, in their raw (still percent-encoded) form.
 * [port] is -1 when the text names none; [host] is null for an address without a server name.
 */
internal class RawUri(
    val scheme: String?,
    val host: String?,
    val port: Int,
    val rawUserInfo: String?,
    val rawPath: String?,
    val rawQuery: String?,
)

/**
 * Parses [text] as an RFC 2396 URI, or returns null when it is not one. Each platform wraps its own
 * strict parser (java.net.URI on the JVM); this module never parses URLs by hand.
 */
internal expect fun parseRawUri(text: String): RawUri?
