# Stage 1 — V1 Mac app, Slice 2: mutations + diagnostics + status-detection rewrite

**Landed on `fleet-ui` 2026-05-09 in `486d5df`.** Still no PR (bundled-PR holding decision continues).

## Goal

Make the Mac app actually usable, not just demo-able: wire the read-write RPCs so the user can drive sessions from the app instead of falling back to the TUI. Then dig into why status updates felt unstable (0 to 13 seconds, sometimes minutes) and rebuild the daemon's status worker to be deterministic. Add a Cmd-Shift-D snapshot mechanism so the loop "user reports lag → I read state → I fix" works without theatre.

## Locked decisions (this round)

| Decision | Choice |
|---|---|
| Mutation scope | Y quick-approve, Delete (with confirm), Rename (inline), Restart, Acknowledge-on-selection, Pin/Unpin repo. **Not** new-session/new-worktree/settings/help/bug-report/command-palette dialogs. |
| Error UX | Top toast banner, mirrors the disconnect banner shape — 5-second ephemeral red row above the split view. Considered NSAlert and inline-per-row; toast won on parity with the TUI's flash-and-vanish errors. |
| Native menu | New `Session` menu, default File menu emptied (no docs to manage). Cmd-Y / Cmd-R / Cmd-Shift-R / Cmd-Backspace are global shortcuts; row context menu mirrors them. |
| Snapshot trigger | `Cmd-Shift-D`, writes `~/.config/fleet/snapshots/snapshot-YYYYMMDD-HHMMSS.md`. **No Finder reveal**, no clipboard copy — predictable path so a future Claude session can read the newest file directly. |
| Snapshot scope | Status-detection focused: per-session anti-flicker state, status transition log, hook fsnotify event log, worker cycle log. Not "full diagnostic dump" — keeps the markdown scannable. |

## What landed

### A. Mutations (Mac app side)

- `app/Sources/Fleet/DaemonClient/Mutator.swift`: thin actor over `FleetFleet.ClientProtocol` exposing `sendKeys / delete / restart / rename / acknowledge / pinRepo / unpinRepo / diagnostics`. Held by `AppModel` while the daemon link is open; cleared via `defer { Task { @MainActor in model.attach(mutator: nil) } }` when the gRPC client closes.
- `app/Sources/Fleet/ViewModel/AppModel.swift`: dispatch helpers (`dispatchQuickApprove`, `dispatchDelete`, `dispatchRestart`, `dispatchRename`, `dispatchAcknowledge`, `dispatchPinRepo`, `dispatchSnapshot`). Each routes through a `run(label:op:)` helper that catches errors → ephemeral toast banner.
  - **Optimistic flip on Y**: `dispatchQuickApprove` flips the local row `waiting → running` *before* awaiting `SendKeys`. The daemon's real `SessionUpdate` reconciles immediately after; the user gets <16ms feedback that their Y landed.
  - **Acknowledge on selection**: `selectedSessionID` `didSet` fires `dispatchAcknowledge` when the new selection is in `finished` state and not already acknowledged. Errors are swallowed (acknowledge is best-effort).
- `app/Sources/Fleet/Views/Sidebar/SessionRow.swift`: row gains a context menu (Approve / Rename / Restart / Delete) and an inline rename mode. Rename swaps the row's `Text` for a focused `TextField`; Enter commits via `dispatchRename`, Esc cancels, focus loss commits.
- `app/Sources/Fleet/Views/Sidebar/RepoGroupHeader.swift`: pin chevron + context menu (Pin/Unpin Repo).
- `app/Sources/Fleet/Views/ContentView.swift`: adds the toast banner above the disconnect banner, plus a `.alert` modifier driven by `model.pendingDeletion` for the delete confirm.
- `app/Sources/Fleet/FleetCommands.swift` (new): `Commands` body installed on the `WindowGroup` via `.commands { FleetCommands(model: model) }`. Empties the default File menu (`CommandGroup(replacing: .newItem) {}`) and adds a `Session` menu with Cmd-Y / Cmd-Shift-R / Cmd-R / Cmd-Backspace / Cmd-Shift-D entries.

### B. Diagnostics snapshot

- `proto/fleet/v1/fleet.proto`: `GetDiagnostics(Empty) returns DiagnosticsResponse { string markdown; google.protobuf.Timestamp captured_at; }`. Markdown shape is intentionally not a stable wire contract — daemon evolves the format freely.
- `internal/daemonsrv/diagnostics.go` (new): renders the snapshot. Type-asserts the `service.Service` interface to a local `diagnosticsProvider` interface (only the in-process `*SessionService` satisfies it) — keeps the diagnostics-only methods off the abstract interface so test fakes don't have to stub them.
- `internal/service/service.go`: bounded ring buffers for cycle log (last 100) and transition log (last 200). Each `statusWorkerCycle` records its kind / duration / counts; each status transition records old → new with source (`hook` / `pane`) and human reason (`priority scan`, `round-robin scan`, etc.).
- `internal/hooks/hook_watcher.go`: bounded ring buffer for fsnotify events (last 100). Each captures the file mtime alongside the daemon's process timestamp so the snapshot can show dispatch lag (fsnotify → debounce → daemon-saw-it).
- `internal/session/session.go`: new `Diagnostics()` method exposing the per-session anti-flicker fields (`hookStatus`, `hookUpdatedAt`, `lastContentHash`, `lastContentChangeAt`).
- `app/Sources/Fleet/DaemonClient/SnapshotWriter.swift` (new): writes the markdown blob atomically to `~/.config/fleet/snapshots/snapshot-YYYYMMDD-HHMMSS.md`. No Finder reveal, no clipboard.
- Markdown sections in each snapshot: per-session table, "Recent status transitions", "Recent hook events", "Recent worker cycles". See `fleet_diagnostics_snapshots.md` in this Claude project's memory dir for the contract.

### C. Status detection rewrite

The reason "ok its working this" came after several rounds. Each fix uncovered the next bottleneck.

#### C.1 — Daemon-side hook nudge + early notify (first attempt)

- `SendKeys` handler: nudge `s.svc.TriggerRefresh()` after a successful send.
- `internal/service/service.go`: forward `hookWatcher.Changes()` into a goroutine that fires `TriggerRefresh()`.
- Worker cycle: emit `notify(EventSessionStatusChanged)` after the priority drain (in addition to the final notify), so the wire-side `SessionUpdate` doesn't wait on git/PR refresh.

**Result**: `running → waiting` dropped from ~5s to ~2s. Better, but still not snappy. Worse: occasionally 5s+ when the worker was mid-`gh pr view`.

#### C.2 — Three-goroutine worker split

The single status worker was eating its own latency: a long full cycle (which could include `gh pr view`) blocked everything queued behind it. Split into three:

- `statusWorker` (existing): full cycles on the 2s ticker. Now skips git/PR refresh.
- `fastWorker` (new): dedicated lane for hook-watcher fsnotify events. `statusWorkerCycle(true)` does hook sync + priority drain + notify only — no auto-name JSONL reads, no round-robin, no git. Cannot queue behind anything.
- `repoWorker` (new): owns git refresh + `gh pr view` on its own ticker. The slow stuff that was blocking everything is off the critical path entirely.

`hookWatcher.Changes()` now feeds `fastTrigger` (a separate channel from `statusTrigger`).

**Result**: full cycles run in ~30-100ms, fast cycles in ~15-25ms. But `running → waiting` was still inconsistent — sometimes instant, sometimes 5+ seconds.

#### C.3 — Use the right return value

Tracing it via the snapshot showed the actual bug: every cycle showed `priority=0, hook-synced=0, status_changed=N` from round-robin scans. Hook sync was running but never enqueueing to priority.

The cause: `Session.UpdateHookStatus()` only stores hook fields — it doesn't flip `Status`. So `if sess.GetStatus() != oldStatus` after `UpdateHookStatus` was **always false**. The priority queue was never populated by hook events. Hook-driven changes had to wait for the round-robin index to coincidentally land on the affected session — with 32-35 sessions and 5 per cycle, that's 0–14s worst case. Sometimes lucky and instant, sometimes 5s+.

Fix: use `UpdateHookStatus`'s actual return value (true when hook fields shifted). Enqueue those sessions to `priorityStatusUpdates`. The priority drain a few microseconds later runs `UpdateStatus()` which reads the fresh hook and flips status. Also gate round-robin pane scans on `!fast` so fast cycles do nothing wasteful.

**Result**: `running → waiting` now consistently ~150-300ms.

#### C.4 — `applyHookFinished` activity guard rework

`waiting → finished` was still bad: 5-10 seconds, sometimes never. Snapshot showed: hook fired, fast cycle ran (`priority=1, hook-synced=1, status_changed=0`), but the row stayed at `waiting`.

`applyHookFinished` had two early-return branches *before* the activity guard:

```go
if paneStatus == StatusRunning { /* keep running, return */ }
if paneStatus == StatusWaiting { /* keep waiting, return */ }   // ← problem
```

The waiting branch was meant for sub-agent delegation (parent agent fires Stop while a child agent has a live permission prompt). It was firing in the normal case too: user just answered, Claude is mid-redraw, the menu is still visible in the pane scrollback when the daemon does `tmux capture-pane`, so `paneStatus == StatusWaiting` and we held forever.

Two follow-ups:

1. **Move the activity guard before the `paneStatus == waiting` branch.** While Claude's writing wrap-up output (activity < 3s old), don't trust the pane content — Claude hasn't redrawn yet. Hold and re-check after activity settles.

2. **Show `running` during the hold instead of preserving stale `waiting`.** The `Stop` hook is the daemon's authoritative signal that the user's input was consumed. Holding the pre-Stop `waiting` lies to the user. While in the activity-guard hold:
   - If `Status` is already `running`, leave it alone.
   - Otherwise (was `waiting`), set to `running` so the row reflects "Claude is wrapping up" — gives instant feedback that the input landed.

After activity settles (>3s of no pane writes), re-scan. If pane still shows the menu, it really is the sub-agent case → `waiting`. Otherwise → `finished`.

#### C.5 — Recheck timer

Even with the guard fix, sessions could sit in `running` indefinitely after `Stop` because nothing scheduled a re-evaluation. Fast cycles fire on hook events; round-robin only sweeps on the 2s ticker (and might not land on the right session for many cycles).

- `Session.PendingRecheckDelay() time.Duration`: returns the exact remaining hold window when the session is in the `applyHookFinished` "hold" state (hook=finished, status≠finished, activity < 3s old). Returns 0 when no recheck is needed. Includes a 200ms buffer so the recheck lands just *after* the guard releases (tmux activity timestamps are second-resolution; we don't want to race the boundary).
- `service.SessionService.scheduleRecheck(sessionID, delay)`: fires `time.AfterFunc(delay, ...)` that enqueues the session to `priorityStatusUpdates` and nudges `fastTrigger`. One-shot per call.
- `updateAndPersistStatus` checks `PendingRecheckDelay` after every scan; reschedules as long as activity keeps getting bumped (Claude wrap-up output extends the window).

**Result**: `waiting → running → finished` is now deterministic. Running flip is instant (hook arrives), finished flip lands within ~3-3.5s of Claude's last pane write.

## New constants and behavior summaries

- `finishedActivityGuard = 3 * time.Second`: the anti-flicker window. `applyHookFinished` won't trust pane state when activity is fresher than this.
- `cycleLogSize = 100`, `transitionLogSize = 200`, `hookEventLogSize = 100`: ring buffer caps for diagnostics.
- `statusRoundRobin = 5`: unchanged; round-robin still scans 5 sessions per *full* cycle.

## Verification

- `go build ./...` clean (existing pre-existing-file linter hints in `service.go` are unchanged; not from this slice).
- `go test ./internal/service/ ./internal/session/ ./internal/daemonsrv/` all green.
- `swift build` clean (one pre-existing warning about strict-concurrency `styleMask` in `FleetApp.swift`, unrelated).
- Live smoke test: every Cmd-shortcut + context-menu action exercised against a daemon with 30+ live sessions. Status latency observed via Cmd-Shift-D snapshots.

## Out of scope (still queued)

- New-session / new-worktree dialogs (and `a`/`n`/`w` keys).
- Settings dialog, help dialog, bug-report dialog, command palette.
- Slot bindings via `Alt+0-9` (TUI parity).
- Theme switcher.
- Light/dark adaptation.
- App icon assets catalog (currently runtime-rendered from `AppIconImage`).
- LRU warm-cache for terminal panes (V2 per `open-questions.md`).
- macOS notifications via `StreamHookEvents` (V2).

## Pointers to write-ups elsewhere

- `~/.claude/projects/-Users-yuvalhayke-code-brizz-code/memory/fleet_diagnostics_snapshots.md` — what's in each snapshot, where it lives, when to read it.
- `486d5df` commit body — top-level changes.
- `internal/session/session.go:609-660` — `applyHookFinished` after the rewrite.
- `internal/service/service.go` — three-goroutine worker (statusWorker / fastWorker / repoWorker) + `scheduleRecheck`.
