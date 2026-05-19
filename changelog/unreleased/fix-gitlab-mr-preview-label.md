---
type: fixed
---
Preview pane now labels GitLab merge requests correctly (`MR !N` instead of `PR #N`). On GitLab `#N` refers to a work item / issue, so labelling an MR with `#` was actively misleading. Static UI labels (help bar, command palette, action log, error messages) now read "PR / MR" so they're accurate on both forges; the sidebar badge already showed `!N` for GitLab.
