// Package daemonclient is the gRPC client implementation of service.Service.
// It speaks to a `fleet daemon` instance over a Unix domain socket and keeps
// a local cache of sessions, repos, and slot bindings populated by the
// server's streaming RPCs.
package daemonclient

import (
	fleetv1 "github.com/brizzai/fleet/gen/proto/fleet/v1"
	"github.com/brizzai/fleet/internal/git"
	"github.com/brizzai/fleet/internal/github"
	"github.com/brizzai/fleet/internal/session"
)

// protoToSession reconstructs a *session.Session from its proto wire form.
// Goes through session.FromRow so the result has a real *tmux.Session
// handle wired up via tmux.ReconnectSession — this is what makes
// IsAlive(), GetTmuxSession(), pane capture, and PTY attach work the same
// way on a daemon-resident session as on an in-process one. Both processes
// share the host's tmux server, so the named session is reachable from
// either side.
func protoToSession(p *fleetv1.Session) *session.Session {
	if p == nil {
		return nil
	}
	row := &session.SessionRow{
		ID:              p.GetId(),
		Title:           p.GetTitle(),
		ProjectPath:     p.GetProjectPath(),
		Status:          string(protoStatusToSessionStatus(p.GetStatus())),
		TmuxSession:     p.GetTmuxSessionName(),
		CreatedAt:       p.GetCreatedAt().AsTime(),
		LastAccessed:    p.GetLastAccessedAt().AsTime(),
		Acknowledged:    p.GetAcknowledged(),
		ClaudeSessionID: p.GetClaudeSessionId(),
		WorkspaceName:   p.GetWorkspaceName(),
		ManuallyRenamed: p.GetManuallyRenamed(),
		FirstPrompt:     p.GetFirstPrompt(),
		TitleGenerated:  p.GetTitleGenerated(),
		PromptCount:     int(p.GetPromptCount()),
	}
	sess := session.FromRow(row)
	// Transient fields that aren't carried in SessionRow.
	sess.ClaudeSessionName = p.GetClaudeSessionName()
	sess.ForkFromID = p.GetForkFromId()
	return sess
}

// protoStatusToSessionStatus maps the proto enum to the internal string-typed
// session.Status. Unknown enums fall through to StatusIdle so a misbehaving
// daemon doesn't desync the TUI display.
func protoStatusToSessionStatus(p fleetv1.Status) session.Status {
	switch p {
	case fleetv1.Status_STATUS_RUNNING:
		return session.StatusRunning
	case fleetv1.Status_STATUS_WAITING:
		return session.StatusWaiting
	case fleetv1.Status_STATUS_FINISHED:
		return session.StatusFinished
	case fleetv1.Status_STATUS_IDLE:
		return session.StatusIdle
	case fleetv1.Status_STATUS_ERROR:
		return session.StatusError
	case fleetv1.Status_STATUS_STARTING:
		return session.StatusStarting
	default:
		return session.StatusIdle
	}
}

// protoToRepoInfo mirrors daemonsrv.convertRepo in reverse. Every field on
// git.RepoInfo has a counterpart on fleetv1.Repo, so this is a straight
// field copy.
func protoToRepoInfo(p *fleetv1.Repo) *git.RepoInfo {
	if p == nil {
		return nil
	}
	return &git.RepoInfo{
		Branch:         p.GetBranch(),
		IsDirty:        p.GetIsDirty(),
		IsWorktreeRepo: p.GetIsWorktreeRepo(),
		PR:             protoToPR(p.GetPr()),
		LastGitRefresh: p.GetLastGitRefresh().AsTime(),
		LastPRRefresh:  p.GetLastPrRefresh().AsTime(),
	}
}

func protoToPR(p *fleetv1.PR) *github.PR {
	if p == nil {
		return nil
	}
	return &github.PR{
		Number:            int(p.GetNumber()),
		Title:             p.GetTitle(),
		URL:               p.GetUrl(),
		State:             p.GetState(),
		ReviewDecision:    p.GetReviewDecision(),
		CIStatus:          p.GetCiStatus(),
		UnresolvedThreads: int(p.GetUnresolvedThreads()),
		HasConflicts:      p.GetHasConflicts(),
	}
}
