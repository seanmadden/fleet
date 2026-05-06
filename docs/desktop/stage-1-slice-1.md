# Stage 1 — V1 Mac app, Slice 1: skeleton → SwiftTerm demo

**Landed on `fleet-ui` 2026-05-07.** No PR opened (bundled-PR holding decision).

## Goal

Get the Mac app to a demo-able state: launches, autospawns the daemon if needed, streams session/repo state from the daemon, shows a sidebar driven by that data, embeds a SwiftTerm pane that hot-swaps onto the selected session's tmux pane on click. **No mutations** — that's slice 2.

## Locked decisions (from the planning round)

| Decision | Choice |
|---|---|
| Slice scope | Skeleton + DaemonClient + sidebar + SwiftTerm pane |
| Build target | SwiftPM-only, `swift run Fleet` from `app/` (no Xcode project, no `.app` bundle yet) |
| Daemon spawn | Resolve `fleet` on `$PATH`; spawn `fleet daemon --detach` if socket dead |
| Themes | Tokyo-Night-ish placeholder colors only — pro UX designer will redo the visual system later |
| grpc-swift | v2 (async/await), products `GRPCCore` + `GRPCNIOTransportHTTP2` + `GRPCProtobuf` |
| Codegen | Pre-generated, committed under `app/Sources/Fleet/DaemonClient/Generated/` (mirrors Go's committed `gen/`) |
| Mocks | `MockData.swift` deleted; UI domain types in `Models/` extended to match proto fields |

## What landed

### Toolchain + codegen
- `app/Package.swift`: bumped to `macOS(.v15)`, added `grpc-swift-2`, `grpc-swift-nio-transport`, `grpc-swift-protobuf`.
- `Makefile`: new `proto-swift` and `proto-swift-check` targets driving `protoc-gen-swift` + `protoc-gen-grpc-swift-2` (installed via `brew install protoc-gen-grpc-swift`, which pulls the lot).
- Generated Swift bindings live at `app/Sources/Fleet/DaemonClient/Generated/fleet/v1/{fleet.pb.swift, fleet.grpc.swift}`. Symbol prefix is `Fleet` (set in proto via `option swift_prefix`), so types are `FleetSession`, `FleetSessionUpdate`, `FleetFleet.Client`, etc.

### App entry
- `app/Sources/Fleet/FleetApp.swift`: `@main` SwiftUI App + `NSApplicationDelegateAdaptor`. The delegate forces `NSApp.setActivationPolicy(.regular)` and `activate(ignoringOtherApps:)` because a SwiftPM executable without an `Info.plist` would otherwise run with `.prohibited` policy — the window opens but stays hidden behind every other app, which manifested in the first smoke run as "nothing opens." This can come out once we ship a real `.app` bundle.

### Daemon client
- `app/Sources/Fleet/DaemonClient/EnsureRunning.swift`: probes the socket via raw `connect()` (200ms timeout), `which fleet` lookup, `Process` spawn of `fleet daemon --detach`, then `waitForSocket` polling up to 3s. Mirrors `internal/daemonclient/autospawn.go`.
- `app/Sources/Fleet/DaemonClient/DaemonClient.swift`: one entry point — `DaemonClientRunner.run(model:)` — which uses `withGRPCClient(transport: .http2NIOPosix(target: .unixDomainSocket(path: …)))` to keep a long-lived client open for the app's lifetime. Spawns three concurrent consumers in a `withThrowingTaskGroup`.
- `app/Sources/Fleet/DaemonClient/StreamConsumers.swift`: per-stream state machine for `ListSessions` and `ListRepos` mirroring `internal/daemonclient/stream.go:65-122` exactly — SNAPSHOT-kind populates `seenIDs`; first ADDED/CHANGED/REMOVED ends the snapshot phase and evicts cache entries the snapshot didn't carry (ghost-pruning on reconnect). `runSlotBindingsRefresher` polls `ListSlotBindings` every 5s — the daemon doesn't expose a streaming RPC for slots and they change rarely.
- `app/Sources/Fleet/DaemonClient/Reconnect.swift`: backoff schedule `[250ms, 1s, 4s, 5s]`, mirrors Go.
- `app/Sources/Fleet/DaemonClient/Convert.swift`: `Fleet*` proto → SwiftUI domain (`Session`, `Repo`, `PRInfo`). Includes the PR-state derivation logic copied from `internal/ui/sidebar.go:366-392`.

### State + views
- `app/Sources/Fleet/ViewModel/AppModel.swift`: `@Observable @MainActor final class`. Holds `sessionsByID`, `reposByRoot`, `slotBindings`, `selectedSessionID`, `filterText`, `connectionState`, `lastError`. Exposes `applySession`/`removeSession`/`finalizeSessionsSnapshot` (and Repo equivalents) to the stream consumers; views read via `displayedRepos` / `sessionsForRepo(root:)`.
- `app/Sources/Fleet/Views/ContentView.swift`: `NavigationSplitView` with sidebar + detail pane. Detail pane gates on `session.isAlive && session.tmuxName != nil` — dead sessions show a placeholder so we don't fire `tmux attach` against a name that no longer resolves.
- `app/Sources/Fleet/Views/Sidebar/{SidebarView, SessionRow, RepoGroupHeader}.swift`: stock SwiftUI `List` with always-visible filter, repo grouping via `Section`, status-icon row with optional slot badge.
- `app/Sources/Fleet/Views/Pane/{TerminalPaneView, EmptyStateView}.swift`: `NSViewRepresentable` wrapping `SwiftTerm.LocalProcessTerminalView`. Hot-swap on session change: `terminate()` (SIGHUP — clean tmux detach) then `startProcess` with the new session name.

## Smoke-test gotchas worth remembering

Three real fixes caught only by manually running against a real daemon, not by the build succeeding. Worth keeping top-of-mind for any future Swift-client work:

1. **SwiftPM executables default to `.prohibited` activation policy.** First smoke run: "nothing opens." The window does open — it's just hidden behind every other app and never gets focus. Fix: `NSApp.setActivationPolicy(.regular) + NSApp.activate(...)` from an `NSApplicationDelegateAdaptor` (`FleetApp.swift`). Once we ship a real `.app` bundle with `Info.plist`, this becomes redundant.

2. **The daemon attaches sessions to the host's default tmux server, not to `~/.config/fleet/tmux.sock`.** The architecture doc said the latter, but the implementation reality (per memory note `desktop_app_initiative.md`) is that the daemon shares the host's default tmux. Adding `-S ~/.config/fleet/tmux.sock` to `tmux attach` makes every session look "missing" (`no sessions` in the pane). Fix: drop the `-S` flag (`TerminalPaneView.swift`). The architecture doc has been amended with a 2026-05-07 note.

3. **`startProcess` before SwiftUI lays out the view spawns tmux at default 80×24.** Visible as a 1–2s flash of stale or tiny pane content while tmux processes the first SIGWINCH after layout. Fix: `DispatchQueue.main.async` the `startProcess` call so layout completes before tmux sees its first pane geometry. Also re-check the desired tmux name inside the deferred block to handle a quick selection-flicker.

## Verification (all green on user's machine)

1. `swift build` clean (no warnings outside `Generated/`).
2. `make proto-swift && make proto-swift-check` — generation reproducible.
3. Cold-start: `pkill -9 -f 'fleet daemon' && rm -f ~/.config/fleet/daemon.sock`, then launch app → window opens, daemon log appears at `~/.config/fleet/daemon.log`, sidebar populates within ~1s.
4. Selection → SwiftTerm renders the live Claude TUI on a `○` (idle/alive) session.
5. Hot-swap → previous client cleanly detaches, new pane renders without flash.
6. Dead session selection → placeholder shown, no broken `tmux attach` invocation.
7. No regressions in TUI: `make build && fleet --tui` still works against the same daemon.

## Cosmetic + UX follow-ups (commits `474f8a4` and `b07437f`, 2026-05-07)

After the main slice landed, two small follow-up passes made the pane look and feel like a real terminal instead of a stock SwiftTerm widget. Both stay placeholder-grade — the UX designer still owns the real visual system.

### Cosmetic polish (`474f8a4`)
- **Bundled JetBrains Mono** registered via `CTFontManagerRegisterFontURLs` at `applicationDidFinishLaunching`. PostScript name is `JetBrainsMono-Regular` (no space), so a naive `NSFont(name: "JetBrains Mono-Regular")` lookup silently falls back to Menlo — first cut shipped that bug.
- **10pt internal padding** by wrapping `LocalProcessTerminalView` in an NSView container with edge constraints — SwiftTerm has no built-in padding API.
- **Tokyo Night palette** via `installColors([Color])` plus matching `nativeBackgroundColor` / `nativeForegroundColor` / `caretColor` / `selectedTextBackgroundColor`. SwiftTerm.Color is a class (non-Sendable) so the array gets minted per call instead of `static let`. Color hex → `Color(red:green:blue:)` requires mirroring 8-bit values into 16-bit components (`UInt16(byte) * 0x101`).

### Window chrome + terminal UX (`b07437f`)
- **Force-dark window appearance** (`NSApp.appearance = NSAppearance(named: .darkAqua)`), transparent title bar, hidden title text, `.fullSizeContentView`, window background set to Tokyo Night so the title-bar slab and resize gutters all read as one surface.
- **Mouse reporting off** (`allowMouseReporting = false`) so click+drag does native Mac selection (Cmd+C copies). Claude TUI is keyboard-driven so we lose nothing tmux-side.
- **Local NSEvent monitor** (`NSEvent.addLocalMonitorForEvents(matching: [.scrollWheel, .keyDown])`) installed by the Coordinator. Two jobs:
  - **Wheel forwarding**: SwiftTerm's `scrollWheel` always scrolls its own (mostly empty) scrollback — never reports upstream. With a full-screen tmux app like Claude that paints stale scrollback over the live alt-screen ("scrolling looks messed up"). We emit SGR mode-1006 wheel sequences (`ESC [ < button ; 1 ; 1 M`, button 64=up / 65=down) so tmux receives them and does the right thing.
  - **Cmd-key remapping**: SwiftTerm doesn't translate Mac-standard line-edit shortcuts. We rewrite Cmd+Left → `^A`, Cmd+Right → `^E`, Cmd+Backspace → `^U`, Cmd+Delete → `^K`. Gated on `term.window?.firstResponder === term` so we don't eat keystrokes meant for the sidebar's filter field.

### Smoke-test gotchas (Swift 6 strict concurrency)
- **`SwiftTerm.LocalProcessTerminalView.scrollWheel` is `public override`, not `open`.** Subclassing to override it fails with "overriding non-open instance method outside of its defining module." That's why we use a local NSEvent monitor instead of subclassing.
- **`NSEvent.addLocalMonitorForEvents` handler isn't `@MainActor`** but is always delivered on the main thread. Doing AppKit work inside leaks "actor-isolated" warnings/errors. The pattern that works: mark `Coordinator` as `@MainActor`, do the work in a `@MainActor` helper, and use `MainActor.assumeIsolated` to call it from the closure — but only flow a Bool back so non-Sendable `NSEvent` doesn't have to cross the actor boundary.
- **Strict-concurrency `deinit` can't access non-Sendable state.** `scrollMonitor` is `Any?` (the opaque token from `addLocalMonitorForEvents`) and Swift 6 rejects touching it from a non-isolated `deinit`. Cleanup goes through SwiftUI's `dismantleNSView(_:coordinator:)` lifecycle hook instead, which is `@MainActor`-safe.

## Out of scope (queued for slice 2)

- All mutations (CreateSession, DeleteSession, SoftDelete/Restore, RestartSession, RenameSession, AcknowledgeSession, PinRepo / UnpinRepo, BindSlot / UnbindSlot, SendKeys for `Y` quick-approve).
- All dialogs (new-session, new-worktree, settings, help, bug-report, command-palette).
- Native menu bar with Cmd-shortcuts.
- Theme switcher and a real visual system (UX designer is upstream).
- Light/dark adaptation.
- App icon, Info.plist, code signing, `.app` bundle generation.
- LRU warm-cache for terminal panes (V2).
- macOS notifications via `StreamHookEvents` (V2).
- `--standalone` in the Mac app — by design, daemon-only.
