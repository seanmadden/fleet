package ui

import (
	"github.com/brizzai/fleet/internal/debuglog"
	"github.com/brizzai/fleet/internal/forge"
	"github.com/brizzai/fleet/internal/git"
	"github.com/brizzai/fleet/internal/github"
	"github.com/brizzai/fleet/internal/gitlab"
	"github.com/brizzai/fleet/internal/workspace"
)

// detectForge picks the forge provider for a repo by inspecting its origin
// remote (and any `.fleet.json` "forge" override), returning nil when the repo
// has no recognised forge or the matching CLI (gh / glab) isn't installed.
//
// Detection order:
//  1. `.fleet.json` "forge" override — wins outright.
//  2. origin host looks like GitLab (gitlab.com / *gitlab*) → GitLab.
//  3. origin host looks like GitHub (github.com / github.*) → GitHub.
//  4. fallback: if `gh` is installed, assume GitHub. This preserves the
//     pre-GitLab behaviour for GHE hosts on unrelated hostnames (fleet used to
//     run `gh pr view` against every repo regardless of remote).
//
// Result is cached per repo by the caller — a repo's forge identity is stable
// for the life of the process.
func detectForge(repoPath string) forge.Provider {
	switch workspace.ForgeOverride(repoPath) {
	case "github":
		return availableOrNil(github.New())
	case "gitlab":
		return availableOrNil(gitlab.New())
	}

	host, _ := forge.ParseRemote(git.RemoteURL(repoPath))
	switch {
	case gitlab.HostMatches(host):
		return availableOrNil(gitlab.New())
	case github.HostMatches(host):
		return availableOrNil(github.New())
	default:
		if github.IsAvailable() {
			return github.New()
		}
		return nil
	}
}

// availableOrNil returns p if its CLI is installed, else nil (and logs why).
func availableOrNil(p forge.Provider) forge.Provider {
	if p.Available() {
		return p
	}
	debuglog.Logger.Debug("forge: CLI not available, PR badge disabled for this forge", "forge", p.Name())
	return nil
}
