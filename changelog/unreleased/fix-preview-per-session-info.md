---
type: fixed
---
Preview pane now shows the selected session's own branch, dirty indicator, and PR/MR badge when it's worktree-backed. Previously the header always rendered the main repo's current branch (e.g. `master`), which became wrong once per-session worktrees became the default for new sessions.
