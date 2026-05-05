# Vision — fleet Mac App

## One-line pitch

A native Mac app for managing many parallel Claude Code sessions, with a real Claude TUI inside every pane and the chrome people actually want around it.

## Why a Mac app, not just the TUI

The TUI works. It's been the daily driver. But there's a ceiling we keep bumping into:

- **One session at a time.** Attaching to a session means leaving every other one. Switching is a context switch, not a glance.
- **No native notifications.** Status flips to Waiting, but unless you have the terminal foregrounded you don't see it. Hooks know; the OS doesn't.
- **Chrome can't grow.** Diffs, PR state, dev-server logs, dock badges, multi-pane grids — none of this fits in a terminal. It's not a TUI design failure, it's a medium ceiling.
- **Onboarding tax.** "Install brew, install tmux, run binary in terminal" is fine for terminal-native users. It's the entire wedge for everyone else.

A Mac app keeps the TUI's strengths (real Claude TUI, real tmux, no proxying) and removes the medium ceiling.

## Target user

In order:

1. **You first.** This is a personal-tool-that-might-grow. Optimize for the daily flow.
2. **Power devs already comfortable with TUIs** — people who'd happily run `fleet --tui` but want a multi-pane glance, native notifications, and a richer diff viewer.
3. **Broader Claude Code users** who never tried the TUI but would download a Mac app from a website — eventually. Onboarding affordances come later.

We're not building for teams or orgs. Solo / small-shop power-user is the target audience.

## The narrative

> "fleet, but with the chrome people show off in screenshots."

We keep the Claude Code TUI inside a real terminal pane. That preserves the "feel" Conductor users explicitly miss. We add native chrome around it: a sidebar that groups sessions by status, tabs, a real diff viewer, notifications, suggested git actions, an embedded dev server panel.

The bet is that the right desktop app is *not* a Conductor clone (which proxies Claude through a custom React renderer). It's an iTerm2-class native shell that hosts the Claude TUI and adds everything *outside* the Claude pane.

## What we explicitly do NOT build

These are tempting but wrong-lane:

- **A custom Claude chat UI.** Conductor does this. Their users say it "feels weird" and "loses Claude's joy." Our pane is a real terminal showing the real Claude TUI. We never reimplement message bubbles, mentions, slash commands, or the model selector inside our own renderer. (We can *send* keystrokes — `/model`, `@terminal` — but we don't render the chat.)
- **Inline GitHub PR comment sync.** That's `gh pr review` territory. We integrate, we don't replace.
- **Agent authoring.** Building system prompts, "CC Agents," etc. — that's Claudia/opcode's lane.
- **Cloud orchestration.** Oz, Jules, Codex Cloud, Copilot Agent already cover this. We are firmly local-first.
- **Markdown / spreadsheet / diagram editors.** Nimbalyst's mistake — feature creep into "AI-native workspace." Stay focused on coding sessions.
- **Tasks / issue management.** Linear and GitHub Issues already won. We integrate (open Linear ticket → spawn session); we don't compete.
- **A Conductor-style forced clone of the GitHub repo.** Top HN complaint. Our wedge is "open any local repo on disk, point and shoot."

## The wedge — preserve at all costs

Three things Conductor refuses that we keep:

1. **Open any local repo, no clone.** You point at a directory. We don't OAuth GitHub on first run.
2. **No setup script required.** Default works. `.fleet.json` is opt-in for users who need a custom workspace provider.
3. **Sessions on the main repo are first-class.** You don't have to create a worktree to run an agent. `a` key (TUI) / "+" button (Mac) spawns a session right where the cursor is, worktree or not.

If we lose any of these, we've become "Conductor but open source," which is a worse Conductor.

## Success criteria

- Yuval uses the Mac app as daily driver and reaches for `fleet --tui` only on remote SSH.
- Multi-pane is genuinely useful (i.e. you actually keep 2-3 sessions visible, not just because you can).
- Native notifications cut "missed Waiting state" incidents to near zero.
- A new user can install, point at a repo, and get a useful session in under 60 seconds without reading docs.
- The Claude TUI inside the embedded pane is indistinguishable from running `claude` in Terminal.app.
