package app.tendshelf.android

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Intent
import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.offline.Download
import androidx.media3.exoplayer.offline.DownloadManager
import androidx.media3.exoplayer.offline.DownloadService
import androidx.media3.exoplayer.scheduler.Scheduler

/**
 * The foreground service that keeps a download going with the screen off (type `dataSync`). It has no scheduler (D2):
 * a scheduler would start it from the background, which Android refuses for this type; the visible activity resumes
 * downloads instead; the player may also start it while it is playing, for the next book (decision 0035), and a refusal
 * only leaves that book held. On Android 15 and later the system lets a `dataSync` service run 6 hours in 24 (D3); at the limit
 * the downloads pause, a notification says so, and the next time the app is opened they go on by byte range.
 */
@UnstableApi
class TsDownloadService : DownloadService(NOTIFICATION_ID, 1000L, CHANNEL, R.string.downloads_channel, 0) {
    override fun onCreate() {
        Offline.init(this)
        super.onCreate()
    }

    override fun getDownloadManager(): DownloadManager = Offline.manager()

    override fun getScheduler(): Scheduler? = null

    /** Our own progress, by the same whole-book bytes as the page; the helper averages per file and starts at nothing. */
    override fun getForegroundNotification(downloads: MutableList<Download>, notMetRequirements: Int): Notification {
        val b = Notification.Builder(this, CHANNEL)
            .setSmallIcon(android.R.drawable.stat_sys_download)
            .setContentIntent(openApp())
            .setOngoing(true)
            .setOnlyAlertOnce(true)
            .setShowWhen(false)
        val now = Offline.notificationProgress(downloads)
        when {
            notMetRequirements != 0 -> b.setContentTitle(getString(R.string.downloads_channel)).setContentText(getString(R.string.downloads_waiting))
            now == null -> b.setContentTitle(getString(R.string.downloads_channel)).setContentText(getString(R.string.downloads_waiting)).setProgress(0, 0, true)
            now.second.known -> b.setContentTitle(now.first).setContentText(getString(R.string.downloads_percent, now.second.pct)).setProgress(100, now.second.pct, false)
            else -> b.setContentTitle(now.first).setContentText(getString(R.string.downloads_working)).setProgress(0, 0, true)
        }
        return b.build()
    }

    override fun onTimeout(startId: Int, fgsType: Int) {
        Offline.onTimeLimit()
        val nm = getSystemService(NotificationManager::class.java)
        nm.createNotificationChannel(NotificationChannel(LIMIT_CHANNEL, getString(R.string.downloads_channel), NotificationManager.IMPORTANCE_DEFAULT))
        nm.notify(
            LIMIT_ID,
            Notification.Builder(this, LIMIT_CHANNEL)
                .setSmallIcon(R.drawable.ic_stat_tendshelf)
                .setContentTitle(getString(R.string.downloads_paused))
                .setAutoCancel(true)
                .setContentIntent(openApp())
                .build(),
        )
        super.onTimeout(startId, fgsType)
    }

    private fun openApp(): PendingIntent = PendingIntent.getActivity(
        this, 2,
        Intent(this, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP or Intent.FLAG_ACTIVITY_NEW_TASK),
        PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
    )

    private companion object {
        const val NOTIFICATION_ID = 4245
        const val CHANNEL = "downloads"
        const val LIMIT_CHANNEL = "downloads_limit"
        const val LIMIT_ID = 4246
    }
}
