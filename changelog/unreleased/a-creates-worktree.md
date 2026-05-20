---
type: changed
---
`a` now creates a new session in a fresh `claude/<8hex>` worktree by default (the old `n` behaviour). Plain repo-scoped sessions (no worktree) are no longer one-keystroke — use "New Session at Path" from the command palette (`:` / `Ctrl+P`) for the rare case where you want a session that lives directly on the main worktree. The `n` binding has been removed.
