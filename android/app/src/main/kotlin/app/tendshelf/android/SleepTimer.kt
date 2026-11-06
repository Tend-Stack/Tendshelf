package app.tendshelf.android

import android.hardware.Sensor
import android.hardware.SensorEvent
import android.hardware.SensorEventListener
import android.hardware.SensorManager
import android.os.Handler
import android.os.SystemClock
import android.util.Log
import androidx.media3.exoplayer.ExoPlayer
import app.tendshelf.shared.ShakeDetector
import app.tendshelf.shared.SleepClock
import app.tendshelf.shared.SleepInfo
import app.tendshelf.shared.SleepMode
import app.tendshelf.shared.SleepStep
import app.tendshelf.shared.chapterEndFor

/**
 * The sleep timer in the service (A6), so a frozen page or a dark screen changes nothing. The rules are
 * [SleepClock]'s, the same as the web's. A 250 ms tick runs only while a timer is on; ExoPlayer's wake lock keeps it
 * alive while sound plays, and when nothing plays the clock does not count anyway. The accelerometer is listened to
 * only during the fade, and a shake is "+10 min".
 */
class SleepTimer(
    private val exo: ExoPlayer,
    private val player: BookPlayer,
    private val sensors: SensorManager?,
    private val main: Handler,
    private val publish: (SleepInfo) -> Unit,
) {
    private val clock = SleepClock()
    private val shake = ShakeDetector()
    private var lastAt = 0L
    private var published = SleepInfo.OFF
    private var publishedAt = 0L
    private var listening = false

    private val tick = object : Runnable {
        override fun run() {
            step()
            if (clock.isOn) main.postDelayed(this, STEP_MS)
        }
    }

    private val listener = object : SensorEventListener {
        override fun onSensorChanged(e: SensorEvent) {
            val size = if (e.sensor.type == Sensor.TYPE_LINEAR_ACCELERATION) {
                Math.sqrt((e.values[0] * e.values[0] + e.values[1] * e.values[1] + e.values[2] * e.values[2]).toDouble()).toFloat()
            } else {
                Math.abs(Math.sqrt((e.values[0] * e.values[0] + e.values[1] * e.values[1] + e.values[2] * e.values[2]).toDouble()).toFloat() - 9.81f)
            }
            if (shake.sample(size, SystemClock.elapsedRealtime())) plus()
        }

        override fun onAccuracyChanged(sensor: Sensor?, accuracy: Int) {}
    }

    val info: SleepInfo get() = clock.info(exo.currentPosition, speed())

    /** The page's choice: `off`, `plus`, `chapter` or minutes as digits. */
    fun command(choice: String, minutes: Int) {
        when (choice) {
            "off" -> off()
            "plus" -> plus()
            else -> {
                val end = if (choice == "chapter") chapterEnd() else 0L
                if (choice == "chapter" && end == null) return
                clock.set(choice, minutes, end ?: 0L)
                begin()
            }
        }
    }

    private fun begin() {
        stopSensor()
        exo.volume = 1f
        lastAt = SystemClock.elapsedRealtime()
        main.removeCallbacks(tick)
        main.post(tick)
        publishNow()
    }

    fun off() {
        clock.off()
        settle()
    }

    private fun plus() {
        if (!clock.isOn) return
        clock.plus(exo.currentPosition, speed())
        stopSensor()
        exo.volume = 1f
        publishNow()
    }

    /** A new book: minutes go on; a chapter timer follows the new book's chapters. */
    fun bookChanged() {
        if (!clock.isOn) return
        chapterEnd()?.let { clock.followChapter(it) }
    }

    /** The book ended. True when a timer was on: it is cleared, and the caller must not ask the queue (D5). */
    fun endedBook(): Boolean {
        if (!clock.isOn) return false
        Log.i(TAG, "sleep: the book ended under the timer, queue not asked")
        off()
        return true
    }

    private fun step() {
        val now = SystemClock.elapsedRealtime()
        val delta = now - lastAt
        lastAt = now
        val position = exo.currentPosition
        val s = clock.step(delta, exo.isPlaying, position, speed(), chapterEnd() ?: position)
        when (s) {
            is SleepStep.Run -> {
                if (exo.volume != s.volume) exo.volume = s.volume
                if (clock.info(position, speed()).fading) startSensor() else stopSensor()
                publishIfDue(now)
            }
            is SleepStep.Finish -> {
                Log.i(TAG, "sleep: finished")
                exo.pause()
                val duration = exo.duration
                s.restAtMs?.let { rest ->
                    // The last chapter ends at the end of the book; resting 1 s short keeps the queue from popping (D6).
                    exo.seekTo(if (duration > 0) minOf(rest, duration - 1000).coerceAtLeast(0) else rest)
                }
                settle()
            }
        }
    }

    private fun settle() {
        main.removeCallbacks(tick)
        stopSensor()
        exo.volume = 1f
        publishNow()
    }

    private fun chapterEnd(): Long? = chapterEndFor(player.chapterList, exo.currentPosition)

    private fun speed() = exo.playbackParameters.speed.toDouble().coerceAtLeast(0.1)

    private fun publishNow() {
        published = info
        publishedAt = SystemClock.elapsedRealtime()
        publish(published)
    }

    /** At most once a second, and at once when the mode or the fade changes. */
    private fun publishIfDue(now: Long) {
        val i = info
        if (i.fading != published.fading || i.mode != published.mode || now - publishedAt >= 1000) publishNow()
    }

    private fun startSensor() {
        if (listening || sensors == null) return
        val sensor = sensors.getDefaultSensor(Sensor.TYPE_LINEAR_ACCELERATION) ?: sensors.getDefaultSensor(Sensor.TYPE_ACCELEROMETER) ?: return
        listening = sensors.registerListener(listener, sensor, SensorManager.SENSOR_DELAY_GAME, main)
        Log.i(TAG, "sleep: shake sensor on")
    }

    private fun stopSensor() {
        if (!listening) return
        sensors?.unregisterListener(listener)
        listening = false
        Log.i(TAG, "sleep: shake sensor off")
    }

    /** Debug and tests only: a jolt as if the sensor had read [size]. */
    internal fun jolt(size: Float) {
        if (shake.sample(size, SystemClock.elapsedRealtime())) plus()
    }

    fun release() {
        main.removeCallbacks(tick)
        stopSensor()
    }

    companion object {
        private const val TAG = "Tendshelf"
        private const val STEP_MS = 250L
    }
}
