import SwiftUI
import WebKit

/// The window: the controller's container, full screen. The web view inside it is replaced at sign-out.
struct ShellView: UIViewRepresentable {
    let controller: ShellController

    func makeUIView(context: Context) -> UIView {
        controller.container
    }

    func updateUIView(_ view: UIView, context: Context) {}
}
