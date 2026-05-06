# Architecture — fleet Mac App

> **SwiftTerm spike validated 2026-05-05** — `spikes/swiftterm-spike/` proved the rendering claim: a `LocalProcessTerminalView` spawned with `tmux attach-session -t <fleet_session>` displays the Claude Code TUI identically to Terminal.app. PTY data flows directly between SwiftTerm and tmux; no proxy in the path.
>
> **Tmux topology amendment 2026-05-07** — V1 implementation reality: the daemon spawns sessions on the **host's default tmux server**, not on a fleet-private socket. Clients (Mac app, TUI) attach with plain `tmux attach-session -t fleet_<id>` (no `-S` flag). The "private socket at `~/.config/fleet/tmux.sock`" plan in this doc was superseded; sections below still reference the original plan for historical context.

## High-level topology

```
┌─────────────────────────────────────────────────────────────────────┐
│                                                                     │
│   fleet daemon  (Go, long-running background service)               │
│   ─────────────────────────────────────────────────────────────     │
│   • Owns SQLite (state.db, slot_bindings, etc.)                     │
│   • Owns hooks/watcher (status file polling)                        │
│   • Owns tmux operations (create, kill, capture, send-keys)         │
│   • Owns git, gh, repo info cache                                   │
│   • Owns chrome-host (existing Chrome NMH bridge)                   │
│   • Owns naming / auto-rename worker                                │
│   ▲                                                                 │
│   │  exposes: gRPC over Unix socket                                 │
│   │  ~/.config/fleet/daemon.sock                                    │
│   ▼                                                                 │
│        ┌──────────────────┬──────────────────┬──────────────────┐   │
│        │                  │                  │                  │   │
│   fleet.app (Swift)   fleet --tui (Go)   fleet CLI (Go)         │   │
│   SwiftUI + SwiftTerm Bubble Tea         Cobra commands         │   │
│        │                  │                  │                  │   │
│   PTY widget directly attaches to tmux server (separate channel)│   │
│   via `tmux -S /run/fleet-tmux.sock attach -t fleet_<id>`       │   │
│                                                                 │   │
└─────────────────────────────────────────────────────────────────────┘
```

Key design choice: **PTY data does not travel through the daemon**. The daemon owns the tmux *server* (it spawns it on first session create with a known socket path). Each client opens its own `tmux attach` against that socket and gets a PTY locally. The daemon stays a control plane; PTY bytes go directly between SwiftTerm and tmux.

This is the same pattern iTerm2's tmux integration uses. It's clean, efficient, and keeps the daemon out of the hot path.

## Process model

### `fleet daemon`

New subcommand. Starts a long-running service:

- launchd `LaunchAgent` plist installed by Mac app on first run (auto-start at login, optional)
- For TUI / CLI users, the binary auto-spawns the daemon if it's not running and the user invokes any command
- Idle daemon footprint target: <30MB RAM, <0.1% CPU when no sessions are active

Daemon owns:
- SQLite at `~/.config/fleet/state.db`
- Tmux server at `~/.config/fleet/tmux.sock` (created on first session)
- Hooks dir at `~/.config/fleet/hooks/`
- Chrome socket at `~/.config/fleet/chrome.sock`
- The new daemon socket at `~/.config/fleet/daemon.sock`

### Clients

- **`fleet.app`** (Swift) — the Mac app. Renders sidebar, panes, dialogs in SwiftUI. Embeds SwiftTerm widgets that attach to the daemon's tmux server.
- **`fleet --tui`** (Go) — the existing Bubble Tea TUI, refactored to talk to the daemon instead of holding state directly. Continues to work over SSH.
- **`fleet add | list | remove | hooks | hook-handler | chrome-host`** — existing CLI subcommands, refactored to call daemon over the socket.

The daemon process is shared. State is unified. The Mac app and the TUI can both be open at the same time and see identical state.

## IPC choice — gRPC over Unix socket

Why gRPC:

- **Strongly typed schema** — `proto/fleet.proto` is a single source of truth Swift, Go, and any future client share.
- **Native streaming** — sidebar updates, status changes, hook events are server-streaming RPCs. No polling.
- **Mature Swift support** — `grpc-swift` is solid; `SwiftProtobuf` generates clean structs.
- **Mature Go support** — first-class.
- **Unix socket transport** keeps it local-only by default; never exposed on network.

Real schema is at [`proto/fleet/v1/fleet.proto`](../../proto/fleet/v1/fleet.proto). Highlights:

- `ListSessions` and `ListRepos` are server-streaming — first message is a snapshot, subsequent messages are added/changed/removed deltas.
- `StreamHookEvents` drives macOS notifications (Mac app subscribes with a session filter; TUI subscribes for the action log).
- `SendKeys` covers quick-approve (Y), mid-flight steering, and any chrome-level interaction with the running Claude TUI.
- `CapturePane` returns ANSI-stripped pane content for `@terminal` mention.
- V2 RPCs (`GetWorkspaceDiff`, `GetSessionStats`, `RunSuggestedAction`) are sketched in proto comments; uncomment when V2 work starts.

Alternative considered: **JSON-RPC over Unix socket**. Simpler, no codegen, but every client reinvents typing. Rejected.

Alternative considered: **CGO / shared library** linking Go core into Swift. Rejected because Go GC pauses can stall the UI thread; a process boundary is healthier.

## Tmux topology

```
fleet daemon
  └─> spawns tmux server with socket at ~/.config/fleet/tmux.sock
       │
       ├─ tmux session: fleet_a1b2c3d4-1715000000  (Yuval/feature-x)
       ├─ tmux session: fleet_e5f6g7h8-1715000050  (api-fix)
       └─ ...

fleet.app (Swift)
  └─> SwiftTerm widget for active session
       └─> spawns local tmux client:
           tmux -S ~/.config/fleet/tmux.sock attach -t fleet_<id>
           This produces a PTY that SwiftTerm renders.
       └─> Ctrl+Q is intercepted by the Swift widget (same as today's TUI),
           translates to a clean `kill <client>` of just this tmux client.

fleet --tui (Go)
  └─> Same pattern, just attaches to the same tmux socket.
```

This means a session opened in the Mac app is immediately visible in the TUI (and vice versa). Tmux is the shared substrate.

The current `creack/pty + golang.org/x/term` PTY+attach code in `internal/tmux/pty.go` is migrated to: the **daemon** never spawns PTYs (it only `tmux new-session -d` to create detached sessions), and each client spawns its own `tmux attach` PTY locally.

## Swift app structure (sketch)

```
macapp/
├── Fleet.xcodeproj
├── Fleet/
│   ├── FleetApp.swift              # @main entry
│   ├── DaemonClient/
│   │   ├── DaemonClient.swift      # gRPC client wrapper
│   │   └── Generated/              # protoc-generated Swift types
│   ├── Views/
│   │   ├── ContentView.swift       # sidebar + active pane
│   │   ├── Sidebar/
│   │   │   ├── SidebarView.swift
│   │   │   ├── SessionRow.swift
│   │   │   └── RepoGroupHeader.swift
│   │   ├── Pane/
│   │   │   ├── SessionPaneView.swift     # SwiftTerm host + toolbar
│   │   │   └── EmptyStateView.swift
│   │   ├── Dialogs/
│   │   │   ├── NewSessionDialog.swift
│   │   │   ├── NewWorkspaceDialog.swift
│   │   │   ├── SettingsDialog.swift
│   │   │   └── BugReportDialog.swift
│   │   └── Toolbar/
│   ├── Models/
│   │   ├── Session.swift
│   │   ├── Repo.swift
│   │   └── Status.swift
│   ├── Notifications/
│   │   └── HookNotificationService.swift
│   └── Resources/
│       └── Assets.xcassets
└── Package.swift                   # SwiftPM deps: grpc-swift, SwiftTerm
```

## Migration path from today's monolithic TUI

Phased so the TUI never breaks:

1. **Extract daemon** — Move `internal/session`, `internal/tmux`, `internal/git`, `internal/github`, `internal/hooks`, `internal/storage` into a daemon binary. The TUI starts to import them through a thin client. Verify TUI still works against the in-process daemon.
2. **Add socket layer** — Daemon listens on `~/.config/fleet/daemon.sock`. TUI connects over socket instead of in-process. Verify TUI still works against the socket.
3. **Auto-spawn daemon** — TUI / CLI auto-start the daemon if not running. Add `fleet daemon stop` and `fleet daemon status` admin commands.
4. **Build Swift app V1** — Single-pane TUI parity. Connect to daemon over the same socket. Ship.
5. **V2 features** in parallel after V1 stabilizes.

Each phase ships independently. We never have a long-lived "TUI is broken while we extract daemon" state.

## Notable subsystems

### Hooks → notifications

Today: hook handler writes JSON to `~/.config/fleet/hooks/<session>.json`. The TUI's HookWatcher polls.

Future: daemon's HookWatcher publishes hook events on a server-streaming RPC (`StreamHookEvents`). The Mac app subscribes and triggers `UNUserNotificationCenter` notifications when status flips to Waiting/Finished. The TUI keeps its existing flash-error / sidebar-update behavior.

### Chrome extension

`fleet chrome-host` (the native messaging host) becomes a daemon-internal worker rather than a separate process. Or stays as-is — the existing socket protocol already routes through `~/.config/fleet/chrome.sock`. Decision deferred.

### `@terminal` mention (V2)

The Mac app's prompt composer is still the Claude TUI inside SwiftTerm — we don't proxy. But we can add a "+" menu next to the SwiftTerm widget that, when clicked, captures the current pane (via daemon `CapturePane` RPC), copies it to clipboard, and shows a hint like "paste with Cmd+V". Or it sends `tmux paste-buffer` directly. Implementation detail.

### Cost analytics (V4)

Daemon parses `~/.claude/projects/<repo>/<session>.jsonl` files to extract per-session token usage. Exposes `GetSessionStats` RPC. Mac app shows a small per-session badge and an aggregated "today's spend" in the sidebar footer.
