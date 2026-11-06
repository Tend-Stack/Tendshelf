package app.tendshelf.android

/** The one server and credential the native side uses. Never handed to the WebView. */
data class Connection(val origin: String, val token: String, val deviceId: String)

fun interface ConnectionSource { fun current(): Connection? }
