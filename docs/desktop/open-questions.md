# Open Questions

Decisions deferred during initial brainstorming. Each has a working assumption to unblock V1 design; revisit when relevant.

## Pane warm-cache strategy (V2)

**Question**: When the user has multiple sessions and switches between them in the sidebar, should the embedded PTY hot-swap (one pane, re-attach on selection) or keep all panes warm (one PTY per session, hidden when not selected)?

**Working assumption (V1)**: Hot-swap. Single PTY widget, re-attach on selection. tmux attach replay is fast enough (~50–100ms). Memory stays cheap.

**Revisit when**: V2 multi-pane lands. If switching between panes feels laggy, move to LRU warm-cache (last 3 stay attached, rest cold).

## Setup script support (V2 / V3)

**Question**: Should fleet support Conductor-style "run setup script on workspace creation"? (e.g. `cp ../.env .env && pnpm install`)

**Working assumption**: fleet's wedge is "no setup script required, default works." Don't break this. The existing `.fleet.json` `workspace.create` shell-provider mechanism already covers users who genuinely need it.

**Revisit when**: a real user complains that monorepos with private packages are too painful. Then consider an *opt-in* `.fleet.json` `workspace.setup` field that runs after worktree creation, with progress shown in the UI. Never make it required.

## Broadcast input mode (V2 multi-pane)

**Question**: When multiple panes are visible, should there be a mode that sends the same prompt to all panes simultaneously (e.g. "spawn 3 Claudes on the same task, compare answers")?

**Working assumption**: Each pane has its own input, only the focused pane receives keystrokes. No broadcast in V1 or V2.a.

**Revisit when**: V2 multi-pane is in daily use. If "compare 3 agents" becomes a real workflow, add a View menu toggle ("Broadcast input to all panes"). Until then, it's solving a hypothetical.

## Embedded chat-wrap vs raw terminal

**Question**: Should the embedded view be a raw terminal (current bet) or a chat-style wrapper around the tmux pane (Conductor-style)?

**Working assumption**: Raw terminal. The whole moat is "the real Claude TUI inside a real terminal." We don't reimplement the chat UI. Power users get the punchy spinner / streaming feel verbatim.

**Revisit when**: never, unless we're explicitly trying to onboard non-terminal users who find the Claude TUI intimidating. Even then: the TUI parity in V1 must remain raw-terminal; a chat-wrap mode would be V5+ and opt-in.

## Why was the previous desktop attempt removed? — RESOLVED 2026-05-05

**Findings from git archaeology** (commit `7dc3289` on `feature/macos-wails-app`, March 8 2026, never merged):

The previous attempt was **Wails v2 + Svelte + xterm.js**, not Electron/Tauri. It actually worked architecturally and the code is well-structured:

- `internal/service/SessionService` — Observer pattern, shared between TUI and desktop, so "TUI continues working identically via the same service" (commit message claim).
- `internal/ptybridge/Bridge` — PTY-to-callback bridge so multiple browser-rendered terminals can attach simultaneously. Distinct from TUI's `pty.go` which uses os.Stdin/os.Stdout.
- `cmd/brizz-desktop/` — Wails bindings for Session/Git/PR DTOs and terminal attach/input/resize.
- Frontend: Svelte components (Sidebar, SessionItem, RepoGroup, Terminal, Header), 4 dialog components, reactive stores, CSS-variable theme system with 5 themes.
- 5,262 insertions in one commit. Genuinely substantial.

**Why it died (inferred — there's no PR, no explicit decision)**:

1. **Never merged. No PR opened.** Work crossed the "got it working" threshold but never the "ship it" threshold.
2. **The branch diverged from master.** The wails branch was created from `f9f21b8` (initial commit) and has its OWN copy of stages 4–6 with different commit hashes than master. Master got rebuilt versions of those stages independently. This means Yuval kept iterating on master in parallel and the wails branch fell out of sync.
3. **The branch hasn't been touched since 2026-03-08** — nearly 2 months stale by now. Master meanwhile shipped: rename to fleet, hook-based status detection improvements, auto-naming, undo-delete, slot bindings, command palette, bug report dialog, chrome extension. None of that is on the wails branch.
4. **The cost of catching the wails branch up to master** was probably the silent killer. Each new master feature was 1-2x harder to backport than to re-ship in the future "real" desktop app.

**Lessons baked into our new plan**:

1. **DO extract the daemon/service layer on `master` FIRST**, before any Mac app work. Don't repeat the side-branch mistake. Stage 0 in `roadmap.md` already does this — we made the right call without knowing.
2. **The `SessionService` observer pattern is real, working code we abandoned.** Resurrect it in the new daemon — even though we're going Swift+SwiftTerm instead of Wails+Svelte, the Go side is the same problem.
3. **The PTY bridge in `internal/ptybridge/bridge.go` is mostly stack-agnostic** — Swift+SwiftTerm needs the same primitives (callbacks for output, methods for input/resize). Resurrect with minor adaptation.
4. **The DTOs in `cmd/brizz-desktop/app.go` (SessionDTO, GitInfoDTO, PRDTO) translate directly to gRPC proto messages.** Lift into `proto/fleet.proto`.
5. **Themes-as-CSS-variables pattern doesn't help for SwiftUI** — but the actual color values from `themes.css` carry over.
6. **Stack pivot is justified**: Wails+WebKit had weaker terminal perf for Claude's heavy ANSI output, and webview keyboard/menu/drag-drop affordances are more work than SwiftUI. Swift+SwiftTerm wins on those axes specifically.

**Action item**: when Stage 0 starts, do `git diff f9f21b8..7dc3289 -- internal/service internal/ptybridge` to extract the still-relevant code. Don't rewrite from scratch what we already have working.

## Cross-platform (forever question)

**Question**: Is Mac-only acceptable forever, or do we keep open the option of porting to Windows/Linux later?

**Working assumption**: Mac-only forever. Swift + SwiftTerm gets us the best Claude TUI rendering. Cross-platform would force Tauri or Electron, which kills the perf moat.

**Revisit when**: someone with a Linux box pays for fleet Pro and complains. Even then, likely answer is "use `fleet --tui` over SSH from a Mac" rather than rewrite the shell.

## Linear / GitHub Issues integration

**Question**: Should the Mac app deep-link Linear / Issues like Conductor? (paste Linear URL → spawn session with issue context prefilled)

**Working assumption**: V4. Useful but not core. Implementation is small (oauth + paste handler + prompt prefill) but we don't ship it before V1/V2/V3 priorities.

**Revisit when**: V3 ships. By then the daemon RPCs are stable enough to add a clean integration layer.

## Modes pattern (Code / Ask / Architect / Debug)

**Question**: Should sessions have an explicit mode that biases the system prompt (Roo Code-style)?

**Working assumption**: V4. Roo's Modes UX is good but additive; fleet's existing per-session settings are already flexible enough. Add when there's clear demand.

**Revisit when**: Yuval wants different default Claude behavior per session for routine tasks (e.g. "always use Architect mode for design sessions"). Then add a Mode picker in the new-session dialog.

## Plan-mode / effort toggles UX surface

**Question**: Should plan-mode (`shift-tab` in Claude TUI) and effort level have toggles in the Mac app's chrome, or stay inside the TUI?

**Working assumption**: Stay inside the TUI in V1. The native chrome shouldn't reach inside the Claude pane to control its modes — that's a leaky abstraction.

**Revisit when**: V3 — when we have `@terminal` mention machinery, we can add a "Plan mode" toggle in the toolbar that sends `shift-tab` via tmux send-keys. Cleaner: a per-session attribute that gets sent on launch.

## Branding launch story

**Question**: When we go public with the Mac app, do we keep `brew install brizzai/tap/fleet` or also offer a `.dmg` download?

**Working assumption**: Both. brew for terminal-native users (existing path), `.dmg` for everyone else (download from website, drag to Applications). Auto-update via Sparkle or custom mechanism.

**Revisit when**: V1 ships. Tackle release infra at that point.

## "Done" state derivation

**Question**: Conductor's "Done" status group — how do sessions get there in fleet's model? Manual mark? Auto-derive from PR-merged?

**Working assumption**: V2 introduces a manual "mark done" action (right-click → Mark Done, or a keybind). Auto-derive from PR-merged is a nice-to-have for V3.

**Revisit when**: V2 status-grouped sidebar lands. If users keep manually marking done, the workflow's right; if nobody uses it, auto-derive instead.

## Daemon lifecycle on Mac

**Question**: launchd LaunchAgent (auto-start at login) vs spawn-on-demand?

**Working assumption**: Spawn-on-demand for V1 — daemon dies when last client closes. Simpler. No login items to manage.

**Revisit when**: hooks-on-pending-sessions become important (i.e. you want notifications even when no client is open). Then promote to LaunchAgent. Settings toggle: "Run fleet in background when app is closed" (default off).

## Telemetry / crash reporting

**Question**: Do we ship telemetry (anonymous usage stats, crash reports) with the Mac app?

**Working assumption**: Crash reporting yes (sentry-style, opt-out), usage telemetry no. Trust matters more than dashboards for a tool this early.

**Revisit when**: launching publicly. Add a Settings → Privacy section that's clear about what's sent.
