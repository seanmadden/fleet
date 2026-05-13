package git

import (
	"time"

	"github.com/brizzai/fleet/internal/debuglog"
	"github.com/brizzai/fleet/internal/forge"
)

// RepoInfo holds cached git and PR metadata for a repository.
type RepoInfo struct {
	Branch         string
	IsDirty        bool
	IsWorktreeRepo bool
	PR             *forge.PR
	LastGitRefresh time.Time
	LastPRRefresh  time.Time
}

// RefreshGitInfo fetches branch, dirty status, and worktree info for a repo.
// Fast operation (<10ms, all local git commands).
func RefreshGitInfo(repoPath string) *RepoInfo {
	return &RepoInfo{
		Branch:         GetBranchName(repoPath),
		IsDirty:        HasUncommittedChanges(repoPath),
		IsWorktreeRepo: IsWorktree(repoPath),
		LastGitRefresh: time.Now(),
	}
}

// RefreshPRInfo fetches PR/MR info via the repo's forge provider and updates the
// RepoInfo. Slower operation (~200ms, network call). A nil provider (repo has no
// recognised forge, or the forge's CLI isn't installed) is a no-op that leaves
// any cached PR in place. ignorePatterns is the per-repo CI-check ignore list
// (path.Match globs); the caller loads it (typically via workspace.IgnorePatterns)
// to keep this package free of a workspace-package dependency.
func RefreshPRInfo(info *RepoInfo, repoPath string, ignorePatterns []string, provider forge.Provider) {
	if provider == nil {
		return
	}
	pr, err := provider.GetPRForBranch(repoPath, info.Branch, ignorePatterns)
	if err != nil {
		debuglog.Logger.Debug("RefreshPRInfo failed", "path", repoPath, "branch", info.Branch, "forge", provider.Name(), "error", err)
	}
	info.PR = pr
	info.LastPRRefresh = time.Now()
}
