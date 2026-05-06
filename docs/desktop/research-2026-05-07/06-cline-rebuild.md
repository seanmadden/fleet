# Cline — the case for/against "rebuild the chat UI"

Research target: Cline (formerly `saoudrizwan/claude-dev`, now `cline/cline`), a VS Code extension that rebuilds the agent loop entirely in TypeScript. It is the canonical example of the "don't use the `claude` CLI, talk to the API directly" approach. Secondary: Roo Code (Cline fork) and Continue.dev — confirmed to use the same pattern.

Clone: `/tmp/fleet-research-clones/cline` (default branch).

---

## 1. Architecture — does Cline call `claude` CLI or the API directly?

**Direct API, not the CLI** — but with a twist.

- `src/core/api/providers/anthropic.ts:1` imports `@anthropic-ai/sdk` and calls `client.messages.create(...)` (line 148) with `stream: true`. This is the default / primary path.
- 45 providers live in `src/core/api/providers/` — anthropic, bedrock, vertex, openai, openrouter, gemini, ollama, lmstudio, deepseek, etc. Cline is **model-agnostic** by design.
- A dedicated `claude-code.ts` provider (`src/core/api/providers/claude-code.ts:32`) does shell out to `claude` — but only as an inference backend (so users with a Claude Pro/Max subscription can route through it). When this provider runs, it disables Claude Code's built-in tools by passing `--disallowedTools "Task,Bash,Glob,Grep,Read,Edit,Write,..."` (`src/integrations/claude-code/run.ts:163-188`) and uses `--max-turns 1` so Cline owns the agent loop, not Claude Code. Even with this provider, Cline reimplements every tool.

The agent loop (`src/core/task/index.ts`, 3,764 lines) is custom. Tool execution lives in `src/core/task/ToolExecutor.ts` (659 lines) plus 24 handlers in `src/core/task/tools/handlers/` (one file per tool).

**Tool-call protocol**: Cline pioneered XML tool calls in the system prompt (`<read_file><path>...</path></read_file>`) and parses them itself (`src/core/assistant-message/parse-assistant-message.ts:26`, custom streaming parser, ~240 lines). Native Anthropic tool-use is only used when the user enables "native tool calls" (`anthropic.ts:96` — `nativeToolsOn`).

---

## 2. Tools they reimplemented (all of them)

`src/core/prompts/system-prompt/tools/` — every tool below has its own spec file *and* its own handler in `src/core/task/tools/handlers/`. Cline ships:

| Cline tool | Equivalent in Claude Code | Notes |
|---|---|---|
| `read_file` | Read | Cline reads via VSCode workspace API, supports PDF/DOCX extraction (`extract-text.ts`) |
| `write_to_file` | Write | Opens VSCode diff editor (`DiffViewProvider`) so user reviews changes inline |
| `replace_in_file` | Edit | SEARCH/REPLACE block syntax; **notorious failure point** (see §9) |
| `apply_patch` | Edit (multi-hunk) | Newer addition for multi-file patches |
| `execute_command` | Bash | Uses VSCode's shell-integration API; "Proceed While Running" mode for long processes |
| `search_files` | Grep | Wraps `ripgrep` directly |
| `list_files` | Glob | Custom — also lists code definitions via tree-sitter |
| `list_code_definition_names` | (not in Claude Code) | tree-sitter-based symbol summary |
| `browser_action` | (not in Claude Code) | Headless Chromium + screenshot per step (Claude Computer Use) |
| `web_fetch` | WebFetch | Fetch + markdown convert |
| `web_search` | WebSearch | Native API or provider's tool |
| `use_mcp_tool` / `access_mcp_resource` | MCP | Custom MCP hub (§5) |
| `ask_followup_question` | AskUserQuestion | Inline chat ask |
| `attempt_completion` | (implicit) | Marks task done; runs final command via approval |
| `plan_mode_respond` | ExitPlanMode | Cline's plan/act mode pair |
| `new_task` | Task / new task | Spawns subtask |
| `subagent` (`use_subagents`) | Task (sub-agent) | Read-only research agents — gets prompt, returns summary (§7) |
| `use_skill` | Skill | Progressive-load skill loader |
| `focus_chain` | TodoWrite | Renders inline todo list |
| `condense` / `summarize_task` | (compaction) | Manual `/compact` and auto-condense |
| `load_mcp_documentation` | (not in CC) | Helps the model author MCP servers |
| `report_bug` | (not in CC) | Pre-fills GitHub issue |
| `generate_explanation` | (not in CC) | "explain changes" slash command |

**Bottom line**: Cline reimplemented every Claude Code tool plus a handful of extras. That's roughly **24 tool handlers + their prompt fragments + diff viewer + browser session manager + terminal abstraction + checkpoint git + MCP hub** — at least 8,000–10,000 LOC of pure tool plumbing before you even get to the chat UI.

---

## 3. Permission UX

Cline's permission flow is a **first-class chat-inline button row**, not a modal. The model emits a tool call, the partial XML streams in, Cline renders a "row" describing what it wants to do, and at the bottom of the chat ChatView shows two buttons: **Approve** and **Reject**.

- Button configs live in `webview-ui/src/components/chat/chat-view/shared/buttonConfig.ts:32-150`. Distinct configs per ask type:
  - `tool_approve` → "Approve" / "Reject"
  - `tool_save` → "Save" / "Reject" (file edits)
  - `command` → "Run Command" / "Reject"
  - `command_output` → "Proceed While Running" (no reject)
  - `browser_action_launch`, `use_mcp_server`, `use_subagents` → "Approve" / "Reject"
  - `followup` → no buttons (user must type a free-form answer)
- Backend approval: `src/core/task/tools/utils/ToolResultUtils.ts:125` `askApprovalAndPushFeedback()` awaits `config.callbacks.ask(...)` which suspends the task; the webview button click resumes it via `yesButtonClicked` / `noButtonClicked`. User can also type feedback alongside reject — that gets appended to `userMessageContent` and the model retries.

**Auto-approve menu** (`webview-ui/src/components/chat/auto-approve-menu/constants.ts`): a docked toolbar with 5 toggles — "Read project files", "Edit project files", "Execute safe commands", "Use the browser", "Use MCP servers" — plus sub-toggles ("read all files / outside workspace"). YOLO mode toggles them all.

**vs Claude Code**: Claude Code's terminal menu (`❯ 1. Yes  2. Yes don't ask again  3. No`) is a single keystroke and per-pattern memory. Cline's flow is mouse-first (though has keyboard nav), per-tool not per-pattern, and shows a rich diff panel for edits — much more reviewable. The cost: every approval requires renderer round-trip and a click; users reflexively flip on auto-approve, which then over-permits.

---

## 4. MCP

Cline ships a full MCP client. `src/services/mcp/McpHub.ts` (1,673 lines) embeds the official `@modelcontextprotocol/sdk` and supports **stdio**, **SSE**, and **streamable HTTP** transports plus OAuth (`McpOAuthManager.ts`). User-editable JSON config drives server lifecycle; a chokidar watcher hot-reloads on edit.

The MCP tools are exposed to the model via the `use_mcp_tool` and `access_mcp_resource` XML tools — Cline injects the server list + tool schemas into the system prompt at runtime, and the model picks one. Each call pops the standard "Approve / Reject" button row with a server-name + tool-name preview.

vs Claude Code: roughly equivalent feature set. Both run user-configured MCP servers; both prompt for permission; both inject tool descriptions into context. A meaningful difference: Cline supports OAuth flow inside the extension (`McpOAuthRedirectResolver.ts`); Claude Code currently relies on external auth helpers.

---

## 5. Hooks, plan mode, slash commands, mentions, checkpoints — what Cline built to keep up

Originally these were Claude-Code-only. Cline has steadily ported them in:

- **Hooks** (`docs/customization/hooks.mdx`, `src/core/hooks/`): 8 lifecycle events — `TaskStart`, `TaskResume`, `TaskCancel`, `TaskComplete`, `PreToolUse`, `PostToolUse`, `UserPromptSubmit`, `PreCompact`. Cline issue [#4658](https://github.com/cline/cline/issues/4658) explicitly states the goal is "compatibility — hooks written for Claude Code work with Cline with no modification."
- **Plan mode / Act mode** (`PlanModeRespondHandler.ts:14`): two-mode toggle. Plan mode is read-only; user reviews the plan; flips to act mode to execute. (Bug noted: issue #9017 — Cline sometimes edits files in plan mode.)
- **Slash commands** (`src/core/slash-commands/index.ts:52`): `/newtask`, `/smol`, `/compact`, `/newrule`, `/reportbug`, `/deep-planning`, `/explain-changes` plus user `.cline/workflows/*.md` files.
- **`@` mentions** (`src/shared/context-mentions.ts:52`, `src/core/mentions/index.ts`): `@/path/to/file`, `@workspace:name/path`, `@http://url`, `@<git-sha>`, `@problems` (VSCode diagnostics), `@terminal` (last terminal output), `@git-changes`. Strict regex, parsed before send, expanded into the user message.
- **Checkpoints** (`src/integrations/checkpoints/CheckpointTracker.ts`): a **shadow git repository** per workspace (uses `simple-git`), commits state after each tool. "Compare" + "Restore" buttons in the UI. This is something Claude Code does NOT have.
- **Compaction / auto-condense** (`src/core/context/context-management/ContextManager.ts:148`, `:234`): both manual `/compact` and automatic context-window-aware condensation.
- **Subagents** (`docs/features/subagents.mdx`, `src/core/task/tools/handlers/SubagentToolHandler.ts`): parallel **read-only** research agents with their own context. Limitations spelled out in docs: "cannot edit files, use the browser, access MCP servers, or spawn nested subagents." Compared to Claude Code's Task tool which can do everything.
- **Skills** (`docs/customization/skills.mdx`): progressive-loaded SKILL.md packs, mirrors Claude Code Skills. Experimental flag-gated.
- **Focus chain** (`src/core/task/focus-chain/`): inline TodoWrite-equivalent.

Most of this is *recent* — issues like #4658 were opened after Anthropic shipped each feature. Cline's velocity on parity is good, but every Claude Code release puts them in catch-up.

---

## 6. What Cline can't do that Claude Code can

- **Run headless / in CI / outside an editor**. Cline is a VSCode extension; the agent loop runs in the extension host. There is now a `cline-cli` (`/cli` directory in repo) but it ships separately and the standalone version is incomplete (multiple "CLI doesn't detect" issues like #9136). Claude Code is shell-native from day one.
- **Native subagent that writes files / runs MCP**. Cline's subagents are read-only by design.
- **Multi-user on same remote machine** — issue #807 shows the extension singleton breaks on shared SSH boxes ("Cannot register multiple views with same id claude-dev.SidebarProvider").
- **CLAUDE.md auto-discovery up the tree** — Cline uses `.clinerules/` and per-workspace settings; the recursive walk pattern Claude Code uses isn't a direct match.
- **Faster edit throughput**. Third-party benchmarks (Morph, vibecoding.app) cite Claude Code at ~3× more edits/minute on identical refactors — single-model focus + native tool-use vs Cline's XML round-trip.
- **Long startup**. Issue #6622: "Cline takes 5–15+ minutes to become ready after VS Code startup." Issue #5289: extension goes solid grey, requires VSCode restart. Claude Code starts in <1s.

---

## 7. What Cline can do that Claude Code can't (or does worse)

- **Diff-viewer-native edit review**. Every `write_to_file` / `replace_in_file` opens a side-by-side VSCode diff. User can edit Cline's edits in place and save. Far superior to terminal diff.
- **Browser-Use tool** (`browser_action`). Headless Chromium loop with screenshots after each click. Claude Code has no equivalent; you'd shell out via MCP.
- **Workspace checkpoints with Compare / Restore Workspace Only / Restore Task and Workspace** — true git-backed time machine of just-Cline's-changes. Claude Code has no native checkpoint system.
- **Model agnostic**. 45 providers including local (Ollama, LM Studio), Bedrock, Vertex, OpenRouter, plus xAI/DeepSeek/Mistral/Groq.
- **Cost meter per task**. Token + $ display per request and per task. Claude Code shows usage but not the inline-per-tool breakdown.
- **Rich chat UI**: streaming markdown via `react-markdown` + `rehype-highlight` + `remark-gfm` (`MarkdownBlock.tsx:5-10`), Mermaid rendering, inline images, copy buttons, quote-reply, syntax-highlighted code blocks, expand/collapse for long tool output, OS notifications.
- **`@` mention-driven context picking** — you can selectively pull `@problems` / `@terminal` / `@/file` instead of letting the model search.

---

## 8. Streaming UX

- The provider yields `{type: "text", text}` chunks (`anthropic.ts` ApiStream) which feed the parser in `parse-assistant-message.ts`. The parser emits partial `AssistantMessageContent` blocks (`partial: true`) as XML closes — the UI re-renders the in-progress block on each chunk.
- `webview-ui/src/components/common/MarkdownBlock.tsx` uses `marked.lexer` to split mid-stream markdown into stable blocks and memoizes each one to avoid re-render thrash. Code highlighting via `rehype-highlight`. Mermaid diagrams via `MermaidBlock.tsx`. Tables, GFM, autolinks supported.
- Tool calls render as **specialized rows** mid-stream (`ChatRow.tsx`, `CommandOutputRow.tsx`, `DiffEditRow.tsx`, `BrowserSessionRow.tsx`) — the user sees the file path / command appear *as the model types the XML*, then the diff fills in.
- Action buttons are in a fixed footer (`ActionButtons.tsx`) so they don't move when content streams.

This is the single biggest UX advantage over a terminal: rich, structured, scrollable, copy-pasteable streaming with proper diffs and screenshots.

---

## 9. Where Cline gets stuck (top user complaints)

- **Diff edit failures**. Many open issues: [#1195](https://github.com/cline/cline/issues/1195), [#1010](https://github.com/cline/cline/issues/1010), [#1511](https://github.com/cline/cline/issues/1511), [#2909](https://github.com/cline/cline/issues/2909), [#4067](https://github.com/cline/cline/issues/4067), [#4011](https://github.com/cline/cline/issues/4011), [#3513](https://github.com/cline/cline/issues/3513), [#8223](https://github.com/cline/cline/issues/8223). `replace_in_file` requires *byte-exact* SEARCH blocks; LF/CRLF, trailing-space, BOM and indentation drift all break it, and the model retries in a loop. Roo Code has the same bug class.
- **5–15 minute startup** on VSCode reload (#6622).
- **Extension freezes / greys out** until VSCode restart (#5289, #533).
- **Multi-user / Remote-SSH conflicts** (#807).
- **Tasks marked "complete" without verification** (#8354) and **multiple failed implementation attempts without clarification** (#8846) — these are model-behavior issues exacerbated by Cline's permissive task loop.
- **Plan-mode leaks edits** (#9017).
- **Local-model freezes** (#8044) — getting stuck on API request when LM Studio routing.
- **Cline CLI not detected by extension** on Windows (#9136).
- **Context-window blow-out before MCPs are even used** — five MCP servers can pre-load 100+ tool defs + 50K tokens (Quickutil benchmark).

---

## 10. Verdict for fleet — what rebuild costs you

If fleet were to abandon `claude` CLI and become a Cline-style direct-API client, this is the bill:

**You must reimplement (lots of code):**
1. The agent loop (3,764 LOC equivalent of `core/task/index.ts`).
2. ~20 tools with their handlers, prompt specs, and approval messages.
3. A streaming XML / native-tool-use parser.
4. A diff viewer integration (Cline gets this for free from VSCode; SwiftUI would need a custom diff component or wrap a webview).
5. Terminal abstraction + shell integration (Cline relies on VSCode's shell-integration API — fleet would have tmux already).
6. MCP hub with stdio/SSE/HTTP transports + OAuth.
7. Permission UI for every tool + auto-approve matrix.
8. Checkpoint shadow-git, file-context tracker, ignore controller.
9. Hooks runtime (8 lifecycle events).
10. `@`-mention parsing + workspace expansion.
11. Slash-command registry + workflow files.
12. Plan/Act mode toggle.
13. Subagents (own context windows + cost rollup).
14. Skills loader.
15. Markdown/syntax-highlight/Mermaid streaming renderer.
16. Token + cost meter per request and per task.

**You lose (no replacement available):**
1. **Anthropic-internal heuristics**: prompt-cache breakpoints, system-prompt compression, context compaction, retry/backoff, the system prompt itself. None of this is documented; you reverse-engineer from claude-code source dumps.
2. **Forward-compat with new Claude Code features**: every Claude Code release becomes a porting project (hooks took Cline ~6 months after Claude Code shipped them).
3. **`claude --resume <session_id>`** and Claude Code's session-state persistence — Cline keeps task history but it's its own format, not interoperable.
4. **Auto-update of Claude Code itself** — users get Anthropic's improvements for free; Cline ships every change manually.
5. **Native tool-use API parity**: when Anthropic ships a new tool (computer use v2, file API), Cline waits.

**You gain:**
1. Rich GUI: diffs, screenshots, inline cost, multi-provider, Mermaid, etc.
2. Model agnosticism (could route to GPT-5, Gemini 3, local Ollama).
3. Don't depend on `claude` CLI being installed / authenticated / up to date.
4. Full control over the loop — can ship features Anthropic won't (browser-use, checkpoints, subagent variants).
5. Ability to run truly headless / serverless without `claude`.

**Cost-benefit for fleet specifically**: fleet's value prop is *managing many parallel Claude Code sessions*, not authoring a chat UI. The Cline approach hands you a chat-UI win (fancy diff / streaming / cost) at the cost of ~30K LOC, ongoing parity-chasing, and giving up Anthropic's prompt-caching + tool-use sophistication. The smart move is: keep `claude` CLI as the engine, *rebuild only the transcript renderer* (so users get rich markdown/diff/syntax-highlighted output instead of raw terminal escape codes) by parsing tmux pane content or piping through Claude Code's stream-json output. That's the 80/20 — Cline's UX win without Cline's reimplementation tax.

---

## Sources

- Cline source (cloned): `github.com/cline/cline` (default branch as of 2026-05-07)
- Cline docs: https://docs.cline.bot
- Cline issues referenced: #807, #1010, #1195, #1511, #2909, #3513, #4011, #4067, #4384, #4658, #5289, #6622, #8044, #8223, #8354, #8846, #9017, #9136, #9174
- Roo Code (cloned): `github.com/RooCodeInc/Roo-Code`
- Continue (cloned): `github.com/continuedev/continue`
- Comparisons: respan.ai, morphllm.com, lowcode.agency, vibecoding.app, emergent.sh, selecthub.com (various 2026 "Claude Code vs Cline" articles)
- Quickutil "MCP Clients Compared" + Genai Unplugged "MCP Servers and Hooks in Claude Code"
