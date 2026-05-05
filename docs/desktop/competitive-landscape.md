# Competitive Landscape

Distilled from full research at `memory/desktop_research.md`. Read that for sources and full feature inventories.

## Conductor.build — the product to beat

Native macOS app, "Run a team of coding agents on your Mac." $22M Series A, free, closed source. Likely Electron + React + TypeScript (rebuild cadence and bundle-size announcements give it away). Apple Silicon required.

### What's good (and we should learn from)

- **Status-grouped sidebar**: Backlog / In progress / In review / Done. Cleanest mental model for parallel agent work. Maps to fleet's Idle / Running / Waiting / Finished.
- **Distinct icons per "needs attention" reason** (plan-approval vs tool-approval vs user-prompt vs running). fleet has only one Waiting state — splitting it would reduce false positives in the Space-key jump.
- **Auto-rename workspaces from PR title** with fallback to branch name, fallback to city codename. Robust three-tier titling.
- **"Suggested Git Actions"** — contextual buttons (Pull, Resolve conflicts, Create PR, Merge) appear when relevant, instead of users memorizing keybinds.
- **Keyboard one-shots**: `⌘⇧R` Code Review, `⌘⇧X` Fix Errors, `⌘⇧M` Merge, `⌘⇧L` Pull. Pre-canned high-value prompts.
- **`@terminal` mention** that attaches pane output to next prompt — fleet's pane capture already does this internally; expose it.
- **Per-commit diff filtering** in their "Pierre Diffs" engine.
- **Plan-mode and effort toggles in the chrome** instead of buried in settings.
- **Mid-flight steering** (v0.50): redirect a running agent without restarting the thread.
- **`⌘K` command palette** (workspace + PR + settings search). fleet has `:` / `Ctrl+P` — keep investing.

### What's bad (the "worktree-only feel")

The user identified Conductor as having a "worktree-only feel" they hate. HN threads decode that into seven concrete failures:

1. **Forced isolation tax.** Every workspace is a fresh worktree → re-install node_modules, re-compile Phoenix deps, re-fetch private packages. Brutal for monorepos.
2. **No "session on main."** Sometimes you want an agent on the actual repo for a hotfix or exploration. Conductor refuses.
3. **`.env` and untracked files lost.** Worktrees inherit tracked files only. Local secrets, dev DB, build caches all vanish in each new workspace.
4. **Forced GitHub OAuth on first run.** Excludes Gitea, self-hosted, local-only experiments.
5. **Loss of native Claude Code feel.** HN comment: "there's a 'feel' to the way Claude Code outputs the text… this is lost with conductor. I just don't feel as joyful using it." This is the single biggest reason fleet's tmux-pane approach is a moat.
6. **Cognitive load.** Five parallel agents is fantasy for most tasks; the orchestration tool needs to make context-switching cheap, and Conductor's three-pane layout per workspace makes every switch expensive.
7. **Worktrees as solution-in-search-of-a-problem.** The bottleneck is human review, not isolation.

### Things we should NOT copy

- **Closed source.** #1 trust complaint about Conductor.
- **Forced clone of GitHub repo** (no "use my existing local checkout" mode).
- **Setup-script-required-or-broken** workflow.
- **Electron heaviness** — we're going native Swift specifically to avoid this.
- **Worktree-only model** — we explicitly support plain repo + worktree + custom shell provider.
- **City-name auto-codenames.** Cute but adds noise on top of fleet's existing prompt-based auto-naming.
- **Inline GitHub diff comment sync.** Outside scope; let `gh pr review` own that.

## Peer scan

### Crystal — DEPRECATED Feb 2026

[stravu/crystal](https://github.com/stravu/crystal) was the original "Conductor but open source" — Electron + TypeScript, worktree per Claude/Codex instance. Praised for "doing one thing really well, no opinionation." **Replaced by Nimbalyst**, which expanded scope into AI-native workspace with markdown/spreadsheet/diagram editors. We will not follow Nimbalyst into that creep — that's a different product.

### Claudia → opcode

Tauri 2 + React + Rust. Different positioning: a **Claude Code project & cost dashboard**, not a parallel-agent orchestrator. Surfaces `~/.claude/projects/` sessions, builds custom CC Agents, tracks token spend, manages MCP servers, does timeline checkpointing. **Steal**: cost analytics dashboard (token spend per session is something fleet currently has zero visibility into), MCP server config UI, timeline/checkpoint metaphor. **Skip**: agent authoring — not our lane.

### Warp 2.0 / Warp Code / Oz

Reframed as an "Agentic Development Environment." Multi-agent panel with status visibility, system + in-app notifications when an agent needs input. **Oz** (Feb 2026) extends this to cloud agent orchestration with audit/governance — out of our scope. **Steal**: notification model (in-app + system), unified prompt input, autonomy-level settings per agent.

### Cline / Roo Code (VS Code extensions)

**Cline** = "human in the loop" by default — every file edit / command requires explicit approval inline. **Roo Code** forks Cline and adds **Modes** (Code, Architect, Ask, Debug, Custom). **Steal**: the Modes pattern — one prompt slot, several behaviors, surfaced as a per-session attribute. **Skip**: per-step approval — too friction-heavy for parallel-agent flow.

### Aider (terminal)

Gold standard for "no UI is the right UI" — pure terminal, slash commands for `/code`, `/ask`, `/architect` mid-session. Pairs with tmux for persistence. **Ceiling**: tops out at single-session use. fleet exists exactly to fill the multi-session gap. **Steal**: mode-switching via slash commands.

### Wave Terminal / Tabby / Zellij

Modern terminal multiplexers. **Zellij** has an always-visible keybinding hint bar at the bottom — directly inspires the bottom hint area in our UX. **Wave** has tile-based "many panes one window" — relevant for V2 multi-pane. **Tabby** has plugin-heavy Electron — we explicitly avoid that path.

### Claude Squad

[smtg-ai/claude-squad](https://github.com/smtg-ai/claude-squad) — tmux + worktrees + TUI, fleet's nearest open-source peer. Worth periodic feature parity checks.

## fleet's natural moats

Things fleet already has that competitors don't, that should be marketed:

1. **tmux pane attach** — preserves the real Claude TUI, sidesteps Conductor's #1 UX failure ("feels weird, loses Claude's joy").
2. **Pluggable workspace provider** — `GitWorktreeProvider` (built-in zero-config) + `ShellProvider` (custom commands via `.fleet.json`) — already breaks out of "worktree-only."
3. **Existing-directory friendly** — point at any local repo, no clone required.
4. **Open source.**
5. **Light** — Go binary, no Electron runtime; the Mac shell will be Swift native, not Electron.

## Top features to close the gap (V2+ priorities)

Ranked by leverage. Detail in `roadmap.md`.

1. Status-grouped sidebar (togglable view).
2. Suggested git actions menu (extend existing `p` for PR).
3. `@terminal` mention to attach pane output to next prompt.
4. Plan-mode / effort toggles in the chrome.
5. Mid-flight steering (send message to running agent without restart).
6. macOS notification fan-out (status flips to Waiting/Finished).
7. Cost / token analytics dashboard.
8. Modes pattern (Code / Ask / Architect / Debug) as session attribute.
