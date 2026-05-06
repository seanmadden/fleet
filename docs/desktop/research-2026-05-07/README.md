# Research sprint — 2026-05-07

10 parallel agents researching how every macOS/desktop product wraps Claude Code, plus Claude Code's own machine-readable surfaces and a stress-test of fleet's SwiftTerm+tmux stack.

**Synthesis (read first):** [`../research-2026-05-07.md`](../research-2026-05-07.md)

## Per-product reports

| # | File | Target | Headline finding |
|---|------|--------|------------------|
| 01 | [01-conductor.md](./01-conductor.md) | **Conductor.build** ($22M Series A, closed source) | Tauri + Claude Agent SDK directly (NOT a CLI wrapper). Every interactive feature reimplemented natively. Hooks unsupported. Their diff/PR/inline-comment chrome is best-in-class. |
| 02 | [02-crystal.md](./02-crystal.md) | **Crystal** (stravu, deprecated → Nimbalyst) | Spawns `claude -p --output-format stream-json` per turn, parses JSON, rebuilds React UI. Plan mode never landed. Image paste is fake (`<attachments>path</attachments>`). MCP-permission-bridge pattern worth stealing. |
| 03 | [03-claudia.md](./03-claudia.md) | **Claudia / opcode** (getAsterisk) | Same pattern as Crystal — Tauri instead of Electron. Hard-codes `--dangerously-skip-permissions`. ~22 hand-rolled tool widgets. Pipe-EOF deadlock on Windows. |
| 04 | [04-claude-squad.md](./04-claude-squad.md) | **Claude Squad** (smtg-ai) | fleet's architectural twin (Go+Bubble Tea+tmux+creack/pty). 52 open issues. No-IPC design cannot reach desktop+TUI goal (issue #242 stale since Jan). Pause feature, trust-prompt auto-dismiss, terminal-tab-per-instance worth stealing. |
| 05 | [05-vibetunnel-and-emerging.md](./05-vibetunnel-and-emerging.md) | **VibeTunnel + 12 others** | Discovered third archetype: VibeTunnel runs **Ghostty compiled to WASM in Node server**, ships structured `BufferSnapshot` to clients. VibeTunnel binary-patches the Claude Code JS bundle to disable a `process.exit(1)` anti-debug check on non-TTY environments. |
| 06 | [06-cline-rebuild.md](./06-cline-rebuild.md) | **Cline** (formerly claude-dev) — the rebuild approach | ~30K LOC reimplementation of what Claude Code gives you for free: 3,764 LOC agent loop, 24 tool handlers, 1,673 LOC MCP hub, custom XML tool parser, shadow-git checkpoints. Hooks took ~6 months to ship parity (#4658). |
| 07 | [07-terminal-hybrids.md](./07-terminal-hybrids.md) | **Warp / Wave / Ghostty / iTerm2 / Hyper / Tabby** | Warp's block model can't handle full-screen TUIs cleanly (parallel `AltScreen` component, still has bugs #9206, #8490). **Claude Code is officially incompatible with `tmux -CC`**. libghostty is fleet's V2 escape hatch. |
| 08 | [08-claude-code-surfaces.md](./08-claude-code-surfaces.md) | **Claude Code itself** — every machine-readable surface | SDK renamed to `@anthropic-ai/claude-agent-sdk`. Stream-JSON event schema, JSONL transcript schema, 25+ hook events, `Query` control surface (`interrupt`, `setPermissionMode`, `rewindFiles`, `mcpServerStatus`, `canUseTool`), `--permission-prompt-tool` MCP integration. |
| 09 | [09-swiftterm-tmux-stack.md](./09-swiftterm-tmux-stack.md) | **SwiftTerm + PTY + tmux stack** stress-test | Top risks: CoreGraphics renderer rebuilds NSAttributedStrings per-line/frame at 4-7k events/sec (#486); resize-narrower causes permanent buffer corruption (#494); weak IME. Mitigations: Metal renderer + buffered PTY pumping at 60Hz + force-reattach hotkey. |
| 10 | [10-landscape.md](./10-landscape.md) | **Wider AI desktop landscape** (Cursor, Zed, Goose, Codex, Aider, Sketch, etc.) | Three architectural camps. **Headless-agent-with-native-UI is the dominant 2026 pattern.** ACP (Agent Client Protocol) emerging as cross-vendor standard. fleet's tmux+SwiftTerm path is genuinely distinctive. |

## Methodology notes

- Agents had a 30-minute time budget each, web search + repo cloning to `/tmp/fleet-research-clones/` authorized.
- Findings cite file:line and source URLs.
- Cloned repos analyzed: Crystal, Claudia, Claude Squad, Cline, VibeTunnel, plus several others discovered during scan.
- Pre-existing context the agents were briefed with: fleet's locked architecture (SwiftUI + SwiftTerm + tmux + Go daemon + gRPC).
