# VibeTunnel & Emerging Claude-Code Wrappers — Research Notes

Date: 2026-05-07. Research target: how the new wave of Claude-Code wrappers / remote-control / multi-agent products render terminals, integrate with Claude, and what fleet should learn or avoid.

---

## 1. VibeTunnel — `amantus-ai/vibetunnel`

URLs:
- Repo: https://github.com/amantus-ai/vibetunnel
- Site: https://vibetunnel.sh / https://docs.vibetunnel.sh
- Author deep dive: https://steipete.me/posts/2025/vibetunnel-turn-any-browser-into-your-mac-terminal

Cloned to `/tmp/fleet-research-clones/vibetunnel`.

### Three-component architecture
README §Architecture (lines 174–180):
> "VibeTunnel consists of three main components: 1. **macOS Menu Bar App** — Native Swift application that manages the server lifecycle. 2. **Node.js Server** — High-performance TypeScript server handling terminal sessions. 3. **Web Frontend** — Modern web interface using Lit components and **ghostty-web**."

So the Mac app is just a launcher / menu bar / settings shell. The real work happens in the Node server.

### Rendering approach: server-side headless terminal emulator (the THIRD WAY)

This is the most interesting architectural finding from the whole research session. VibeTunnel **does not** terminal-attach (à la tmux pipe-pane / ttyd) and it **does not** rebuild a chat UI. Instead it runs a full terminal emulator (Ghostty, compiled to WASM) **on the server**, feeds the PTY stream into it, and ships the resulting cell grid over WebSocket to the browser.

`/tmp/fleet-research-clones/vibetunnel/web/src/server/services/terminal-manager.ts:3`
```ts
import { CellFlags, Ghostty, type GhosttyTerminal } from 'ghostty-web';
```
`terminal-manager.ts:130–136`:
> "Manages terminal instances and their buffer operations for terminal sessions. Provides high-performance terminal emulation using **ghostty-web (WASM) terminals**, with sophisticated flow control, buffer management, and real-time change notifications. Handles asciinema stream parsing, terminal resizing, and **efficient binary encoding of terminal buffers**."

`terminal-manager.ts:199–215`:
```ts
async getTerminal(sessionId: string): Promise<GhosttyTerminal> {
  ...
  const ghostty = await ensureGhostty();
  const terminal = ghostty.createTerminal(80, 24, { scrollbackLimit: SCROLLBACK_LIMIT });
  ...
}
```

So per session: a Ghostty terminal lives on the server, holding 10K-line scrollback. The server subscribes to file watchers on each session's asciinema stream file (PTY output captured by the `vt fwd` Rust-ish binary `native/vt-fwd`), feeds bytes into Ghostty, and emits a `BufferSnapshot` (cell grid + cursor + viewport) when changed.

`terminal-manager.ts:120–127` — the snapshot shape:
```ts
interface BufferSnapshot {
  cols: number; rows: number; viewportY: number;
  cursorX: number; cursorY: number;
  cells: BufferCell[][];
}
```

The web client doesn't actually parse ANSI. It receives parsed cells. The ghostty-web wasm is also loaded **on the client** for some flows (test mocks at `web/src/test/setup.ts:6` confirm `ghostty-web` is mocked for client tests).

**Why this matters for fleet**: this is a strict third option beyond "attach to tmux PTY (TUI)" and "rebuild chat UI on top of Anthropic API" — keep the agent in a real PTY/tmux for fidelity, but render the cell grid yourself on the destination platform (web / SwiftTerm). It's how you get a real terminal experience while still having structured access to the cells (selection, search, link detection).

### PTY allocation: not tmux

VibeTunnel uses **node-pty + a native helper `vt-fwd`** + asciinema-format files on disk to capture sessions, **not tmux**. From the README §"How it Works" (lines 609–615):
> "Command Resolution → Session Creation → **PTY Allocation: A pseudo-terminal is allocated to preserve terminal features** → I/O Forwarding: All input/output is forwarded between your terminal and the browser in real-time → Process Management."

The `vt fwd` wrapper is a bash → node command that creates a session, allocates a PTY for the wrapped command (e.g. `vt claude`), and tee's I/O to an asciinema cast file under `~/.vibetunnel/`. The Node server watches those files.

### Claude Code involvement: NOT a wrapper, but they DO patch the binary

VibeTunnel is positioned as a generic terminal-over-the-web tool ("perfect for monitoring Claude Code, ChatGPT, or any terminal-based AI tools"). But they have **Claude-specific code**.

`web/src/server/utils/claude-patcher.ts:59–113` — they literally rewrite the claude CLI bundle to remove an anti-debugging check:
```ts
export function patchClaudeBinary(claudePath: string): string {
  ...
  const content = fs.readFileSync(claudePath, 'utf8');

  // Multiple patterns to match different variations of anti-debugging checks
  const patterns = [
    /if\([A-Za-z0-9_$]+\(\)\)process\.exit\(1\);/g,
    /if\s*\([A-Za-z0-9_$]+\(\)\)\s*process\.exit\(1\);/g,
    /if\([A-Za-z0-9_$]+\(\)\)process\.exit\(\d+\);/g,
  ];
  ...
  const newContent = patchedContent.replace(pattern, 'if(false)process.exit(1);');
  ...
  fs.writeFileSync(claudePath, patchedContent);
```

So Claude Code apparently exits when it detects a non-TTY / debugger-ish environment (probably `process.stdin.isTTY === false` or similar), and VibeTunnel binary-patches the JS to neutralize the check. They keep a backup and restore on cleanup. This is the kind of friction fleet might also hit when running claude under a non-Mac-Terminal PTY.

Also Claude/AI awareness in the client: `web/src/client/utils/ai-sessions.ts:7`:
```ts
const AI_ASSISTANTS = ['claude', 'gemini', 'openhands', 'aider', 'codex'];
```
And they have an LLM-prompt hack to update terminal titles (`ai-sessions.ts:31–36`):
> "IMPORTANT: You MUST use the 'vt title' command to update the terminal title. DO NOT use terminal escape sequences. Run: vt title \"Brief description of current task\""
That's literally injected into the AI session's stdin to trick it into setting a useful title.

### Notable architectural choices fleet should think about
1. **Server-side terminal emulator (Ghostty WASM)** — gives you a structured cell grid + cursor + scrollback to do things tmux's `capture-pane` can't (precise URL detection, OSC parsing, fast diffs). Big leap in capability.
2. **Asciinema cast files as the on-disk transport** — every session is captured in asciinema-v2 format. Trivially replayable, shareable, and the PTY stream is already disk-buffered (unlike tmux's RAM-only scrollback).
3. **Flow control at the cell-grid level** (terminal-manager.ts:75–95): high/low watermarks (80%/50%), pending-line buffer (10K), pause-timeout (5min) — nontrivial backpressure. fleet's status-detection-via-pane-capture has nothing like this.
4. **Git Follow Mode** — git hooks on worktrees that auto-checkout the same branch in main repo (README lines 326–331). This is exactly the kind of nicety conductor.build also has but fleet currently doesn't.
5. **Tailscale Funnel + Serve** integration (lines 200–262) for HTTPS-from-anywhere with zero config. Free-tier remote access without exposing the Mac.
6. **iOS app + Discord bot + push notifications** — the "remote control your agent from your phone" is a real positioning, not just a feature.

### Limitations / complaints (light)
- Apple Silicon required for native app; Intel users only get the npm package.
- Mobile paste UX is awkward (lines 848–858, requires a workaround input box).
- Anti-debug patching is fragile — they explicitly warn "No anti-debugging pattern found - Claude binary may have changed" (claude-patcher.ts:101).
- iOS app marked "still work in progress and not recommended for production use yet" (line 170).

---

## 2. Sketch.dev — `boldsoftware/sketch`

URLs:
- Site: https://sketch.dev (now rebranded to "Shelley" per their blog)
- Repo: https://github.com/boldsoftware/sketch
- Author piece: https://sketch.dev/blog/programming-with-agents

### Architecture
- **Languages**: Go 52.6%, TypeScript 45.2%. Has both `termui/` and `webui/` directories.
- **Direct Anthropic API** — requires `ANTHROPIC_API_KEY`. NOT a Claude Code wrapper. Their own agent loop in `llm/`.
- **Container-isolated**: each session creates a Dockerfile, builds it, copies the repo in, runs the agent inside. Prevents agent from touching host fs / production credentials.
- **Git-native**: agent commits to `sketch/*` branches in your host repo via container→host git push.
- Multiple parallel agents = multiple containers.
- **Editable diff view** in the web UI — you can type into the right side of a diff and it becomes a commit (very different UX from terminal-attach).
- VS Code remote: container exposes SSH, sketch generates a `vscode://` URL to attach.

### Why fleet cares
This is the "rebuild" school done right. They don't render terminal at all in the primary UX — they show diffs, commits, and a chat. The terminal is incidental (you can SSH into the container). Fully different positioning from fleet, but a valid signal that "show diffs prominently" is a feature dimension we can borrow.

---

## 3. OpenCode — `sst/opencode`

URLs:
- Site: https://opencode.ai
- Repo: https://github.com/sst/opencode
- DeepWiki: https://deepwiki.com/sst/opencode

### Architecture
- **Client/server split** by design. Backend handles LLM inference, tool execution, session persistence, MCP servers. Frontends (TUI, desktop, web, mobile) connect over **HTTP + SSE**.
- **TypeScript monorepo, Turbo + Bun**.
- **Provider-agnostic**: uses Vercel AI SDK to abstract 75+ providers from `models.dev`. Not a Claude Code wrapper — its own agent loop. Supports Claude, OpenAI, Google, local models.
- **TUI built with Ink** (React-for-CLI), in `cli/cmd/tui/`. Component-based, dialogs, frecency-history autocomplete, 20+ themes.
- **Agent personas built in**: `build`, `plan`, `general`, `explore` (read-only). Subagent system. (https://opencode.ai/docs/agents/)
- Desktop app is **BETA**, downloads available for macOS/Win/Linux. Stack not publicly documented but likely Tauri given the SST team's bias.

### Why fleet cares
- Strongest existing parallel: client/server with multiple frontends. Fleet's daemon→TUI/Desktop architecture directly mirrors this.
- They went **Ink** for TUI; fleet is Bubble Tea. Both viable. Ink+React shines for dialog-heavy UIs but at the cost of Node + slower startup.
- Subagent personas (build/plan/general/explore) are a productized pattern. Fleet currently has the underlying tmux/agent-team plumbing but not user-facing personas.
- They DO NOT wrap claude CLI — they reimplement the loop. Fleet's bet on `claude` CLI is the opposite bet, and creates a lock-in advantage when Claude releases features (skills, plugins) but a disadvantage on multi-provider.

---

## 4. opcode — `winfunc/opcode`

URLs:
- Site: https://opcode.sh
- Repo: https://github.com/winfunc/opcode

### Architecture
- **Tauri 2 + React 18 + TypeScript + Rust backend**, SQLite via rusqlite.
- Manages **existing Claude Code sessions** — reads `~/.claude/projects/` to browse/resume.
- "CC Agents" feature: user-defined system prompts running as separate background processes.
- UI is web-rendered (Tailwind v4 + shadcn/ui). Doesn't expose deep terminal rendering details — likely chat-style with command output panels rather than full terminal emulator.

### Why fleet cares
- Confirms `~/.claude/projects/` is becoming a de-facto session-state API surface. Fleet already taps `~/.claude/settings.json`; could similarly read project-state for richer history browsing.
- Tauri is the popular choice (vs Electron) for new entrants — smaller bundle, native-ish. Fleet's SwiftUI native bet is even tighter but sacrifices Windows/Linux entirely.

---

## 5. Conductor.build

URLs:
- Site: https://www.conductor.build
- Docs: https://www.conductor.build/docs
- Architecture write-up: https://rywalker.com/research/conductor

### Architecture
- Mac desktop app, runs Claude Code + Codex agents in **isolated git worktrees**.
- Local-first: clones repos, runs entirely on your Mac.
- ⌘+N creates a new workspace; centralized dashboard tracks all agents.
- Works with Claude Pro/Max subscription OR API key.
- Stack details NOT publicly documented (asked, no public answer). Strong indicators (judging from polished UI, native feel) it's Electron or Tauri rather than fully native.
- "Seamless Git Integration handles all worktree management automatically."

### Why fleet cares
This is the closest direct competitor to fleet's desktop V1. Same fundamental thesis: parallel agents, worktree isolation, single Mac dashboard. Fleet's differentiation has to be (a) TUI-first/keyboard-first ergonomics for power users, (b) tmux interop so headless / SSH workflows still work, (c) deeper Claude-Code-specific intelligence (status detection, hook integration). Fleet's prior memo notes Conductor as the Stage 0 reference; this confirms the framing.

---

## 6. CodeBuff (formerly Manicode) — `CodebuffAI/codebuff`

URLs:
- Site: https://codebuff.com
- Repo: https://github.com/CodebuffAI/codebuff

### Architecture
- TypeScript 97%, monorepo with `cli/`, `sdk/`, `web/`.
- **Multi-agent loop**: Base2 Orchestrator → File Picker → Planner → Editor → Reviewer. Each agent has its own context window.
- OpenRouter for model routing; not Claude-locked.
- TUI framework not publicly documented; likely Ink given TS-heavy stack.

### Why fleet cares
Demonstrates that "split into specialized sub-agents with isolated contexts" is a credible product axis. Claude Code's own agent-team feature is the same idea less productized. Fleet's status detection already has structural awareness of agent-team UI (per CLAUDE.md "Agent team status: sub-agents don't fire hooks") — but doesn't yet productize it.

---

## 7. Crystal / Nimbalyst — `stravu/crystal`

URLs:
- Repo (deprecated): https://github.com/stravu/crystal
- Successor: https://nimbalyst.com

### Architecture
- **Electron desktop app**, manages parallel Claude Code + Codex sessions in **git worktrees**.
- Real-time session list, automatic commits per iteration, comprehensive diff viewer, rebase tooling.
- Deprecated Feb 2026 → Nimbalyst.

### Why fleet cares
Same worktree-per-agent model as Conductor and fleet. Worth checking Nimbalyst for the latest evolution but they appear to be a paid/closed successor.

---

## 8. amux — `mixpeek/amux`

URLs:
- Repo: https://github.com/mixpeek/amux

### Architecture
- **Single-file Python app**, inline HTML/CSS/JS (49% HTML, 43% Python).
- Parses **ANSI-stripped tmux output** with no hooks / no patches / no Claude modifications.
- Web dashboard with kanban board (SQLite), notes (Quill editor), CRM, channels, file browser, scheduler.
- iOS PWA support.
- Atomic kanban task claiming, agent-to-agent messaging, self-healing restarts.

### Why fleet cares
Closest in spirit to fleet but is a control-plane / orchestrator rather than a TUI. Demonstrates that "tmux + capture-pane parsing + web dashboard" is a viable architecture even with zero hooks. Fleet's CLAUDE.md notes "Hooks are authoritative" — amux proves you can ship without hooks if you accept text-pattern fragility.

---

## 9. dmux — `formkit/dmux`

URLs:
- Repo: https://github.com/formkit/dmux

### Architecture
- TypeScript + HTML, Node 18+, pnpm.
- Creates **tmux pane + git worktree + launches agent** (Claude/Codex/Cline + 8 more).
- AI-generated branch names + commit messages.
- Lifecycle hooks for worktree create / pre-merge / post-merge.

### Why fleet cares
Direct overlap. Fleet's `w` keybinding (new worktree session) is essentially what dmux launches. Lifecycle hooks are a feature fleet doesn't have yet — could be wired into the workspace provider.

---

## 10. Codeman — `Ark0N/Codeman`

URLs:
- Repo: https://github.com/Ark0N/Codeman

### Architecture
- Node.js + TypeScript + **Fastify** backend.
- **xterm.js at 60fps** in browser.
- WebUI ↔ Fastify REST + SSE ↔ Session Manager ↔ PTY handlers ↔ tmux ↔ Claude Code CLI.
- Claims 20 parallel sessions, sessions persist across server restart via tmux.
- Background subagent watching via `~/.claude/projects/*/subagents` directory.

### Why fleet cares
Codeman is **architecturally the closest non-VibeTunnel project to "what fleet's web mode would look like."** Same tmux + SSE + xterm.js stack as classic ttyd/wetty but with Claude-specific awareness (`~/.claude/projects/*/subagents`).

The xterm.js+SSE stack means **client-side ANSI parsing**, which is the OPPOSITE choice from VibeTunnel's server-side Ghostty-WASM approach. This is the central rendering question for fleet's eventual web/desktop split.

---

## 11. claudecodeui (CloudCLI) — `siteboon/claudecodeui`

URLs:
- Repo: https://github.com/siteboon/claudecodeui

### Architecture
- React + Vite + Tailwind, TS-heavy.
- Auto-discovers sessions from `~/.claude` folder; reads/writes shared config.
- Supports Claude Code, Cursor CLI, Codex, Gemini CLI.
- Includes "Integrated Shell Terminal" but transport details undocumented in README.

### Why fleet cares
Reinforces `~/.claude` as the de-facto session inventory directory. Multi-CLI support is a feature axis fleet has explicitly punted on (CLAUDE.md: "Claude Code only").

---

## 12. VibeKit — `superagent-ai/vibekit`

URLs:
- Site: https://docs.vibekit.sh
- Repo: https://github.com/superagent-ai/vibekit

### Architecture
- **MIT-licensed npm SDK**: `@vibe-kit/sdk`.
- Embeds coding agents into web apps, not a desktop app.
- **Docker sandbox** (or E2B / Daytona / Modal / Fly.io) per agent.
- Sensitive-data redaction before bytes leave the box; full observability (file reads/writes, shell, API calls).
- Supports Claude Code, Codex, Gemini, Grok, OpenCode as drop-in agents.

### Why fleet cares
VibeKit is a **layer below** fleet — they sandbox an agent for embedding in your SaaS. Fleet is end-user-facing. But VibeKit's redaction layer is interesting: fleet's bug-report dialog already sanitizes home dir to `~`; VibeKit goes further (redacts secrets pre-egress). Could be a feature for fleet's own telemetry/logging.

---

## 13. Plandex — `plandex-ai/plandex`

URLs:
- Repo: https://github.com/plandex-ai/plandex

### Architecture
- Open-source CLI agent, **client/server**.
- Dockerized self-hosted server OR cloud option.
- 2M token effective context, tree-sitter syntax for 30+ langs.
- "Cumulative diff review sandbox" — changes staged outside project until ready.
- OpenAI-API-spec-compatible LLM agnostic.
- REPL mode with fuzzy autocomplete.

### Status
**Winding down 10/3/2025**, no longer accepting new users.

### Why fleet cares
Cautionary tale — even an open-source CLI agent that was technically solid (2M context, sandboxed diffs) couldn't survive Claude Code's gravitational pull. Reinforces fleet's bet on being an **interface to claude** rather than a competing agent runtime.

---

# Cross-cutting patterns

## Three rendering archetypes (the "third way" finding)

| Archetype | Examples | Approach |
|---|---|---|
| **Terminal-attach** | fleet (TUI), amux, dmux, claude-tmux, opcode (partial) | Wrap claude in tmux/PTY, read text via `capture-pane` or `pipe-pane`, status detection via text patterns + Claude hooks. Renderer: tmux+native terminal OR xterm.js client-side ANSI parse. |
| **Server-side cell-grid emulator** | **VibeTunnel** | Wrap claude in PTY → asciinema cast file → server-side Ghostty-WASM emulates terminal → ship cell grid over WS to thin client. |
| **Full rebuild** | sketch.dev, opcode, codebuff, claudia, OpenCode, opcode | No terminal in primary UX. Chat + diffs + file browser. Either own agent loop (sketch, opencode, codebuff) or call claude programmatically (claudia, opcode). |

Fleet's current bet is archetype 1. The desktop initiative will need to choose: stay archetype 1 with SwiftTerm (matches the TUI parity goal), OR move to archetype 2 with a server-side emulator + custom view (matches VibeTunnel's gain in capability — selection, search, link parsing, cell-level diffs). Archetype 3 is off-thesis.

## Claude Code "lock-in" surface area

Most wrappers exploit one or more of these:
- `~/.claude/projects/*` — session state directory (claudia, opcode, claudecodeui, Codeman)
- `~/.claude/settings.json` — hooks (fleet, others)
- `claude --resume <id>` — session resume (fleet, conductor)
- Stdin injection of "set your title" prompts (VibeTunnel's hack)
- **Binary patching** for anti-debug evasion (VibeTunnel only — interesting / fragile)

Fleet uses the first three; the binary-patch trick is something to keep in our back pocket if claude CLI ever starts refusing fleet-driven PTYs.

## Worktree isolation is now table stakes

Conductor, Crystal/Nimbalyst, Codeman, dmux, amux, fleet all run agents in separate git worktrees by default. This is no longer a differentiator — it's the floor.

## Mobile / remote-control is the rising axis

VibeTunnel, amux (PWA), agent-of-empires, claudecodeui all explicitly position around "your phone is the dashboard". Fleet is Mac-only; if remote-control becomes a key buyer driver, fleet's roadmap might need a "headless mode + web bridge" companion rather than a full mobile rebuild.
