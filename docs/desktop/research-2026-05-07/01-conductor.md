# Conductor.build — Research Findings

Date: 2026-05-07
Researcher: fleet competitive analysis

## TL;DR

Conductor.build is the **dominant closed-source paid macOS competitor** for parallel Claude Code session management. It is **NOT** a terminal/PTY wrapper around the `claude` CLI. It is a **WebView-based desktop app (Tauri, with strong corroborating signals it could be Electron) that drives Claude Code through Anthropic's Claude Agent SDK (TypeScript) running under Bun**. The chat UI is a fully **rebuilt native composer** (CodeMirror-powered), not the Claude Code TUI. There is, however, a separate **integrated xterm.js terminal pane** for shell commands and a "Big Terminal" mode for running an agent in an actual terminal.

---

## 1. Company / Product Background

- **Company:** Melty Labs — Y Combinator S24 batch.
- **Founders:** Charlie Holtz (CEO, ex-Replicate growth, ex-Point72 quant dev), Jackson de Campos (ex-Netflix ML infra). Met at Brown playing Ultimate Frisbee. Previously built **Melty** (open-source AI code editor on GitHub: meltylabs/melty).
- **Funding:** $22 million Series A (announced March 30, 2026). Originally seed-funded.
- **Pricing:** **Free.** No subscription, no tiered pricing, no feature gates. (Bring your own Claude/Codex auth.) Source: Conductor homepage + Producthunt.
- **Platform:** macOS only (Apple Silicon required; Intel under development). Windows on a waitlist.
- **Current version (May 5, 2026):** 0.51.0
- **Customers featured:** Linear, Vercel, Notion, Ramp, Life360, Square, Reducto, Spotify.

> "We tried cloning our repo into three directories and running Claude in each of them, but it felt like driving a Subaru with a jet engine strapped on." — founders, via YC profile.

Sources:
- https://www.conductor.build/
- https://www.ycombinator.com/companies/conductor
- https://www.producthunt.com/products/conductor-aa77ddef-e6d3-4805-a179-7b2e17b6e22e

---

## 2. Rendering Approach — **REBUILT NATIVE CHAT UI** (high confidence)

### Primary evidence (direct claim, single-source)

George Taskos's Medium review (Mar 2026) states explicitly:

> "**built on the Claude Code SDK (the TypeScript wrapper, not the CLI directly) and Tauri for the desktop app.**"
> "The UI consists of a left sidebar with your workspaces (each named after a city), a middle chat interface same as Claude Code with @file tagging and slash commands, and a right panel with live file changes in a git diff view plus an integrated terminal."

Source: https://georgetaskos.medium.com/scaling-the-loop-run-5-claude-code-sessions-in-parallel-with-conductor-build-539b52888a81

### Corroborating evidence from official changelog (multiple data points)

From https://www.conductor.build/changelog and https://www.conductor.build/changelog/0.49.0-conductor-allegro-gpt-5-5:

- **0.48.0 (Apr 14, 2026):** "We've **removed our dependency on Node and now run the Claude Code executable via Bun**." — confirms they SPAWN the Claude Code executable as a child process; Conductor itself isn't running Claude inline.
- **0.49.0 (Apr 22, 2026):** "Rebuilt app for 50% faster performance; 150MB smaller bundle." — bundle reduction is consistent with a JS/web framework optimization (Tauri or Electron).
- **0.49.0:** "**Fixed a terminal rendering crash by using xterm's built-in DOM renderer**." — confirms **xterm.js** is used for the integrated terminal (not SwiftTerm, not native NSTerminal).
- **0.38.3 (Mar 10, 2026):** "**The code editor is now powered by CodeMirror, making it faster and lighter**." — confirms CodeMirror (web-based) editor, not native NSTextView.
- **0.44.0:** "Rebuilt Composer" with @-mentions, mention pills, file pills, ⌘Z support. UI primitives ("pills", "composer", "command palette") align with a web/React-style component model.
- **0.38.1–0.38.2:** "Rewrite the terminal from scratch for improved performance and reliability" — referring to the integrated shell terminal pane.

### What this means

Conductor has TWO surfaces:
1. **The chat panel** — a rebuilt native composer (web tech, CodeMirror, not a terminal). Streams agent output as structured messages, file pills, diff views.
2. **An integrated terminal panel** (xterm.js) — for running shell commands like `pnpm dev` inside the workspace. There's also a "Big Terminal" mode (⌘⇧T) where you can run a full terminal in the center panel and run any agent there directly.

> "you can now run a full terminal in the center panel with ⌘⇧T and run any agent you'd like" — changelog context.

So they offer the terminal as an *escape hatch*, but the main interaction is through the rebuilt chat UI.

### Tauri vs Electron — confidence assessment

- George Taskos says Tauri explicitly.
- Changelog mentions Bun + xterm.js + CodeMirror — all framework-agnostic web-tech indicators.
- The 150MB bundle drop COULD fit Electron or a Tauri rewrite, but a 150MB drop is more dramatic for an Electron app (full Chromium ~120MB).
- **Confidence: medium-high that it's Tauri.** Single primary source + circumstantial alignment with Tauri's WebView + Rust backend pattern. No leaked binary inspection done.

---

## 3. How Claude Code is Invoked

From official FAQ (https://www.conductor.build/docs/faq):

> "Conductor comes bundled with its own installation of Claude Code and Codex" — located at `~/Library/Application Support/com.conductor.app/bin`.

> "By default, Conductor uses the auth tokens already saved on your machine. If you're logged into Claude Code with an API key, Conductor uses that; if you're logged in with Claude Pro or Max, Conductor uses that."

Combined with the changelog Bun reference:

> "We've removed our dependency on Node and now run the Claude Code executable via Bun." (0.48.0)

**Process model (inferred):** Conductor spawns the bundled Claude Code executable as a child process under Bun, then communicates with it via the **Claude Agent SDK** (TypeScript) — most likely using `stream-json` input/output mode where messages are exchanged as structured JSON over stdio rather than via the interactive TUI. The SDK's `query()` async iterator is exactly what their composer would consume.

This is consistent with their MCP integration:
> "If a repository has an .mcp.json file at its project root, Conductor agents in that workspace inherit those MCP servers." — Conductor docs (`/docs/core/mcp`).
And experimental agent teams:
> "In Settings → Env, add `CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1` to enable Claude Code's experimental agent teams feature." — FAQ.

---

## 4. Handling of Claude Code Interactive Features

Because Conductor uses the SDK (not the TUI), each interactive feature is **reimplemented** in their UI:

| Feature                 | Conductor's approach |
|-------------------------|----------------------|
| Permission prompts (allow/deny) | Native UI dialog. Settings → tool approvals. "0.45.0: Claude Code tool approvals in Settings." "0.41.0: Tool approval as enterprise setting." |
| Plan mode               | Native toggle. "0.43.0: Codex skills, plan mode, fast mode available." "0.41.0: Codex plan mode beta; `/plan` command toggles." |
| Slash commands          | Native dropdown. Loads `~/.claude/commands/` markdown files. "0.31.3: Slash commands load independently; keyboard navigation." Native commands like `/clear`, `/compact`, `/agents` are NOT documented in the Conductor docs — only user-defined ones are. (Possibly handled by SDK passthrough.) |
| MCP servers             | Reads `.mcp.json` from repo root, passes to agents. "0.50.0: Show MCP status in chat (experimental)." `/mcp-status` dialog (0.48.0). |
| Sub-agents / agent teams| Behind feature flag (`CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1`). |
| Hooks                   | **No mention of Claude Code hooks support anywhere in changelog or docs.** |
| @file mentions          | Native composer with searchable file pills. |
| Image paste             | Native attachment via `+` icon; "0.25.6: Auto-resize images >8k pixels." "0.29.3: Non-image files pasteable in Composer." |
| Spinner / loading state | Custom UI — and a known regression: HN feedback noted "intermediary output being suppressed" removes the sense of Claude actively working. |
| Sandbox approvals       | None — "Agents run directly on your system without sandboxing." (FAQ). Permission requests appear as macOS TCC prompts only. |
| Custom system prompts   | "Conductor injects system prompts that explain to the agent that it's running inside Conductor." (FAQ) |

---

## 5. Worktree / Git / PR Integration — **Their Strongest Feature**

- Each workspace = its own git worktree, auto-named after a city (Raleigh, Yokohama, etc.).
- > "each Conductor workspace is a new git worktree" — homepage.
- > "Conductor spins up a git worktree, runs your setup script, and auto-names the branch. You're looking at a fresh isolated checkout in about 10 seconds."
- **Diff Viewer** opens with ⌘⇧D. Inline comments become composer attachments sent back to the agent.
- Create PR: ⌘⇧P. Agent can open PRs, read diffs, comment, and respond to review comments.
- **Linear integration:** pull a ticket into a workspace as the task description.
- `/resolve-merge-conflicts` ships as a default slash command.
- Checkpoints for rollback — "automatic snapshots that let you roll back."
- Shared `.context` directory for non-committed notes (0.28.2).
- Migration: from nested `.conductor` directories to `~/conductor/workspaces/`.

---

## 6. Sessions / Persistence

- > "Conductor restores the chat history and workspace state it has saved."
- Workspaces are archivable; archived workspaces in History tab.
- Data stored at `~/Library/Application Support/com.conductor.app` and `~/conductor`.
- **No mention** of relying on `~/.claude/projects/*.jsonl` — they likely persist their own chat history (since they own the SDK message stream), but I could not find verbatim confirmation either way.
- Big Terminal mode preserves session history on restart.

---

## 7. Pain Points / User Complaints (From HN, Reddit, Reviews)

### From the Show HN (https://news.ycombinator.com/item?id=44594584)
- **GitHub permissions controversy:** "Full read-write access required to all your Github account's repos. Not just code. Settings, deploy keys. The works." Cause: GitHub OAuth doesn't support fine-grained permissions. **Fixed** with GitHub App + local `gh` CLI fallback.
- **Forces re-clone:** "I wanted a simple git worktree manager for my existing, already-checked-out repository. Instead, it requests Github permissions and clones the repo from Github." Workaround: setup script.
- **UI feel:** "I just don't feel as joyful using it" vs. the native Claude Code TUI. Specifically: "intermediary output being suppressed" and missing Esc-to-interrupt patterns.
- Needs reinstalling dependencies per worktree (the universal worktree problem; not Conductor-specific).

### From the docs (https://www.conductor.build/docs/troubleshooting/issues)
> "**The library we use to do @-mentions breaks the undo history.**"
> "**We're streaming output from your shell along to the UI, and at some point along the way, the stream is probably getting corrupted.**"

### From later HN (https://news.ycombinator.com/item?id=46029331)
- scottmf: "it would be nice to be able to just have a session working on the main branch as concurrent work in worktrees can get messy."
- scottmf: "Codex support is a recent addition however, and **it's not clear if MCP servers and other rules apply to Codex.**"

### From Julian Astrada blog
- **Cost:** "running multiple agents in parallel means multiple API calls."
- **Context doesn't persist between sessions** (per New Stack hands-on review).
- Untracked files like `.env` and `node_modules` aren't in new worktrees.

### Twitter / general sentiment
- Very positive: "fully converted to @conductor_build — feels like going from typing with two fingers to having eight arms."
- "First AI coding tool I've used that ACTUALLY feels like the future of software development."
- One user cancelled Cursor because of it.

---

## 8. Pricing & Business Model

> "Right now we don't [make money]. We're a small team running on seed funding" — FAQ. (Pre-Series A statement; still free as of May 2026.)
- Plans for future team collaboration features as monetization.
- No subscription, no API key markup, BYO-keys.

---

## 9. Versions / Bundled Dependencies (May 2026)

- Conductor 0.51.0 ships Claude Agent SDK 0.128.0, Codex 0.125.0.
- Bundled Claude Code at `~/Library/Application Support/com.conductor.app/bin`.
- Supports Opus 4.6/4.7 (1M context), Sonnet 4.6, GPT-5.2 through 5.5.

---

## 10. Implications for fleet's SwiftTerm + tmux Choice

### Validation
1. **The market chose this differentiator.** Conductor is the dominant tool in this space and chose to **rebuild the chat UI** rather than wrap the TUI. This is a real strategic axis — but it locks them out of certain Claude Code TUI features (hooks, every native slash command, the actual Claude rendering).
2. **They use xterm.js for the integrated terminal** — confirming that even the "rebuilt UI" camp keeps a real terminal escape hatch. fleet's SwiftTerm widget is on the same axis as their xterm.js-but-as-the-main-surface decision.
3. **Their pain points map directly to "we rebuilt the UI" tradeoffs:** suppressed intermediary output (UI doesn't reflect what Claude is actually doing), undo history broken by their @-mention library, shell stream corruption (their custom terminal renderer has bugs that real terminals never have), no hooks support, ambiguity about MCP/feature passthrough. **fleet attaching to the real `claude` TUI inside tmux skips all these classes of bugs by construction.**

### Challenge
1. **Worktree integration is their killer feature.** Linear ticket → workspace → PR is a smooth pipeline. fleet's worktree story (built-in `.fleet.json`, copy `.claude/settings.local.json`, phantom pending entries) is comparable but Conductor's PR/diff/inline-review-loop is more polished.
2. **Conductor's native composer wins on @file mentions, image paste, native ⌘ shortcuts, mention pills, file diff inline.** SwiftTerm + tmux + real Claude TUI inherits whatever Claude Code's TUI offers — which is constrained to terminal capabilities (no inline diff panels in the chat surface; users have to leave the terminal for git diff).
3. **They have $22M and 6 people; we have a $0 OSS TUI.** Different ballpark of polish/distribution. But the open-source angle is a real differentiator they cannot match.
4. **They support Codex too.** fleet is Claude-only.
5. **Their architecture is faster to iterate on chat UX** (web tech) but can never offer 100% fidelity to whatever Claude Code's TUI does. fleet's TUI-attach approach gets every Claude Code feature **for free**, **forever**, including hooks, sub-agent UI, plan mode toggling animations, the spinner, the `/agents` flow — Conductor has to chase every one.

### Concrete biggest takeaway
> The single biggest validation: **Conductor has explicitly NO support for Claude Code hooks documented anywhere**, and ships several known bugs (stream corruption, undo breakage, "feels different from native") that are direct consequences of rebuilding the chat surface. fleet's SwiftTerm-attaches-to-real-claude-in-tmux model **eliminates all of these classes of bugs by definition** and is also the only V1 path that lets us ride every future Claude Code feature without product work.
>
> The single biggest challenge: **Their worktree → PR → inline-review-comment loop is genuinely best-in-class** and rebuilding it on top of a TUI-rendered chat is awkward. fleet should expect to invest heavily in side panels (diff viewer, PR comments) that complement (not compete with) the embedded terminal's chat surface.

---

## Source Index

| URL | Used for |
|-----|----------|
| https://www.conductor.build/ | Product overview, customer logos, Series A |
| https://www.conductor.build/docs | Doc TOC |
| https://www.conductor.build/docs/faq | Bundled Claude Code path, auth, agent teams flag, sandbox |
| https://www.conductor.build/docs/troubleshooting/issues | Known issues — undo history, stream corruption |
| https://www.conductor.build/docs/concepts/agent-modes | Plan mode, fast mode |
| https://www.conductor.build/docs/concepts/workspaces-and-branches | Workspace model |
| https://www.conductor.build/docs/concepts/workflow | ⌘⇧N, ⌘⇧D, ⌘⇧P shortcuts |
| https://www.conductor.build/docs/core/slash-commands | `~/.claude/commands/` markdown |
| https://www.conductor.build/docs/core/mcp | `.mcp.json` inheritance |
| https://www.conductor.build/changelog | Bun, xterm.js, CodeMirror, terminal rewrite, version history |
| https://www.conductor.build/changelog/0.49.0-conductor-allegro-gpt-5-5 | xterm DOM renderer, 150MB bundle drop |
| https://www.conductor.build/changelog/0.44.0-new-sidebar-rebuilt-composer-codex-checkpoints | Composer rebuild |
| https://georgetaskos.medium.com/scaling-the-loop-run-5-claude-code-sessions-in-parallel-with-conductor-build-539b52888a81 | **Tauri + Claude Code SDK TS wrapper** primary citation |
| https://thenewstack.io/a-hands-on-review-of-conductor-an-ai-parallel-runner-app/ | Hands-on review (paywalled excerpt) |
| https://julianastrada.com/blog/conductor-parallel-agents | Worktree per workspace, terminal in each workspace, cost concern |
| https://madewithlove.com/blog/conductor-running-multiple-ai-coding-agents-in-parallel/ | Checkpoints, spotlight testing, multi-model mode, "context doesn't persist" |
| https://biggo.com/news/202507210115_Conductor_App_GitHub_Permissions_Controversy | OAuth permissions controversy + fix |
| https://news.ycombinator.com/item?id=44594584 | Show HN launch thread (Aug 2025) |
| https://news.ycombinator.com/item?id=46029331 | "95% of my workflow" + MCP/Codex ambiguity |
| https://news.ycombinator.com/item?id=45520043 | "they really pioneered git worktrees" |
| https://www.ycombinator.com/companies/conductor | YC profile, founders, Series A |
| https://www.producthunt.com/products/conductor-aa77ddef-e6d3-4805-a179-7b2e17b6e22e | Free pricing, founder quote, testimonials |
