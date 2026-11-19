package app.tendshelf.shared

/**
 * What the app tells the page after each change of the player, and what it remembers to send only changes
 * (decision 0048, D4). Both phone apps use it, so the page hears the same words from either. Main thread only.
 */
class PushDiff {
    private var lastError = ""
    private var lastAsk: Ask? = null
    private var lastRewoundSeq = UNSET
    private var lastSleepOn = false

    /** A new page said hello: it knows nothing, so it gets the open question again and no old rewind. */
    fun hello() {
        lastAsk = null
        lastRewoundSeq = UNSET
    }

    /** The app left the screen: a rewind heard with the screen off is not announced later. */
    fun hidden() {
        lastRewoundSeq = UNSET
    }

    /** The app came back: an open question is sent again (the iPhone has no notification that carries it). */
    fun shown() {
        lastAsk = null
    }

    /** The messages for [s], in the order the page expects: state, error, sleep, ask, rewound. */
    fun messages(s: PlayerSnapshot): List<String> {
        val out = ArrayList<String>(5)
        out += encodeState(s)
        // Errors cross as one of the fixed codes; `signedOut` does not (sign-out replaces the page).
        if (s.error != lastError) {
            lastError = s.error
            if (s.error in ERROR_CODES) out += encodeError(s.error)
        }
        // The sleep timer: with every push while it runs, and once more when it turns off.
        if (s.sleep != SleepInfo.OFF || lastSleepOn) out += encodeSleep(s.sleep)
        lastSleepOn = s.sleep != SleepInfo.OFF
        if (s.ask != lastAsk) {
            // Only the changes cross: a question opens the toast, none closes it.
            if (s.ask != null || lastAsk != null) out += encodeAsk(s.ask)
            lastAsk = s.ask
        }
        if (lastRewoundSeq != UNSET && s.rewoundSeq != lastRewoundSeq && s.rewoundS > 0) out += encodeRewound(s.rewoundS)
        lastRewoundSeq = s.rewoundSeq
        return out
    }

    private companion object {
        const val UNSET = -1
    }
}
