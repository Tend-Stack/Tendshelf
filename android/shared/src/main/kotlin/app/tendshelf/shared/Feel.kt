package app.tendshelf.shared

/** What the activity does with one key event (decision 0033). */
enum class PageKey { Pass, Swallow, Next, Prev }

/** The `android.view.KeyEvent` values; shared has no Android. */
const val KEY_VOLUME_UP = 24
const val KEY_VOLUME_DOWN = 25
const val KEY_ACTION_DOWN = 0

/**
 * Volume down is the next page, volume up the previous one, only while [armed]. A held key turns one page: the
 * repeats and the key-up are swallowed so the volume does not change either. Anything else passes.
 */
fun pageKeyFor(armed: Boolean, keyCode: Int, action: Int, repeatCount: Int): PageKey {
    if (!armed || (keyCode != KEY_VOLUME_UP && keyCode != KEY_VOLUME_DOWN)) return PageKey.Pass
    if (action != KEY_ACTION_DOWN || repeatCount > 0) return PageKey.Swallow
    return if (keyCode == KEY_VOLUME_DOWN) PageKey.Next else PageKey.Prev
}

/**
 * What the reader page asked for, and whether the app is in front. The screen is held only while the page holds
 * `wake 1` and the app is started; the keys are taken only while the page asked and the app is in front.
 * [awake], [front] and [reset] return the new [screenOn] when it changed, else null.
 */
class ReaderFeel {
    private var keys = false
    private var awake = false
    private var front = false

    val keysArmed: Boolean get() = keys && front
    val screenOn: Boolean get() = awake && front

    fun keys(on: Boolean) {
        keys = on
    }

    fun awake(on: Boolean): Boolean? = change { awake = on }

    fun front(on: Boolean): Boolean? = change { front = on }

    /** A new page or a detached bridge: forget what the old page asked for; [front] stays. */
    fun reset(): Boolean? = change {
        keys = false
        awake = false
    }

    private fun change(block: () -> Unit): Boolean? {
        val before = screenOn
        block()
        return if (screenOn != before) screenOn else null
    }
}
