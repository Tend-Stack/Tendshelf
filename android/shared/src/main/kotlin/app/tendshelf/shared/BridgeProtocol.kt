package app.tendshelf.shared

/**
 * The words the web page and the app say to each other (plan A4, protocol version 1).
 *
 * Page to native is plain words with a strict grammar, so there is no parser to attack: at most [MAX_LENGTH] ASCII
 * characters, single spaces, ids from a small charset, numbers with fixed digit counts. Anything else is dropped,
 * with no reply. No message carries a URL, a path, a header or a token, and nothing here reads one.
 *
 * Native to page is JSON built only by the `encode*` functions below from fixed fields; no free text crosses.
 */
sealed interface BridgeMessage {
    data object Hello : BridgeMessage

    /** [atMs] null is the page's `-`: the best known place. */
    data class Start(val bookId: String, val atMs: Long?) : BridgeMessage

    data object Play : BridgeMessage
    data object Pause : BridgeMessage
    data class Seek(val ms: Long) : BridgeMessage
    data class Speed(val speed: Double) : BridgeMessage

    /** The page's answer to native `back`: false means it closed nothing, so native walks history. */
    data class Back(val handled: Boolean) : BridgeMessage

    /** Lowercase `#rrggbb`. */
    data class Theme(val hex: String) : BridgeMessage

    /** The page's answer to the place question: `answer here` keeps this place, `answer there` goes to the other one. */
    data class Answer(val keepHere: Boolean) : BridgeMessage
}

const val MAX_LENGTH = 128

/** The one place a page string becomes a message. Null for anything outside the grammar. */
fun parseBridge(raw: String?): BridgeMessage? {
    if (raw == null || raw.isEmpty() || raw.length > MAX_LENGTH) return null
    if (raw.any { it.code < 0x20 || it.code > 0x7e }) return null
    val parts = raw.split(' ')
    // split keeps empty parts for doubled, leading or trailing spaces; none is allowed.
    if (parts.any { it.isEmpty() }) return null
    return when (parts[0]) {
        "hello" -> if (parts.size == 2 && parts[1] == "1") BridgeMessage.Hello else null
        "play" -> if (parts.size == 1) BridgeMessage.Play else null
        "pause" -> if (parts.size == 1) BridgeMessage.Pause else null
        "start" -> {
            if (parts.size != 3 || !validId(parts[1])) return null
            if (parts[2] == "-") return BridgeMessage.Start(parts[1], null)
            val ms = millis(parts[2]) ?: return null
            BridgeMessage.Start(parts[1], ms)
        }
        "seek" -> if (parts.size == 2) millis(parts[1])?.let { BridgeMessage.Seek(it) } else null
        "speed" -> if (parts.size == 2) speed(parts[1])?.let { BridgeMessage.Speed(it) } else null
        "back" -> if (parts.size == 2 && (parts[1] == "0" || parts[1] == "1")) BridgeMessage.Back(parts[1] == "1") else null
        "theme" -> if (parts.size == 2 && validHex(parts[1])) BridgeMessage.Theme(parts[1]) else null
        "answer" -> if (parts.size == 2 && (parts[1] == "here" || parts[1] == "there")) BridgeMessage.Answer(parts[1] == "here") else null
        else -> null
    }
}

private fun validId(s: String): Boolean =
    s.length in 1..64 && s.all { it in 'A'..'Z' || it in 'a'..'z' || it in '0'..'9' || it == '_' || it == '-' }

/** 1 to 12 ASCII digits, so no sign, no exponent and no overflow. */
private fun millis(s: String): Long? =
    if (s.length in 1..12 && s.all { it in '0'..'9' }) s.toLong() else null

/** `d.dd`, 0.50 to 3.00 in steps of 0.05. */
private fun speed(s: String): Double? {
    if (s.length != 4 || s[1] != '.' || !s[0].isAsciiDigit() || !s[2].isAsciiDigit() || !s[3].isAsciiDigit()) return null
    val hundredths = (s[0] - '0') * 100 + (s[2] - '0') * 10 + (s[3] - '0')
    if (hundredths < 50 || hundredths > 300 || hundredths % 5 != 0) return null
    return hundredths / 100.0
}

private fun Char.isAsciiDigit() = this in '0'..'9'

private fun validHex(s: String): Boolean =
    s.length == 7 && s[0] == '#' && s.drop(1).all { it in '0'..'9' || it in 'a'..'f' }

/** The full player state the page draws. Fixed fields; [PlayerSnapshot.error] is not sent (see [encodeError]). */
fun encodeState(s: PlayerSnapshot): String =
    "{\"t\":\"state\",\"bookId\":${jsonString(s.bookId)},\"positionMs\":${s.positionMs.coerceAtLeast(0)}," +
        "\"durationMs\":${s.durationMs.coerceAtLeast(0)},\"playing\":${s.playing},\"buffering\":${s.buffering}," +
        "\"ended\":${s.ended},\"speed\":${speedText(s.speed)},\"chapterIdx\":${s.chapterIdx.coerceAtLeast(0)}}"

/** Only two codes exist; anything else (`background` too) is `stream`, so no error text can cross. */
fun encodeError(code: String): String = "{\"t\":\"error\",\"code\":${jsonString(if (code == "book") "book" else "stream")}}"

/**
 * The place question for the page's toast. [deviceLabel] is the one free-text field that crosses (D6): the server
 * already gives the page that label, and the page sets it as text. A null [ask] closes the toast (empty bookId).
 */
fun encodeAsk(a: Ask?): String =
    "{\"t\":\"ask\",\"bookId\":${jsonString(a?.bookId.orEmpty())},\"positionMs\":${(a?.positionMs ?: 0).coerceAtLeast(0)}," +
        "\"deviceLabel\":${jsonString(a?.deviceLabel.orEmpty())}}"

/** "Rewound {s} s" once, after a resume that went back. */
fun encodeRewound(s: Int): String = "{\"t\":\"rewound\",\"s\":${s.coerceIn(0, 3600)}}"

fun encodeBack(): String = "{\"t\":\"back\"}"

private fun speedText(speed: Double): String {
    val h = if (speed.isNaN()) 100 else Math.round((speed * 100).coerceIn(50.0, 300.0)).toInt()
    return "${h / 100}.${(h % 100).toString().padStart(2, '0')}"
}

/**
 * Token bucket: [perSecond] messages a second with a burst of the same size. Pure: the caller passes the clock.
 * Checked before any allocation for a message (threat B11).
 */
class TokenBucket(private val perSecond: Int, private var nowMs: Long) {
    private var tokens = perSecond.toDouble()

    fun take(now: Long): Boolean {
        val elapsed = (now - nowMs).coerceAtLeast(0)
        nowMs = now
        tokens = minOf(perSecond.toDouble(), tokens + elapsed * perSecond / 1000.0)
        if (tokens < 1.0) return false
        tokens -= 1.0
        return true
    }
}
