# Terminal-Native AI Products — Research for fleet (Mac desktop)

Date: 2026-05-07
Audience: fleet desktop team (SwiftUI + SwiftTerm + tmux + Go daemon)
Question: How do other "terminal IS the UI" products handle TUI agents like Claude Code, and does that validate or challenge our architecture?

---

## TL;DR

- Warp's block model is incompatible-by-default with Claude Code's TUI: Warp explicitly punts to an `AltScreen` component for vim-style apps, and users hit duplicate input fields, ghosting, hangs and Rust panics specifically when running Claude Code inside Warp blocks.
- Anthropic shipped a dedicated "fullscreen rendering" mode in Claude Code v2.1.89 that uses the alternate screen buffer like vim/htop — and **explicitly calls out that it is incompatible with iTerm2's `tmux -CC` integration**. This is critical for fleet's tmux-CC-style ambitions.
- Wave Terminal is Electron + xterm.js + Go backend + Tsunami widget framework, with a Claude Code "integration" that is mainly a wrapper around the regular CLI in an xterm.js block.
- Ghostty is the de facto best terminal for Claude Code per multiple 2026 reviews — and is now releasing **libghostty**, a C library that owns emulation + Metal rendering + PTY management that Swift apps (Kytos) embed via xcframework.
- iTerm2 is the only mainstream Mac terminal with deep `tmux -CC` control mode integration. fleet's plan to spawn local tmux clients is directly inspired by it, but Anthropic warns Claude Code's flicker-free fullscreen mode breaks under `tmux -CC`.

---

## 1. Warp — block model meets full-screen TUI

### Architecture

Rust + Metal (macOS) / Vulkan (Linux) / DirectX (Windows). Custom UI framework "inspired by Flutter." Renders only rectangles, images, and glyphs — "just 200 lines of shader code" per how-warp-works blog. >144 FPS.

The viewport is a `BlockList`: "an ordered list of *blocks*: typed, self-contained units of content that stack vertically and scroll together." Each command produces its own grid (using precmd/preexec hooks) so output cannot overwrite previous output — addressing a "fundamental limitation of the VT100 specification."

### How Warp deals with full-screen TUIs

From the official block-model post:

> "Fullscreen programs — vim, less, or anything else that takes over the whole terminal via the alt screen — sit outside this setup entirely. The alt screen has no scrollback, gets replaced wholesale when the program exits, and doesn't map onto 'a command and its output.' Rather than shoehorn it into the block list, [`AltScreen`] lives alongside the list with its own `GridStorage`."

> "Programs swap between the primary screen (our block list) and the alt screen via the standard control sequences for that mode, and Warp renders whichever one is active."

So the block model is essentially bypassed for vim/htop/Claude-Code-in-fullscreen. Two parallel rendering paths.

### Claude Code-specific support

Warp shipped a first-party plugin (`warpdotdev/claude-code-warp`):

- Six Claude Code hooks (SessionStart, Stop, Notification, PermissionRequest, UserPromptSubmit, PostToolUse) emit OSC 777 escape sequences.
- "Each hook script builds a structured JSON payload (via `build-payload.sh`) and sends it to `warp://cli-agent`" — Warp parses it for native notifications and session UI.
- Warp's marketing for Claude Code surfaces Vertical Tabs (group sessions w/ git-branch metadata), unified notifications, and a Code Review panel that can send inline comments to a running agent session.

This is essentially Warp's version of fleet's hook-based status detection — a sidechannel for structured state, with the actual rendering still driven by the TUI.

### Bugs users hit running Claude Code in Warp

From open GitHub issues:

- **#9206 "Warp Claude Code UI Issue":** "terminal input shows multiple times, text is duplicated, etc.... very buggy... prevents me from using Warp daily." User confirmed exclusive to Warp, doesn't repro in other terminals.
- **#8490:** Duplicate input prompt + status bar lines when running Claude Code v2.1.17 in WSL through Warp. Doesn't repro in VS Code or Windows Terminal — "specific to Warp's latest rendering engine/Agent Mode changes."
- **#8409:** Warp hangs for minutes, >100% CPU during Claude Code sessions. macOS system log: "Reporting 108.33s HID response delay" — main thread blocked nearly 2 minutes. Suggests synchronous work on the UI thread while parsing Claude Code's high-rate ANSI stream.
- **#8756:** Rust panic "Cursor should currently be on a block" when keypressing during Claude Code render.

This is the cost of doing your own emulator + custom block layout in front of a 60fps React-Ink TUI: edge cases multiply.

### Key takeaway for fleet

Warp's "blocks" thesis is great for ordinary commands, but they had to build a separate `AltScreen` component as soon as TUIs entered the picture. fleet wraps real PTYs in real terminal widgets — we don't have this dichotomy and we don't have to chase Claude Code's evolving render pipeline. **fleet's "real terminal widget" choice is validated.**

---

## 2. Claude Code's fullscreen mode — the iTerm2/tmux-CC collision

This is the most operationally important finding for fleet.

From `code.claude.com/docs/en/fullscreen` (Claude Code v2.1.89+, run `/tui fullscreen` or set `CLAUDE_CODE_NO_FLICKER=1`):

> "Fullscreen rendering is an alternative rendering path... It draws the interface on the terminal's alternate screen buffer, like `vim` or `htop`, and only renders messages that are currently visible."

> "The difference is most noticeable in terminal emulators where rendering throughput is the bottleneck, such as the VS Code integrated terminal, tmux, and iTerm2."

And the warning:

> "**Fullscreen rendering is incompatible with iTerm2's tmux integration mode, which is the mode you enter with `tmux -CC`**. In integration mode, iTerm2 renders each tmux pane as a native split rather than letting tmux draw to the terminal. The alternate screen buffer and mouse tracking do not work correctly there: the mouse wheel does nothing, and double-click can corrupt the terminal state. Don't enable fullscreen rendering in `tmux -CC` sessions. **Regular tmux inside iTerm2, without `-CC`, works fine.**"

### Why this matters for fleet

fleet's planned architecture is a tmux-CC-spiritual-successor: Go daemon owns long-running tmux sessions, the desktop client renders each pane in a SwiftTerm widget. Two paths to consider:

1. **Real tmux-CC control mode**: parse `%output`, `%window-add`, `%session-changed` notifications; render each pane in our own widget. → We hit the same fullscreen-mode incompatibility iTerm2 has.
2. **PTY-attach per session (current TUI design)**: spawn a tmux *client* per session that just attaches to the persisted server, all bytes flow through a normal PTY. → Claude Code's fullscreen mode "Just Works" because we are a normal terminal from its perspective.

The TUI already does (2) and the daemon plan continues that. Validate: stay on PTY-attach, do not pursue full `tmux -CC` parsing for V1.

---

## 3. iTerm2 + `tmux -CC` — fleet's spiritual ancestor

From iTerm2's tmux-integration docs and the tmux Control Mode wiki:

- `tmux -CC` puts a tmux client into control mode (designed by George Nachman of iTerm2 specifically to make this integration possible).
- Protocol is text: commands wrap in `%begin .../%end ...` guards, asynchronous notifications begin with `%`: `%output <pane-id> <bytes>`, `%window-add`, `%window-close`, `%window-pane-changed`, `%session-changed`, `%pause`, `%continue`, etc.
- iTerm2 renders each tmux pane as a native iTerm2 split. tmux server keeps running on detach/SSH-loss; iTerm2 is a *view* over the persisted state.
- Benefits per docs: native search, native trackpad scroll, no `^B` prefix, native splits/tabs UI.

**Architectural lesson:** iTerm2's split-rendering + native UI requires its own VT emulator per pane, which is exactly why `tmux -CC` breaks Claude Code's fullscreen mode (alt-screen sequences and mouse tracking don't round-trip cleanly through the protocol). fleet should **not** assume "iTerm2 does it so we can too" — the TUIs that exist now didn't exist when Nachman designed -CC.

---

## 4. Wave Terminal — open-source xterm.js comparison point

`github.com/wavetermdev/waveterm` — Electron app, TypeScript frontend (43%) + Go backend (49%).

- Uses **xterm.js v6** for terminal rendering (`TermWrap` is the core class managing the xterm.js instance).
- Frontend↔backend via **WSH RPC** (Wave Shell RPC).
- New **Tsunami widget framework** for custom widgets alongside terminal blocks.
- AI integration is sidecar: "Bring your own API keys for OpenAI, Claude, or Gemini" — a chat panel, not in-terminal agent UI.
- Has a documented "Claude Code Integration" page but it's effectively running the CLI in an xterm.js terminal block, with AI being a separate widget.

### Takeaway

Wave's stack (Electron + xterm.js + Go backend + RPC) is the path-of-least-resistance for cross-platform terminal+AI hybrids. fleet has gone Mac-native (SwiftUI + SwiftTerm) which gets us native scroll, native menu bar, no Electron memory tax, and tighter integration with macOS — at the cost of cross-platform. Given fleet is Mac-only, this is the right choice.

---

## 5. Ghostty — best-in-class for Claude Code, plus libghostty

Ghostty is Mitchell Hashimoto's terminal — Zig + GPU-rendered + native splits/tabs/Quake-mode. Per termdock.com 2026 review:

> "Ghostty is the best terminal emulator for most developers running AI CLI tools in 2026. Fast. Native. Stays out of your way."

> "Ghostty renders text faster than any competitor on macOS... When Claude Code streams a 200-line refactor into your terminal, that speed difference means zero visual stutter."

No built-in AI hooks. Mitchell's stance is "the terminal is the UI; the AI lives in the CLI."

### libghostty — possibly relevant for fleet V2

Mitchell announced **libghostty**, a C library that owns the emulation + Metal rendering + PTY management. Kytos (jwintz, March 2026) is a SwiftUI-native terminal built on libghostty:

> "libghostty is a C library that owns the emulation, Metal rendering, PTY management, and shell integration scripts. The host application provides an NSView surface and forwards input events."

> "The combination of libghostty for the terminal core and KelyphosKit for the shell gives Kytos a full IDE-quality interface with roughly 1,500 lines of Swift application code."

### Tradeoff vs SwiftTerm

| Aspect | SwiftTerm | libghostty |
|---|---|---|
| Language | Swift native | Zig static lib + C header (xcframework) |
| Rendering | CoreText (default) + optional Metal renderer | Metal-only, GPU-first |
| Production users | Secure Shellfish, La Terminal, CodeEdit | Kytos, future Ghostty Swift wrapper |
| Maturity | Years of fuzzing; "selection and accessibility lag xterm.js" | Pre-1.0 API, churning |
| Setup complexity | SwiftPM | xcframework + Carbon + Metal linker flags + terminfo bundle |
| Ownership | Pure Swift, easy to patch | C/Zig, harder to debug, but battle-tested in Ghostty itself |

### Takeaway

For V1 stick with SwiftTerm — proven, pure Swift, easy iteration. Watch libghostty for V2: if Ghostty ships a stable Swift wrapper, swapping in could be a 1500-line change for rendering parity with the fastest terminal on macOS.

---

## 6. iTerm2 AI plugin

iTerm2 added an optional AI plugin (separate component, isolated from terminal process for security):

- "Command Generator" — describe a task, get a command.
- "Codecierge" — task-oriented assistant that can run commands.
- Annotates command output inline.
- BYO API key (OpenAI, Azure, Ollama). Key in macOS keychain.

Not an agent runner — it's a sidekick for an interactive shell. Different category from Claude Code / fleet.

---

## 7. Hyper, Tabby — quick takes

**Hyper (vercel/hyper):** Electron + xterm.js, plugin system in Node + React + Redux. No first-party Claude Code support. Several community Electron-wrappers exist (ClaudeTerminal, Better Agent Terminal, Pilos Agents) that combine xterm.js + node-pty + Claude Code in tabs — same recipe as fleet desktop but cross-platform/Electron.

**Tabby (Eugeny/tabby):** Modern Electron terminal, xterm-webgl frontend. Open issue #10648: "Claude code seems to constantly scroll to the top of the history and there's no way to go back to the bottom without manual scroll bar usage." Doesn't repro in iTerm or Mac Terminal — i.e. xterm.js renderers handle Claude Code's render differently than native ones, especially in tmux scrollback edge cases.

(Note: there's also a separate "Tabby" — tabbyml.com — which is an OpenAI-style coding assistant, not a terminal. Easy to confuse.)

---

## Synthesis for fleet

**Does fleet's "real terminal widget" choice hold up?**

Yes — strongly validated.

1. **Warp's experience is the cautionary tale.** Building your own emulator + block layout in Rust delivers 144 FPS but invites a stream of Claude Code-specific bugs (UI duplication, hangs, panics). Warp's own block-model post admits they had to build a parallel `AltScreen` rendering path to support TUIs at all.
2. **iTerm2's `tmux -CC` is a fascinating ancestor but not a model to follow for V1.** Anthropic has explicitly carved Claude Code's flicker-free mode out of `tmux -CC` support — full-screen TUIs and `-CC`'s split-rendering are at odds. fleet's PTY-attach-per-session approach side-steps this entirely.
3. **SwiftTerm is the right V1 choice.** CoreText + optional Metal, production-deployed in Secure Shellfish/CodeEdit, pure Swift = fast iteration. libghostty is the V2 escape hatch if rendering speed becomes the bottleneck.
4. **Auxiliary integrations (notifications, session UI) belong in our hooks/daemon layer, not in the terminal renderer.** That's exactly where Warp put its Claude Code integration too — OSC 777 sidechannel, not block parsing.

The architectural lesson across all these products: **the terminal emulator should stay simple and correct; agent-aware UX lives in the chrome around the terminal, fed by structured side-channels (hooks, OSC sequences, daemon RPC).** fleet already does this — Go daemon + SwiftUI chrome + SwiftTerm widget + Claude Code hooks. Hold the line.

---

## Sources

- [How Warp Works](https://www.warp.dev/blog/how-warp-works)
- [The Block Model Behind Warp's Agentic Development Environment](https://www.warp.dev/blog/block-model-behind-warps-agentic-development-environment)
- [Warp — best terminal for Claude Code](https://www.warp.dev/agents/claude-code)
- [warpdotdev/claude-code-warp (official Warp + Claude Code plugin)](https://github.com/warpdotdev/claude-code-warp)
- [Warp issue #9206 — Claude Code UI duplication](https://github.com/warpdotdev/Warp/issues/9206)
- [Warp issue #8490 — duplicate prompt/status in WSL+Warp](https://github.com/warpdotdev/warp/issues/8490)
- [Warp issue #8409 — hangs and >100% CPU during Claude Code](https://github.com/warpdotdev/warp/issues/8409)
- [Warp issue #8756 — Rust panic in Claude Code](https://github.com/warpdotdev/Warp/issues/8756)
- [Claude Code — Fullscreen rendering docs](https://code.claude.com/docs/en/fullscreen)
- [iTerm2 tmux Integration docs](https://iterm2.com/documentation-tmux-integration.html)
- [tmux Control Mode wiki](https://github.com/tmux/tmux/wiki/Control-Mode)
- [Wave Terminal repo](https://github.com/wavetermdev/waveterm)
- [Wave Terminal docs](https://docs.waveterm.dev/)
- [Ghostty docs](https://ghostty.org/docs)
- [Libghostty Is Coming — Mitchell Hashimoto](https://mitchellh.com/writing/libghostty-is-coming)
- [Kytos: A Native macOS Terminal Built on Ghostty](https://jwintz.gitlabpages.inria.fr/jwintz/blog/2026-03-14-kytos-terminal-on-ghostty/)
- [Best terminal emulator for AI CLI 2026 (termdock)](https://www.termdock.com/en/blog/best-terminal-emulator-ai-cli-2026)
- [SwiftTerm](https://github.com/migueldeicaza/SwiftTerm)
- [iTerm2 AI Plugin](https://iterm2.com/ai-plugin.html)
- [iTerm2 AI Chat docs](https://iterm2.com/documentation-ai-chat.html)
- [Tabby issue #10648 — Claude Code scrolling](https://github.com/Eugeny/tabby/issues/10648)
- [Hyper terminal](https://hyper.is/)
- [HN — Claude Code TUI fullscreen discussion](https://news.ycombinator.com/item?id=45417148)
