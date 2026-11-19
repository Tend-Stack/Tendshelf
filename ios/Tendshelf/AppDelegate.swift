import UIKit

/// The background download session and the episode refresh task need the app delegate: when the system wakes the app for a finished download it hands
/// over a completion handler, which `BackgroundTransport` calls once every event was delivered.
final class AppDelegate: NSObject, UIApplicationDelegate {
    func application(_ application: UIApplication, didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]?) -> Bool {
        // The refresh task that checks the followed shows for new episodes (decision 0048, I3c); the handler must exist now.
        EpisodeAuto.register()
        return true
    }

    func application(
        _ application: UIApplication, handleEventsForBackgroundURLSession identifier: String, completionHandler: @escaping () -> Void
    ) {
        if identifier == BackgroundTransport.identifier {
            BackgroundTransport.pendingCompletion = completionHandler
        } else {
            completionHandler()
        }
    }
}
