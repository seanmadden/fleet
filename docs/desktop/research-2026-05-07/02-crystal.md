# Crystal (stravu/crystal) — Research Notes

Date: 2026-05-07
Repo: https://github.com/stravu/crystal (clone: `/tmp/fleet-research-clones/crystal`)

> NOTE: As of 2026-02 Crystal is **deprecated** and replaced by Nimbalyst (https://nimbalyst.com/, see `README.md` lines 1–56 and `CHANGELOG.md` line 4). Code described here is the last public Crystal source.

## TL;DR — Rendering approach

Crystal **does NOT embed a real Claude Code TUI**. It spawns the `claude` CLI in **headless stream-json mode**, parses each JSON line, and re-renders messages in a custom React component (`RichOutputView`). It runs the process in a `node-pty` PTY (xterm-color, 80×30) only to get unbuffered stdio — **not for visual rendering**. There is a separate xterm.js panel (`TerminalPanel.tsx`) for *bash* terminals attached to the worktree, but Claude itself is never shown there.

Definitive evidence — `main/src/services/panels/panels/claude/claudeCodeManager.ts:72`:

```ts
const args = ['--verbose', '--output-format', 'stream-json'];
```

The matching `--input-format=stream-json` is **not** used. They send a single `-p <prompt>` per spawn (line 123, 135), so it's the SDK headless one-shot mode. Multi-turn = a fresh `claude --resume <id>` per turn.

## Tech stack

- Electron (main/renderer split). `main/` is the Node side, `frontend/` is React+Vite+TS.
- Renderer: React + Zustand + Tailwind + Lucide; markdown via `MarkdownPreview`.
- IPC: `ipcMain.handle` + a preload bridge; renderer calls `window.electronAPI.invoke(...)`.
- DB: SQLite via better-sqlite3 (`main/src/services/database.ts`).
- Process model: `@homebridge/node-pty-prebuilt-multiarch`.
- Terminal panel (separate): `@xterm/xterm` + `@xterm/addon-fit` (`frontend/src/components/panels/TerminalPanel.tsx:2-9`).
- MCP: `@modelcontextprotocol/sdk` for permission-prompt tool bridge.

## Process model

**One spawned `claude` per panel per turn**, not one long-lived process.

`main/src/services/panels/cli/AbstractCliManager.ts:582`:

```ts
ptyProcess = pty.spawn(command, args, {
  name: 'xterm-color',
  cols: 80,
  rows: 30,
  cwd,
  env
});
```

Why a PTY at all? Because `claude` was originally interactive — running it under a PTY avoids stdio buffering. They never resize, never read raw screen content; they only consume stdout chunks. Each chunk is `JSON.parse`d (`claudeCodeManager.ts:170-238`).

`continuePanel` (`claudeCodeManager.ts:464-524`) **kills the existing process** and respawns with `--resume <id> -p <prompt>`. So follow-up turns are not piped into stdin of an existing `claude`; they're new spawns.

`processes` map is keyed by panel id. Cleanup in `killProcess` walks the descendant pid tree (`getAllDescendantPids`).

Worktree creation: `main/src/services/worktreeManager.ts:136,158`:

```ts
await execWithShellPath(`git worktree add "${worktreePath}" ${branchName}`, { cwd: projectPath });
await execWithShellPath(`git worktree add -b ${branchName} "${worktreePath}" ${baseRef}`, { cwd: projectPath });
```

No tmux involved.

## Output parsing → custom UI

`claudeCodeManager.ts:170-238` parses each stdout line as JSON, falling back to "stdout"/"stderr" if not JSON. Messages flow through `panelEventBus` → DB (`session_outputs` table, raw) → IPC → renderer.

Renderer transforms the raw stream into a `UnifiedMessage[]` via `frontend/src/components/panels/ai/transformers/ClaudeMessageTransformer.ts`:

- `parseUserMessage` — strips `<local-command-stdout>` wrappers (slash command results), filters out tool_result-only messages (line 178-234).
- `parseAssistantMessage` — splits content blocks into `text`, `thinking`, and `tool_call` segments (line 236-297).
- `parseSystemMessage` — handles `init`, `context_compacted`, `error`, `git_operation`, `git_error` (line 299-397).
- `parseResultMessage` — final result with cost/duration.
- Tool calls and results are linked via `tool_use_id` and `parent_tool_use_id` (sub-agent tools).

Rendered by `frontend/src/components/panels/ai/RichOutputView.tsx` (1855 lines) — a fully custom chat UI with collapsible tool calls, todo list renderer, sub-agent grouping, copy buttons, scroll anchoring, etc.

## Permission prompts

Crystal does **not** see or render Claude's "1. Yes / 2. Yes don't ask again / 3. No" interactive menu. Instead it bypasses it via the **MCP permission-prompt tool** (`--permission-prompt-tool`), and renders its own dialog.

Flow:

1. `claudeCodeManager.ts:147` adds:
   ```ts
   args.push('--permission-prompt-tool', 'mcp__crystal-permissions__approve_permission',
            '--allowedTools', 'mcp__crystal-permissions__approve_permission');
   ```
2. A child Node process `mcpPermissionBridge.ts` runs an MCP stdio server exposing `approve_permission`. It connects to the main Electron process over a Unix socket (`permissionIpcPath`).
3. `mcpPermissionBridge.ts:127-160` — when `claude` calls `approve_permission`, the bridge forwards to main, main pops a React `PermissionDialog`, user clicks allow/deny, response goes back over the socket, MCP returns `{ behavior, updatedInput, message }` to claude.

Two modes (`defaultPermissionMode`):
- `'ignore'` → `--dangerously-skip-permissions` (line 92).
- `'approve'` → MCP bridge described above.

There is no third "raw passthrough" mode. **Crystal cannot show or relay Claude's native permission TUI** — by design.

## Slash commands, sub-agents, MCP, hooks, plan-mode

- **Slash commands**: extracted from the `system.init` JSON message (`slash_commands` field) and stashed in localStorage (`ClaudeMessageTransformer.ts:299-315`). Detected in assistant tool_use as `name === 'SlashCommand'` (`claudeCodeManager.ts:194-209`). User types `/foo` and it goes to claude verbatim through `-p`. Issue #195 reports custom slash commands not working for Codex; #237 says "Show Thinking" missing — slash UI is fragile.
- **Sub-agents (Task tool)**: `parent_tool_use_id` linkage in `ClaudeMessageTransformer.ts:88-96, 122-150`. Sub-agent calls are nested as `childToolCalls`. Sub-agents do show up because they emit JSON via the parent's stream — no separate process.
- **MCP servers**: `claudeCodeManager.ts:570-648` walks `~/.claude.json` projects + global, plus repo-local `.mcp.json`, **merges** with Crystal's own permission server, writes a temp `crystal-mcp-<sessionId>.json`, passes via `--mcp-config`. Worktrees-vs-base-project MCP is a known pain point (issue #144).
- **Hooks**: not handled by Crystal at all — they fall through to user's `~/.claude/settings.json`.
- **Plan mode**: not exposed as a UI toggle. Closest equivalent is the `ultrathink` checkbox which literally appends the string `"\nultrathink"` to the prompt (`useClaudePanel.ts:250`, `ClaudeInputWithImages.tsx:67`). Issue #87 is a feature request to add real plan-mode integration — never landed.
- **Image paste / `@file` mentions**:
  - Images: clipboard paste handler in `ClaudeInputWithImages.tsx:145-200`, max 10 MB. Saved to disk via IPC `sessions.saveImages` (`useClaudePanel.ts:286`), then the prompt is rewritten as `<attachments>\n…\n/path/to/image.png\n</attachments>` and sent as text (`useClaudePanel.ts:303-305`). They never use Anthropic's vision content blocks — they let claude open the file from disk via Read tool.
  - `@file` mentions: `FilePathAutocomplete.tsx` provides path autocomplete in the textarea, then the literal `@path` is sent as text.

## Resumability

- Captured from the first `system.init` JSON message: `sessionManager.ts:601-603`:
  ```ts
  if (output.type === 'json' && isJSONMessage(..., 'system', 'init') && data.session_id) {
    this.db.updateSession(id, { claude_session_id: data.session_id });
  }
  ```
- Stored in SQLite `sessions.claude_session_id`.
- Used on every turn after the first via `--resume <claude_session_id>` (`claudeCodeManager.ts:108-119`). Hard-fails if missing (line 117).
- Skip-resume escape hatch: `skip_continue_next` flag set after `/compact` to start fresh (line 502-517).

## Known limitations / issues found

- **Issue #102** "session gets overwritten if using Claude Code CLI anywhere else" — because `--resume` keys off the worktree path inside Claude's own state, running `claude` outside Crystal in the same worktree forks the session and Crystal then resumes the wrong tail. Foundational design bug.
- **Issue #228** "Crystal unable to start Claude Code" — generic spawn-fail with no error surfacing; spinner forever. They added a 5-min availability cache and retry-with-node fallback (`AbstractCliManager.ts:566-650`) precisely for this class of bug.
- **Issue #144** "MCP servers from project not being recognized by Claude" — root cause: claude keys MCP config by cwd, and worktrees have a different cwd than the base repo. Crystal's mitigation = synthesize a merged `crystal-mcp-*.json` (`claudeCodeManager.ts:570-908`).
- **Issue #221** "Full Access mode switches back to Workspace, tasks hanging" — codex panels stuck initializing.
- **Issue #202** "Terminal tab text formatting messes up after switching to Claude tab" — the xterm.js terminal panel re-fits incorrectly when hidden.
- **Issue #216** "Error at the end of each session" — codex MCP timeout stderr leaks into output for every turn.
- **Issue #87** "Kanban Planning System with Claude Plan Mode Integration" — never implemented; no real plan-mode support.
- **Issue #195** "Codex slash commands seem to be blocked" — slash command capture only works for ones Claude advertises in `system.init`.
- **`docs/SESSION_OUTPUT_SYSTEM.md:3`** explicit warning: "the session output handling system is complex and fragile. Modifying it frequently causes issues like duplicate messages, disappearing content, or blank screens." Tells you how brittle the JSON-stream-rebuild approach is.
- **Project status**: deprecated 2026-02. README directs users to Nimbalyst.

## Source URLs

- Repo: https://github.com/stravu/crystal
- Architecture doc: https://github.com/stravu/crystal/blob/main/docs/CRYSTAL_ARCHITECTURE.md
- Session output warning: https://github.com/stravu/crystal/blob/main/docs/SESSION_OUTPUT_SYSTEM.md
- Issues searched: stravu/crystal #102, #144, #195, #202, #216, #221, #228, #237, #87.
