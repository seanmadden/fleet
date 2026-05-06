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
        FontRegistration.register()
        NSApp.setActivationPolicy(.regular)
        // Force-dark appearance on every window so the title bar doesn't
        // sit as a bright slab above a dark terminal pane. Also dissolves
        // the hard seam at the top of the sidebar. This is hygiene, not a
        // design opinion — when the UX designer hands over a real visual
        // system we'll switch to whatever they spec.
        NSApp.appearance = NSAppearance(named: .darkAqua)
        NSApp.activate(ignoringOtherApps: true)

        // Apply the unified-ish chrome on every window the app opens.
        // Doing it here (vs from `.task` in SwiftUI) avoids a one-frame
        // flash of the default title bar before SwiftUI mounts.
        DispatchQueue.main.async {
            for window in NSApp.windows {
                Self.style(window)
            }
        }
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool {
        true
    }

    static func style(_ window: NSWindow) {
        window.titlebarAppearsTransparent = true
        window.titleVisibility = .hidden
        window.styleMask.insert(.fullSizeContentView)
        // Match the terminal's background so the title-bar area, the
        // window edges during a resize, and the detail pane all read as
        // one surface.
        window.backgroundColor = TokyoNight.background
    }
}
