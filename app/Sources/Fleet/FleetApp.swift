import AppKit
import SwiftUI

@main
struct FleetApp: App {
    @State private var model = AppModel()
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var appDelegate

    var body: some Scene {
        WindowGroup("Fleet") {
            ContentView(model: model)
                .frame(minWidth: 800, minHeight: 480)
                .task {
                    model.start()
                }
        }
        .windowStyle(.titleBar)
    }
}

// SwiftPM executables don't ship an Info.plist, so AppKit defaults the
// activation policy to .prohibited — the window opens, but the process
// never becomes foreground and the window is hidden behind every other
// app. Forcing `.regular` + `activate(ignoringOtherApps:)` here makes
// `swift run Fleet` (and the bare debug binary) behave like a normal
// Mac app. When we ship a real `.app` bundle this can come out.
final class AppDelegate: NSObject, NSApplicationDelegate {
    func applicationDidFinishLaunching(_ notification: Notification) {
        NSApp.setActivationPolicy(.regular)
        NSApp.activate(ignoringOtherApps: true)
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool {
        true
    }
}
