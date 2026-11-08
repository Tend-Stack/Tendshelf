package app.tendshelf.shared

/** The question "You were at {time} on {device}. Continue there?" for one book. */
data class Ask(val bookId: String, val positionMs: Long, val deviceLabel: String)

/** A break this long, or a place this far apart, counts. The web player's rule (`BREAK_MS`). */
const val BREAK_MS = 60_000L

/** The word shown when the other device has no name. */
const val OTHER_DEVICE = "another device"

/**
 * Whether to ask before going on: the server holds a place that is not finished, not from this device, not
 * written by the server itself, and more than [BREAK_MS] away from [localMs]. Same rule as `maybeAskContinue`.
 */
fun conflict(bookId: String, localMs: Long, remote: RemoteProgress?, ownDeviceId: String): Ask? {
    if (remote == null || remote.finished) return null
    if (remote.deviceId == ownDeviceId || remote.deviceId == "server") return null
    if (kotlin.math.abs(remote.positionMs - localMs) <= BREAK_MS) return null
    return Ask(bookId, remote.positionMs, remote.deviceLabel.ifEmpty { OTHER_DEVICE })
}

/** A time as the question shows it: `1:20`, or `1:02:03` from an hour. */
fun clockText(ms: Long): String {
    val total = (ms.coerceAtLeast(0) / 1000)
    val h = total / 3600
    val m = (total % 3600) / 60
    val s = total % 60
    fun two(n: Long) = n.toString().padStart(2, '0')
    return if (h > 0) "$h:${two(m)}:${two(s)}" else "$m:${two(s)}"
}
