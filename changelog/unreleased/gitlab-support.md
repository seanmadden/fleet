---
type: added
---
GitLab support: repos hosted on GitLab now get a merge-request badge on the repo header (`!N` instead of `#N`), driven by the `glab` CLI — pipeline status, merge conflicts, approval state and unresolved discussions, refreshed on the same cadence as GitHub PRs. `p` opens the MR in the browser. The forge is auto-detected from the `origin` remote URL (gitlab.com or any `*gitlab*` host); self-hosted instances on an unrelated hostname can force it with `"forge": "gitlab"` in `.fleet.json`. `glab` is optional, exactly like `gh` — install it (`brew install glab`) and authenticate (`glab auth login`) to see MR badges. Known gap vs GitHub: `pr_checks.ignore` globs aren't yet applied to GitLab pipeline jobs (the head pipeline's rolled-up status is used as-is).
