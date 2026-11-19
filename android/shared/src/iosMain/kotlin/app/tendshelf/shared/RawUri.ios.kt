package app.tendshelf.shared

import platform.Foundation.NSURLComponents

/**
 * iOS: Foundation's NSURLComponents, held to the strictness of java.net.URI where the two differ (the Android answers
 * are the reference; RawUriTest lists the rows where iOS may be stricter). Foundation is forgiving: it percent-encodes
 * spaces and non-ASCII, and reads `mailto:a@b.c` as a path. So this refuses the text java.net.URI refuses, answers an
 * opaque URI (`scheme:` followed by no slash) as java.net.URI does, and refuses a host that is not a plain DNS name,
 * IPv4 or bracketed IPv6 address.
 */
internal actual fun parseRawUri(text: String): RawUri? {
    if (text.any { it.code > 0x7e || it.code <= 0x20 || it in "\\\"<>^`{|}" }) return null
    if (hasBadPercent(text)) return null
    val c = NSURLComponents.componentsWithString(text) ?: return null
    val scheme = c.scheme
    if (scheme != null) {
        val rest = text.substringAfter(':')
        // "https:" alone is no URI; "mailto:a@b.c" is opaque, with no host, path or query of its own.
        if (rest.isEmpty()) return null
        // "https://" and "https://?x": an authority marker with nothing after it.
        if (rest.startsWith("//") && rest.getOrNull(2).let { it == null || it == '?' || it == '#' }) return null
        if (!rest.startsWith("/")) return RawUri(scheme, null, -1, null, null, null)
    }
    val rawHost = c.percentEncodedHost
    val host = when {
        rawHost == null || rawHost.isEmpty() -> null
        rawHost.startsWith("[") -> if (rawHost.endsWith("]") && rawHost.drop(1).dropLast(1).all { it.isHex() || it == ':' || it == '.' }) rawHost else return null
        rawHost.contains(':') -> "[$rawHost]"
        rawHost.all { it in 'A'..'Z' || it in 'a'..'z' || it in '0'..'9' || it == '.' || it == '-' } -> rawHost
        else -> return null
    }
    val port = c.port?.intValue ?: -1
    if (port != -1 && port !in 0..65535) return null
    val user = c.percentEncodedUser
    val password = c.percentEncodedPassword
    val userInfo = when {
        user == null -> null
        password == null -> user
        else -> "$user:$password"
    }
    return RawUri(scheme, host, port, userInfo, c.percentEncodedPath, c.percentEncodedQuery)
}

/** True when the text has a `%` that is not followed by two hex digits (java.net.URI refuses it; Foundation may not). */
private fun hasBadPercent(text: String): Boolean {
    for (i in text.indices) {
        if (text[i] != '%') continue
        val a = text.getOrNull(i + 1)
        val b = text.getOrNull(i + 2)
        if (a == null || b == null || !a.isHex() || !b.isHex()) return true
    }
    return false
}

private fun Char.isHex(): Boolean = this in '0'..'9' || this in 'a'..'f' || this in 'A'..'F'
