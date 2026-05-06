# SwiftTerm + tmux stack — stress-test research

Date: 2026-05-07. Time-boxed ~30 min.

Stack under review: SwiftUI app embeds **SwiftTerm** widget; widget runs `tmux attach-session -t <name>` against a tmux server managed by a Go daemon.

---

## 1. SwiftTerm production readiness

**Activity (snapshot Mar 2026):**
- v1.13.0 released 2026-03-27, ~1,014 commits on main, 1.5k stars, 310 forks.
- 57 open issues, 19 open PRs. Moderate, not heavy, ongoing development.
- Sole effective maintainer: Miguel de Icaza.

**Production track record:**
- README cites Secure Shellfish, La Terminal, CodeEdit as commercial users.
- Anders Borum contributed reliability fixes + sixel parser to harden it for production.

**Confirmed feature support (per README + source):**
- 24-bit / TrueColor, 256-color, ANSI.
- Bold, italic, underline, strikethrough, dim/faint (SGR 2).
- OSC 8 hyperlinks.
- Mouse events.
- Sixel + iTerm2-style + Kitty graphics protocols.
- Bracketed paste mode (wraps with `ESC [ 200~ … 201~`, found in source).
- Unicode/UTF-8, grapheme clusters, emoji.
- Selection engine + find bar.
- Termcast (asciinema `.cast`) playback.
- Optional Metal renderer (per #479; CoreText/CoreGraphics is the default).
- Synchronized output (CSI 2026) with 1 s timeout.
- Alternate screen buffer — yes, required by the VT100/xterm baseline; full-screen TUIs (vim/htop/tmux) work.

**Likely missing / undocumented:**
- **OSC 52 clipboard** — not in feature list, no source mention. Likely missing.
- **Kitty keyboard protocol** — no mention. #456 is an open feature request for `XTMODKEYS`/`XTQMODKEYS`, suggesting modern keyboard protocols are not yet implemented.
- **IME** — README does not mention IME; source has *some* Korean Hangul composition handling, but no public documentation. Issue tracker shows no SwiftTerm-specific IME bug, but other Mac-native terminals (kitty, wezterm, zed) all have known IME bugs — this is a hard-mode area.
- Semantic prompt support is requested (#458) but not implemented.

**Verdict:** Production-grade for SSH-client use cases. For a Claude-Code-on-tmux workflow it is functional but with several rough edges (see §2).

---

## 2. Known SwiftTerm bugs to plan for

Open issues that matter for fleet:

| # | Title | Impact for fleet |
|---|---|---|
| **#494** | Buffer reflow produces duplicate/orphan lines on narrowing | Hits any user resizing the window. Permanent corruption — even resizing back doesn't fix. Affects vim/htop/tmux any time width shrinks. **Active, no workaround.** |
| **#486** | Cannot scroll when fed via `DispatchQueue.main.async` | Exact pattern most apps use to feed PTY data. Causes scroll fight + circular-buffer wrap glitches. Workaround: drain via `Timer.scheduledTimer`. **Will bite us.** |
| **#479** | Feature request: Metal renderer | Default CoreGraphics path rebuilds NSAttributedString per line + draws via CoreText every frame — **CPU-bound for high-throughput TUIs**. Claude Code is known to stream 4 000–6 700 scroll events/sec inside tmux (anthropics/claude-code#9935). Combine with #486 and you have a real perf concern. Optional Metal renderer exists (per release notes) but is opt-in. |
| **#450** | Fails to resolve on Swift 6.2.3 / macOS 15.7.3 | Transitive dep on unstable `swift-subprocess`. Build-system gotcha; pin SwiftTerm version carefully. |
| **#437** | Cost of `-enforce-exclusivity` | Performance, low priority. |
| **#423** | Black-on-black text after `nativeForegroundColor=white` | Color/theming bug, niche. |
| **#483** | External keyboard shortcuts not activated | Keyboard event plumbing — relevant if we want app-level shortcuts to work over the embedded view. |
| **#456** | XTMODKEYS / XTQMODKEYS | Modern keyboard protocols not yet supported. |
| **#458** | Semantic Prompt | Not implemented; nice-to-have for shell integration. |
| **#271** | Backspace doesn't auto-repeat after a space | Long-standing input-handling edge case. |
| **#43** | EscapeParser issue with utf8 | Old; verify it's been resolved. |

**Pattern:** rendering & resize issues are the chronic theme. Input / IME / clipboard receive less attention.

---

## 3. Alternatives to SwiftTerm

| Option | Status (2026) | Verdict |
|---|---|---|
| **SwiftTerm** | Active, production-used | The default choice. Has rough edges but is shippable today. |
| **libghostty / libghostty-vt** | `libghostty-vt` (the VT parser+state) is **available now in Zig + C**, usable from Swift via the C ABI. `libghostty-spm` ships a prebuilt `GhosttyKit.xcframework` Swift Package. **But** the full "view widget" (rendering, input, PTY) is *not* yet shipped — Mitchell Hashimoto's blog calls Swift frameworks "future". Production examples exist (Kytos, Fantastty, macterm) but those teams wrote the AppKit glue themselves. | **Strong long-term candidate** but using it today means we own the renderer + PTY plumbing. ~3–6 months out from drop-in parity with SwiftTerm. Worth tracking. |
| Apple `Terminal.app` | Closed source, no embedding API | No. |
| `iTermLib` | iTerm2's emulator core has never been shipped as a reusable library. | No. |
| `swift-libxterm` | Doesn't exist as a real package. | No. |
| Hyper, Wave, Warp, Tabby | Electron / cross-platform stacks; not embeddable inside a SwiftUI app. | No. |
| Roll-your-own xterm parser | 2–3 engineer-years. | No. |

**Honest verdict:** for V1, **SwiftTerm is the only realistic option**. Watch libghostty as the V2 escape hatch — it's specifically designed for embedding and the API has been production-proven inside Ghostty itself (which is famously fast — ~100× Terminal.app/iTerm on benchmarks).

---

## 4. `LocalProcessTerminalView` API

Shipped in SwiftTerm for macOS as an `NSView` that owns a child PTY.

**Surface:**
- `startProcess(executable:args:environment:execName:currentDirectory:)` — exposes cwd parameter (added recently per release notes).
- `terminate()` — kills child.
- `send(...)` / `dataReceived(...)` — bi-directional data.

**`LocalProcessTerminalViewDelegate` callbacks:**
- `sizeChanged(source:newCols:newRows:)` — notification only; the view itself already calls `getWindowSize()` + `PseudoTerminalHelpers.setWinSize()` (which is `ioctl(TIOCSWINSZ)`) when the NSView is resized. So **PTY resize-on-window-resize is automatic.**
- `processTerminated(exitCode:)` — process death detection. ✅
- `setTerminalTitle(...)` — for OSC 0/1/2.
- `hostCurrentDirectoryUpdate(...)` — OSC 7.

**Signal forwarding:** Ctrl+C / Ctrl+Z / etc. are forwarded as their byte values through `send()` like any keystroke — line discipline does the rest. **Ctrl+Q is *not* special-cased** (we'd want to intercept it at the NSView level to match TUI's detach behavior, before SwiftTerm passes it to the PTY).

**IME on Apple Silicon:** No documented support. Code has Korean Hangul handling but no test coverage advertised. Plan for IME bugs — every other native Mac terminal has had them.

**Sandbox caveat:** README warns that the macOS sandbox severely limits practical use; we'd ship un-sandboxed (acceptable for our distribution model).

**Don't override the view's own delegate** — the docs explicitly warn this breaks internal event routing. Use `processDelegate` instead.

---

## 5. tmux multi-client and Unix-socket quirks

- `-S /path/to/socket` is fully supported and is the right way to isolate the fleet daemon's tmux server from user-side tmux.
- **Multiple clients on same session view the *same* window** — unlike GNU screen. So Mac app + TUI + remote ssh attaching simultaneously all see identical content. ✅
- **Default resize behavior:** server resizes the window to the *smallest* attached client. If the user has TUI on a small laptop terminal + Mac app at 1080p, the bigger one sees padding. Mitigated via the `aggressive-resize` window option (only constrains size when both clients are *currently viewing* the window) — but `aggressive-resize` is documented as **incompatible with iTerm2's tmux integration** (`tmux -CC`), and for fleet's plain-attach model it should be safe.
- **Status bar contention:** any client can change `set-option status-...`; all clients see the change. Per-session status config is fine; per-client status differs (each client has its own status line height).
- **window-size option** (tmux 3.x) provides finer control: `largest`, `smallest`, `manual`, `latest`. `latest` (size to most recently used client) is often what you want for app-driven workflows.

**Recommendation:** set `window-size latest` and `aggressive-resize on` server-wide in the daemon's tmux conf. Document the multi-client sizing semantics. Don't try to support `tmux -CC`.

---

## 6. tmux `-CC` control mode — does fleet need it?

What `-CC` is:
- Text protocol (designed by George Nachman for iTerm2). Server emits `%output`, `%window-add`, `%window-close`, `%session-changed`, `%pane-mode-changed`, etc., wrapped in `%begin/%end/%error` guard blocks.
- Client renders panes natively (one app tab per tmux window) — terminal scrollback / find / mouse all become app-native instead of tmux's.
- Session persistence: if the app dies, server stays up; reattach reopens windows.
- Constraints: requires a controlling tty (stdin pipe doesn't work — see tmux#3085). A tab can't mix tmux + non-tmux panes. Plugins frequently break. Single-client per session works best.

**Should fleet use `-CC`?** **No, not for V1.**
- We already have a higher-level model (Go daemon owns sessions, Mac app is a thin client). `-CC` would duplicate that work and force us to render panes ourselves.
- `-CC` is best when you want native scrollback/find inside an app that is otherwise terminal-emulator-shaped (i.e., iTerm2). Fleet's UX is sidebar-navigation-of-sessions, not "iTerm2 with tmux superpowers". Plain attach maps cleanly to "one SwiftTerm view per active session".
- Reference issue: `alternate screen buffer and mouse tracking do not work correctly in tmux -CC sessions` — a hard blocker for Claude Code.
- Plain `attach-session` is well-trodden, simple, and matches how the fleet TUI already works.

If we ever want native scrollback search across all sessions, revisit.

---

## 7. PTY pitfalls on macOS

Things to pre-solve in the daemon (Go) and the Swift host:

1. **`forkpty` is *not* safe to call from Swift** (Apple Forums consensus). Use `posix_openpt` + `grantpt` + `unlockpt` + `ptsname` + manual `fork`/`execve`, or — better — let SwiftTerm's `LocalProcessTerminalView` do it (it already wraps this correctly). For the daemon side, Go's `os/exec` + `creack/pty` is fine.
2. **Window size sync via `ioctl(TIOCSWINSZ)`** — SwiftTerm handles this on view resize; daemon must propagate the size to the tmux client, which propagates it via `refresh-client -C cols x rows`. Verify resize round-trips correctly.
3. **Zombie reaping** — Swift `Process` reaps via `terminationHandler`. Daemon must `Wait()` on every spawned PTY child (Go's `cmd.Wait` does it). Don't ignore SIGCHLD.
4. **PTY exhaustion** — `kern.tty.ptmx_max` is finite; "forkpty resource temporarily unavailable" is real if we leak. Audit that every closed session releases its PTY.
5. **UTF-8 / encoding** — SwiftTerm handles UTF-8 well, but tmux's control-mode escape rule (replace bytes < 0x20 + backslash with octal) is *not* relevant for plain attach.
6. **XON/XOFF flow control** — disable in raw mode (`stty -ixon`); otherwise Ctrl+S freezes the pane. tmux disables it by default.
7. **Locale propagation** — set `LC_ALL=en_US.UTF-8` (or user's) in the spawned shell environment.
8. **`TERM` envvar** — set to `xterm-256color` (or `tmux-256color` if shipping a tmux terminfo). Some apps refuse italics if `TERM` is `xterm`.

---

## 8. Reset / corrupted-state on disconnect

**What can corrupt:**
- Killing a tmux client mid-frame leaves the *client* state mid-render, but the *server*'s pane buffer is intact. tmux's pane history is the source of truth.
- On reattach, tmux issues a full redraw of the visible region from its internal grid → no garbage, no orphan partial frames.
- Edge cases:
  - If the alternate screen buffer is in use (vim, htop, Claude Code) and the *application* dies mid-frame, tmux's grid still reflects the last fully-applied state. No garbage, but possibly a stale half-frame.
  - If SwiftTerm's renderer disconnects from the PTY mid-OSC (e.g., partial OSC 8 hyperlink), SwiftTerm's parser holds the partial state — reconnecting to a fresh tmux attach starts a new session with its own clean state.
- **Issue #494 (reflow on narrow)** — if user resizes during a tmux disconnect, *SwiftTerm* may corrupt its scrollback even after reattach.

**Recovery levers:**
- `refresh-client -R` from the daemon forces tmux to redraw.
- `printf '\033c'` (full reset) inside the pane works for shell sessions, ruins TUI state.
- Easiest: kill the SwiftTerm view, spawn a new attach, problem gone.

**Recommendation:** wire a "Force redraw" command (Cmd-R or menu) that re-attaches the SwiftTerm widget to the same tmux session. Cheap, robust.

---

## Sources

- SwiftTerm repo & issues:
  - https://github.com/migueldeicaza/SwiftTerm
  - https://github.com/migueldeicaza/SwiftTerm/issues
  - https://github.com/migueldeicaza/SwiftTerm/issues/494 (reflow corruption)
  - https://github.com/migueldeicaza/SwiftTerm/issues/486 (DispatchQueue scroll)
  - https://github.com/migueldeicaza/SwiftTerm/issues/479 (Metal renderer)
  - https://github.com/migueldeicaza/SwiftTerm/issues/450 (Swift 6.2.3 build)
  - https://github.com/migueldeicaza/SwiftTerm/issues/442 (Mac feature list)
  - https://migueldeicaza.github.io/SwiftTerm/Classes/LocalProcessTerminalView.html
- iTerm2 / tmux control mode:
  - https://iterm2.com/documentation-tmux-integration.html
  - https://github.com/tmux/tmux/wiki/Control-Mode
  - https://github.com/tmux/tmux/issues/3085
- Ghostty / libghostty:
  - https://mitchellh.com/writing/libghostty-is-coming
  - https://github.com/ghostty-org/ghostty
  - https://github.com/Uzaaft/awesome-libghostty
- PTY on macOS:
  - https://developer.apple.com/forums/thread/688534 (forkpty + Swift)
  - https://github.com/microsoft/node-pty/issues/590
- Claude Code in tmux quirks:
  - https://github.com/anthropics/claude-code/issues/9935 (4 000–6 700 scroll/sec)
  - https://github.com/anthropics/claude-code/issues/29937 (rendering corruption in tmux)
- tmux multi-client:
  - https://mutelight.org/practical-tmux
  - https://github.com/tmux-plugins/tmux-sensible/issues/24

---

## Top-line conclusions

**Top 3 risks:**
1. **SwiftTerm renderer is CoreGraphics-by-default and CPU-bound** under fast streaming output (Claude Code in tmux can hit 4–7 k scroll events/sec). Issue #486 (`DispatchQueue.main.async` scroll fight) is the exact pattern we'd use to feed bytes. Without care this will jitter and feel slow.
2. **Resize-narrowing reflow corruption (#494)** is open with no workaround. Real users hitting it will see permanent buffer artifacts.
3. **IME and modern keyboard protocols are weak.** No advertised IME support; no XTMODKEYS / Kitty keyboard. International users + power-user keybinds will surface bugs we'll have to file upstream and live with.

**Top 3 mitigations:**
1. **Enable SwiftTerm's optional Metal renderer** from day one. Buffer incoming PTY data in the daemon and drain on a `Timer.scheduledTimer`-style cadence (~60 Hz) instead of pushing every byte through `DispatchQueue.main.async`. This sidesteps both #486 and the CoreGraphics CPU cost.
2. **Wire a "force re-attach" action** (and bind it to a hotkey) that destroys + recreates the SwiftTerm widget pointing at the same tmux session — cheap robust recovery for #494, OSC parser hangs, frozen renders, and disconnect corruption.
3. **Pin SwiftTerm version + carry a small fork**. We *will* hit issues that need patches faster than upstream merges. Maintain a fork branch, plus a feature flag we can flip to swap in **libghostty** when its Swift framework ships (6–12 mo). Set `window-size latest` + `aggressive-resize on` in the daemon's tmux conf to dodge multi-client resize ugliness.

**Alternative stack worth tracking:** **libghostty-spm + Ghostty's Metal renderer**, once the Swift framework wrapper ships (Mitchell expects ~6 mo from his post). Already used in production (Kytos, Fantastty, macterm) — but those teams wrote the AppKit glue. For fleet V2 it could replace SwiftTerm entirely with a meaningful perf and feature jump.

**Verdict:** **stack is sound for V1**, with two preconditions — (a) opt into SwiftTerm's Metal renderer + buffer PTY pumping, and (b) plan a force-reattach escape hatch. Watch libghostty as the V2 swap-in.
