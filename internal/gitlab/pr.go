// Package gitlab implements the forge.Provider interface on top of the `glab`
// CLI. A GitLab merge request is mapped onto the same shape fleet uses for
// GitHub pull requests (see internal/forge): state strings are normalised to the
// GitHub spellings, the pipeline status becomes the "CI status", and an MR with
// unresolved discussions is reported with a single unresolved-thread count so
// the badge lights up the same way.
//
// Known gap vs GitHub: pr_checks.ignore globs are not yet applied to GitLab
// pipeline jobs — the badge uses the head pipeline's rolled-up status as-is.
package gitlab

import (
	"encoding/json"
	"os/exec"
	"strconv"
	"strings"

	"github.com/brizzai/fleet/internal/debuglog"
	"github.com/brizzai/fleet/internal/forge"
)

// Provider fetches MR metadata via the GitLab CLI.
type Provider struct{}

// New returns a GitLab forge provider.
func New() *Provider { return &Provider{} }

// Name implements forge.Provider.
func (*Provider) Name() string { return "gitlab" }

// Available reports whether the `glab` CLI is installed and runnable.
func (*Provider) Available() bool {
	return exec.Command("glab", "--version").Run() == nil
}

// IsAvailable is the package-level shorthand for (&Provider{}).Available().
func IsAvailable() bool { return (&Provider{}).Available() }

// HostMatches reports whether host looks like a GitLab instance: gitlab.com, or
// any host containing "gitlab" (covers the common self-hosted naming, e.g.
// gitlab.example.com). Self-hosted instances on an unrelated hostname need an
// explicit `"forge": "gitlab"` in .fleet.json.
func HostMatches(host string) bool {
	host = strings.ToLower(host)
	return host == "gitlab.com" || strings.Contains(host, "gitlab")
}

type glabPipeline struct {
	ID     int    `json:"id"`
	Status string `json:"status"`
}

// glabMR is the subset of `glab mr view -F json` (the GitLab MR API object) we use.
type glabMR struct {
	IID                         int           `json:"iid"`
	Title                       string        `json:"title"`
	WebURL                      string        `json:"web_url"`
	State                       string        `json:"state"` // opened, closed, merged, locked
	ProjectID                   int           `json:"project_id"`
	HasConflicts                bool          `json:"has_conflicts"`
	DetailedMergeStatus         string        `json:"detailed_merge_status"`
	BlockingDiscussionsResolved *bool         `json:"blocking_discussions_resolved"`
	Pipeline                    *glabPipeline `json:"pipeline"`
	HeadPipeline                *glabPipeline `json:"head_pipeline"`
}

type glabApprovals struct {
	ApprovalsRequired int  `json:"approvals_required"`
	ApprovalsLeft     int  `json:"approvals_left"`
	Approved          bool `json:"approved"` // GitLab >= 16.0
	ApprovedBy        []struct {
		User struct {
			Username string `json:"username"`
		} `json:"user"`
	} `json:"approved_by"`
}

// GetPRForBranch returns the MR whose source branch is branch, or nil if none.
// ignorePatterns is accepted for interface parity with the GitHub provider but
// is not yet applied to GitLab pipeline jobs.
func (*Provider) GetPRForBranch(repoPath, branch string, _ []string) (*forge.PR, error) {
	if branch == "" || branch == "HEAD" {
		return nil, nil
	}

	cmd := exec.Command("glab", "mr", "view", branch, "-F", "json")
	cmd.Dir = repoPath
	output, err := cmd.Output()
	if err != nil {
		// glab exits non-zero when no MR exists for the branch, or when the
		// host isn't authenticated — both expected, not surfaced as an error.
		return nil, nil
	}

	pr, mr, err := parseMR(output)
	if err != nil {
		debuglog.Logger.Debug("gitlab: GetPRForBranch JSON parse failed", "path", repoPath, "branch", branch, "error", err)
		return nil, nil
	}
	if pr == nil {
		return nil, nil
	}
	pr.ReviewDecision = reviewDecision(repoPath, mr.ProjectID, mr.IID)
	return pr, nil
}

// parseMR parses `glab mr view -F json` output into a normalised PR. The
// ReviewDecision is left empty here — it needs a separate approvals API call
// (see reviewDecision). Returns (nil, _, nil) when the payload contains no MR.
func parseMR(data []byte) (*forge.PR, glabMR, error) {
	var mr glabMR
	if err := json.Unmarshal(data, &mr); err != nil {
		return nil, mr, err
	}
	if mr.IID == 0 {
		return nil, mr, nil
	}

	unresolved := 0
	if mr.BlockingDiscussionsResolved != nil && !*mr.BlockingDiscussionsResolved {
		unresolved = 1 // glab mr view doesn't give a count; 1 is enough to flag the badge
	}

	return &forge.PR{
		Number:            mr.IID,
		Title:             mr.Title,
		URL:               mr.WebURL,
		State:             normalizeState(mr.State),
		CIStatus:          pipelineStatus(mr.HeadPipeline, mr.Pipeline),
		UnresolvedThreads: unresolved,
		HasConflicts:      mr.HasConflicts || strings.EqualFold(mr.DetailedMergeStatus, "conflict"),
		Forge:             "gitlab",
	}, mr, nil
}

// normalizeState maps GitLab MR states onto the GitHub spellings used by the badge.
func normalizeState(state string) string {
	switch strings.ToLower(state) {
	case "merged":
		return "MERGED"
	case "opened":
		return "OPEN"
	default: // closed, locked
		return "CLOSED"
	}
}

// pipelineStatus picks the head pipeline (latest) when present, falling back to
// the MR's pipeline field, and maps the GitLab status onto SUCCESS/FAILURE/PENDING.
// Neutral terminal states (canceled, skipped, manual) and an absent pipeline map
// to "" so they don't colour the badge.
func pipelineStatus(head, fallback *glabPipeline) string {
	p := head
	if p == nil {
		p = fallback
	}
	if p == nil {
		return ""
	}
	switch strings.ToLower(p.Status) {
	case "success":
		return "SUCCESS"
	case "failed":
		return "FAILURE"
	case "running", "pending", "created", "preparing", "waiting_for_resource", "scheduled":
		return "PENDING"
	default:
		return ""
	}
}

// reviewDecision returns "APPROVED" when the MR has met its approval requirement,
// else "". GitLab has no clean per-MR "changes requested" signal, so that case
// isn't reported here — unresolved discussions still flag the badge separately.
func reviewDecision(repoPath string, projectID, iid int) string {
	if projectID == 0 || iid == 0 {
		return ""
	}
	endpoint := "projects/" + strconv.Itoa(projectID) + "/merge_requests/" + strconv.Itoa(iid) + "/approvals"
	cmd := exec.Command("glab", "api", endpoint)
	cmd.Dir = repoPath
	output, err := cmd.Output()
	if err != nil {
		debuglog.Logger.Debug("gitlab: approvals query failed", "project", projectID, "mr", iid, "error", err)
		return ""
	}
	var ap glabApprovals
	if err := json.Unmarshal(output, &ap); err != nil {
		debuglog.Logger.Debug("gitlab: approvals JSON parse failed", "project", projectID, "mr", iid, "error", err)
		return ""
	}
	return approvalDecision(ap)
}

// approvalDecision derives the GitHub-style review decision from a GitLab
// approvals payload: "APPROVED" when the approval rule is satisfied (or, when no
// approvals are required, at least one approval exists), else "".
func approvalDecision(ap glabApprovals) string {
	approved := ap.Approved ||
		(ap.ApprovalsRequired > 0 && ap.ApprovalsLeft == 0) ||
		(ap.ApprovalsRequired == 0 && len(ap.ApprovedBy) > 0)
	if approved {
		return "APPROVED"
	}
	return ""
}
