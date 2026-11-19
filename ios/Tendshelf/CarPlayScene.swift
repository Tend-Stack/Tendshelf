import CarPlay
import UIKit

/// The CarPlay scene's delegate (decision 0048, I4c): the car screen connects and disconnects without the phone window; both go to `CarPlay`.
/// The class name is the one `Info.plist` names (`$(PRODUCT_MODULE_NAME).CarPlayScene`).
final class CarPlayScene: UIResponder, CPTemplateApplicationSceneDelegate {
    func templateApplicationScene(_ templateApplicationScene: CPTemplateApplicationScene, didConnect interfaceController: CPInterfaceController) {
        _ = ShellController.shared // started from the car alone: the player, the downloads and the connection exist before a list loads
        CarPlay.shared.connect(interfaceController)
    }

    func templateApplicationScene(_ templateApplicationScene: CPTemplateApplicationScene, didDisconnectInterfaceController interfaceController: CPInterfaceController) {
        CarPlay.shared.disconnect()
    }
}
