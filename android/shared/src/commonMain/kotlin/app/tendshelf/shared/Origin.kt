package app.tendshelf.shared

// URL parsing goes through the expect/actual parseRawUri (java.net.URI on the JVM); no Android import.

/** The scheme, host and port of the server the person chose. Host is lowercased, IPv6 keeps its brackets, the default port is null. */
data class Origin(val scheme: String, val host: String, val port: Int?) {
    val text: String get() = if (port == null) "$scheme://$host" else "$scheme://$host:$port"
    val plainHttp: Boolean get() = scheme == "http"

    /** Addresses a home network uses: a warning for plain http is softer there. */
    val privateAddress: Boolean get() = isPrivateHost(host)

    /** True when [url] has exactly this scheme, host and port, and no userinfo. Anything unparsable is false. */
    fun matches(url: String): Boolean {
        val u = parseUrl(url) ?: return false
        return u.scheme == scheme && u.host == host && u.port == port
    }

    /**
     * True when [sourceOrigin], as a WebView reports it for a message (scheme://host[:port], no path), is exactly this
     * origin. Normalised by [parse], so case and a default port do not matter; a different port, a trailing dot, userinfo,
     * another scheme, `null` (a sandboxed document) and anything unparsable are all false (bridge threat B5).
     */
    fun sameSource(sourceOrigin: String): Boolean = parse(sourceOrigin) == this

    fun healthUrl(): String = "$text/healthz"
    fun meUrl(): String = "$text/api/v1/me"
    fun homeUrl(): String = "$text/?shell=android&next=/"

    /** A book's page (the widget's tap). [bookId] must pass [validId]; the caller checks. */
    fun bookUrl(bookId: String): String = "$text/?shell=android&next=%2Fbooks%2F$bookId"

    /** The page that signs in and then asks for a code, as A1's flow says: next is the escaped /app/connect path. */
    fun connectUrl(challenge: String): String = "$text/?shell=android&next=%2Fapp%2Fconnect%3Fchallenge%3D$challenge"

    /** The sign-in page that ends at an invitation (A1 flow in decision 0028): `next` is the escaped /invite path. */
    fun inviteUrl(token: String): String = "$text/?shell=android&next=%2Finvite%2F$token"

    /** The same for a pairing: `next` holds /pair/TOKEN with the challenge and this phone's name, each escaped. */
    fun pairUrl(token: String, challenge: String, name: String): String {
        val inner = "/pair/$token?challenge=" + formUrlEncode(challenge) + "&name=" + formUrlEncode(name)
        return "$text/?shell=android&next=" + formUrlEncode(inner)
    }

    companion object {
        /** An origin typed with its scheme; http and https only, nothing after the port but an optional slash. */
        fun parse(input: String): Origin? {
            val t = input.trim()
            val lower = t.lowercase()
            val scheme = when {
                lower.startsWith("https://") -> "https"
                lower.startsWith("http://") -> "http"
                else -> return null
            }
            var rest = t.substring(scheme.length + 3)
            if (rest.endsWith("/")) rest = rest.dropLast(1)
            if (rest.isEmpty() || rest.any { it == '/' || it == '?' || it == '#' || it == '@' || it == '\\' || it.isWhitespace() }) return null
            val host: String
            val portText: String?
            if (rest.startsWith("[")) {
                val close = rest.indexOf(']')
                if (close < 0) return null
                host = rest.substring(0, close + 1).lowercase()
                val after = rest.substring(close + 1)
                if (after.isNotEmpty() && !after.startsWith(":")) return null
                portText = after.removePrefix(":").ifEmpty { null }.also { if (after == ":") return null }
                if (!host.drop(1).dropLast(1).all { it.isDigit() || it in 'a'..'f' || it == ':' || it == '.' }) return null
            } else {
                val colon = rest.indexOf(':')
                host = (if (colon < 0) rest else rest.substring(0, colon)).lowercase()
                portText = if (colon < 0) null else rest.substring(colon + 1).also { if (it.isEmpty()) return null }
                if (host.isEmpty() || !host.all { it in 'a'..'z' || it in '0'..'9' || it == '-' || it == '.' }) return null
                if (host.startsWith(".") || host.startsWith("-") || host.contains("..")) return null
            }
            val port = if (portText == null) null else portText.toIntOrNull()?.takeIf { it in 1..65535 && portText.all(Char::isDigit) } ?: return null
            val default = if (scheme == "https") 443 else 80
            return Origin(scheme, host, if (port == default) null else port)
        }

        /** What to try for an address typed without a scheme: https first, then http. With a scheme, only that one. */
        fun candidates(typed: String): List<Origin> {
            val t = typed.trim()
            if (t.contains("://")) return listOfNotNull(parse(t))
            return listOfNotNull(parse("https://$t"), parse("http://$t"))
        }
    }
}

internal class ParsedUrl(val scheme: String, val host: String, val port: Int?, val path: String, val query: String?)

/** http(s) URLs only, strictly: userinfo, backslashes and unparsable text give null. The port is null when it is the scheme's default. */
internal fun parseUrl(url: String): ParsedUrl? {
    if (url.any { it == '\\' || it.isWhitespace() }) return null
    val uri = parseRawUri(url) ?: return null
    val scheme = uri.scheme?.lowercase() ?: return null
    if (scheme != "http" && scheme != "https") return null
    if (uri.rawUserInfo != null) return null
    val host = uri.host?.lowercase() ?: return null
    val default = if (scheme == "https") 443 else 80
    val port = if (uri.port == -1 || uri.port == default) null else uri.port
    return ParsedUrl(scheme, host, port, uri.rawPath ?: "", uri.rawQuery)
}

private fun isPrivateHost(host: String): Boolean {
    if (host.startsWith("[")) {
        val h = host.drop(1).dropLast(1)
        return h == "::1" || h.startsWith("fc") || h.startsWith("fd") ||
            h.startsWith("fe8") || h.startsWith("fe9") || h.startsWith("fea") || h.startsWith("feb")
    }
    val parts = host.split('.')
    if (parts.size == 4 && parts.all { p -> p.isNotEmpty() && p.all(Char::isDigit) && p.toInt() <= 255 }) {
        val a = parts[0].toInt()
        val b = parts[1].toInt()
        return a == 10 || a == 127 || (a == 172 && b in 16..31) || (a == 192 && b == 168) ||
            (a == 169 && b == 254) || (a == 100 && b in 64..127)
    }
    return host.endsWith(".local") || !host.contains('.')
}
