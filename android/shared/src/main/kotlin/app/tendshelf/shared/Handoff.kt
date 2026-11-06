package app.tendshelf.shared

import java.net.URLDecoder

enum class Kind { Invite, Pair }

/** What an invitation link or a pairing link holds: the server's address, which one it is, and its one-time token (decision 0028). */
data class Handoff(val origin: Origin, val kind: Kind, val token: String)

private const val MAX_TEXT = 2048
private const val TOKEN_LENGTH = 43

/**
 * Reads pasted text or an opened `tendshelf://open?link=` address. The first whitespace-separated word that starts with
 * https://, http:// or tendshelf:// is the candidate; trailing `.,;:)>` is stripped. A `tendshelf:` link is unwrapped once,
 * and must hold an http(s) address (a wrapped `tendshelf:` is refused). The path must be exactly `/invite/` or `/pair/`
 * and a 43-character token; the query and fragment are ignored. Anything else is null. Pure: no request is made.
 */
fun parseHandoff(text: String): Handoff? {
    if (text.length > MAX_TEXT) return null
    val word = text.split(Regex("\\s+")).firstOrNull {
        val l = it.lowercase()
        l.startsWith("https://") || l.startsWith("http://") || l.startsWith("tendshelf://")
    } ?: return null
    var link = word.trimEnd('.', ',', ';', ':', ')', '>')
    if (link.lowercase().startsWith("tendshelf://")) {
        val u = try { java.net.URI(link) } catch (_: Exception) { return null }
        if (u.scheme?.lowercase() != "tendshelf" || u.host?.lowercase() != "open") return null
        val raw = u.rawQuery ?: return null
        val value = raw.split('&').firstOrNull { it.substringBefore('=') == "link" } ?: return null
        link = try { URLDecoder.decode(value.substringAfter('=', ""), "UTF-8") } catch (_: Exception) { return null }
        val l = link.lowercase()
        if (!l.startsWith("https://") && !l.startsWith("http://")) return null
    }
    val u = parseUrl(link) ?: return null
    val kind = when {
        u.path.startsWith("/invite/") -> Kind.Invite
        u.path.startsWith("/pair/") -> Kind.Pair
        else -> return null
    }
    val token = u.path.substringAfter('/', "").substringAfter('/', "")
    if (token.length != TOKEN_LENGTH || !token.all { it in 'A'..'Z' || it in 'a'..'z' || it in '0'..'9' || it == '-' || it == '_' }) return null
    val authority = if (u.port == null) u.host else "${u.host}:${u.port}"
    val origin = Origin.parse("${u.scheme}://$authority") ?: return null
    return Handoff(origin, kind, token)
}
