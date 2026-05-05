# Stage 0 — First-PR Plan

> **Prerequisite confirmed (2026-05-05)**: SwiftTerm spike at `spikes/swiftterm-spike/` validated that SwiftTerm renders the Claude Code TUI faithfully when attached to a real fleet tmux session. The Mac-app rendering bet is de-risked. Stage 0 is unblocked.

The point of Stage 0 is to extract a daemon-shaped architecture from today's monolithic TUI **on master**, with the TUI still working at every commit. Once Stage 0 lands, the Mac app becomes a "build a second client" project instead of "rewrite the world."

This plan is the lesson from `feature/macos-wails-app` (March 2026): that branch had good code but lived too long off master and silently rotted. **No long-lived feature branches.** Every PR here is small enough to merge within 1–3 days.

## The non-negotiables

1. **Each PR keeps the TUI working** — no feature regressions, no behavioral changes for TUI users.
2. **Each PR is independently mergeable** — if we stop after any PR, we've left master in a better state.
3. **No code on a side branch beyond ~1 week.** If a PR is taking longer, split it.
4. **Resurrect the `feature/macos-wails-app` code rather than rewrite.** `internal/service/SessionService` and `internal/ptybridge/Bridge` already exist there and work.

## PR sequence

### PR 1 — Proto schema + buf toolchain + committed generated code

**Goal**: lock the daemon contract before touching any code.

Files:
- `proto/fleet/v1/fleet.proto` — already exists (this PR's only schema work is reviewing it).
- `proto/README.md` — already exists.
- `proto/buf.yaml` — buf v2 config (`version: v2`, modules entry).
- `buf.gen.yaml` — Go codegen plugin config (`protoc-gen-go`, `protoc-gen-go-grpc`).
- `buf.gen.swift.yaml` — Swift codegen plugin config (deferred until PR 5+; placeholder file is fine).
- `gen/proto/fleet/v1/fleet.pb.go` — generated, committed.
- `gen/proto/fleet/v1/fleet_grpc.pb.go` — generated, committed.
- `Makefile` additions:
  - `make proto` — runs `buf generate`.
  - `make proto-check` — runs `buf generate` against a temp dir, diffs against `gen/`, fails if mismatch (CI gate).
- `.github/workflows/proto.yml` — runs `make proto-check` on PRs that touch `proto/` or `gen/proto/`.
- `.pre-commit-config.yaml` — add a `proto-check` hook so generated code can't drift locally.

Acceptance:
- `make build` still works.
- `make proto` regenerates with zero diff against committed `gen/`.
- `go vet ./...` and `go build ./...` succeed against the new `gen/` package.
- CI green.

Estimated diff: +500 lines generated, +50 lines hand-written.

---

### PR 2 — Resurrect `internal/service/SessionService` and `internal/ptybridge`

**Goal**: bring the working observer-pattern service layer back to master, without changing TUI behavior.

Source: `git diff f9f21b8..7dc3289 -- internal/service internal/ptybridge` from the wails branch.

Steps:
1. Cherry-pick `internal/service/service.go` and `internal/service/events.go` from `7dc3289`.
2. Cherry-pick `internal/ptybridge/bridge.go` from `7dc3289`.
3. **Adapt to current master's package structure.** The wails branch was March 2026; master has since added auto-naming, slot bindings, undo-delete, hook improvements — the SessionService API needs new methods. List of likely additions:
   - `BindSlot(slot int, sessionID string)`, `UnbindSlot(slot int)`, `LoadSlotBindings()`
   - `Acknowledge(sessionID string)`
   - `RestoreDeleted(sessionID string)` (undo-delete)
   - Auto-naming hooks: `OnFirstPrompt`, `OnPromptCount`
   - Pin/unpin repos
4. Add a `Service` interface (so the daemon and the in-process TUI can both depend on it).
5. Unit tests for SessionService (port from wails branch + add coverage for new methods).
6. The TUI is **not yet** refactored to use SessionService. The service exists alongside the current direct-package access.

Acceptance:
- `go test ./internal/service/... ./internal/ptybridge/...` passes.
- `internal/service` exports a `SessionService` type with methods covering everything the TUI does today.
- TUI is untouched; behavior unchanged.

Estimated diff: +1,500 lines, mostly resurrected.

---

### PR 3 — TUI calls SessionService internally (still in-process)

**Goal**: refactor `internal/ui/` to consume SessionService for all mutations and subscribe to its events, instead of poking `internal/session`, `internal/storage`, `internal/git`, `internal/github` directly.

This is the biggest behavioral-risk PR — large mechanical refactor. Split if it grows past ~1,500 LoC.

Steps:
1. `home.go` and `app.go` accept a `*service.SessionService` in their constructors.
2. Every direct call to `session.NewSession`, `storage.SaveSession`, `git.RefreshGitInfo`, etc. routed through service.
3. The worker-goroutine pattern in `internal/ui/` migrates into the service. UI subscribes to events and reacts.
4. Existing tests in `internal/ui/` updated; add integration test that TUI behavior matches a recorded golden trace.
5. `--standalone` mode (in-process service) is the only mode for now — no socket yet.

Acceptance:
- TUI is feature-identical (manual smoke test against the existing daily flow).
- All `internal/ui/` and `internal/session/` tests still green.
- Golden status-detection tests pass unchanged.

Estimated diff: +800/-1,200 lines.

---

### PR 4 — `fleet daemon` subcommand serving over Unix socket

**Goal**: introduce the daemon process. TUI is not yet a client.

Files:
- `cmd/fleet/daemon.go` — `fleet daemon` subcommand. Boots SessionService, listens on `~/.config/fleet/daemon.sock`, registers gRPC server.
- `internal/daemonsrv/server.go` — implements `proto/fleet/v1` `Fleet` service interface. Translates between proto messages and SessionService method calls.
- `internal/daemonsrv/stream.go` — server-streaming logic (`ListSessions`, `ListRepos`, `StreamHookEvents`).
- `cmd/fleet/daemon_admin.go` — `fleet daemon status` and `fleet daemon stop` (talks to socket; useful for debugging).

Steps:
1. Server-streaming logic publishes snapshot then deltas as SessionService observers fire.
2. Socket file mode 0600 (user-only). Refuse to start if socket exists and is alive (use SO_REUSEADDR-equivalent or check + remove stale).
3. `fleet daemon` is foreground by default; flag `--detach` to background-spawn (later: launchd plist).
4. **TUI is unchanged** — still in-process. Daemon is a separate runnable now but nobody connects.

Acceptance:
- `fleet daemon` starts cleanly.
- Manual: `grpcurl -unix ~/.config/fleet/daemon.sock list` lists `fleet.v1.Fleet`.
- Manual: `grpcurl -unix … fleet.v1.Fleet/ListSessions` returns the snapshot of current sessions.
- TUI still works in `--standalone` mode (which is the default).

Estimated diff: +700 lines.

---

### PR 5 — TUI becomes a daemon client (with auto-spawn)

**Goal**: switch the TUI's default mode from in-process to daemon-client. Keep `--standalone` as the escape hatch.

Files:
- `internal/daemonclient/client.go` — thin wrapper around `proto/fleet/v1` generated gRPC client. Handles connect, retry, reconnect.
- `internal/daemonclient/autospawn.go` — if no daemon listening, auto-spawn `fleet daemon --detach` and wait for socket to become ready.
- `internal/ui/` — switch from `*service.SessionService` dependency to a `daemonclient.Client` interface that mimics SessionService's surface.

Steps:
1. The `Service` interface added in PR 2 has two implementations now: in-process (PR 2/3) and daemon-client (this PR).
2. `fleet --tui` connects to daemon by default.
3. `fleet --tui --standalone` keeps the in-process path (debugging, single-binary use).
4. Streaming RPCs power the live sidebar — UI subscribes to `ListSessions` stream, reacts to deltas.
5. SendKeys, CapturePane, restart, rename, etc. all route through gRPC.

Acceptance:
- Cold start: `fleet --tui` boots, daemon auto-spawns, TUI shows sessions identically to before.
- Warm start: daemon already running → TUI connects without spawning.
- Killing the daemon mid-session: TUI shows "disconnected" banner, auto-reconnects when back. (Stretch goal — fall back to error overlay if reconnect logic is too much for V1.)

Estimated diff: +600/-200 lines.

---

## Optional follow-up (Stage 0.5)

After PR 5 lands, before Mac app work starts:

- **launchd LaunchAgent** for daemon auto-start at login (opt-in setting).
- **`fleet daemon logs`** subcommand that tails the daemon's slog output.
- **Telemetry of daemon health** in the bug report dialog.

These are nice-to-haves; not blockers for V1.

## Branching strategy

- Each PR cuts a fresh branch off `master` (`stage0/proto-toolchain`, `stage0/service-extraction`, etc.).
- Rebase against master daily if the PR is open >24h.
- Merge with squash. No long-lived "stage0-everything" branch.
- If a PR sits >5 days unmerged, split it.

## Rollout / fallback

PR 5's `--standalone` flag is the escape hatch: if the daemon path proves unstable, users can stay on the in-process path with `fleet --tui --standalone`. We delete `--standalone` only after the Mac app ships and has used the daemon path for 30 days without daemon-specific bugs.

## What this Stage 0 does NOT do

- Doesn't write any Swift.
- Doesn't change TUI behavior (each PR is behavior-preserving for TUI users).
- Doesn't add notifications, multi-pane, diff viewer — all V1+ Mac app concerns.
- Doesn't migrate the chrome-extension native host into the daemon (separate decision).

## Estimated total

5 PRs, ~3,500 lines of net new code (mostly generated proto), ~3 weeks calendar time for one developer working part-time.

When all 5 PRs are merged: master is daemon-shaped, TUI works against either in-process or daemon path, Mac app work can start cleanly off master.
