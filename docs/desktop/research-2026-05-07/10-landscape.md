# 2026 Landscape: Mac/Desktop Apps for AI Coding Agents

Research date: 2026-05-07. For the **fleet** project (SwiftUI + SwiftTerm + tmux + Go daemon). Goal: identify architectural patterns in the broader category — not just Claude Code wrappers.

---

## TL;DR

Three architectural camps:

1. **Wrap-real-CLI in PTY/terminal widget** — Conductor, Opcode/Claudia, Crystal/Nimbalyst, fleet. Spawn the actual `claude` (or `codex`) binary in a pty, render output via xterm.js or SwiftTerm. **Few players. Mostly Tauri+xterm.js.** fleet's tmux+SwiftTerm angle is ~unique on Mac.
2. **Headless agent + custom UI** (stream-json / SDK / ACP) — Claudia/Opcode (in part), Conductor (uses TS SDK, not CLI), Zed Agent Panel (via ACP), Sketch (own loop, web UI), Goose Desktop, Devin, Sourcegraph Amp, Cursor Agents Window. **Dominant pattern in 2026.** Lets vendors render diffs/checkpoints/timelines natively, escape ANSI.
3. **Editor-first IDE w/ agent baked in** (rebuild) — Cursor (VS Code fork), Windsurf (VS Code fork w/ Cascade), Zed (Rust-native w/ ACP for external agents), Continue.dev (extension), GitHub Copilot, Cody. Their "agent" is in-process or a forked VS Code extension; terminal is incidental.

**Cloud/web outliers:** Devin, GitHub Copilot Workspace/Cloud Agent, Replit Agent, bolt.new, Lovable, Sketch (containerized cloud sandbox) — agent runs in a remote container, UI is a web app.

---

## Per-Product Notes

### Cursor (VS Code fork)
- **Wrap vs rebuild:** Rebuild. Has its own agent loop ("Composer 2" + sub-agents). VS Code fork.
- **UI:** Full IDE; dedicated "Agents Window" (Cursor 3, Apr 2026) for parallel agents across local, worktree, SSH, cloud.
- **Process:** Electron (VS Code fork) + Cursor CLI for terminal/automation.
- **Interesting:** Cursor 3 explicitly pivoted from "AI pair-programmer" → "agent orchestrator." Sub-agents in parallel with own context. Background Cloud Agents triggered by webhooks (must come *through* Cursor).
- Source: https://www.infoq.com/news/2026/04/cursor-3-agent-first-interface/, https://cursor.com/cli, https://cursor.com/docs/subagents

### Zed (Rust-native editor)
- **Wrap vs rebuild:** Hybrid via ACP (Agent Client Protocol). Zed runs a Rust adapter that **launches Claude Code as a subprocess and speaks ACP to it** (via stdio JSON-RPC). Not embedded in a terminal — output is parsed and rendered native.
- **UI:** Native Rust GPU UI. "Agent Panel" (cmd-?). Renders Claude/Codex/Gemini threads in chat-style native widgets.
- **Process:** Native Rust app, Claude Code child process over ACP.
- **Interesting:** Zed pushed ACP as **the open standard** for "external agents in editors" — Goose, Cursor (JetBrains plugin uses ACP), Claude Agent SDK all support it. This is the closest thing to a category-wide protocol.
- Source: https://zed.dev/blog/claude-code-via-acp, https://zed.dev/acp, https://zed.dev/docs/ai/agent-panel

### Windsurf (Cognition, ex-Codeium)
- **Wrap vs rebuild:** Full rebuild. VS Code fork, own agent ("Cascade") and own model (SWE-1.5).
- **UI:** Native chat panel inside fork, no terminal embed of an external CLI.
- **Process:** Electron (VS Code fork).
- **Interesting:** Two-agent design (planner + executor). "Codemaps" visual code nav. Bought by Cognition (Devin) Dec 2025.
- Source: https://windsurf.com/cascade

### Sketch.dev / Shelley
- **Wrap vs rebuild:** Own agent loop in Go, very minimal — "tool=bash" loop pattern.
- **UI:** Web UI (not a desktop app per se). Renders diffs and accepts review comments on a diff view.
- **Process:** Agent runs in a **Docker container per session** with isolated git checkout; pushes commits to host repo's `sketch/*` branches.
- **Interesting:** Same parallelism story as fleet (multi-session, isolated workspaces) but via containers, not git worktrees + tmux. Confirms the "isolated workspaces" thesis is widespread.
- Source: https://github.com/boldsoftware/sketch, https://sketch.dev/blog/agent-loop

### OpenAI Codex CLI (`codex`)
- **Wrap vs rebuild:** It's a CLI itself (Rust binary). The OpenAI "Codex App" desktop wrapper exists.
- **UI:** Codex App is OpenAI's hosted desktop wrapper; CLI also has TUI.
- **Interesting:** Codex 2026 added **Unix socket transport** for app-server integrations, pagination/resume/fork APIs, sticky environments. Mirrors Claude Code's stream-json direction. Codex is the most direct Claude-Code analog.
- Source: https://github.com/openai/codex, https://developers.openai.com/codex/cli

### Goose Desktop (now Agentic AI Foundation, ex-Block)
- **Wrap vs rebuild:** Own agent loop in Rust (`goosed` daemon).
- **UI:** Desktop app **migrating Electron v40 → Tauri v2** (proposal, ~1600 LOC Rust replaces 2400 LOC Electron main). Frontend stays React 19 + Vite.
- **Process:** Tauri (target) + Rust `goosed` backend daemon. Speaks ACP to integrate with external agents (incl. Claude).
- **Interesting:** **Daemon + thin client** pattern is identical to fleet's split (Go daemon + clients). Goose's `goosed` is a useful reference. They migrated Electron→Tauri explicitly for bundle/memory savings.
- Source: https://github.com/aaif-goose/goose/discussions/7332, https://goose-docs.ai/

### Aider
- Terminal-native, Python. **No first-party desktop GUI.** Several community wrappers (e.g., `aiderx`) exist. Watch-mode lets it pick up `# AI` comments edited from any IDE.
- Source: https://github.com/Aider-AI/aider

### Plandex
- Terminal CLI, Go. Own agent loop, tree-sitter project map, sandbox/diff-review. No native desktop wrapper.
- Source: https://github.com/plandex-ai/plandex

### Continue.dev
- VS Code/JetBrains extension + Continue CLI. **Three-process architecture: core ↔ extension ↔ React webview gui** with message passing. Open source. No standalone desktop.
- Source: https://github.com/continuedev/continue

### Cody (Sourcegraph) / Sourcegraph Amp
- Cody = VS Code/JetBrains extension; `@sourcegraph/cody-agent` is a JSON-RPC server over stdio for non-JS clients.
- Amp = Sourcegraph's 2026 agentic CLI + IDE-extension product. CLI rebuilt; threads sync to ampcode.com; modes (smart/rush/deep). Command allowlisting in repo.
- Interesting: Cody's agent-over-stdio JSON-RPC pattern predates ACP and is essentially the same idea.
- Source: https://sourcegraph.com/amp, https://sourcegraph.com/docs/cody

### GitHub Copilot Workspace / Cloud Agent
- Pure cloud. Coding Agent runs in a GitHub Actions environment, opens a PR, pushes commits. UI is github.com web. VS Code's "agent mode" delegates to it.
- Source: https://code.visualstudio.com/docs/copilot/copilot-cloud-agent, https://github.com/features/copilot/agents

### Anthropic Claude Desktop App + **Cowork**
- Single desktop app with three modes: Chat, **Cowork** (folder-scoped agent), Claude Code (terminal embed).
- Cowork (Jan 2026 macOS, Feb 2026 Windows): grants scoped folder access; agent plans + executes multi-step tasks. **Anthropic itself** ships a Claude Code embed inside the desktop app — integrated terminal, file editor, diff viewer.
- Implication: Anthropic legitimized "embed Claude Code inside a desktop app" — fleet is in good company on the wrap-real-CLI pattern (though Claude Desktop's terminal embed isn't documented as a separate process / pty model).
- Source: https://www.anthropic.com/product/claude-cowork, https://code.claude.com/docs/en/desktop

### Conductor (conductor.build) — closest peer
- **Mac-only desktop app** for parallel Claude Code / Codex sessions in git worktrees. **Tauri** + React.
- **Critically: built on the Claude Code TypeScript SDK, not the CLI directly.** That means Conductor runs the agent loop *itself* via SDK and renders chat in iMessage-style native UI. There's a "terminal" panel for actual shell, but the *agent* is not a pty-attached CLI.
- This is **architecturally different from fleet**. fleet runs `claude` in tmux, attaches via SwiftTerm; Conductor headlessly drives the SDK and renders chat natively.
- Source: https://www.conductor.build/, https://docs.conductor.build/, https://georgetaskos.medium.com/scaling-the-loop-run-5-claude-code-sessions-in-parallel-with-conductor-build-539b52888a81

### Crystal → Nimbalyst (stravu)
- **Closest spiritual sibling to fleet.** Multi-session Claude/Codex in git worktrees. macOS/Windows/Linux desktop, Electron-based historically; Nimbalyst is the active fork.
- **Tool Panel System: multiple terminal instances per session, scrollback persistence.** This implies pty + xterm.js (Electron). Closest "embed real CLI in terminal widget" peer.
- SQLite session persistence. iOS companion app (Nimbalyst).
- Source: https://github.com/stravu/crystal, https://nimbalyst.com/

### Claudia → **Opcode** (Asterisk Labs)
- Tauri 2 + React + Rust. Renamed mid-2025.
- **Uses portable-pty** to spawn Claude Code per session; output streamed to React via Tauri events. Each session has its own pty process.
- This is exactly fleet's pattern, in Rust+web instead of Swift+tmux. **Direct architectural peer.**
- Features: timeline checkpoints + branching, MCP server UI, OS-level sandboxing (seccomp/Seatbelt).
- Source: https://opcode.sh/, https://github.com/winfunc/opcode

### Claw / Clarc (jamesrochabrun, ttnear)
- **SwiftUI native macOS apps** for Claude Code.
- Claw: thin SwiftUI wrapper around the Swift Claude Code SDK (jamesrochabrun/ClaudeCodeSDK). Reuses `claude auth login` credentials.
- Clarc: SwiftUI native, per-project windows, approval modals for tool calls.
- Both **SDK-driven, not CLI-pty-attached.** Native chat UI.
- Source: https://github.com/jamesrochabrun/Claw, https://github.com/ttnear/Clarc

### VibeTunnel
- Browser-based terminal — wraps **any** CLI (incl. Claude Code) and forwards to a browser. Built with Claude Code in 24 hours by steipete + Mario + Armin.
- Architecture: `vt` shim runs in your terminal, pipes to a server, served as xterm.js in a browser. Lets you control Claude Code remotely.
- **Most relevant:** confirms there's a small ecosystem doing pty-passthrough-of-real-CLI but they all bridge to a *browser* not a native app.
- Source: https://github.com/amantus-ai/vibetunnel, https://steipete.me/posts/2025/vibetunnel-turn-any-browser-into-your-mac-terminal

### Devin / Cognition
- Cloud-only autonomous engineer. Desktop app + CLI is a thin gRPC/WebSocket client to Cognition's cloud sandbox. Streams remote terminal/editor/browser to local UI (<50ms). REST API; assignable from Slack/Jira/web.
- Devin 2.0: parallel Devins, each with own cloud IDE.
- Cognition acquired Windsurf Dec 2025 — combining cloud agent + local IDE.
- Source: https://cognition.ai/blog/devin-2

### Replit Agent / bolt.new / Lovable / v0
- Pure web vibecoding. Agent loop in vendor cloud, browser UI. No desktop. Replit Agent 4 (Mar 2026) — parallel tasks, checkpoint rollback, 30+ integrations.
- Source: https://lovable.dev/guides/bolt-vs-replit-vs-lovable

### Warp (Rust terminal w/ Agent Mode)
- Terminal-first; **opens in May 2026 as open source** (MIT/AGPL dual). Rust + custom GPU UI framework `warpui`. Cloud Agents = "Oz" (background, webhook/cron).
- Talks MCP. Local agent in the terminal itself, not wrapping `claude`. Warp is its own thing — they want the terminal to *be* the agent.
- Source: https://www.warp.dev/, https://github.com/warpdotdev/warp

---

## Cluster Map

### Cluster A — "Embed real CLI in pty/terminal widget" (fleet's camp)
- **fleet** (SwiftUI + SwiftTerm + **tmux**)
- **Opcode/Claudia** (Tauri + React + portable-pty)
- **Crystal/Nimbalyst** (Electron + xterm.js + node-pty, presumed)
- **Anthropic Claude Desktop Code mode** (terminal embed; mechanism not documented publicly)
- **VibeTunnel** (browser-side variant — wraps any CLI in xterm.js over a tunnel)

Total: ~4 desktop apps + 1 browser-tunnel. **All but fleet use xterm.js.** fleet's SwiftTerm path is genuinely uncommon; the **tmux** session backing (vs raw pty) appears unique among peers.

### Cluster B — "Headless agent + native UI" (dominant in 2026)
- **Conductor** (TS SDK + Tauri/React)
- **Zed Agent Panel** (Claude Code child process via ACP, native render)
- **Goose Desktop** (`goosed` Rust daemon + React, → Tauri)
- **Cursor Agents Window** (own agent + sub-agent fan-out)
- **Sketch** (Go agent loop + Docker sandbox + web UI)
- **Devin** (cloud sandbox + streaming UI)
- **Sourcegraph Amp / Cody** (CLI/extension + JSON-RPC core)
- **Continue.dev** (core ↔ extension ↔ React webview)
- **Claw / Clarc** (Swift SDK + native SwiftUI)

### Cluster C — "IDE rebuild with agent inside"
- **Cursor**, **Windsurf**, **Zed**, **Continue.dev**, **GitHub Copilot/VS Code agent mode**
- The agent is part of the editor, no separate session model.

### Cluster D — "Pure cloud / web vibecode"
- **GitHub Copilot Cloud Agent / Workspace**, **Replit Agent**, **bolt.new**, **Lovable**, **v0**, **Devin**

---

## Patterns & Surprises

### The dominant pattern in 2026 is **headless agent + native UI**, not pty-wrap.
Conductor (TS SDK), Zed (ACP), Goose (`goosed`), Sketch (own loop), Claw/Clarc (Swift SDK), Continue (core ↔ webview) — all bypass the CLI's TUI and drive the agent programmatically so they can render diffs/checkpoints/threads natively without ANSI parsing.

### **ACP (Agent Client Protocol) is becoming the integration standard.**
Zed pushed it. Cursor's JetBrains plugin uses it. Goose speaks it. Sourcegraph Cody had a similar JSON-RPC-over-stdio pattern earlier. Anthropic's TS Agent SDK is ACP-compatible. **If fleet wanted to render chat natively in V2, ACP would be the path** — same protocol Zed adopted.

### Fleet's exact architecture (SwiftUI + SwiftTerm + tmux + real `claude` PTY) is **rare**.
- Opcode is the closest peer (Tauri+portable-pty+React); Crystal/Nimbalyst is similar but Electron+web.
- **No one else uses tmux as the session backend that we found.** fleet's bet — that tmux gives you free reattach/persistence/multiplexing — looks distinctive.
- Anthropic's Claude Desktop also embeds a terminal but doesn't document the mechanism; likely xterm.js inside Electron.

### Surprise #1: Conductor is **not** a CLI wrapper.
Despite the surface impression, Conductor uses the Claude Code TypeScript SDK and renders chat in native iMessage-like UI. Their "terminal" panel is for the user's shell, not the agent. They are squarely Cluster B, not A.

### Surprise #2: Goose's daemon-client split is identical to fleet's daemon-client split.
`goosed` (Rust) ↔ Tauri/React clients. fleet's `fleet daemon` (Go) ↔ TUI/Mac clients. Worth studying their Tauri command surface as a reference for fleet's daemon API.

### Surprise #3: Anthropic itself ships Claude Code embedded in a desktop app.
Validates the wrap-real-CLI category from the vendor side. fleet isn't doing something weird.

### Surprise #4: Crystal got renamed to Nimbalyst (Feb 2026) and added an iOS companion app.
Multi-session-on-mobile is a real product direction. Mobile-companion-for-fleet is plausible.

### Surprise #5: Cursor 3 (Apr 2026) explicitly reframed itself from IDE → agent orchestrator.
The "agents window" is essentially what fleet's sidebar is. fleet's design instinct (parallel sessions as a first-class object) is converging with the rest of the category.

### Notable absences
- **Helix** — no AI-coding-agent integration found; it's a modal text editor.
- **dyad.dev** — appears to be a free comparison-of-app-builders site, not its own coding agent product.
- **Claude.ai web** — has Skills + Agent Skills but not deep file/terminal access on Mac (that's Cowork's territory).

---

## Implications for fleet

1. **The terminal-attach approach is the minority** — and that's defensible if the value is "reattach across reboots, real ANSI, real `claude --resume`, real tmux scrollback, no SDK lag behind CLI features." fleet's pitch should lean on **fidelity** vs Conductor's lag-behind-SDK problem.
2. **Adopt ACP if/when fleet adds a native chat surface** — would let fleet's daemon serve any IDE that speaks ACP and any agent that speaks ACP (Claude, Codex, Gemini, Goose).
3. **Goose Desktop (Tauri+`goosed`) is the closest architecture analog** — read their Tauri command surface, NMH-style auto-install patterns, and migration write-up.
4. **Opcode is the closest *implementation* peer** — portable-pty + React+Tauri does what fleet does in SwiftTerm+SwiftUI.
5. **Tmux is fleet's quiet differentiator.** No peer uses it for session persistence. Worth highlighting in marketing as "your sessions survive reboots, network drops, and `cmd+Q`."
