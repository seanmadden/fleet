---
type: changed
---
`n` now creates a fresh `claude/<8hex>` worktree at the cursor instantly — no dialog. The branch auto-renames to `claude/<slug>` once the first auto-title resolves. `w` (the old "new worktree" dialog) is removed; the command palette gains "New Session in Existing Worktree" for attaching to a worktree fleet didn't create and "New Session at Path" for the historic path picker. Fleet never pushes or opens MRs — that's still up to you.
