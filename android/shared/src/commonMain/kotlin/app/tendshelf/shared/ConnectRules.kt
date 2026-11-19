package app.tendshelf.shared

/** Where the phone is in connecting to its server. The bridge follows it: it attaches in Connected, detaches in NoServer. */
enum class ConnectState { NoServer, NeedsSignIn, Connecting, Connected, Reconnecting }

/** Pages where a person signs in, joins or connects: no automatic connect starts on them. */
fun signInPath(path: String): Boolean =
    path == "/login" || path == "/setup" || path == "/welcome" || path.startsWith("/invite/") || path.startsWith("/pair/") || path.startsWith("/app/connect")

/** What the shell does when the main frame finishes a page of the server. */
enum class PageStep {
    /** Nothing. */
    None,

    /** Ask `/api/v1/me` with the held token: a 401 signs out, a yes moves to Reconnecting. */
    CheckMe,

    /** Start the sign-in to get a code (the PKCE flow). */
    BeginConnect,
}

/**
 * The decisions of the page-finished rule, pure. [path] is the raw path of the finished page (a page of the server
 * origin). [connectLoading] is true while the app's own connect page is loading, already cleared when [path] is a
 * sign-in path.
 */
fun afterPageFinished(state: ConnectState, path: String, hasToken: Boolean, connectLoading: Boolean, suppressAutoConnect: Boolean): PageStep {
    if (path == "/login" && hasToken && state != ConnectState.Reconnecting) return PageStep.CheckMe
    if (state == ConnectState.Reconnecting && hasToken && !connectLoading && path != "/login" && !path.startsWith("/app/connect")) {
        return PageStep.BeginConnect
    }
    if (!hasToken && !suppressAutoConnect && !connectLoading && state != ConnectState.Connecting && !signInPath(path)) return PageStep.BeginConnect
    return PageStep.None
}

/** The raw path of [url] when it is a page of this origin, else null. */
fun Origin.pathOf(url: String): String? {
    if (!matches(url)) return null
    return parseRawUri(url)?.rawPath ?: ""
}
