package app.tendshelf.shared

/** What `/healthz` said about itself: the `app` word and the install's `id` (null when the server sends none). */
data class Identity(val app: String?, val id: String?)

/**
 * Whether the address is answered by the Tendshelf the phone was connected to. A plain 404 or 401, a wipe of
 * downloads, or the bearer token may only follow a yes. [pinned] is the install id kept at connect time. An empty
 * pin (a server from before `/healthz` had an id) accepts any Tendshelf; otherwise the id must be the same.
 * No answer at all is a no.
 */
fun sameServer(pinned: String?, answer: Identity?): Boolean {
    if (answer == null || answer.app != "tendshelf") return false
    if (pinned.isNullOrEmpty()) return true
    return answer.id == pinned
}
