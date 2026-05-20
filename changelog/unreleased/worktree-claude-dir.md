---
type: changed
---
Per-session worktrees now live inside the main repo at `.claude/worktrees/<name>` instead of as sibling directories (`<repo>-<name>`). This matches Claude Code's own Agent-isolation convention and keeps the parent directory clean. Fleet adds `/.claude/worktrees/` to the repo's `.git/info/exclude` on first create so the directory stays invisible to `git status` on the main checkout — no tracked `.gitignore` change required. Worktrees created by older fleet builds at the old sibling path are still destroyable by name.
