import AppKit
import SwiftTerm
import SwiftUI

// TerminalPaneView wraps SwiftTerm.LocalProcessTerminalView. It binds to a
// `Session?`; when the session changes, the same widget gets its current
// PTY torn down and a fresh `tmux attach-session` started for the new
// session. One terminal view for the whole window — V1 has no warm cache.
//
// PTY data does NOT travel through the daemon (see docs/desktop/architecture.md).
// We attach directly to the daemon-owned tmux server at
// `~/.config/fleet/tmux.sock`, the same socket the TUI uses, so a session
// opened in either client is reachable from the other.
struct TerminalPaneView: NSViewRepresentable {
    let session: Session?

    func makeCoordinator() -> Coordinator { Coordinator() }

    func makeNSView(context: Context) -> LocalProcessTerminalView {
        let view = LocalProcessTerminalView(frame: .zero)
        view.font = NSFont.userFixedPitchFont(ofSize: 13) ?? NSFont.systemFont(ofSize: 13)
        view.allowMouseReporting = true
        view.optionAsMetaKey = true
        context.coordinator.terminal = view
        scheduleAttach(view: view, coordinator: context.coordinator)
        return view
    }

    func updateNSView(_ nsView: LocalProcessTerminalView, context: Context) {
        scheduleAttach(view: nsView, coordinator: context.coordinator)
    }

    /// Schedules `startProcess` on the next runloop turn. SwiftUI lays out
    /// representables *after* `makeNSView`/`updateNSView` returns, so the
    /// view's bounds are still `.zero` when we'd otherwise spawn — tmux
    /// would attach with a default 80×24 PTY and redraw on the next
    /// SIGWINCH (a visible 1–2s flash). Bouncing through `DispatchQueue.main`
    /// guarantees the view has its real size before tmux sees its first
    /// pane geometry.
    private func scheduleAttach(
        view: LocalProcessTerminalView,
        coordinator: Coordinator
    ) {
        let target = session?.tmuxName
        guard target != coordinator.attachedTmuxName else { return }
        coordinator.attachedTmuxName = target

        // Tear down the previous client immediately so a stale pane isn't
        // visible while we wait for the next runloop turn. terminate() sends
        // SIGHUP, which tmux maps to a clean client detach.
        view.terminate()

        guard let tmuxName = target else { return }

        DispatchQueue.main.async {
            // The view may have been recycled or selection may have changed
            // again before our turn comes up; re-check that we still want to
            // attach to this name.
            guard coordinator.attachedTmuxName == tmuxName else { return }
            view.startProcess(
                executable: Coordinator.tmuxPath,
                args: ["attach-session", "-t", tmuxName],
                environment: Self.terminalEnvironment(),
                execName: nil
            )
        }
    }

    final class Coordinator {
        var terminal: LocalProcessTerminalView?
        var attachedTmuxName: String?
        // Resolve once at coordinator creation (i.e. first view materialise).
        // tmux is required for any session to be useful; if it's missing the
        // app should fail loudly elsewhere — this is a sane fallback.
        static let tmuxPath: String = {
            let parentPath = ProcessInfo.processInfo.environment["PATH"] ?? ""
            for dir in parentPath.split(separator: ":") {
                let candidate = "\(dir)/tmux"
                if FileManager.default.isExecutableFile(atPath: candidate) {
                    return candidate
                }
            }
            return "/opt/homebrew/bin/tmux"
        }()
    }

    private static func terminalEnvironment() -> [String] {
        var env = ProcessInfo.processInfo.environment
        env["TERM"] = "xterm-256color"
        env["LANG"] = env["LANG"] ?? "en_US.UTF-8"
        env["COLORTERM"] = "truecolor"
        return env.map { "\($0.key)=\($0.value)" }
    }
}
