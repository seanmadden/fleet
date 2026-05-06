# Claudia (getAsterisk / opcode) — Research Report

**Repo:** https://github.com/getAsterisk/claudia (also published as `getAsterisk/opcode`)
**Stack:** Tauri 2 + Rust backend + React/Vite/TypeScript frontend (Bun)
**Cloned commit:** `70c16d8` (HEAD of `main`, single commit on shallow clone)
**Tagline:** "A powerful GUI app and Toolkit for Claude Code."

---

## TL;DR — Rendering approach (definitive)

**Claudia does NOT embed a terminal and does NOT run interactive `claude`.**

Concretely:
- `Cargo.toml` has **no `portable-pty`, no `pty-process`, no `tauri-plugin-pty`**. The only shell-adjacent dep is `tauri-plugin-shell = "2"` (used for opening URLs).
- `package.json` has **no `xterm`, no `xterm.js`, no terminal renderer**.
- For every send-prompt path Claudia spawns `claude -p <prompt> --output-format stream-json --verbose --dangerously-skip-permissions` (one-shot, non-interactive), parses each JSONL line in Rust, forwards it as a Tauri event to the WebView, and the React frontend **renders its own bespoke chat UI** (Markdown + `react-syntax-highlighter` + per-tool widgets).
- For *resuming* a session it shells out with `--resume <session-id>` and additionally reads `~/.claude/projects/<project_id>/<session_id>.jsonl` directly to seed the chat history before any new stream begins.

So the answer is **(b) + (c) combined**: live data via `--output-format=stream-json`, history hydration via the JSONL files Claude writes to disk. Never (a).

---

## 1. Process model — how `claude` is invoked

### File: `src-tauri/src/commands/claude.rs`

`execute_claude_code` (new session), `continue_claude_code` (`-c`), and `resume_claude_code` (`--resume <id>`) all build the same arg vector and call `spawn_claude_process`.

```rust
// claude.rs:935-944  (execute_claude_code)
let args = vec![
    "-p".to_string(),
    prompt.clone(),
    "--model".to_string(),
    model.clone(),
    "--output-format".to_string(),
    "stream-json".to_string(),
    "--verbose".to_string(),
    "--dangerously-skip-permissions".to_string(),
];
```

```rust
// claude.rs:1000-1011 (resume_claude_code)
let args = vec![
    "--resume".to_string(),
    session_id.clone(),
    "-p".to_string(),
    prompt.clone(),
    "--model".to_string(),
    model.clone(),
    "--output-format".to_string(),
    "stream-json".to_string(),
    "--verbose".to_string(),
    "--dangerously-skip-permissions".to_string(),
];
```

The CC Agents path does the same plus `--system-prompt`:

```rust
// agents.rs:757-768 (run_agent → spawn_agent_system)
let args = vec![
    "-p".to_string(),
    task.clone(),
    "--system-prompt".to_string(),
    agent.system_prompt.clone(),
    "--model".to_string(),
    execution_model.clone(),
    "--output-format".to_string(),
    "stream-json".to_string(),
    "--verbose".to_string(),
    "--dangerously-skip-permissions".to_string(),
];
```

A web-server variant (`web_server.rs:494/607/695`) does the same — same flags, just over HTTP/WS.

### Stream parsing in Rust

`spawn_claude_process` uses `tokio::process::Command` with piped stdout/stderr and a tokio task per pipe (no PTY at all):

```rust
// claude.rs:1184-1274
let mut child = cmd.spawn()
    .map_err(|e| format!("Failed to spawn Claude: {}", e))?;
let stdout = child.stdout.take().ok_or("Failed to get stdout")?;
let stderr = child.stderr.take().ok_or("Failed to get stderr")?;
let stdout_reader = BufReader::new(stdout);
...
let stdout_task = tokio::spawn(async move {
    let mut lines = stdout_reader.lines();
    while let Ok(Some(line)) = lines.next_line().await {
        // Parse "system:init" to extract the session_id
        if let Ok(msg) = serde_json::from_str::<serde_json::Value>(&line) {
            if msg["type"] == "system" && msg["subtype"] == "init" {
                if let Some(claude_session_id) = msg["session_id"].as_str() { ... }
            }
        }
        // Append to a ring-buffer "live output" and emit to frontend
        let _ = registry_clone.append_live_output(run_id, &line);
        if let Some(ref session_id) = *session_id_holder_clone.lock().unwrap() {
            let _ = app_handle.emit(&format!("claude-output:{}", session_id), &line);
        }
        let _ = app_handle.emit("claude-output", &line); // generic fallback
    }
});
```

Lines are emitted **as raw JSON strings** on `claude-output:<session_id>` and a generic `claude-output` event. `claude-error:<sid>` and `claude-complete:<sid>` mirror stderr/exit. The Rust side does no semantic interpretation beyond extracting the session id.

### Cancellation

`cancel_claude_execution` (`claude.rs:1019-1149`) tries three escalating approaches: ProcessRegistry-tracked PID → `child.kill().await` on the held tokio handle → fall back to `kill -KILL <pid>` / `taskkill /F /PID`. It always emits `claude-cancelled` + `claude-complete` so the UI unblocks even if no live process was found. There is a `ClaudeProcessState` mutex that holds *one* `tokio::process::Child` for backward-compat, plus a `ProcessRegistry` (`process/registry.rs`, 537 lines) that supports concurrent runs.

### History hydration on resume

```rust
// claude.rs:880-917
pub async fn load_session_history(
    session_id: String,
    project_id: String,
) -> Result<Vec<serde_json::Value>, String> {
    let claude_dir = get_claude_dir().map_err(|e| e.to_string())?;
    let session_path = claude_dir
        .join("projects").join(&project_id)
        .join(format!("{}.jsonl", session_id));
    ...
    for line in reader.lines() {
        if let Ok(line) = line {
            if let Ok(json) = serde_json::from_str::<serde_json::Value>(&line) {
                messages.push(json);
            }
        }
    }
    Ok(messages)
}
```

So the answer to "do they read `~/.claude/projects/.../.jsonl`?" is **yes, on session open** (to render past turns) — and they then layer live `stream-json` events on top.

---

## 2. Frontend rendering — bespoke chat UI

### Stream consumer: `src/components/ClaudeCodeSession.tsx` (1762 lines)

Listens via `@tauri-apps/api/event`'s `listen()` to `claude-output:<sid>` (and `claude-output` generic until the init message arrives), parses payload as JSON, pushes to `messages` state.

```ts
// ClaudeCodeSession.tsx:447-462
const outputUnlisten = await listen(`claude-output:${sessionId}`, async (event: any) => {
  setRawJsonlOutput(prev => [...prev, event.payload]);
  const message = JSON.parse(event.payload) as ClaudeStreamMessage;
  setMessages(prev => [...prev, message]);
});
```

`ClaudeStreamMessage` interface (`AgentExecution.tsx:61-76`):
```ts
export interface ClaudeStreamMessage {
  type: "system" | "assistant" | "user" | "result";
  subtype?: string;
  message?: { content?: any[]; usage?: {input_tokens; output_tokens}; };
  usage?: { input_tokens; output_tokens; };
  [key: string]: any;
}
```

There's a clever generic→specific listener swap — Claudia starts with the *unscoped* `claude-output` event because Claude may emit a *new* session id even on `--resume`, then attaches `claude-output:<sid>` once `system:init` arrives (`ClaudeCodeSession.tsx:528-602`).

### Renderer: `src/components/StreamMessage.tsx` (739 lines)

Does a giant switch on `message.type`/`subtype` and message-content `type`:

```ts
// StreamMessage.tsx:98-107
if (message.type === "system" && message.subtype === "init") {
  return <SystemInitializedWidget sessionId={...} model={...} cwd={...} tools={...} />;
}
```

Assistant text is rendered with **ReactMarkdown + remark-gfm + Prism react-syntax-highlighter** in the home-grown `claudeSyntaxTheme`:

```ts
// StreamMessage.tsx:131-156 (assistant text rendering)
<ReactMarkdown
  remarkPlugins={[remarkGfm]}
  components={{
    code({ inline, className, children, ...props }: any) {
      const match = /language-(\w+)/.exec(className || '');
      return !inline && match ? (
        <SyntaxHighlighter style={syntaxTheme} language={match[1]} PreTag="div" {...props}>
          {String(children).replace(/\n$/, '')}
        </SyntaxHighlighter>
      ) : <code className={className} {...props}>{children}</code>;
    }
  }}
>{textContent}</ReactMarkdown>
```

### Tool widgets (`src/components/ToolWidgets.tsx`)

Every Claude built-in tool gets a hand-rolled React widget keyed off `content.name?.toLowerCase()`:

```
TodoWidget, TodoReadWidget, LSWidget, ReadWidget (+ ReadResultWidget),
GlobWidget, BashWidget, WriteWidget, GrepWidget,
EditWidget (+ EditResultWidget), MultiEditWidget (+ MultiEditResultWidget),
MCPWidget, CommandWidget, CommandOutputWidget, SummaryWidget,
SystemReminderWidget, SystemInitializedWidget, TaskWidget,
LSResultWidget, ThinkingWidget, WebSearchWidget, WebFetchWidget
```

Tool *results* are then matched back to their `tool_use_id` to suppress the raw display when a dedicated widget exists (`StreamMessage.tsx:373-395`). Edit results are recognized by string heuristic `"has been updated. Here's the result of running \`cat -n\`"` (`StreamMessage.tsx:454-467`); LS results by tree-pattern detection (`StreamMessage.tsx:488-522`); Read results by `^\s*\d+→` line prefixes (`StreamMessage.tsx:538-539`).

MCP tools are caught by the generic `content.name?.startsWith("mcp__")` check (`StreamMessage.tsx:202-206`) and dumped through a generic `MCPWidget`.

Sub-agents (`Task` tool) get a `TaskWidget` that recursively shows the sub-agent's prompt + result block (`StreamMessage.tsx:185-188`). Plan mode shows up only as a `LogOut` icon for the `exit_plan_mode` tool (`ToolWidgets.tsx:1820`) — **no plan-mode UX whatsoever**.

### Streaming display

There is no token-by-token streaming. Each JSONL line in `--output-format stream-json` is one fully-formed assistant turn / tool_use / tool_result / system event. Claudia renders message-by-message, not token-by-token. The "streaming" is purely "as messages arrive while the run is in flight."

### Slash commands — re-implemented in JS

`SlashCommandPicker.tsx` (565 lines) + `commands/slash_commands.rs` (471 lines). The Rust side parses `~/.claude/commands/**/*.md` with YAML frontmatter (`allowed-tools`, `description`) and exposes them to JS. The JS side detects `/` typed at start-of-word in `FloatingPromptInput.tsx:464-475`, opens the picker, and **substitutes the markdown body inline into the user prompt** before sending. Built-in Claude slash commands like `/compact`, `/clear`, `/init` are *not* re-implemented — only user/project custom commands work.

### `@file` mentions

`FloatingPromptInput.tsx:478-483, 539-558` — typing `@` opens a `FilePicker` populated by `list_directory_contents` / `search_files` Rust commands; selected paths are inserted as `@path` (or `@"path with spaces"`) into the prompt text. Claude's CLI itself handles the actual file-attaching — Claudia just helps you type the mention.

### Image paste

`FloatingPromptInput.tsx:247-260, 276-348, 391-405` — drag-and-drop or paste detected, image paths normalized to `@path` mentions, embedded image previews tracked separately. Again, Claudia is just enriching the text input; the CLI does the real work.

### Permission prompts (the "1/2/3 menu")

**They are bypassed entirely.** Every spawn passes `--dangerously-skip-permissions` (claude.rs:943, 975, 1010; agents.rs:767; web_server.rs:494/607/695). There is no UI for approving tools — the model can do anything within the configured agent's `enable_file_read`/`enable_file_write`/`enable_network` flags (which are *advisory only*, enforced by the agent's system prompt, not by Claude's permission system).

### Hooks

`commands/claude.rs:2056-2158` exposes `get_hooks_config` / `update_hooks_config` that read/write the `hooks` section of `~/.claude/settings.json` (per-scope). They surface a `HooksEditor.tsx` UI plus `lib/hooksManager.ts` that merges user/project/local configs. Hooks themselves still execute via Claude's normal hook mechanism — Claudia is just a settings editor.

---

## 3. Resumability / sessions / CC Agents

### Sessions
- Project root encoded as `~/.claude/projects/<encoded-path>/`
- Session files are `<session_id>.jsonl` written by Claude itself
- Claudia lists projects by reading the directory, parses the first user message of each JSONL for the preview (`extract_first_user_message`, `claude.rs:194`), and resumes by calling `claude --resume <id>`.
- `SessionPersistenceService` (in `src/services/sessionPersistence.ts`) saves a tab → session-id mapping to `localStorage` so closed tabs reopen on the right session.

### CC Agents (`commands/agents.rs`, 1996 lines)
- A "CC Agent" is a row in a SQLite `agents` table with: `name`, `icon`, `system_prompt`, `default_task`, `model`, `enable_file_read`, `enable_file_write`, `enable_network`, `hooks`. (`agents.rs:23-35, 229-252`)
- Running an agent = `claude -p <task> --system-prompt <agent.system_prompt> --model <m> --output-format stream-json --verbose --dangerously-skip-permissions` (`agents.rs:757-768`). It is the same one-shot execution path as a regular session, just with a baked-in system prompt.
- Each run is recorded in `agent_runs` table with the JSONL captured to disk for later replay.
- This is **not** the Claude Code SDK — it's just a structured way to invoke the CLI.

### Checkpoints / Timeline (`src-tauri/src/checkpoint/`)
- `manager.rs`, `state.rs`, `storage.rs` implement a custom snapshot system (separate from Claude's session jsonl) that lets the user "fork" mid-conversation and roll back working-directory changes. Independent of `--resume`.

---

## 4. Limitations and known issues

From the open issues on `getAsterisk/opcode` (the repo was renamed to opcode mid-flight; `claudia` still mirrors):

- **#462 — Tauri events never reach frontend.** `ClaudeCodeSession.tsx` uses `require("@tauri-apps/api/event").listen` inside a try/catch. Vite ships pure ESM, so `require` throws `ReferenceError`, caught silently → falls back to a DOM-event listener that the Rust side never dispatches to. Result: **infinite spinner with no output on macOS/Linux for users building from source**. This is the kind of bug that comes from running stream-json over Tauri events instead of using a more robust transport.
- **#447 — Windows infinite spinner.** Claude on Windows spawns Node.js workers that inherit the stdout pipe write-handle. When `claude.exe` exits, the workers still hold the handle, the Rust BufReader never sees EOF, `claude-complete` never fires, spinner spins forever. Proposed fix is a platform adapter that drops the stdout reader after `system:init` and reads the JSONL file from disk instead. This shows the brittleness of "parse stdout JSONL" as the primary live transport.
- **#321 — Failed to load claude installations on Windows.**
- **#444 — Failed to load CLAUDE.md for paths with spaces / iCloud Drive.**
- **#208 — Usage limit notification fires after ~20 minutes** of use vs. VS Code Claude Code extension where it doesn't. Claudia's usage tracker (`commands/usage.rs`, 714 lines) reads cost.json/usage data from disk and is apparently more aggressive than Claude's built-in throttling. Source: [Issue #208](https://github.com/getAsterisk/opcode/issues/208).
- **#418 — No click-to-open-files-in-IDE.**
- **No plan-mode UI.** `exit_plan_mode` tool is just an icon (`ToolWidgets.tsx:1820`).
- **No interactive permission UI.** Hard-coded `--dangerously-skip-permissions` everywhere. Claudia explicitly trades safety for UX simplicity.
- **No streaming inside a single assistant turn.** `stream-json` emits whole turns; you watch turns appear, not tokens.
- **Tool widgets are hand-rolled per Claude tool name** — when Anthropic renames or adds a tool, Claudia silently falls back to a generic JSON dump.
- **Agent project rebranded → fork drift.** The repo at `getAsterisk/claudia` and `getAsterisk/opcode` (and `winfunc/opcode`) all point at the same code; community is fragmented and several issues note "Is this maintained?" (#431, #364).
- **TODO comments in hot path** — `ClaudeCodeSession.tsx:861` has `has_attachments: false, // TODO: Add attachment support when implemented` despite the README marketing image-paste support.

Recent commit (sole commit visible on shallow clone): `70c16d8 fix(web): use wss:// for HTTPS connections and apply cargo fmt` — focus has moved to the web-server variant of Claudia.

---

## 5. What this means for fleet

Claudia is the strongest counter-example to fleet's terminal-attached approach. Their bet: parse Anthropic's documented stream protocol → build a beautiful web UI on top → never see a TTY. Their pain points show why fleet's tmux+SwiftTerm approach is defensible:

1. **They can never render permission prompts**, because rendering them requires the interactive PTY they don't have. Hard-coding `--dangerously-skip-permissions` is the only escape hatch — and it's a security/UX tradeoff fleet doesn't have to make.
2. **They can never render plan mode, sub-agent menus, `/compact` flows, or any future Claude interactive UX** without re-implementing each one. fleet gets all of them for free.
3. **stdout-pipe handle inheritance bites them on Windows** (#447). A real PTY doesn't have this problem because EOF is signaled on TTY hangup.
4. **Tool widgets are an endless treadmill.** Every tool needs a custom React component; rename a tool → silent fallback. Anthropic ships new tools every quarter.
5. **The `--output-format stream-json` API is undocumented-ish and brittle** — they had to special-case the `system:init` event and even build a generic-then-scoped listener swap because Claude can mint a fresh session_id on `--resume`.
6. **History hydration via JSONL works fine and is worth stealing** — both approaches end up reading `~/.claude/projects/.../*.jsonl` for "show me past sessions." fleet should keep doing this.
7. **CC Agents = system-prompt + sandboxed flags + JSONL run log.** A useful product idea fleet could mirror as "Templates" or "Agent presets" without changing the rendering model.
8. **Slash commands as markdown files** (`~/.claude/commands/**/*.md` with YAML frontmatter) are a clean pattern Claudia exposes well. fleet already supports them via passthrough but could surface a picker.

**Fleet's terminal-first thesis is validated by Claudia's bug list**: #462 (event transport broken), #447 (pipe-handle EOF deadlock on Windows), `--dangerously-skip-permissions` as a permanent shortcut, plan mode that has no UI. Every one of these is a problem fleet doesn't have because tmux + a real PTY *is* the rendering layer.
