package app.tendshelf.shared

/**
 * Two jolts within [windowMs] are a shake, as in the web player's `onMotion`. A jolt is a [peak] (m/s^2, gravity
 * already removed) at least [gapMs] after the last one, so one hard shake that rings is not counted twice.
 */
class ShakeDetector(private val peak: Float = 12f, private val gapMs: Long = 150, private val windowMs: Long = 1000) {
    private var lastAt = 0L
    private var seen = false
    private var peaks = 0

    /** True on the second jolt; the count then starts again. */
    fun sample(size: Float, nowMs: Long): Boolean {
        if (size <= peak || (seen && nowMs - lastAt < gapMs)) return false
        peaks = if (seen && nowMs - lastAt < windowMs) peaks + 1 else 1
        lastAt = nowMs
        seen = true
        if (peaks >= 2) {
            peaks = 0
            return true
        }
        return false
    }
}
