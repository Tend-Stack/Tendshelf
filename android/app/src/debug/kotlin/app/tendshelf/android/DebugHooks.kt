package app.tendshelf.android

import android.app.Activity
import android.content.Intent
import android.util.Log
import app.tendshelf.shared.Origin

/**
 * Debug builds only (src/debug): emulator checks drive the app from `am start` and `am startservice`. The release
 * source set has a stub with the same names, so none of this reaches a release APK (checked with the APK's dex).
 *
 * Activity extras: `server` fills the address; `--ez bridge false` leaves the bridge off, so the page plays by itself.
 * Service: action [ACTION], `--es op start --es book <id> [--el at <ms>]` starts through the stored device token;
 * `server` and `token` extras name another server and bearer for probes. Other ops drive the player directly.
 */
object DebugHooks {
    const val ACTION = "app.tendshelf.DEBUG_PLAYER"

    fun serverExtra(intent: Intent): String? = intent.getStringExtra("server")

    fun bridgeEnabled(intent: Intent): Boolean = intent.getBooleanExtra("bridge", true)

    /** The shared emulator sits behind a lock screen with a credential nobody enters. */
    fun onActivityCreate(activity: Activity) {
        activity.setShowWhenLocked(true)
        activity.setTurnScreenOn(true)
    }

    fun onServiceStart(service: PlaybackService, intent: Intent?): Boolean {
        if (intent?.action != ACTION) return false
        keepAlive(service)
        run(service, intent)
        return true
    }

    /**
     * `am start-foreground-service` demands startForeground within seconds, and the emulator's lock screen
     * keeps the app in the background. A throwaway notification satisfies that; the media notification takes
     * over once a book plays, and this one is removed after 10 s.
     */
    private fun keepAlive(service: PlaybackService) {
        val nm = service.getSystemService(android.app.NotificationManager::class.java)
        nm.createNotificationChannel(android.app.NotificationChannel("debug", "Debug", android.app.NotificationManager.IMPORTANCE_LOW))
        val n = android.app.Notification.Builder(service, "debug").setSmallIcon(android.R.drawable.ic_media_play).setContentTitle("debug").build()
        service.startForeground(4242, n, android.content.pm.ServiceInfo.FOREGROUND_SERVICE_TYPE_MEDIA_PLAYBACK)
        service.registerSession()
        service.main.postDelayed({ nm.cancel(4242) }, 10_000)
    }

    private fun run(service: PlaybackService, i: Intent) {
        val player = service.player
        val exo = service.exo
        when (i.getStringExtra("op")) {
            "start" -> {
                val server = i.getStringExtra("server")
                val token = i.getStringExtra("token")
                if (server != null && token != null) {
                    // A probe against another server; the origin must still parse.
                    val origin = Origin.parse(server)?.text ?: return
                    service.connections = ConnectionSource { Connection(origin, token, "debug") }
                }
                service.engine.start(i.getStringExtra("book").orEmpty(), if (i.hasExtra("at")) i.getLongExtra("at", -1) else null)
            }
            "seek" -> player.seekTo(i.getLongExtra("ms", 0))
            "speed" -> player.setPlaybackSpeed(i.getFloatExtra("speed", 1f))
            "play" -> player.play()
            "pause" -> player.pause()
            "next" -> player.seekToNext()
            "prev" -> player.seekToPrevious()
            "back" -> player.seekBack()
            "forward" -> player.seekForward()
            "stop" -> service.engine.stopAndClear()
            "dump" -> Log.i(
                PlaybackService.TAG, "dump pos=${exo.currentPosition} dur=${exo.duration} speed=${exo.playbackParameters.speed} " +
                    "playing=${exo.isPlaying} state=${exo.playbackState} chapter=${player.chapterIdx} " +
                    "title=${player.mediaMetadata.title} error=${service.session?.sessionExtras?.getString(BookEngine.EXTRA_ERROR)}",
            )
        }
    }
}
