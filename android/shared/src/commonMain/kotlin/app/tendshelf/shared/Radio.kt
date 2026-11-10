package app.tendshelf.shared

/**
 * Live radio for the native player (R3, decision 0038). A station is a live item with no end and no place: it plays from the
 * server's own relay, never from the station. Everything here is plain logic with no platform calls.
 */

/** True for a station id (`rs_...`); a book, a show and an episode have other prefixes. */
fun isStationId(id: String): Boolean = id.startsWith("rs_") && id.length > 3

/** A saved or recently played station as the server lists it. [artUrl] is a path on the server, empty when the server names none. */
data class StationCard(
    val id: String,
    val name: String,
    val codec: String,
    val genre: String,
    val country: String,
    val favourite: Boolean,
    val lastPlayedAt: Long,
    /** Plain words when the last play failed, else empty. */
    val problem: String,
    val artUrl: String = "",
)

/** The row's title; a nameless station is never a blank row. */
fun stationRowTitle(s: StationCard): String = s.name.trim().ifEmpty { "Radio station" }

/** The row's second line: the genre, then the country ("Jazz, Spain"). Empty when the server names neither. */
fun stationSubline(s: StationCard): String = listOf(s.genre.trim(), s.country.trim()).filter { it.isNotEmpty() }.joinToString(", ")

object RadioJson {
    /** `GET /api/v1/radio/stations` or `/recent`: the rows with a usable id, in the server's order. */
    fun stations(body: String): List<StationCard> {
        val arr = parseJsonObject(body).getArray("items")
        return (0 until arr.size).mapNotNull { arr.optObject(it) }.mapNotNull { o ->
            val id = o.optString("id")
            if (!isStationId(id) || !validId(id)) return@mapNotNull null
            StationCard(
                id = id,
                name = o.optString("name"),
                codec = o.optString("codec"),
                genre = o.optString("genre"),
                country = o.optString("country"),
                favourite = o.optBoolean("favourite"),
                lastPlayedAt = o.optLong("lastPlayedAt", 0),
                problem = o.optString("problem"),
                artUrl = o.optString("artUrl"),
            )
        }
    }

    /** `GET /api/v1/radio/now/{id}`: the title the station sent, cleaned for a lock screen; empty when it sent none. */
    fun now(body: String): String = nowTitle(parseJsonObject(body).optString("title"))
}

/** At most 200 characters, no control characters, trimmed: the server cleans it too, but the notification never trusts text. */
fun nowTitle(raw: String): String = raw.filter { it >= ' ' && it != '\u007f' }.trim().take(200)

/** The notification's second line: the song or show now on air, or the word for "live" when the station sends none. */
fun liveSubtitle(now: String, liveWord: String): String = now.trim().ifEmpty { liveWord }

/** How often the app asks the server what is on air, while the station plays. */
const val RADIO_NOW_POLL_MS = 15_000L

/** The first ask comes soon after the sound starts, so the title is there before the person looks. */
const val RADIO_NOW_FIRST_MS = 2_000L

/**
 * Whether and when to open the relay again after it ended or failed. [code] is the HTTP status the relay answered, 0 when
 * there was none (the network dropped, the 12-hour limit or 60 seconds of silence ended the stream). [attempt] counts the
 * tries since sound last played; [retryAfterS] is the relay's `Retry-After`, 0 when absent. Null means stop.
 * A busy relay (503: the person already has a live station open, or every server slot is in use) is tried three times, about
 * five seconds apart; a break in the stream twice, three seconds apart; a refusal (sign-in, gone) never.
 */
fun liveRetryDelayMs(code: Int, attempt: Int, retryAfterS: Int): Long? = when {
    code == 401 || code == 403 || code == 404 || code == 410 -> null
    code == 503 -> if (attempt < 3) (retryAfterS.takeIf { it > 0 } ?: 5).coerceIn(2, 15) * 1000L else null
    else -> if (attempt < 2) 3_000L else null
}

/**
 * The failure word the player reports once retries are over: `signedOut`, `book` (the station is gone), `busy` (the
 * one-live-per-person or server-slot 503) or `stream`.
 */
fun liveFailureKind(code: Int): String = when (code) {
    401 -> "signedOut"
    403, 404, 410 -> "book"
    503 -> "busy"
    else -> "stream"
}

/** The extra buttons the media card, lock screen and car can show beside play and pause. */
enum class MediaButton { Back, Forward, Speed }

/**
 * Which extra buttons go with the item now loaded (RA-1). A live station has nothing to seek and no speed, so it publishes
 * none: only play and pause remain. A book or episode gets Back and Forward, plus Speed for a controller that shows it.
 */
fun mediaButtonsFor(live: Boolean, showSpeed: Boolean): List<MediaButton> = when {
    live -> emptyList()
    showSpeed -> listOf(MediaButton.Back, MediaButton.Forward, MediaButton.Speed)
    else -> listOf(MediaButton.Back, MediaButton.Forward)
}
