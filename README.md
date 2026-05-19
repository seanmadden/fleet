<p align="center">
  <img src=".github/assets/logo.svg" alt="fleet logo" width="80" />
  <h1 align="center">fleet</h1>
  <p align="center">
    <strong>Run 10 Claude Code agents. Stay sane.</strong>
  </p>
  <p align="center">
    A terminal cockpit for orchestrating Claude Code sessions in parallel.
    <br />
    See which agents need you. Jump in, direct, jump out.
  </p>
  <p align="center">
    <a href="https://goreportcard.com/report/github.com/brizzai/fleet"><img src="https://goreportcard.com/badge/github.com/brizzai/fleet" alt="Go Report Card"></a>
    <a href="https://github.com/brizzai/fleet/releases/latest"><img src="https://img.shields.io/github/v/release/brizzai/fleet" alt="GitHub release"></a>
    <a href="LICENSE"><img src="https://img.shields.io/badge/License-Apache_2.0-blue.svg" alt="License: Apache 2.0"></a>
    <a href="https://golang.org/doc/devel/release.html"><img src="https://img.shields.io/github/go-mod/go-version/brizzai/fleet" alt="Go version"></a>
    <a href="https://github.com/brizzai/fleet/actions/workflows/ci.yml"><img src="https://img.shields.io/github/actions/workflow/status/brizzai/fleet/ci.yml?branch=master" alt="Build Status"></a>
  </p>
</p>

<br />

<p align="center">
  <img src=".github/assets/demo.png" alt="fleet screenshot" width="900" />
</p>

<p align="center">
  <em>Sessions grouped by repo &middot; Real-time status via hooks &middot; PR state &middot; One-key approve</em>
</p>

<br />

Your agents are coding. fleet keeps you in control.

- 👀 **See** — real-time status across every repo
- ⚡ **Act** — jump, approve, repeat
- 🚀 **Ship** — PRs, branches, worktrees — all visible

## Install

### Homebrew (recommended)

```bash
brew install brizzai/tap/fleet
```

### Shell script

```bash
curl -fsSL https://raw.githubusercontent.com/brizzai/fleet/master/install.sh | bash
```

Requires [`gh`](https://cli.github.com/).

### Go install

```bash
go install github.com/brizzai/fleet/cmd/fleet@latest
```

Requires Go 1.26+.

### From source (fork / local dev)

```bash
git clone <your-fork-url> && cd fleet
./install-dev.sh           # symlink ~/.local/bin/fleet -> build/fleet
# or: make install-dev
```

Subsequent `make build` runs are picked up automatically (the install is a
symlink into `build/`). Add `--copy` to install a static copy, `--dir <path>`
to install elsewhere, or `--name fleet-dev` to install under an alternate
name so it doesn't shadow your Homebrew install.

### Requirements

- macOS
- [tmux](https://github.com/tmux/tmux) (`brew install tmux`)
- [Claude Code](https://docs.anthropic.com/en/docs/claude-code)

## Quick Start

```bash
# Launch
fleet

# 'a' — new session in current repo
# 'n' — workspace picker with path autocomplete
# '?' — all keybindings
```

## Features

### Real-Time Status

Every agent's state, always visible. Hook-based detection — no polling, no delay.

`● running` &nbsp; `◐ waiting` &nbsp; `● finished` &nbsp; `○ idle` &nbsp; `✕ error`

### Jump + Approve

**`Space`** jumps to the next session that needs attention. **`Y`** approves the prompt without attaching. Two keys, done. Cycle through a dozen waiting agents in seconds.

### Git-Native Sessions

Sessions live under their repo. Branch name, dirty state, and full PR status on every header — CI pass/fail, review state, changes requested, unresolved threads. Collapse groups, filter with **`/`**, switch branches with **`b`**. Works with GitHub (via [`gh`](https://cli.github.com/)) and GitLab merge requests (via [`glab`](https://gitlab.com/gitlab-org/cli)) — the forge is auto-detected from the `origin` remote; both CLIs are optional, install whichever you use.

### Worktrees

**`w`** creates a new worktree with branch picker. Zero config — works with any repo. Each worktree gets its own isolated session. Custom workspace commands via `.fleet.json` if you need them.

### Fork Sessions

**`f`** forks a session — branches off the Claude conversation at that point. Try a different approach without losing the original. Both sessions keep running independently.

### And more

- **Session resume** — restart with **`r`**, Claude picks up exactly where it left off
- **Full terminal attach** — **`Enter`** for full PTY, **`Tab`** for split mode (beta), **`Ctrl+Q`** to detach
- **Auto-naming** — sessions title themselves from your prompt
- **5 themes** — tokyo-night, catppuccin-mocha, rose-pine, nord, gruvbox (**`S`** to switch)
- **Chrome tab control** — **`p`** opens the PR / merge request in Chrome, reuses existing tab
- **Bug reports** — **`!`** captures diagnostics and opens a pre-filled GitHub issue

## Why fleet?

There are a dozen multi-agent session managers now. Most try to support every AI CLI under the sun. fleet takes the opposite approach: **go deep on Claude Code, and nothing else.**

Every feature is designed around how Claude Code actually works — hooks, conversation resume, session IDs, prompt structure. No generic "send keystrokes and hope" abstraction layer.

### vs. the alternatives

|                                     | fleet | claude-squad | ccmanager | agent-deck |
|-------------------------------------|:----------:|:------------:|:---------:|:----------:|
| **Status detection**                | ✅ Hooks (real-time) | ✅ Pane scraping | ✅ Pane scraping | ✅ Hooks |
| **PR state** (CI + reviews + threads) | ✅ | — | — | — |
| **Smart session naming**            | ✅ | — | — | — |
| **Fork conversation**              | ✅ | — | — | ✅ |
| **Open PR in browser**             | ✅ | — | — | — |
| **Session resume**                  | ✅ | — | — | ✅ |
| **Git worktrees**                   | ✅ | ✅ | ✅ | ✅ |
| **Multi-agent** (Codex, Gemini…)    | — | ✅ | ✅ | ✅ |
| **Linux**                           | — | ✅ | ✅ | ✅ |
| **No tmux dependency**              | — | — | ✅ | — |

**The trade-off is intentional.** Claude-squad and ccmanager support 5+ agents — but treat them all the same. fleet knows what Claude Code *is*. It reads hook status files. It resumes conversations. It knows your PR has 2 unresolved threads. It names sessions from your actual prompt. That depth is only possible by going narrow.

If you use Claude Code as your primary agent and want the tightest integration, this is it.

## Keybindings

| Key | Action |
|-----|--------|
| `j` / `k` | Navigate up/down |
| `Enter` | Attach to session |
| `Ctrl+Q` | Detach from session |
| `Tab` | Focus/unfocus preview (split mode, beta) |
| `Space` | Jump to next waiting/finished session |
| `a` | New session (current repo) |
| `n` | New session (workspace picker) |
| `w` | New worktree session |
| `Y` | Quick approve waiting prompt |
| `f` | Fork session |
| `d` | Delete session |
| `r` | Restart session |
| `R` | Rename session |
| `b` | Switch git branch |
| `e` | Open in editor |
| `p` | Open PR in browser |
| `/` | Filter sessions |
| `S` | Settings |
| `!` | Bug report / diagnostics |
| `?` | Help |
| `q` | Quit |

## Contributing

See [CONTRIBUTING.md](.github/CONTRIBUTING.md) for development setup and guidelines.

## License

Apache 2.0
