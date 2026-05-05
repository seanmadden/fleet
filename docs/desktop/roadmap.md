# Roadmap

Staged build plan. Each stage ships independently. Timelines are gut estimates, not commitments.

## Stage 0 — Daemon extraction (precondition for any UI work)

Pure refactor of the existing TUI. No new features. Goal: TUI works identically against an in-process or out-of-process daemon.

- Extract `internal/session`, `internal/tmux`, `internal/git`, `internal/github`, `internal/hooks`, `internal/storage` into a daemon package.
- Add `cmd/fleet/daemon.go` subcommand that runs the daemon as a service.
- Define `proto/fleet.proto` with the RPCs in `architecture.md`.
- Generate Go bindings; refactor TUI to be a daemon client.
- Auto-spawn daemon if not running, or run in-process for backwards compat.
- Smoke test: TUI behavior unchanged before and after.

**Estimate**: 2–3 weeks. Risk: high (touches every internal package).

## Stage 1 — V1 Mac app (TUI parity)

The Mac app is *just* a native shell around the existing TUI behavior. Single embedded session pane, hot-swap on selection.

### Must-have

- SwiftUI sidebar with same data as TUI (repos, sessions, statuses, slot bindings, filter).
- SwiftTerm pane that attaches to the active session via shared tmux server.
- All TUI dialogs ported as SwiftUI sheets: new-session, new-worktree, delete-confirm, rename, settings, help, bug-report, command-palette.
- All TUI keybindings work, plus native Cmd-* equivalents in the menu bar.
- Native title bar, native menu bar, native theme awareness (light/dark).
- Settings dialog parity (themes, auto-name, copy-claude-settings, etc.).

### Nice-to-have (slip if needed)

- Drag-drop a directory onto the dock icon → "open as repo."
- Multi-window support (open a second fleet window with the same daemon).
- Status-grouped sidebar mode (toggle in View menu).

### Explicitly out

- Multi-pane (V2).
- Diff viewer (V2).
- Notifications (V2).
- Suggested actions toolbar (V2).
- Cost analytics (V4).

**Definition of "done"**: Yuval uses the Mac app instead of the TUI for an entire week without regressions.

**Estimate**: 4–6 weeks of Swift work, on top of Stage 0.

## Stage 2 — V2 chrome upgrades (the headline desktop wins)

Three bundles, ship in any order based on energy:

### V2.a — Multi-pane

- 1 / 2-split / 2×2 layouts.
- Click + ⌘ on sidebar entry → add to next free pane.
- Drag from sidebar onto pane to assign.
- Per-pane close button (detach from grid, session keeps running).
- `⌘1`–`⌘4` switches active pane (the one receiving keystrokes).
- Pane warm-cache decision (see `open-questions.md`) — start with hot-swap to keep memory low; switch to LRU if switching feels slow.

### V2.b — Native diff viewer + macOS notifications

- Right-side diff panel (togglable). Calls daemon `GetWorkspaceDiff` RPC; renders unified diff with syntax highlighting.
- Per-commit filter (dropdown of recent commits).
- File-list navigator (left side of diff panel).
- Hooks → notifications: `UNUserNotificationCenter` integration. Status flips to Waiting/Finished/Error trigger native notifications.
- Dock badge with count of attention-needed sessions.
- Settings: notification preferences (which states to notify on).

### V2.c — Suggested git actions toolbar + dev-server panel

- Bottom toolbar with contextual buttons (Pull / Review / Fix Errors / Resolve / Open PR / Merge). Each button enabled only when state allows.
- Buttons either send a slash command into Claude (e.g. "Fix Errors") or shell out via daemon to `gh` / `git` (e.g. "Merge").
- Bottom-anchored dev-server panel (toggle with `⌘J`). Spawns a sub-tmux pane in the same session running a configurable command (default: read from `package.json` `scripts.dev`).
- Detect bound port; show "Open in browser" button. Optional: detect changes, auto-restart dev server on file save.

**Estimate per bundle**: 2–3 weeks each.

## Stage 3 — V3 Claude-specific killer features

Each one is a small, focused win that compounds with V2:

### V3.a — `@terminal` mention

A "+" menu near the prompt area in the SwiftTerm widget that:
1. Captures current pane (daemon `CapturePane` RPC).
2. Pastes into the next-prompt buffer.
3. Optionally: prepends "Here's what's currently on screen:\n```\n…\n```\n" framing.

### V3.b — Mid-flight steering

Send a tmux send-keys message to a running session without restart. New keybind: `⌘⏎` opens a small input field that sends a follow-up prompt to the active session. (Today, you'd attach and type — this lets you steer from outside the pane.)

### V3.c — Cost / token analytics

- Daemon parses `~/.claude/projects/<repo>/<session>.jsonl` to extract token counts.
- New `GetSessionStats` RPC.
- Sidebar shows tiny token-count badge per session.
- Footer shows aggregated "today's spend" (count + $ estimate based on model).
- Settings dialog has a "Usage" tab with per-day breakdown.

**Estimate per**: 1 week each.

## Stage 4 — V4 polish + power-user features

- **Modes pattern** (Code / Ask / Architect / Debug) — per-session attribute, sets system prompt.
- **Plan-mode / effort toggles** in the chrome.
- **MCP server status panel** (steal Conductor's `/mcp-status` UX).
- **Linear deep-link integration** — paste a Linear URL → spawns session prefilled with issue context.
- **Vercel deployment status badges** in the sidebar (per-branch).
- **Graphite stack visualization** (if user starts using Graphite).
- **Drag-drop branches** between workspaces (if it makes sense — TBD).
- **Session export** — "save this conversation as markdown" for sharing.

**Estimate**: ongoing.

## Out of scope (forever, or until proven otherwise)

- Cross-platform (Windows / Linux). Mac-only is the bet. Tauri rewrite is a separate conversation if cross-platform ever becomes worth it.
- Cloud orchestration / remote agents.
- Custom Claude chat UI.
- Inline GitHub PR comment sync.
- Agent authoring (CC Agents).
- Markdown / spreadsheet / diagram editors.
- Tasks / issue tracking system of our own.

## Decision gates between stages

After each stage, ask:

1. **Daily-driver test**: am I using the new thing instead of the old thing?
2. **Regression test**: have any existing flows degraded?
3. **Investment test**: is the next stage's marginal value worth its cost? (Maybe V2.c is more important than V2.a — let usage decide.)

Don't pre-commit to stage order beyond Stage 0 → V1.
