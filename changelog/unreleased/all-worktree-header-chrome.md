---
type: improved
---
Sidebar repo group header now hides its main-repo branch, dirty `*`, and PR badge when every session in the group is worktree-backed. The per-session rows already carry that info (each worktree has its own branch + PR), so the header chrome was just noise — typically pointing at `master`/`main` and the main worktree's dirty state, which had nothing to do with where work was actually happening. Mixed groups (any non-worktree session under the header) keep their header chrome.
