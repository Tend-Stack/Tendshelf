package app.tendshelf.shared

/** What the phone's speech engine says about one voice; the app fills it from `android.speech.tts.Voice`. */
data class VoiceInfo(
    val id: String,
    val lang: String,
    val network: Boolean,
    val notInstalled: Boolean,
    val isDefault: Boolean,
)

/** The most voices sent to the page. */
const val MAX_VOICES = 200

/**
 * The voices the page may use (decision 0032): none that needs a network connection or is not installed, none whose
 * id or language is outside the bridge grammar. Sorted by language then id, capped at [MAX_VOICES], and only the
 * voice named [defaultId] is flagged as the default.
 */
fun usableVoices(all: List<VoiceInfo>, defaultId: String?): List<VoiceInfo> =
    all.filter { !it.network && !it.notInstalled && validId(it.id) && validLang(it.lang) }
        .sortedWith(compareBy({ it.lang }, { it.id }))
        .take(MAX_VOICES)
        .map { it.copy(isDefault = it.id == defaultId) }
