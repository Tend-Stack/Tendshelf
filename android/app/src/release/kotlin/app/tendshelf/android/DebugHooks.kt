package app.tendshelf.android

import android.app.Activity
import android.content.Intent

/**
 * The release copy of the debug hooks: nothing. The real ones live in src/debug, so a release APK holds no start
 * action, no start extras (`server`, `user`, `pass`, `token`, `book`) and no way to switch the bridge off.
 */
object DebugHooks {
    fun onServiceStart(service: PlaybackService, intent: Intent?): Boolean = false
    fun serverExtra(intent: Intent): String? = null
    fun onActivityCreate(activity: Activity) {}
    fun bridgeEnabled(intent: Intent): Boolean = true
}
