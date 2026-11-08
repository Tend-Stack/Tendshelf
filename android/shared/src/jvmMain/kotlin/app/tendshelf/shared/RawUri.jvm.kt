package app.tendshelf.shared

import java.net.URI

internal actual fun parseRawUri(text: String): RawUri? {
    val uri = try { URI(text) } catch (_: Exception) { return null }
    return RawUri(uri.scheme, uri.host, uri.port, uri.rawUserInfo, uri.rawPath, uri.rawQuery)
}
