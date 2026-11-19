import UIKit

/// Only the background download session needs the app delegate: when the system wakes the app for a finished download it hands
/// over a completion handler, which `BackgroundTransport` calls once every event was delivered.
final class AppDelegate: NSObject, UIApplicationDelegate {
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
