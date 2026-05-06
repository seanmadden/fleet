# Claude Squad — Deep Dive vs. fleet

**Repo**: https://github.com/smtg-ai/claude-squad
**Stars**: 7,341 | **Forks**: 519 | **Open issues**: 52 | **Default branch**: `main` | **Latest release**: v1.0.17
**Language/stack**: Go 1.x, Bubble Tea, Lipgloss, tmux, creack/pty, cobra
**Repo size**: ~4,850 lines of Go (excluding tests/UI overlay).
**License**: AGPL-3.0
**Launch HN**: https://news.ycombinator.com/item?id=44630194 (July 2025; co-creator's "most popular Claude Code multiplexer" comment)

This is fleet's closest peer in the wild — same language (Go), same UI framework (Bubble Tea/Lipgloss), same shell-out approach (tmux), same session-per-worktree mental model. But it's much smaller in surface area and meaningfully behind fleet on several axes.

---

## 1. Process / Render Architecture

**Verdict: nearly identical to fleet — tmux + creack/pty + Bubble Tea.** No novel ideas vs. fleet.

`session/tmux/tmux.go:91-153`
```go
// Start creates a new tmux session and starts the program (claude/aider/codex/gemini) in it.
cmd := exec.Command("tmux", "new-session", "-d", "-s", t.sanitizedName, "-c", workDir, t.program)
ptmx, err := t.ptyFactory.Start(cmd)
// ... poll DoesSessionExist with exponential backoff up to 50ms, max 2s timeout
// Set history-limit=10000 (default 2000), enable mouse=on
// Then call Restore() which does: tmux attach-session via PTY for live preview
```

Key facts:
- Tmux session prefix: `claudesquad_` (`tmux.go:60`). Same pattern as fleet's `fleet_`.
- Exact-match session lookup uses `-t=name` not `-t name` — same pitfall fleet should be aware of (`tmux.go:458-462`).
- Sets `history-limit 10000` and `mouse on` per session at start (fleet does similar config).
- Attach uses Ctrl+Q (ASCII 17) intercept on stdin to detach (`tmux.go:319-324`) — **identical to fleet's Ctrl+Q PTY intercept**.
- They have a known stdin-junk hack: "nuke first stdin bytes within 50ms because terminals (warp, iterm) emit control sequences" (`tmux.go:305-317`). Worth checking fleet for the same issue.
- `Restore()` (`tmux.go:183-191`) creates a new PTY by attaching tmux for size control while detached — this is what allows them to re-attach without losing scrollback after pause.

**No daemon-as-server architecture.** They have a "daemon" but it's just a separate process for autoyes mode (described below) — not a long-lived state daemon like fleet's design. The TUI itself owns all tmux sessions and storage I/O, with no IPC. Fleet's daemon-and-clients design is **strictly more sophisticated** — Claude Squad cannot show two parallel UIs on the same sessions.

`session/tmux/pty.go:18-22` — they wrap `creack/pty.Start(cmd)` in a `Pty` struct with a `PtyFactory` interface for testing. Cleaner test seam than fleet has.

## 2. Worktree Management

**Verdict: simpler than fleet, missing several fleet capabilities, but has some sharp tactical wins.**

Key files: `session/git/worktree.go`, `worktree_ops.go`, `worktree_git.go`.

### Worktree storage location
**They put worktrees OUTSIDE the source repo**: `~/.claude-squad/worktrees/<sanitized-branch>_<unix-nano-hex>` (`worktree.go:11-18, 49-69`). fleet (per CLAUDE.md) and most peers put worktrees as siblings of the repo or inside `.git/worktrees`. The PR `#258 add configuration for sibling worktrees` was **closed without merging** — so they're committed to this design despite community pushback. This is a real differentiation point; users complain that this layout breaks IDE indexers, requires absolute path bookmarks, etc.

The unique-path suffix (`_<nano>`) means stale worktrees never collide on cleanup-failure. Worth stealing for fleet if it doesn't already do this.

### Branch creation
`worktree.go:73-91` — single naming scheme: `<branch_prefix><session_title>` where `branch_prefix` defaults to `<username>/`. Sanitized with `[^a-z0-9\-_/.]+` regex (`util.go:13-33`). They handle Windows usernames with backslashes (DOMAIN\user) — see merged PR #221.

### Setup flow
`worktree_ops.go:13-66` — distinct paths for new vs. existing branch. If user picked an existing branch:
1. `worktree remove -f <path>` (idempotent cleanup)
2. Try local: `show-ref refs/heads/<branch>` → `worktree add <path> <branch>`
3. Fallback to remote: `show-ref refs/remotes/origin/<branch>` → `worktree add -b <branch> <path> origin/<branch>` (auto-creates tracking branch)

If new branch: `worktree add -b <branch> <path> <HEAD-SHA>` from current HEAD SHA, **not from main/master**. They explicitly note this is to avoid inheriting uncommitted changes (`worktree_ops.go:88-94`). They have a `TODO: we might want to give an option to use main/master instead`. **fleet already does this — pre-fills base branch with default (main/master) — fleet wins here.**

### Branch picker UX
`session/git/worktree_git.go:14-53` — really nice piece. Two-step:
1. `FetchBranches` (`git fetch --prune`) runs in background as a fire-and-forget when picker opens.
2. `SearchBranches` calls `git branch -a --sort=-committerdate --format=%(refname:short)` — then in-memory case-insensitive substring filter, dedup `origin/` prefix, cap at 50 results.

UI uses **150ms debounce** (`app.go:860-868`) on filter input + version-tagged debounced messages to avoid stale results racing in (`branchSearchDebounceMsg.version`). This is a Bubble Tea-idiomatic, race-free debounce pattern fleet should consider stealing for its workspace picker.

### Cleanup
`worktree_ops.go:99-134` — cleanup removes worktree, deletes branch (UNLESS `isExistingBranch=true`, in which case preserve), prunes. Tracking the `isExistingBranch` flag in `GitWorktreeData` (`storage.go:34`) so cleanup honors it across restarts. Worth checking fleet's `internal/workspace/provider.go` does the same.

### Pause = checkout-and-keep-branch
`instance.go:412-469` — the "pause" feature is novel and clean:
1. If dirty, commit locally with `[claudesquad] update from '<title>' on <date> (paused)`.
2. **Detach** PTY but keep tmux session alive (`DetachSafely`).
3. Remove worktree (NOT cleanup), prune.
4. Copy branch name to system clipboard via `atotto/clipboard`.

`Resume` (`instance.go:472-525`) recreates worktree, re-attaches PTY (preferring restoring existing tmux session if alive — note `if i.tmuxSession.DoesSessionExist()`; if not, starts fresh in same workdir).

This pause-with-clipboard-handoff is **clever UX**: pause a session, paste branch name into your real shell, do whatever, then resume.

### IsBranchCheckedOut guard
Before deleting (kill) or other ops, they check if the branch is checked out anywhere else (`worktree_git.go:161-167`) and refuse the op. **Fleet should consider this.**

## 3. Multi-Agent Abstraction

**Verdict: there is essentially no abstraction — it's hard-coded `if/else` chains over program names.**

`session/tmux/tmux.go:22-25`:
```go
const ProgramClaude = "claude"
const ProgramAider = "aider"
const ProgramGemini = "gemini"
```

The program is just a shell command string stored on `Instance.Program` (`session/instance.go:41`). Differences between agents live in **two structural-string-match checks**:

`session/tmux/tmux.go:242-249` — readiness/prompt detection:
```go
if t.program == ProgramClaude {
    hasPrompt = strings.Contains(content, "No, and tell Claude what to do differently")
} else if strings.HasPrefix(t.program, ProgramAider) {
    hasPrompt = strings.Contains(content, "(Y)es/(N)o/(D)on't ask again")
} else if strings.HasPrefix(t.program, ProgramGemini) {
    hasPrompt = strings.Contains(content, "Yes, allow once")
}
// no Codex! see issue #266
```

`session/tmux/tmux.go:163-179` — trust-prompt auto-dismiss:
```go
if strings.HasSuffix(t.program, ProgramClaude) {
    if strings.Contains(content, "Do you trust the files in this folder?") || ... {
        TapEnter()
    }
} else { // assume aider
    if strings.Contains(content, "Open documentation url for more info") {
        TapDAndEnter()
    }
}
```

That's it. **No interface, no plugin system, no per-agent struct.** Issue #266 (open since March 2026) shows that adding Codex broke their model: prompts get dropped because there's no agent-specific readiness check, and they have no abstraction to extend.

**Profiles** (`config/config.go:29-82`, added Mar 2026 by mufeez/profiles PR #264) gives users named programs (`{name: "codex", program: "codex"}`) selectable in the new-session overlay. But they're just labeled command strings — no per-agent capability metadata, not even hooks.

**For fleet**: this is a *negative* example. If fleet ever adds multi-agent, do it via an interface-based `Agent` provider with structural methods (`DetectReady() bool`, `DismissTrust() error`, `SendPrompt(string) error`) rather than this if-chain. Issue #266's Codex bug is a direct consequence of the if-chain design.

## 4. Status Detection

**Verdict: pure pane-scrape, hash-based diff, no hooks. Strictly inferior to fleet's hook-first approach.**

`session/tmux/tmux.go:193-256` — entire status detection:

```go
type statusMonitor struct {
    prevOutputHash []byte
}

func (t *TmuxSession) HasUpdated() (updated bool, hasPrompt bool) {
    content, err := t.CapturePaneContent()
    // ... per-program string match for hasPrompt (see §3)
    if !bytes.Equal(t.monitor.hash(content), t.monitor.prevOutputHash) {
        t.monitor.prevOutputHash = t.monitor.hash(content)
        return true, hasPrompt
    }
    return false, hasPrompt
}
```

Status states are derived in `app/app.go:238-256`:
- `updated=true` → `Running`
- `hasPrompt=true` → also fires `TapEnter()` if AutoYes (Yolo mode)
- otherwise → `Ready`

Polling cadence: 500ms metadata tick (`app/app.go:930-954`) running `tmux capture-pane` + `git diff` for every active instance, in parallel goroutines via `sync.WaitGroup`.

**Statuses are only 4**: `Running, Ready, Loading, Paused` (`session/instance.go:17-28`). No "Finished", no "Error", no "Idle", no "Waiting" subtype distinguishing waiting-for-input from waiting-on-network. fleet's 6-status model (Running/Waiting/Finished/Idle/Error/Starting) is more granular.

**No hook integration.** They don't write `~/.claude/settings.json` hooks; they don't read Claude session_id; they don't survive a Claude restart. fleet's `internal/hooks/` directory has *no analog* in claude-squad.

**Limitations from issues**:
- #215 ("TUI Interactions very slow"): pane capture errors in a tight loop made the TUI unresponsive for 5 seconds per keypress. Caused by tight 500ms loop with no retry/backoff on tmux errors.
- #216 ("Error capturing pane content after starting cs"): same root cause.
- #266: Codex prompt-detection broken because adding agents requires editing string literals.

**For fleet**: don't regress to pane-scrape-only. The hook-based detection in `internal/hooks/claude_hooks.go` is a real moat and a key reason fleet's status is more accurate. **Memory `Status Detection — Key Principles` is a competitive advantage.**

## 5. Persistence / State

**Verdict: JSON file. No database. Minimal. Strictly less robust than fleet's SQLite.**

`config/state.go:11-46` — single file `~/.claude-squad/state.json`:
```go
type State struct {
    HelpScreensSeen uint32          // bitmask of dismissed help screens
    InstancesData   json.RawMessage // serialized []InstanceData
}
```

`session/storage.go:11-25` — InstanceData payload includes:
- `title, path, branch, status, height, width, created_at, updated_at, auto_yes, program`
- Embedded `Worktree` (repo_path, worktree_path, session_name, branch_name, base_commit_sha, is_existing_branch)
- Embedded `DiffStats` (added/removed/content)

**Save on every change**: `m.storage.SaveInstances(m.list.GetInstances())` is called after every state-changing op (`app.go:224, 310-312`, etc.) — full-rewrite of the JSON file each time. With many sessions this gets expensive (and unsafe under concurrent writes — they have no locking).

**No `claude --resume`.** They don't capture or replay the Claude session ID. When you restart claude-squad, sessions are restored by re-attaching to live tmux panes (`instance.go:140-143, 247-252`). If tmux died or the host rebooted, the session is gone. **fleet's `internal/session/storage.go claude_session_id` + `claude --resume` recovery is strictly better.**

`config/config.go:155-185` — config file `~/.claude-squad/config.json`:
- `default_program`, `auto_yes`, `daemon_poll_interval`, `branch_prefix`, `profiles[]`
- That's the entire config surface. fleet's config has 6 distinct keys + theme + per-repo `.fleet.json`.

## 6. Daemon / "AutoYes" Mode

**Verdict: fundamentally different from fleet's daemon. It's a yolo-mode background-presser, not a state server.**

`daemon/daemon.go:19-88`:
- Forked when `--autoyes` flag is set, runs `cs --daemon`.
- Loads instances from disk, polls each via `HasUpdated()`, calls `TapEnter()` for any pending prompt.
- Polling interval default: 1000ms (`config.go:42`).
- On SIGINT/SIGTERM: saves instances and exits.
- PID file at `~/.claude-squad/daemon.pid` (`daemon.go:115-126`).

**Critical issue**: when the TUI launches, it kills the daemon (`main.go:69-72`); when the TUI exits with autoyes enabled, it relaunches the daemon (`main.go:62-68`). So the TUI and daemon **never run concurrently** — they share the JSON state file by alternating ownership. There's no IPC, no socket, no gRPC.

This is *much* simpler than fleet's daemon design but fundamentally limited:
- Cannot have a desktop GUI and TUI open at the same time.
- Cannot stream live updates between processes.
- No two-clients-on-one-session model.

**fleet's daemon-and-clients architecture is correct.** Claude-squad's design literally cannot support fleet's stated desktop+TUI duality.

## 7. Limitations / Recent Bugs / Open Issues

Sorted by relevance to fleet:

| # | Issue | Lesson for fleet |
|---|-------|------------------|
| #284 | Pause race: 500ms metadata tick fires `git add -N .` concurrently with user pressing `c`, causing index-lock failure that bricks the session into deleted-worktree-but-still-running state | Audit fleet for same race in worktree-rm + diff polling. fleet's worker-goroutine queue likely already serializes this, but worth checking |
| #280 | Diff content held in memory for *every* active session even though only the selected one is rendered. N×fullDiff memory. Their fix: `--numstat` for inactive, full diff only for selected. | fleet probably already does this in `internal/git/`, but verify |
| #277 | New tmux sessions don't inherit env vars when reusing tmux server (Codex breaks on missing `CPA_API_KEY`). Suggested fix: use a dedicated tmux socket per app | **fleet probably has this same bug.** Should we use a custom `-L fleet-socket`? Worth investigating |
| #266 | Codex prompts get sent before Codex is ready and are silently dropped. Race: `instanceStartedMsg` fires before agent finishes init. No `WaitForReady` polling. | fleet's hook-based readiness is immune (UserPromptSubmit hook fires only when ready); claude-squad has no analog |
| #260 | Worktree env setup hook (deps, .env files, port isolation). Same problem fleet's `copy_claude_settings` partially addresses, but they don't have *anything*. **fleet's `.fleet.json` workspace-shell-commands is exactly the answer.** | fleet wins; consider documenting the `.fleet.json` shell-command escape hatch more prominently |
| #245 | `CLAUDE_SQUAD_HOME` env var for custom config dir (open since March, no progress) | fleet should support `FLEET_CONFIG_DIR` for the same reason |
| #56 | "Multiple git repos" — open since April 2025 (over a year). They literally only support running from inside one repo at a time (`main.go:46-48`: `must be run from within a git repository`). **fleet's multi-repo sidebar is a major differentiator.** | fleet wins on day one |
| #242 | "Make a desktop app" — community asking. They've done nothing. | fleet's desktop initiative directly addresses this gap |
| #154 | Remappable keys (open since July 2025) | fleet's centralized `internal/ui/keybindings.go` should easily support this |
| #271 | Invisible text input on macOS (creates without showing prompt) | UI bug — possibly a Lipgloss/AdaptiveColor issue |
| #275 | Windows binary fails immediately because creack/pty is unsupported on Windows | fleet's "Mac only" policy sidesteps this entirely |

**Recently merged PRs of interest**:
- PR #253 (Feb 2026, miamachine): "move expensive operations off UI event loop" — they only just learned the lesson fleet hardcoded from day 1. The PR introduced `tickUpdateMetadataCmd` running `tmux capture-pane` + `git diff` in parallel goroutines via WaitGroup. fleet's `internal/ui/worker.go` and snapshotted-active-instances pattern is more mature.
- PR #256 (Mar 2026): "Improve worktree startup time" — they switched from `go-git` library (`PlainOpen`, slow) to shelling out to `git show-ref` (`worktree_ops.go:30-34`). fleet should never have used go-git in the first place; verify it doesn't.
- PR #247 (Feb 2026, luckyDaveKim): **Terminal tab** — separate tmux session per instance for an interactive shell in the worktree dir, cached per-instance. `ui/terminal.go` 362 lines. **Worth stealing**: a "shell tab" alongside the Claude attach view, pre-cd'd to the worktree. Useful for quick `git diff`, `npm test`, etc. without detaching from claude-squad.
- PR #264 (Mar 2026): Profiles (named programs in dropdown).
- PR #176 (Jul 2025): "Support scrolling in preview pane" — `ui/preview.go:185-249` shows their approach: use `viewport.Model`, capture scrollback via `capture-pane -S - -E -`, exit with ESC. fleet probably already has equivalent.

## 8. Other Clever Things

- **Branch prefix per-user** (`config.go:96-103`): default branch prefix is `<username>/` so multi-developer machines don't collide. Cute touch fleet might want.
- **Clipboard handoff on pause** (`instance.go:467`): copies branch name to OS clipboard so user can `git checkout <pasted>` in their main shell. Small UX win worth stealing.
- **history-limit 10000** per session at start (`tmux.go:133-136`): bigger scrollback for preview/copy mode. Verify fleet matches.
- **Trust prompt auto-dismissal** (`tmux.go:155-180`): on first attach, if pane shows "Do you trust the files…" or "new MCP server", auto-press Enter. This bypasses Claude's first-run prompt for the user. **fleet probably wants this** — repeatedly prompting users to trust folders for every new worktree is annoying.
- **Branch search debounce with version tags** (`app.go:860-880`): nice race-free debounce pattern for branch picker.
- **Confirmation modals** (`app.go:993-1015`): kill (D) and push (p) both gated behind confirmation. fleet has equivalents.
- **Help screens per-action with bitmask of "seen"** (`config/state.go:131-138`, `app.go:781-789`): first time you do an action, a help screen appears explaining it; bitmask remembers and never shows again. Nicer onboarding than fleet's `?`-only help. **Worth stealing** for workspace creation, attach, pause, etc.
- **Push to GitHub via `gh repo sync`** (`worktree_git.go:69-124`): commits, pushes, then opens branch URL via `gh browse --branch`. fleet has PR view via `gh`; this is a cleaner one-button "submit my work". `s` key (or `p` in their map). **Fleet might want a similar key for "commit + push current work + open URL".**

## 9. What fleet Does Better

- **Hook-based status detection** with structural agent-team-pane checks (claude-squad has zero hooks)
- **SQLite + WAL persistence** vs. claude-squad's full-file JSON rewrite
- **claude --resume** captured from hooks (claude-squad cannot survive tmux death)
- **Multi-repo sidebar with grouping** (claude-squad: must run from inside one repo)
- **Theme system + settings dialog** (claude-squad: zero theming)
- **Auto-naming sessions from first prompt** (claude-squad: user types title manually each time)
- **Slot bindings (Alt+0-9 jump-to)** (claude-squad: arrow-key navigation only)
- **Command palette** (claude-squad: only the bottom menu)
- **Bug-report dialog with diagnostics + action log + error history** (claude-squad: dump tmux pane and hope)
- **Daemon-as-server design** allowing TUI + future GUI to share state (claude-squad: serial ownership of JSON file)
- **Chrome tab control NMH bridge** (claude-squad: `gh browse` via subprocess)
- **Pinned repos with empty-state** (claude-squad: no concept of repo vs. session)
- **Per-repo shell-provider workspace config (`.fleet.json`)** (claude-squad: only `worktree add`)
- **Quick-approve `Y` key with menu+Y/n detection** (claude-squad: only AutoYes daemon, no manual one-shot)
- **Undo-delete (z within 5s)** (claude-squad: no undo)

## 10. What fleet Should Steal

In rough priority order:

1. **Trust prompt auto-dismissal** (`tmux.go:155-180`). Fleet creates many worktrees; pressing Enter on Claude's "trust this folder" screen every time is friction. Detect + dismiss automatically.
2. **Pause feature** (`instance.go:412-469`): commit-locally, remove worktree, keep branch + tmux session, copy branch to clipboard. Distinct from delete. Good for "I'm done with this for now but keep the work".
3. **First-time help-screen bitmask** (`config/state.go:43`): contextual one-time help for actions like attach, kill, pause. Better onboarding for new users.
4. **Branch search with version-tagged debounce** (`app.go:860-880`): clean Bubble Tea debounce pattern fleet's workspace_picker can adopt.
5. **`history-limit 10000`** on every tmux session for richer scrollback in preview and shell tab.
6. **Terminal tab per instance** (`ui/terminal.go`, PR #247): a second tmux session in the worktree dir for ad-hoc shell commands without detaching from claude. Cached per-instance, pre-cd'd. Genuine UX win.
7. **`is_existing_branch` flag → "preserve branch on cleanup"** (`worktree.go:30-44`): if user picked an existing branch, never delete it on session-kill. Confirm fleet has this.
8. **Worktree-path uniqueness via `_<unix-nano>` suffix** (`worktree.go:67`): even sanitized-branch collisions don't clobber.
9. **`IsBranchCheckedOut` guard before delete** (`worktree_git.go:161-167`): refuse to kill a session whose branch is currently checked out elsewhere — protects against data loss.
10. **`branch_prefix` defaulting to `<username>/`** (`config.go:96-103`): nice for multi-user machines (server scenarios). Low effort.
11. **Profile dropdown for program selection** (PR #264): if fleet ever wants multi-agent, do it via labelled `{name, program}` profiles in the new-session overlay. But layered on a proper agent interface (see §3).
12. **`gh repo sync --source -b <branch>` for one-button push** (`worktree_git.go:69-124`): a quick "commit + push + open URL" key. fleet has PR badge already; add the inverse.

## 11. Reasons fleet's tmux+daemon+gRPC architecture is wrong

**None I can find.** The Claude Squad architecture is *strictly less ambitious* than fleet's:

- They have no IPC layer at all.
- They have no daemon-as-server (only daemon-as-yolomode-presser).
- They cannot support a desktop GUI without a major rewrite.
- They cannot share sessions between TUI and any other process.

The Claude Squad maintainers have explicitly punted on the desktop app (#242 untouched since Jan 2026). Fleet's bet on a daemon + clients design (Stage 0 PRs 3c, 4, 5 in your recent commits) is the *correct* answer to the question Claude Squad cannot answer: "how does the TUI and a future GUI share live state?"

If anything, the only architectural critique is: Claude Squad's pure-pane-scrape status detection works "well enough" for many users despite being clearly inferior to hooks — meaning fleet's hooks investment may not be visible to casual users. But it pays off in correctness for power users with dozens of sessions, and it's a moat.

---

## Citations

- Source: https://github.com/smtg-ai/claude-squad
- Launch HN: https://news.ycombinator.com/item?id=44630194
- Issue #56 (multi-repo): https://github.com/smtg-ai/claude-squad/issues/56
- Issue #242 (desktop app): https://github.com/smtg-ai/claude-squad/issues/242
- Issue #260 (env setup hook): https://github.com/smtg-ai/claude-squad/issues/260
- Issue #266 (Codex prompt race): https://github.com/smtg-ai/claude-squad/issues/266
- Issue #277 (env vars not inherited): https://github.com/smtg-ai/claude-squad/issues/277
- Issue #280 (diff memory): https://github.com/smtg-ai/claude-squad/issues/280
- Issue #284 (pause race): https://github.com/smtg-ai/claude-squad/issues/284
- PR #253 (async metadata): https://github.com/smtg-ai/claude-squad/pull/253
- PR #247 (terminal tab): https://github.com/smtg-ai/claude-squad/pull/247
- PR #264 (profiles): https://github.com/smtg-ai/claude-squad/pull/264
- PR #256 (worktree perf): https://github.com/smtg-ai/claude-squad/pull/256
- PR #176 (preview scroll): https://github.com/smtg-ai/claude-squad/pull/176
