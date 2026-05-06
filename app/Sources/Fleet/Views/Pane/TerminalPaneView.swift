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

    // SwiftTerm has no built-in concept of internal padding, so we wrap it
    // in an NSView that insets the terminal by `padding` on every edge.
    // Anything else (caret color, font) is set on the inner view directly.
    private static let padding: CGFloat = 10
    private static let cornerRadius: CGFloat = 4

    func makeCoordinator() -> Coordinator { Coordinator() }

    func makeNSView(context: Context) -> NSView {
        let container = NSView(frame: .zero)
        container.wantsLayer = true
        // Match the terminal's native background so the padding doesn't
        // show as a visible seam if the terminal redraws lazily.
        container.layer?.backgroundColor = TokyoNight.background.cgColor

        let term = LocalProcessTerminalView(frame: .zero)
        term.translatesAutoresizingMaskIntoConstraints = false
        term.font = Self.preferredFont()
        term.allowMouseReporting = true
        term.optionAsMetaKey = true

        // Placeholder palette — Tokyo Night. Real visual system comes from
        // the UX designer; this just stops us from looking like a 1990s xterm.
        term.installColors(TokyoNight.ansi())
        term.nativeBackgroundColor = TokyoNight.background
        term.nativeForegroundColor = TokyoNight.foreground
        term.caretColor = TokyoNight.cursor
        term.selectedTextBackgroundColor = TokyoNight.selectionBackground

        container.addSubview(term)
        NSLayoutConstraint.activate([
            term.leadingAnchor.constraint(equalTo: container.leadingAnchor, constant: Self.padding),
            term.trailingAnchor.constraint(equalTo: container.trailingAnchor, constant: -Self.padding),
            term.topAnchor.constraint(equalTo: container.topAnchor, constant: Self.padding),
            term.bottomAnchor.constraint(equalTo: container.bottomAnchor, constant: -Self.padding),
        ])

        context.coordinator.terminal = term
        scheduleAttach(view: term, coordinator: context.coordinator)
        return container
    }

    func updateNSView(_ nsView: NSView, context: Context) {
        guard let term = context.coordinator.terminal else { return }
        scheduleAttach(view: term, coordinator: context.coordinator)
    }

    private static let fontSize: CGFloat = 13.5

    private static func preferredFont() -> NSFont {
        // The bundled font registers under the family "JetBrains Mono"
        // (with space) but its PostScript name is "JetBrainsMono-Regular"
        // (no space). NSFont(name:) takes the PostScript name; if that
        // miss-resolves we fall back to the family lookup via NSFontManager.
        if let f = NSFont(name: "JetBrainsMono-Regular", size: fontSize) {
            return f
        }
        if let f = NSFontManager.shared.font(
            withFamily: "JetBrains Mono",
            traits: [],
            weight: 5,
            size: fontSize
        ) {
            return f
        }
        return NSFont.userFixedPitchFont(ofSize: fontSize) ?? NSFont.systemFont(ofSize: fontSize)
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
