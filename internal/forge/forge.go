// Package forge abstracts over code-hosting platforms (GitHub, GitLab) so the
// rest of fleet can render a single "PR badge" without caring which forge a repo
// lives on. Each concrete forge lives in its own package (internal/github,
// internal/gitlab) and implements Provider; the ui layer picks the right one per
// repo via remote-URL inspection (see internal/ui detectForge).
//
// The vocabulary here is GitHub-flavoured ("PR", "ReviewDecision") because that's
// what the codebase grew up with — a GitLab merge request maps onto the same
// shape, with state strings normalised to the GitHub spellings.
package forge

import "strings"

// PR is a normalised pull/merge request. State, ReviewDecision and CIStatus use
// the GitHub spellings regardless of the underlying forge so renderPRBadge stays
// forge-agnostic.
type PR struct {
	Number            int
	Title             string
	URL               string
	State             string // OPEN, CLOSED, MERGED
	ReviewDecision    string // APPROVED, CHANGES_REQUESTED, REVIEW_REQUIRED, ""
	CIStatus          string // SUCCESS, FAILURE, PENDING, ""
	UnresolvedThreads int    // count of unresolved review threads / discussions
	HasConflicts      bool   // forge reports merge conflicts
	Forge             string // "github" | "gitlab" — drives badge labelling (#N vs !N)
}

// Provider fetches PR/MR metadata for a single forge.
type Provider interface {
	// Name is the forge identifier ("github" | "gitlab").
	Name() string
	// Available reports whether the backing CLI (gh / glab) is installed.
	Available() bool
	// GetPRForBranch returns the PR/MR for branch, or nil if there is none.
	// ignorePatterns are path.Match globs applied to CI check / job names before
	// the CI status is rolled up (see workspace.IgnorePatterns).
	GetPRForBranch(repoPath, branch string, ignorePatterns []string) (*PR, error)
}

// ParseRemote extracts the host and "owner/repo" path (with any GitLab
// subgroups, no ".git" suffix) from a git remote URL. It handles the common
// forms:
//
//	https://host/owner/repo(.git)
//	http://host/owner/repo(.git)
//	git@host:owner/repo(.git)
//	ssh://git@host[:port]/owner/repo(.git)
//	host:owner/repo(.git)            (scp-like, no user)
//
// On anything it can't parse it returns ("", "").
func ParseRemote(rawURL string) (host, path string) {
	s := strings.TrimSpace(rawURL)
	if s == "" {
		return "", ""
	}

	switch {
	case strings.HasPrefix(s, "ssh://"):
		s = strings.TrimPrefix(s, "ssh://")
		s = stripUserInfo(s)
		host, path = splitHostPath(s, "/")
	case strings.HasPrefix(s, "https://"):
		host, path = splitHostPath(strings.TrimPrefix(s, "https://"), "/")
	case strings.HasPrefix(s, "http://"):
		host, path = splitHostPath(strings.TrimPrefix(s, "http://"), "/")
	case strings.HasPrefix(s, "git://"):
		host, path = splitHostPath(strings.TrimPrefix(s, "git://"), "/")
	default:
		// scp-like: [user@]host:owner/repo
		s = stripUserInfo(s)
		host, path = splitHostPath(s, ":")
	}

	// Drop a port if one snuck through (ssh://git@host:22/...).
	if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	path = strings.TrimPrefix(path, "/")
	path = strings.TrimSuffix(path, "/")
	path = strings.TrimSuffix(path, ".git")
	return host, path
}

func stripUserInfo(s string) string {
	if i := strings.IndexByte(s, '@'); i >= 0 {
		return s[i+1:]
	}
	return s
}

// splitHostPath splits s into the part before the first sep and the rest.
func splitHostPath(s, sep string) (string, string) {
	i := strings.Index(s, sep)
	if i < 0 {
		return s, ""
	}
	return s[:i], s[i+len(sep):]
}
