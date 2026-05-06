# Claude Code: Machine-Readable Surfaces (Research for fleet)

Date: 2026-05-07
Scope: every non-TUI surface `claude` exposes — what fleet (Mac SwiftUI app wrapping Claude in tmux) could integrate against without abandoning terminal-attach.
Local install probed: `/Users/yuvalhayke/.local/bin/claude` v2.1.131.

---

## TL;DR

Claude Code is now positioned as **two things**:
1. **A binary** (`claude`) that runs an interactive TUI by default, but with `-p/--print` becomes a fully programmable agent with stream-json I/O.
2. **The Agent SDK** (`@anthropic-ai/claude-agent-sdk` / `claude-agent-sdk` Python — renamed from "Claude Code SDK"), which is a library wrapping that same binary. The TS SDK *bundles a native Claude binary*, so installing the SDK installs Claude Code.

Everything fleet's TUI cares about — sessions, hooks, transcripts, tool use, permissions, MCP, sub-agents, slash commands — is exposed as structured data through one of: stream-json events, JSONL transcripts, settings.json, or the SDK's TS/Python types. None of it requires screen-scraping a terminal.

---

## 1. Headless / Print Mode (`claude -p`)

### Invocation
```bash
claude -p "query" --output-format stream-json --input-format stream-json --include-partial-messages --include-hook-events --verbose
```

The doc note explicitly says: **"The CLI was previously called 'headless mode.' The `-p` flag and all CLI options work the same way."** There is no separate "headless" binary — `-p` is just the flag.

### Output formats
- `text` (default)
- `json` — single result object: `{ result, session_id, usage, total_cost_usd, modelUsage, ... }`
- `stream-json` — newline-delimited JSON (NDJSON), one event per line

### Stream-JSON event types

The stream is a 1:1 superset of the Claude Messages API streaming events plus session/tool/permission wrapper messages. Tool calls **are** visible. Tool results **are** visible.

Event categories (gathered from `streaming-output` doc + transcript inspection):

| Top-level `type` | Subtypes / shape | Purpose |
|---|---|---|
| `system` | `init`, `bridge_status`, `compact_boundary`, `stop_hook_summary`, `turn_duration`, `api_retry`, `plugin_install` | Session lifecycle metadata |
| `assistant` | n/a | Wraps a `BetaMessage` from the Anthropic SDK with `content[]` blocks of type `text`, `thinking`, `tool_use` |
| `user` | n/a | Wraps a `MessageParam` — text or `tool_result` blocks |
| `stream_event` | passthrough of API `message_start`, `content_block_start`, `content_block_delta` (with `text_delta` or `input_json_delta`), `content_block_stop`, `message_delta`, `message_stop` | Token-level streaming when `--include-partial-messages` is set |
| `result` | `success`, `error_max_turns`, `error_during_execution`, `error_max_budget_usd`, `error_max_structured_output_retries` | Final terminal event with `result`, `total_cost_usd`, `usage`, `modelUsage`, `permission_denials`, `num_turns`, `duration_ms`, `structured_output` |
| Hook lifecycle events | when `--include-hook-events` is set | every PreToolUse/PostToolUse/etc fires here as a stream record |

### `system/init` shape (from SDK reference)
```ts
{
  type: "system", subtype: "init",
  uuid, session_id, claude_code_version, cwd,
  model, permissionMode, apiKeySource,
  tools: string[],                    // "Read", "Edit", "Bash", "Agent", "WebFetch"...
  mcp_servers: { name, status }[],    // every connected MCP server, with state
  slash_commands: string[],           // every /command (including bundled skills)
  output_style, skills: string[], plugins: { name, path }[],
  agents?: string[], betas?: string[]
}
```
**This is gold for fleet** — read it once and you know every tool, MCP, skill, plugin, and command available in the session, machine-readable.

### Permissions in headless
- `--permission-mode <default|acceptEdits|plan|auto|dontAsk|bypassPermissions>` — required upfront because there's no human at the terminal.
- `--permission-prompt-tool <mcp_tool_name>` — delegate permission decisions to an MCP tool (this is the docked-in escape hatch for desktop apps).
- `--allowedTools "Read,Bash(git diff *)"` — pre-approve via permission-rule syntax.
- `--dangerously-skip-permissions` aka `--permission-mode bypassPermissions`.
- Without delegation, headless mode does **not** block on prompts; it fails or denies according to the active mode. `dontAsk` is the explicit "deny anything not pre-approved" mode.

### Stream-JSON **input** format (`--input-format stream-json`)
Bidirectional control channel. You write `SDKUserMessage` objects (`{ type: "user", message: {...}, parent_tool_use_id, shouldQuery }`) on stdin. With `--replay-user-messages` they're echoed back. Combined with `--permission-prompt-tool` this is how Anthropic's own Mac app (and Conductor, Crystal, etc.) interleave permission Q&A.

### Other automation flags worth knowing
`--max-turns N` · `--max-budget-usd 5.00` · `--no-session-persistence` · `--session-id <uuid>` · `--fork-session` · `--json-schema '...'` (structured output) · `--bare` (skip all auto-discovery — recommended for SDK/CI calls; will become default for `-p`) · `--init-only` (run Setup+SessionStart hooks then exit) · `--mcp-config` · `--strict-mcp-config` · `--setting-sources user,project,local` · `--system-prompt[-file]`, `--append-system-prompt[-file]` · `--exclude-dynamic-system-prompt-sections` (better cache reuse for shared workloads) · `--agents '<json>'` (define subagents inline) · `--agent <name>` · `--effort low|medium|high|xhigh|max` · `--fallback-model` · `--include-partial-messages` · `--include-hook-events` · `--replay-user-messages`.

---

## 2. Claude **Agent** SDK (renamed from Claude Code SDK)

> "The Claude Code SDK has been renamed to the Claude Agent SDK." — [docs](https://code.claude.com/docs/en/agent-sdk/overview)

### Packages
- TypeScript: `npm install @anthropic-ai/claude-agent-sdk` (bundles native binary; v0.2.111+ for Opus 4.7).
- Python: `pip install claude-agent-sdk`.
- The legacy npm name `@anthropic-ai/claude-code` is still around (it ships the binary itself); `@anthropic-ai/claude-agent-sdk` is the programmable wrapper.

### TS API (verbatim from doc)
```ts
function query({ prompt, options }: {
  prompt: string | AsyncIterable<SDKUserMessage>;
  options?: Options;
}): Query;  // AsyncGenerator<SDKMessage, void> + control methods
```

**`Options` selected fields:**
`abortController`, `additionalDirectories`, `agents` (Record<string, AgentDefinition>), `allowedTools`, `disallowedTools`, `canUseTool`, `cwd`, `env`, `executable: 'bun'|'deno'|'node'`, `forkSession`, `hooks` (per-event callback arrays), `includePartialMessages`, `maxBudgetUsd`, `maxTurns`, `mcpServers`, `model`, `outputFormat: { type:'json_schema', schema }`, `permissionMode`, `permissionPromptToolName`, `plugins`, `resume`, `sessionId`, `settingSources: ('user'|'project'|'local')[]`, `systemPrompt: string | { type:'preset', preset:'claude_code', append?, excludeDynamicSections? }`, `thinking`, `tools`.

**`Query` control methods (huge for desktop UIs):**
```ts
interface Query extends AsyncGenerator<SDKMessage, void> {
  interrupt(): Promise<void>;
  rewindFiles(userMessageId, options?): Promise<RewindFilesResult>;
  setPermissionMode(mode): Promise<void>;
  setModel(model?): Promise<void>;
  setMaxThinkingTokens(n | null): Promise<void>;
  initializationResult(): Promise<SDKControlInitializeResponse>;
  supportedCommands(): Promise<SlashCommand[]>;
  supportedModels(): Promise<ModelInfo[]>;
  supportedAgents(): Promise<AgentInfo[]>;
  mcpServerStatus(): Promise<McpServerStatus[]>;
  accountInfo(): Promise<AccountInfo>;
  reconnectMcpServer(name): Promise<void>;
  toggleMcpServer(name, enabled): Promise<void>;
  setMcpServers(servers): Promise<McpSetServersResult>;
  streamInput(stream): Promise<void>;
  stopTask(taskId): Promise<void>;
  close(): void;
}
```

### `canUseTool` callback (the permission delegation surface)
```ts
type CanUseTool = (
  toolName: string,
  input: Record<string, unknown>,
  options: { signal, suggestions?, blockedPath?, decisionReason?, toolUseID, agentID? }
) => Promise<
  | { behavior: "allow"; updatedInput?, updatedPermissions?, toolUseID? }
  | { behavior: "deny";  message; interrupt?; toolUseID? }
>;
```
Permission evaluation order (per docs): **Hooks → Deny rules → Permission mode → Allow rules → canUseTool**. `canUseTool` is the only step that requires a live human-in-the-loop callback.

### In-process MCP (no subprocess)
```ts
import { tool, createSdkMcpServer } from "@anthropic-ai/claude-agent-sdk";

const myServer = createSdkMcpServer({
  name: "my-tools", version: "1.0.0",
  tools: [
    tool("greet", "Greet the user", { name: z.string() },
      async ({ name }) => ({ content: [{ type:"text", text:`hi ${name}` }] }))
  ]
});
// then pass it via options.mcpServers
```
Means a desktop app can register Swift/Go-callable tools without spawning a separate MCP process. (TS-only today; Python equivalent in roadmap.)

### `AgentDefinition` (subagents declaratively)
```ts
{ description, prompt, tools?, disallowedTools?, model?, mcpServers?, skills?,
  initialPrompt?, maxTurns?, background?, memory?, effort?, permissionMode? }
```

### `HookCallback` and `HookEvent`
```ts
type HookCallback = (input, toolUseID, { signal }) => Promise<HookJSONOutput>;
type HookEvent = "PreToolUse"|"PostToolUse"|"PostToolUseFailure"|"PostToolBatch"|
                 "Notification"|"UserPromptSubmit"|"SessionStart"|"SessionEnd"|
                 "Stop"|"SubagentStart"|"SubagentStop"|"PreCompact"|
                 "PermissionRequest"|"Setup"|"TeammateIdle"|"TaskCompleted"|
                 "ConfigChange"|"WorktreeCreate"|"WorktreeRemove";
```

### `SDKResultMessage.usage` carries
`input_tokens`, `output_tokens`, `cache_creation_input_tokens`, `cache_read_input_tokens`, plus per-model breakdown via `modelUsage[name]`. `total_cost_usd` is computed.

---

## 3. Session transcripts: `~/.claude/projects/<slug>/<sessionUUID>.jsonl`

Path pattern: `~/.claude/projects/-Users-yuvalhayke-code-brizz-code-fleet-ui/78c22a39-8fbd-44d1-9a32-fddbb1c8edb0.jsonl`. Slugged absolute cwd; one file per session UUID; sister directory of same name holds `tool-results/` overflow blobs.

### Record types observed (live inspection of fleet's own sessions)
Every line is a JSON object. Top-level `type` values seen:

| `type` (subtype) | Selected keys |
|---|---|
| `last-prompt` | `lastPrompt`, `leafUuid`, `sessionId` |
| `permission-mode` | `permissionMode`, `sessionId` |
| `ai-title` | `aiTitle`, `sessionId` (auto-generated session title) |
| `agent-name` | `agentName`, `sessionId` |
| `system/bridge_status` | `subtype`, `content`, `url` (claude.ai web bridge), `cwd`, `gitBranch`, `version`, `entrypoint`, `parentUuid`, `uuid`, `timestamp` |
| `system/compact_boundary` | `compactMetadata`, `level`, `logicalParentUuid`, plus standard envelope |
| `system/stop_hook_summary` | `hookCount`, `hookErrors`, `hookInfos`, `preventedContinuation`, `stopReason`, `toolUseID` |
| `system/turn_duration` | `durationMs`, `messageCount` |
| `file-history-snapshot` | `messageId`, `snapshot.trackedFileBackups`, `isSnapshotUpdate` (powers `/rewind`) |
| `user` | `message: { role, content }`, `parentUuid`, `promptId`, `sessionId`, `permissionMode`, `cwd`, `gitBranch`, `version`, `userType: "external"`, `entrypoint: "cli"`, `isSidechain`, `isCompactSummary?`, `isVisibleInTranscriptOnly?` |
| `attachment` | `attachment{}` plus envelope (pasted images, files) |
| `assistant` | `message: BetaMessage`, `requestId`, plus envelope |
| `queue-operation` | `operation`, `content`, `sessionId` |

### Assistant message content blocks
`message.content[]` blocks of type `text`, `thinking`, `tool_use`. **`tool_use` block keys: `["caller","id","input","name","type"]`** — `caller` is fleet-relevant: tracks which subagent issued the call.

### User message tool results
When the assistant called a tool, the next `user` line contains `message.content[]` with `tool_result` blocks: **`["content","is_error","tool_use_id","type"]`**. The same line carries a sidecar **`toolUseResult`** field with rich provider data, varying by tool:
- Bash: `{ interrupted, isImage, noOutputExpected, stderr, stdout }`
- Task/Agent: `{ agentId, taskId, task, status, statusChange, success, prompt, isAsync, total_deferred_tools, updatedFields }`
- File ops: `{ file, type, description }`
- Search: `{ matches, query }`
- Long-output redirect: `{ canReadOutputFile, outputFile }`

### Usage fields per assistant turn
`message.usage`: `input_tokens`, `output_tokens`, `cache_creation_input_tokens`, `cache_read_input_tokens`, `cache_creation`, `inference_geo`, `iterations`, `service_tier`, `speed`, `server_tool_use`.

### Session ID storage
The transcript filename is the session UUID. `claude --resume <uuid>` (or `--resume <name>`) replays from this JSONL. `--fork-session` writes a new UUID; `--session-id <uuid>` lets the caller pick the UUID upfront. fleet already uses this exact mechanism today.

### Implication for fleet
You can sidecar-tail this JSONL **right now**, parsing assistant blocks, tool calls, tool results, usage, and session metadata, without touching the TUI. It's structured, append-only, durable across restarts. Today fleet only reads `claude_session_id` for resume; the same file holds everything you'd need to render a chat-style preview, cost summary, or tool-call timeline.

---

## 4. Hooks (`~/.claude/settings.json`)

### Lifecycle events (per `code.claude.com/docs/en/hooks` + SDK `HookEvent` union)
**Once per session**: `SessionStart`, `SessionEnd`, `Setup`
**Once per turn**: `UserPromptSubmit`, `Stop`, `StopFailure`
**Per tool call**: `PreToolUse`, `PostToolUse`, `PostToolUseFailure`, `PermissionRequest`, `PermissionDenied`, `PostToolBatch`
**Other**: `UserPromptExpansion`, `SubagentStart`, `SubagentStop`, `TaskCreated`, `TaskCompleted`, `TeammateIdle`, `Notification`, `ConfigChange`, `CwdChanged`, `FileChanged`, `InstructionsLoaded`, `PreCompact`, `PostCompact`, `Elicitation`, `ElicitationResult`, `WorktreeCreate`, `WorktreeRemove`.

### Common input fields
```json
{ "session_id":"...", "transcript_path":"/path/to/transcript.jsonl",
  "cwd":"/...", "permission_mode":"default", "hook_event_name":"PreToolUse" }
```
Plus event-specific fields: `prompt` (UserPromptSubmit), `tool_name`+`tool_input` (PreToolUse), `tool_output`+`tool_use_id` (PostToolUse), `notification_type`, `agent_type`+`agent_id` (Subagent*), `source` (SessionStart: startup|resume|clear|compact), `cwd` (CwdChanged), `file_path`+`file_change_type` (FileChanged).

### Hook handler types
- `command` — stdin gets JSON, exit 0=allow, exit 2=block (stderr surfaced), other=non-blocking error
- `http` — POST JSON to URL with header substitution from `allowedEnvVars`
- `mcp_tool` — call an MCP server tool with `${tool_input.x}` substitutions
- `prompt` — single-turn LLM evaluation (yes/no decision)
- `agent` — spawn a subagent (experimental)

### Output JSON
```json
{
  "continue": true,
  "stopReason": "...",
  "suppressOutput": false,
  "decision": "block",
  "reason": "...",
  "hookSpecificOutput": {
    "hookEventName": "PreToolUse",
    "permissionDecision": "allow|deny|ask|defer",
    "permissionDecisionReason": "...",
    "updatedInput": {},
    "additionalContext": "extra text appended to Claude's context"
  }
}
```
Crucially, hooks can **rewrite `tool_input`**, **inject `additionalContext`** into Claude's view, and **gate every tool call**. fleet's existing status hooks barely use this surface.

### Settings precedence
`~/.claude/settings.json` (user) → `.claude/settings.json` (project, sharable) → `.claude/settings.local.json` (project, gitignored). Session-level overrides via `--settings <file-or-json>` and `--setting-sources user,project,local`.

---

## 5. MCP

### Configuration
- Per-user: managed by `claude mcp` subcommands; persists to `~/.claude.json` / settings.
- Per-project (sharable): `.mcp.json` at repo root.
- Per-session: `--mcp-config <path-or-json>` (repeatable, space-separated), `--strict-mcp-config` to ignore everything else.
- In-process (TS SDK only): `createSdkMcpServer({ name, version, tools: [tool(...)] })`.

### Server transports: stdio, HTTP, SSE.

### Discovery
- Inside a session: `/mcp` slash command lists/manages servers; `mcp_servers` field on `system/init` exposes them programmatically.
- SDK `Query.mcpServerStatus()` → array; `Query.toggleMcpServer(name, enabled)` and `Query.reconnectMcpServer(name)` give live control.

### MCP prompts as commands
Servers can expose prompts that surface as `/mcp__<server>__<promptName>` slash commands, dynamically discovered.

---

## 6. Slash commands

Source of truth: `code.claude.com/docs/en/commands`. Two flavors:

- **Built-in commands** (coded into the CLI): `/add-dir`, `/agents`, `/autofix-pr`, `/branch`/`/fork`, `/btw`, `/chrome`, `/clear`/`/reset`/`/new`, `/color`, `/compact`, `/config`/`/settings`, `/context`, `/copy`, `/cost`, `/desktop`/`/app`, `/diff`, `/doctor`, `/effort`, `/exit`/`/quit`, `/export`, `/extra-usage`, `/fast`, `/feedback`/`/bug`, `/focus`, `/heapdump`, `/help`, `/hooks`, `/ide`, `/init`, `/insights`, `/install-github-app`, `/install-slack-app`, `/keybindings`, `/login`, `/logout`, `/mcp`, `/memory`, `/mobile`/`/ios`/`/android`, `/model`, `/passes`, `/permissions`/`/allowed-tools`, `/plan`, `/plugin`, `/powerup`, `/privacy-settings`, `/recap`, `/release-notes`, `/reload-plugins`, `/remote-control`/`/rc`, `/remote-env`, `/rename`, `/resume`/`/continue`, `/review`, `/rewind`/`/checkpoint`/`/undo`, `/sandbox`, `/schedule`/`/routines`, `/security-review`, `/setup-bedrock`, `/setup-vertex`, `/skills`, `/stats`, `/status`, `/statusline`, `/stickers`, `/tasks`/`/bashes`, `/team-onboarding`, `/teleport`/`/tp`, `/terminal-setup`, `/theme`, `/tui`, `/ultraplan`, `/ultrareview`, `/upgrade`, `/usage`, `/voice`, `/web-setup`.
- **Bundled skills** (prompt-driven, model can also auto-invoke): `/batch`, `/claude-api`, `/debug`, `/fewer-permission-prompts`, `/loop`/`/proactive`, `/simplify`. Plus user/project/plugin skills under `.claude/skills/<name>/SKILL.md` and old `.claude/commands/*.md` files (still supported).

**Mode-specific:** `/plan` enters plan mode; `/effort` and `/model` are session-wide; `/sandbox` only on supported platforms; `/desktop` only macOS/Windows; `/upgrade` only Pro/Max plans. **In `-p` mode, slash commands are not invocable** ("describe the task instead"). A subset (`/init`, `/review`, `/security-review`) is exposed via the SDK Skill tool.

### Programmatic enumeration
- `system/init`'s `slash_commands: string[]` lists every command in the active session.
- SDK: `Query.supportedCommands(): Promise<SlashCommand[]>`.

---

## 7. `claude --resume <session_id>`

- Argument is either the **session UUID** (filename of the JSONL transcript) or a **session name** set via `-n` / `/rename`.
- Resumes from the last `parentUuid` chain head in the JSONL. Replays the full transcript into Claude's context.
- `--fork-session` creates a new UUID instead of overwriting the original (lets you branch).
- `--continue` (`-c`) is the no-arg variant — most recent conversation in cwd.
- `--from-pr <num|url>` resumes a session linked to a PR (auto-linked when Claude created the PR).
- Programmatic equivalent: `query({ options: { resume: sessionId } })`. `system/init` reports the resumed `session_id`.
- fleet already does this exact dance via hook-captured session IDs.

---

## 8. Full CLI flag list (verbatim from `claude --help` v2.1.131 + cli-reference)

**Subcommands:** `agents`, `auth login|logout|status`, `auto-mode defaults|config`, `doctor`, `install [stable|latest|<version>]`, `mcp`, `plugin|plugins`, `project purge`, `remote-control`, `setup-token`, `ultrareview`, `update|upgrade`.

**Top-level flags** (full list — `claude --help` does not show every flag, see cli-reference): `--add-dir`, `--agent`, `--agents`, `--allow-dangerously-skip-permissions`, `--allowedTools`/`--allowed-tools`, `--append-system-prompt`, `--append-system-prompt-file`, `--bare`, `--betas`, `--brief`, `--channels`, `--chrome`, `-c`/`--continue`, `--dangerously-load-development-channels`, `--dangerously-skip-permissions`, `-d`/`--debug`, `--debug-file`, `--disable-slash-commands`, `--disallowedTools`, `--effort`, `--exclude-dynamic-system-prompt-sections`, `--fallback-model`, `--file`, `--fork-session`, `--from-pr`, `-h`/`--help`, `--ide`, `--include-hook-events`, `--include-partial-messages`, `--init`, `--init-only`, `--input-format`, `--json-schema`, `--maintenance`, `--max-budget-usd`, `--max-turns`, `--mcp-config`, `--mcp-debug` (deprecated), `--model`, `-n`/`--name`, `--no-chrome`, `--no-session-persistence`, `--output-format`, `--permission-mode`, `--permission-prompt-tool`, `--plugin-dir`, `--plugin-url`, `-p`/`--print`, `--remote`, `--remote-control`/`--rc`, `--remote-control-session-name-prefix`, `--replay-user-messages`, `-r`/`--resume`, `--session-id`, `--setting-sources`, `--settings`, `--strict-mcp-config`, `--system-prompt`, `--system-prompt-file`, `--teleport`, `--teammate-mode`, `--tmux`, `--tools`, `--verbose`, `-v`/`--version`, `-w`/`--worktree`.

**Sandboxing:** `/sandbox` slash command + `--permission-mode plan|dontAsk|bypassPermissions`. There's no `--sandbox-dir` flag yet — sandboxing is platform-managed (macOS Seatbelt, Linux namespaces).

---

## 9. What's "just" the Anthropic Messages API and what isn't

### Just Messages API + Tool Use
- The token-level streaming (`message_start`, `content_block_delta` with `text_delta`/`input_json_delta`, etc.) is verbatim Anthropic SDK.
- `assistant.message` and `user.message` shapes are `BetaMessage`/`MessageParam` from `@anthropic-ai/sdk`.
- Tool use / tool result block schema is the Messages API tool-use schema.
- Usage/cost tracking is `usage` from the Messages API plus a derived `total_cost_usd`.

### **NOT** in the Messages API (Claude Code adds)
- **The agent loop.** Claude Code drives the tool-call loop on your behalf.
- **The built-in tools** (Read/Edit/Write/Bash/Grep/Glob/WebFetch/WebSearch/Task/Agent/AskUserQuestion/Monitor) are **client-side** — Claude Code implements them; the API only sees the schemas.
- **Hooks**, **skills**, **plugins**, **subagents**, **agent teams**, **slash commands**, **memory/CLAUDE.md**, **/rewind & file-history snapshots**, **session persistence (JSONL)**, **MCP server orchestration**, **permission modes & prompts**, **statusline**, **remote-control / Claude.ai web bridge**, **fork/branch/checkpoint**, **/compact** — all are Claude Code constructs above the API.
- **`thinking` blocks** are an extended-thinking feature; supported by the API but Claude Code surfaces them in the transcript.
- **Auto-mode classifier** (model-based permission approver) is Claude Code; rules dumpable via `claude auto-mode defaults`.

If you only call the Messages API directly, you'd be reimplementing the entire stack. Conversely, anyone wrapping the `claude` binary inherits all of it for free.

---

## 10. What fleet can build *around* the terminal-attach model

Right now fleet wraps `claude` in tmux and screen-reads status. Every machine-readable surface above is a "build chrome around the terminal" lever:

1. **Sidecar-tail the JSONL** for every active session. Render a chat-style preview pane in SwiftUI while keeping the real TUI authoritative. fleet already knows `claude_session_id`; the file is `~/.claude/projects/<slug>/<id>.jsonl`. The transcript contains tool calls, file diffs (via `file-history-snapshot`), thinking blocks, costs, and `ai-title` updates.
2. **Watch hook events.** fleet already adds a `UserPromptSubmit` + `Stop` hook for status detection. Adding hooks for `PreToolUse`/`PostToolUse`/`PermissionRequest`/`Notification`/`SubagentStart`/`TeammateIdle`/`FileChanged` gives a structured event bus per session. Hook output can post to a Unix socket (similar to fleet's existing `chrome.sock`).
3. **Pre-flight one `claude --bare -p --include-hook-events --include-partial-messages --output-format stream-json --init-only --session-id <new-uuid>`** to materialise the `system/init` event without starting a turn — gives you tools, MCPs, slash commands, plugins, agents, models for the SwiftUI launcher's per-session settings panel.
4. **Drive ad-hoc agents from the UI** via the SDK (`@anthropic-ai/claude-agent-sdk` from a Node sidecar, or shell out to `claude -p`). Examples that could be one-click buttons in fleet: `/security-review`, `/ultrareview`, `/simplify`, `/insights`, `/team-onboarding`, "summarize last N turns".
5. **Custom permission UX.** `--permission-prompt-tool <mcp-tool>` lets fleet host a Swift-native approve/deny modal for headless agents fleet itself spawns — without changing the user's interactive Claude session.
6. **In-process MCP** (TS SDK) means fleet can register custom tools (e.g. "open file in IDE", "show diff in fleet sidebar") without spawning extra processes.
7. **`Query.interrupt()`, `setPermissionMode`, `setModel`, `rewindFiles`** — these control methods are why Conductor/Crystal don't use tmux at all; they pipe stream-json end-to-end. fleet doesn't have to abandon tmux to use these — it can run its *spawning* path through the SDK while still attaching the terminal.
8. **Multiplex Claude.ai web sessions.** `--remote-control`, `--teleport`, `--from-pr`, `--remote` let fleet present "Open in app" pickers for sessions started on the web. The `bridge_status` JSONL record includes the claude.ai URL.
9. **Cost dashboards.** Every `result` event has `total_cost_usd` and per-model usage; every assistant turn JSONL line has `usage`. Aggregate across the `~/.claude/projects/` tree for a "cost per repo / per session / per day" view.
10. **`/rewind` / file-history.** Each `file-history-snapshot` record carries `trackedFileBackups`. fleet could expose a Time-Machine-like rewind affordance per session.

### What still needs the terminal
- Actual interactive prompts (the input bar, status line, ANSI-styled output) — that's what the user is choosing to keep when they pick fleet over a pure-headless desktop.
- Tools that depend on TTY (`/voice`, certain interactive pickers like `/diff`, `/copy`).
- Anthropic OAuth login — `claude auth login` opens a browser; not blocker-level, but you don't want to wrap it.

Everything else can be done out-of-band against structured surfaces.

---

## Sources

- https://code.claude.com/docs/en/cli-reference
- https://code.claude.com/docs/en/headless
- https://code.claude.com/docs/en/hooks
- https://code.claude.com/docs/en/mcp
- https://code.claude.com/docs/en/commands
- https://code.claude.com/docs/en/skills
- https://code.claude.com/docs/en/agent-sdk/overview
- https://code.claude.com/docs/en/agent-sdk/streaming-output
- https://code.claude.com/docs/en/agent-sdk/typescript
- https://code.claude.com/docs/en/agent-sdk/permissions
- https://docs.claude.com/en/docs/agent-sdk/* (redirects to code.claude.com)
- Local probe: `claude --help` (v2.1.131); `~/.claude/projects/-Users-yuvalhayke-code-brizz-code-fleet-ui/*.jsonl` (schema only, no content); `~/.claude/settings.json` (hook config layout).
