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

    /** `sleep off`, `sleep plus`, `sleep chapter` or `sleep 1..999`. [choice] is `off`, `plus`, `chapter` or the digits; [minutes] is 0 for the first three. */
    data class Sleep(val choice: String, val minutes: Int) : BridgeMessage

    /** `dl add <id>`, `dl remove <id>` or `dl ask <id>` (A7): keep a book on the phone, drop it, or ask how it stands. */
    data class Download(val action: String, val bookId: String) : BridgeMessage

    /** `dl room` (L10, Download all): the page asks how many bytes the phone can still take for downloads; the answer is [encodeRoom]. */
    data object DownloadRoom : BridgeMessage

    /** `tts voices`: the page asks for the voices on the phone that need no network (decision 0032). */
    data object TtsVoices : BridgeMessage

    /** `tts stop`: stop speaking now. */
    data object TtsStop : BridgeMessage

    /**
     * `say <n> <rate> <voiceId> <text>`: speak one sentence (decision 0032). The first page message that carries free text;
     * [text] passed [parseSay]'s rules, goes only to the speech engine and is never logged or echoed.
     */
    data class Say(val n: Int, val rate: Double, val voiceId: String, val text: String) : BridgeMessage

    /** `wake 0|1`: the page asks the app to hold (1) or release (0) the screen (decision 0033). */
    data class Wake(val on: Boolean) : BridgeMessage

    /** `pagekeys 0|1`: the reader asks for the volume keys (1) or gives them back (0) (decision 0033). */
    data class PageKeys(val on: Boolean) : BridgeMessage

    /** `quiet 0|1`: skip silence (quiet skip) off or on, applied at once (decision 0033). */
    data class Quiet(val on: Boolean) : BridgeMessage

    /** The Download the next book switch in Settings changed (decision 0035). */
    data class AutoDownload(val on: Boolean) : BridgeMessage
}

const val MAX_LENGTH = 128

/** The longest text `say` carries, in UTF-16 units. */
const val SAY_TEXT_MAX = 4000

/** The longest whole `say` message: "say " + 9 digits + rate + a 64-character id + 3 spaces + the text, rounded up. */
const val SAY_MAX_LENGTH = 4200

/** The one place a page string becomes a message. Null for anything outside the grammar. */
fun parseBridge(raw: String?): BridgeMessage? {
    if (raw != null && raw.startsWith("say ")) return parseSay(raw)
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
        "sleep" -> if (parts.size == 2) sleepWord(parts[1]) else null
        "tts" -> when {
            parts.size == 2 && parts[1] == "voices" -> BridgeMessage.TtsVoices
            parts.size == 2 && parts[1] == "stop" -> BridgeMessage.TtsStop
            else -> null
        }
        "wake" -> if (parts.size == 2 && (parts[1] == "0" || parts[1] == "1")) BridgeMessage.Wake(parts[1] == "1") else null
        "pagekeys" -> if (parts.size == 2 && (parts[1] == "0" || parts[1] == "1")) BridgeMessage.PageKeys(parts[1] == "1") else null
        "autodl" -> if (parts.size == 2 && (parts[1] == "0" || parts[1] == "1")) BridgeMessage.AutoDownload(parts[1] == "1") else null
        "quiet" -> if (parts.size == 2 && (parts[1] == "0" || parts[1] == "1")) BridgeMessage.Quiet(parts[1] == "1") else null
        "dl" -> if (parts.size == 2 && parts[1] == "room") BridgeMessage.DownloadRoom else if (parts.size == 3 && validId(parts[2]) && parts[1] in DL_WORDS) BridgeMessage.Download(parts[1], parts[2]) else null
        else -> null
    }
}

/**
 * `say <n> <rate> <voiceId> <text>`. The head (up to the fourth space) keeps the single-space ASCII grammar; the text is the rest:
 * 1 to [SAY_TEXT_MAX] UTF-16 units, no code point below 0x20 or from 0x7f to 0x9f, no unpaired surrogate.
 */
private fun parseSay(raw: String): BridgeMessage? {
    if (raw.length > SAY_MAX_LENGTH) return null
    val a = raw.indexOf(' ')
    val b = raw.indexOf(' ', a + 1)
    val c = raw.indexOf(' ', b + 1)
    val d = raw.indexOf(' ', c + 1)
    if (b < 0 || c < 0 || d < 0) return null
    val nText = raw.substring(a + 1, b)
    val rateText = raw.substring(b + 1, c)
    val voice = raw.substring(c + 1, d)
    val text = raw.substring(d + 1)
    if (nText.length !in 1..9 || nText[0] == '0' || !nText.all { it.isAsciiDigit() }) return null
    val rate = speed(rateText, 300) ?: return null // read aloud keeps its own 3.00 ceiling
    // validId also allows a bare "-" (the page's "no place" word); a voice id must hold a letter or a digit.
    if (!validId(voice) || voice.none { it.isLetterOrDigit() }) return null
    if (!validSayText(text)) return null
    return BridgeMessage.Say(nText.toInt(), rate, voice, text)
}

private fun validSayText(t: String): Boolean {
    if (t.isEmpty() || t.length > SAY_TEXT_MAX) return false
    var i = 0
    while (i < t.length) {
        val ch = t[i]
        when {
            ch.code < 0x20 || ch.code in 0x7f..0x9f -> return false
            Character.isHighSurrogate(ch) -> {
                if (i + 1 >= t.length || !Character.isLowSurrogate(t[i + 1])) return false
                i++
            }
            Character.isLowSurrogate(ch) -> return false
        }
        i++
    }
    return true
}

private val DL_WORDS = setOf("add", "remove", "ask")

/** 1 to 999 with no leading zero, or one of three words. */
private fun sleepWord(w: String): BridgeMessage? = when {
    w == "off" || w == "plus" || w == "chapter" || w == "episode" -> BridgeMessage.Sleep(w, 0)
    w.length in 1..3 && w.all { it.isAsciiDigit() } && w[0] != '0' -> BridgeMessage.Sleep(w, w.toInt())
    else -> null
}

fun validId(s: String): Boolean =
    s.length in 1..64 && s.all { it in 'A'..'Z' || it in 'a'..'z' || it in '0'..'9' || it == '_' || it == '-' }

/** 1 to 12 ASCII digits, so no sign, no exponent and no overflow. */
private fun millis(s: String): Long? =
    if (s.length in 1..12 && s.all { it in '0'..'9' }) s.toLong() else null

/** `d.dd`, 0.50 to 3.50 in steps of 0.05. */
private fun speed(s: String, maxHundredths: Int = 350): Double? {
    if (s.length != 4 || s[1] != '.' || !s[0].isAsciiDigit() || !s[2].isAsciiDigit() || !s[3].isAsciiDigit()) return null
    val hundredths = (s[0] - '0') * 100 + (s[2] - '0') * 10 + (s[3] - '0')
    if (hundredths < 50 || hundredths > maxHundredths || hundredths % 5 != 0) return null
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

/** Only three codes exist; anything else is `stream`, so no error text can cross. `background` is a refused foreground start. */
fun encodeError(code: String): String =
    "{\"t\":\"error\",\"code\":${jsonString(if (code == "book" || code == "background") code else "stream")}}"

/** The sleep timer for the page to draw. [leftMs] is real listening time; the page counts it down between pushes. */
fun encodeSleep(s: SleepInfo): String =
    "{\"t\":\"sleep\",\"mode\":\"${s.mode.name.lowercase()}\",\"choice\":${jsonString(s.choice)}," +
        "\"leftMs\":${s.leftMs.coerceIn(0, 86_400_000L)},\"fading\":${s.fading}}"

/**
 * The place question for the page's toast. [deviceLabel] is the one free-text field that crosses (D6): the server
 * already gives the page that label, and the page sets it as text. A null [ask] closes the toast (empty bookId).
 */
fun encodeAsk(a: Ask?): String =
    "{\"t\":\"ask\",\"bookId\":${jsonString(a?.bookId.orEmpty())},\"positionMs\":${(a?.positionMs ?: 0).coerceAtLeast(0)}," +
        "\"deviceLabel\":${jsonString(a?.deviceLabel.orEmpty())}}"

/** "Rewound {s} s" once, after a resume that went back. */
fun encodeRewound(s: Int): String = "{\"t\":\"rewound\",\"s\":${s.coerceIn(0, 3600)}}"

/** How a book stands on the phone; [sized] false says the percent is not known yet (older pages ignore the extra field); [state] is one of `none`, `queued`, `downloading`, `paused`, `ready`, `failed`, `stale`. */
fun encodeDl(bookId: String, state: String, pct: Int, sized: Boolean = true): String {
    val known = state in setOf("none", "queued", "downloading", "paused", "ready", "failed", "stale")
    return "{\"t\":\"dl\",\"bookId\":${jsonString(bookId)},\"state\":\"${if (known) state else "none"}\",\"pct\":${pct.coerceIn(0, 100)}${if (sized) "" else ",\"known\":false"}}"
}

/** The room left for downloads, after the spare every download keeps free ([SPARE_BYTES]); the page compares a whole batch with it. */
fun encodeRoom(freeBytes: Long): String = "{\"t\":\"room\",\"bytes\":${roomFor(freeBytes)}}"

/** A voice the page may use: [id] passed [validId], [lang] is `[A-Za-z0-9-]{2,35}`; a voice that fails either is left out; at most 200. */
fun encodeVoices(list: List<VoiceInfo>): String {
    val items = list.filter { validId(it.id) && validLang(it.lang) }.take(MAX_VOICES).joinToString(",") {
        "{\"id\":${jsonString(it.id)},\"lang\":${jsonString(it.lang)},\"def\":${it.isDefault}}"
    }
    return "{\"t\":\"voices\",\"list\":[$items]}"
}

/** One speech event for utterance [n]; an unknown [ev] is `error`. */
fun encodeTts(n: Int, ev: String): String {
    val word = if (ev == "start" || ev == "end" || ev == "error" || ev == "stopped") ev else "error"
    return "{\"t\":\"tts\",\"n\":${n.coerceIn(0, 999_999_999)},\"ev\":\"$word\"}"
}

fun validLang(s: String): Boolean =
    s.length in 2..35 && s.all { it in 'A'..'Z' || it in 'a'..'z' || it in '0'..'9' || it == '-' }

/** The volume key the reader should treat as a page turn; the page ignores any other direction. */
fun encodePageKey(next: Boolean): String = "{\"t\":\"pagekey\",\"dir\":\"${if (next) "next" else "prev"}\"}"

fun encodeBack(): String = "{\"t\":\"back\"}"

private fun speedText(speed: Double): String {
    val h = if (speed.isNaN()) 100 else Math.round((speed * 100).coerceIn(50.0, 350.0)).toInt()
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
