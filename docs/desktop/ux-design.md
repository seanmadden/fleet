# UX Design — fleet Mac App

## V1 layout — sidebar + single embedded pane

```
┌────────────────────────────────────────────────────────────────────────────┐
│ ●●●  fleet                                          [⚙] [+ New Session]    │  ← title bar (native)
├──────────────────────┬─────────────────────────────────────────────────────┤
│                      │  Yuval/feature-x · main↗  · #1234 ✓                 │  ← session toolbar
│ ⌕ filter…            │  ────────────────────────────────────────────────── │
│                      │                                                     │
│ ▼  brizzai/brizzai   │                                                     │
│  ●  feature-x  [1]   │                                                     │
│  ◐  api-fix          │       (SwiftTerm widget — real Claude TUI)          │
│  ○  docs-pr          │                                                     │
│                      │                                                     │
│ ▶  brizzai/fleet     │                                                     │
│                      │                                                     │
│ ▶  In review (2)     │                                                     │
│ ▶  Done (4)          │                                                     │
│                      │                                                     │
│                      │                                                     │
├──────────────────────┴─────────────────────────────────────────────────────┤
│  ⌘1 Pull  ⌘⇧R Review  ⌘⇧X Fix Errors  ⌘⇧M Merge  ⌘⇧P Open PR              │  ← suggested actions (V2+)
└────────────────────────────────────────────────────────────────────────────┘
```

### What's new vs the TUI

- **Window chrome**: native title bar, traffic-light close, native menu bar (Fleet / File / Edit / View / Session / Window / Help).
- **Native `+ New Session` button** in the title bar — opens the new-session dialog. (`a` for repo-scoped instant, `n` for picker, `w` for new worktree are still keyboard shortcuts.)
- **Filter input** is always visible, not just when `/` is pressed.
- **Suggested actions bar** at the bottom — keybinds shown as native buttons. Only buttons relevant to the selected session's state are enabled.
- **Native menu bar** carries every action so non-keyboard users can discover them.

### What's preserved from the TUI

- Sidebar grouping by repo (existing).
- Status icons: ● running/finished, ◐ waiting, ○ idle/starting, ✕ error.
- Slot bindings `[N]` badges.
- Tree lines for nested sessions in a repo.
- All keybindings (j/k nav, Space jump, etc.) work — just augmented with native Cmd-* equivalents.

## Sidebar — two view modes (toggle in View menu)

### Mode 1: Group by repo (default — same as TUI today)

```
▼  brizzai/brizzai
 ●  feature-x  [1]
 ◐  api-fix
 ○  docs-pr
▼  brizzai/fleet
 ●  desktop-app
```

### Mode 2: Group by status (Conductor-style)

```
▼  In progress (3)
 ◐  api-fix          · brizzai/brizzai
 ◐  bug-1234         · brizzai/fleet
 ●  feature-x  [1]   · brizzai/brizzai
▼  Backlog (5)
 ○  docs-pr          · brizzai/brizzai
 ○  …
▼  In review (2)
▼  Done (4)
```

The two modes show the same data, regrouped. User can toggle anytime. fleet's existing status enum (Idle / Running / Waiting / Finished / Error / Starting) maps to these groups:

| TUI status | Group label |
|---|---|
| Running | In progress |
| Waiting | In progress (with badge: "needs you") |
| Idle / Starting | Backlog |
| Finished | In review |
| (manually marked done) | Done |
| Error | In progress (with error badge) |

"Done" requires either a new manual marker or auto-derivation (e.g. session deleted with PR merged → moves to Done for 7 days then expires). Decide later.

## Session toolbar (top of pane)

```
Yuval/feature-x · main↗ · #1234 ✓               [restart] [editor] [open PR] [⋯]
```

- **Title** (auto-named or manually renamed) — click to inline-rename.
- **Branch indicator** with default-branch arrow if PR is against main.
- **PR badge** (existing colors: green ✓ approved, yellow pending, red ✕ failed, ↩ changes-requested, purple ⇡ merged).
- **Action buttons** — restart, open in editor, open PR — all wired to existing keybindings.
- **`⋯` overflow** for less-common actions (rename, delete, slot-bind, copy session ID, etc.).

## Suggested-actions bar (bottom) — V2

Conductor-style contextual buttons. Only enabled when relevant:

| Button | Enabled when | Keybind |
|---|---|---|
| Pull latest | branch behind upstream | `⌘⇧L` |
| Review | session has Finished + diff non-empty | `⌘⇧R` |
| Fix Errors | last build/test failed | `⌘⇧X` |
| Resolve conflicts | branch has unmerged paths | (no keybind) |
| Open PR | branch has remote, no PR yet | `⌘⇧P` |
| Merge | PR exists, approved + CI green | `⌘⇧M` |

Each button sends a slash command into the active Claude session via tmux send-keys (e.g. "Fix Errors" sends `/fix-errors` or a curated prompt). Or shells out to `gh` directly (e.g. "Merge" calls `gh pr merge`).

## V2 — multi-pane layout

```
┌────────────────────────┬───────────────────────────────────────────────────┐
│ ⌕ filter…              │  feature-x      [×]      api-fix          [×]    │
│                        │  ────────────────────    ──────────────────────   │
│ ▼ brizzai/brizzai      │                          │                       │
│  ●  feature-x ★        │   Claude TUI #1          │  Claude TUI #2         │
│  ◐  api-fix    ★       │                          │                       │
│  ○  docs-pr            │                          │                       │
│                        │                          │                       │
│ ▶ brizzai/fleet        │  ────────────────────    ──────────────────────   │
│                        │  docs-pr        [×]      bug-1234         [×]    │
│                        │  ────────────────────    ──────────────────────   │
│                        │                          │                       │
│                        │   Claude TUI #3          │  Claude TUI #4         │
│                        │                          │                       │
│                        │                          │                       │
└────────────────────────┴───────────────────────────────────────────────────┘
```

- ★ marker in sidebar = pinned to a pane.
- Click sidebar entry while holding `⌘` to add it to the next free pane (1→2→3→4).
- Drag from sidebar onto a pane to put it there.
- Each pane has its own `[×]` close to detach (session keeps running, just leaves the grid).
- Active pane has a colored border. Keystrokes go only to active pane.
- `⌘1` / `⌘2` / `⌘3` / `⌘4` switches active pane.
- Supports 1, 2 (split), 4 (2×2) layouts. Maybe 3 (1+2 split) later.

**Open question** (see `open-questions.md`): broadcast input mode (send same prompt to all panes) — useful for "spawn 3 agents on the same task." Toggle in View menu.

## Notifications

| Trigger | Notification |
|---|---|
| Session goes Waiting | macOS notification: "{title} is waiting for input" — clicking opens app and selects session |
| Session goes Finished | macOS notification: "{title} finished" — clicking opens app |
| Session errors | macOS notification with red badge |
| Multiple Waiting | Dock badge with unread count |

User can configure per-state in Settings ("notify on: Waiting / Finished / Error / All / None"). Default: notify on Waiting + Error.

## Keybindings (Cmd-prefix native shortcuts)

The TUI's letter keys (`a`, `n`, `w`, `d`, `r`, `R`, `e`, `p`, `Y`, `S`, `?`, `:`, `Ctrl+P`, etc.) keep working when focus is in the sidebar. Plus native Cmd-* equivalents from the menu bar:

| TUI key | Action | Mac shortcut |
|---|---|---|
| j / k | Navigate sidebar | ↑ / ↓ |
| Enter | Attach (focus pane) | Return |
| Space | Jump to next attention | (none — too risky) |
| a | New session at repo | ⌘N |
| n | New session (any repo) | ⇧⌘N |
| w | New worktree session | ⌥⌘N |
| d | Delete | ⌘⌫ |
| z | Undo delete | ⌘Z |
| r | Restart | ⌘R |
| R | Rename | (none — Return on selected row enters rename) |
| e | Open in editor | ⌘E |
| p | Open PR in browser | ⌘⇧P (also bound for "Open PR" action) |
| Y | Quick approve | ⌘Y |
| / | Filter | ⌘F |
| : / Ctrl+P | Command palette | ⌘K (shopping for the muscle memory; Ctrl+P also works) |
| S | Settings | ⌘, (standard macOS) |
| ! | Bug report | (Help menu → Report issue) |
| ? | Help | ⌘? |
| q | Quit | ⌘Q |

Slot bindings (Alt+0-9) → keep as Alt; Mac convention is Alt-* for "secondary" shortcuts. Plain 0-9 to jump-to-slot also kept.

## Native window features (free with SwiftUI)

- Multiple windows (`⌘N` for new fleet window — second sidebar + pane). Useful for two monitors.
- Full-screen mode.
- Split view with another app.
- Stage Manager support.
- Native dark mode / light mode (with theme override in settings — same 5 themes as TUI).
- VoiceOver / accessibility.
- Drag-drop a directory onto the dock icon → "open as repo, spawn session."
