import SwiftUI

@main
struct TendshelfApp: App {
    @UIApplicationDelegateAdaptor(AppDelegate.self) private var appDelegate
    @State private var controller = ShellController()

    var body: some Scene {
        WindowGroup {
            ShellView(controller: controller)
                .ignoresSafeArea()
                .onAppear { controller.flow.launch() }
                .onOpenURL { controller.flow.openLink($0) }
        }
    }
}
