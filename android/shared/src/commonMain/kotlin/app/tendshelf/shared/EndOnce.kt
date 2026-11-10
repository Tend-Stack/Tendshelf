package app.tendshelf.shared

/**
 * The end of an item is handed off once (finished sent, queue asked). The flag is cleared when a new item starts and
 * when the player leaves its ended state, so a replay of the same item in the same process is handed off again.
 */
class EndOnce {
    private var handled = ""

    /** True the first time [id] ends since the last [reset]; false for a repeat of the same end. */
    fun claim(id: String): Boolean {
        if (id.isEmpty() || handled == id) return false
        handled = id
        return true
    }

    fun reset() {
        handled = ""
    }
}
