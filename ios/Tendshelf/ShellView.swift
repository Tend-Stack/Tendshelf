import SwiftUI
import WebKit

/// The window: the controller's `WKWebView`, full screen.
struct ShellView: UIViewRepresentable {
    let controller: ShellController

    func makeUIView(context: Context) -> WKWebView {
        controller.webView
    }

    func updateUIView(_ webView: WKWebView, context: Context) {}
}
