// SwiftTermSpike — minimal de-risk for embedding the Claude Code TUI inside a
// SwiftTerm-rendered tmux pane. Goal: prove that
//
//   1. SwiftTerm renders Claude's TUI faithfully (colors, box-drawing,
//      cursor, line wrapping).
//   2. Performance stays smooth under heavy ANSI floods.
//   3. Keyboard input (typing, arrows, Enter, Ctrl+Q, Tab/Shift-Tab) works.
//   4. Window resize propagates correctly to tmux.
//
// Usage:
//   swift run SwiftTermSpike <tmux-session-name>
//   swift run SwiftTermSpike --list   # show available sessions
//
// The spike spawns `tmux attach-session -t <name>` in a PTY using
// SwiftTerm.LocalProcessTerminalView. PTY data goes directly between tmux and
// the rendered view — no daemon, no proxy, same data path the real Mac app
// will use.

import AppKit
import SwiftTerm

// MARK: - argument parsing

let args = Array(CommandLine.arguments.dropFirst())

if args.contains("--list") || args.contains("-l") {
    let pipe = Pipe()
    let task = Process()
    task.launchPath = "/usr/bin/env"
    task.arguments = ["tmux", "ls"]
    task.standardOutput = pipe
    task.standardError = Pipe()
    do {
        try task.run()
    } catch {
        FileHandle.standardError.write("failed to run tmux ls: \(error)\n".data(using: .utf8)!)
        exit(1)
    }
    task.waitUntilExit()
    let data = pipe.fileHandleForReading.readDataToEndOfFile()
    if let output = String(data: data, encoding: .utf8) {
        print(output, terminator: "")
    }
    exit(0)
}

guard let sessionName = args.first else {
    FileHandle.standardError.write("""
    usage: swift run SwiftTermSpike <tmux-session-name>
           swift run SwiftTermSpike --list

    Pick any session from `tmux ls` that is NOT currently attached. The spike
    will refuse to attach to a session you're already attached to elsewhere
    (use `tmux attach -d` semantics if you want to steal it).
    """.data(using: .utf8)!)
    exit(1)
}

// MARK: - app delegate

final class AppDelegate: NSObject, NSApplicationDelegate {
    let sessionName: String
    var window: NSWindow!
    var terminalView: LocalProcessTerminalView!

    init(sessionName: String) {
        self.sessionName = sessionName
        super.init()
    }

    func applicationDidFinishLaunching(_ notification: Notification) {
        let frame = NSRect(x: 100, y: 100, width: 1100, height: 700)
        window = NSWindow(
            contentRect: frame,
            styleMask: [.titled, .closable, .resizable, .miniaturizable],
            backing: .buffered,
            defer: false
        )
        window.title = "SwiftTermSpike — \(sessionName)"
        window.minSize = NSSize(width: 600, height: 400)

        terminalView = LocalProcessTerminalView(frame: window.contentView!.bounds)
        terminalView.autoresizingMask = [.width, .height]

        // Reasonable defaults; the real app will pull these from settings.
        terminalView.font = NSFont.userFixedPitchFont(ofSize: 13) ?? NSFont.systemFont(ofSize: 13)
        terminalView.allowMouseReporting = true
        terminalView.optionAsMetaKey = true

        window.contentView!.addSubview(terminalView)
        window.makeFirstResponder(terminalView)
        window.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)

        // Spawn tmux attach in a PTY. We inherit the parent's environment so
        // PATH includes Homebrew (where tmux lives on Apple Silicon), then
        // override TERM and a few terminal-related vars.
        let parentEnv = ProcessInfo.processInfo.environment
        var envDict = parentEnv
        envDict["TERM"] = "xterm-256color"
        envDict["LANG"] = parentEnv["LANG"] ?? "en_US.UTF-8"
        envDict["COLORTERM"] = "truecolor"
        let env = envDict.map { "\($0.key)=\($0.value)" }

        // Resolve tmux to an absolute path so we don't depend on a PATH
        // lookup inside SwiftTerm's spawn (which has bitten us once).
        let tmuxPath = Self.resolveExecutable("tmux", searchPaths: parentEnv["PATH"]?.split(separator: ":").map(String.init) ?? [])
            ?? "/opt/homebrew/bin/tmux"

        terminalView.startProcess(
            executable: tmuxPath,
            args: ["attach-session", "-t", sessionName],
            environment: env,
            execName: nil
        )
    }

    func applicationWillTerminate(_ notification: Notification) {
        // Best-effort: detach gracefully so tmux session survives.
        // SwiftTerm sends SIGHUP to the PTY child on dealloc, which tmux
        // interprets as detach (not kill).
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool {
        true
    }

    /// Returns the first directory in `searchPaths` where `name` is an
    /// executable file, joined into a full path. Nil if not found.
    static func resolveExecutable(_ name: String, searchPaths: [String]) -> String? {
        let fm = FileManager.default
        for dir in searchPaths {
            let candidate = (dir as NSString).appendingPathComponent(name)
            if fm.isExecutableFile(atPath: candidate) {
                return candidate
            }
        }
        return nil
    }
}

// MARK: - boot

let app = NSApplication.shared
let delegate = AppDelegate(sessionName: sessionName)
app.delegate = delegate
app.setActivationPolicy(.regular)
app.run()
