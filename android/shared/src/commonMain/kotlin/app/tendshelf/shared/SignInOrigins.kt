package app.tendshelf.shared

import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonPrimitive

/** The most provider origins the sign-in window will keep (plan P3). */
const val MAX_SIGN_IN_ORIGINS = 4

/**
 * Reads the server's `GET /login/oidc/origins` answer (`{"origins":["https://auth.example.com"]}`) into origins the
 * WebView may stay on while a person signs in with a provider (decision 0037). The server is only trusted this far:
 * at most [MAX_SIGN_IN_ORIGINS] entries, each a bare origin (no path, userinfo or query), `https`, or `http` only when
 * the server itself is `http` and the host is the server's host. Anything else is dropped; a bad body gives an empty set.
 */
fun parseSignInOrigins(body: String, server: Origin): Set<Origin> {
    val list = try {
        parseJsonObject(body)["origins"] as? JsonArray
    } catch (_: Exception) {
        null
    } ?: return emptySet()
    val out = LinkedHashSet<Origin>()
    for (item in list) {
        if (out.size >= MAX_SIGN_IN_ORIGINS) break
        val text = (item as? JsonPrimitive)?.takeIf { it.isString }?.content ?: continue
        val o = Origin.parse(text) ?: continue
        val allowed = o.scheme == "https" || (server.scheme == "http" && o.host == server.host)
        if (allowed) out.add(o)
    }
    return out
}
