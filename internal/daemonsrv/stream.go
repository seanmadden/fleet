package daemonsrv

import (
	"google.golang.org/grpc"

	fleetv1 "github.com/brizzai/fleet/gen/proto/fleet/v1"
	"github.com/brizzai/fleet/internal/service"
	"github.com/brizzai/fleet/internal/session"
)

// deltaObserver bridges service.Event fan-out into a per-stream channel.
// Drops are intentional: the stream loop recomputes deltas from current
// service state on every wake-up, so a missed coalesced wake-up just means
// the next one carries cumulative changes.
type deltaObserver struct {
	ch chan struct{}
}

func newDeltaObserver(buf int) *deltaObserver {
	return &deltaObserver{ch: make(chan struct{}, buf)}
}

func (d *deltaObserver) OnEvent(_ service.Event) {
	select {
	case d.ch <- struct{}{}:
	default:
		// Coalesce: a wake-up is already pending; nothing to do.
	}
}

// ── Sessions ───────────────────────────────────────────────────────────────

// sessionFingerprint captures only the fields convertSession projects, so two
// sessions with identical fingerprints produce wire-equal SessionUpdates and
// we can suppress no-op CHANGED deltas. Kept comparable (no slices/maps) so
// it can be used directly as a map value.
type sessionFingerprint struct {
	title             string
	projectPath       string
	st                session.Status
	tmuxName          string
	acknowledged      bool
	claudeSessionID   string
	claudeSessionName string
	workspaceName     string
	manuallyRenamed   bool
	firstPrompt       string
	titleGenerated    bool
	promptCount       int
	forkFromID        string
	isAlive           bool
}

func fingerprintOf(s *session.Session) sessionFingerprint {
	return sessionFingerprint{
		title:             s.Title,
		projectPath:       s.ProjectPath,
		st:                s.Status,
		tmuxName:          s.TmuxSessionName,
		acknowledged:      s.Acknowledged,
		claudeSessionID:   s.ClaudeSessionID,
		claudeSessionName: s.ClaudeSessionName,
		workspaceName:     s.WorkspaceName,
		manuallyRenamed:   s.ManuallyRenamed,
		firstPrompt:       s.FirstPrompt,
		titleGenerated:    s.TitleGenerated,
		promptCount:       s.PromptCount,
		forkFromID:        s.ForkFromID,
		isAlive:           safeIsAlive(s),
	}
}

func filterByRepo(sessions []*session.Session, repoFilter string) []*session.Session {
	if repoFilter == "" {
		return sessions
	}
	out := make([]*session.Session, 0, len(sessions))
	for _, s := range sessions {
		if session.GetRepoRoot(s.ProjectPath) == repoFilter {
			out = append(out, s)
		}
	}
	return out
}

// ListSessions sends a SNAPSHOT for every existing session, then continues
// to push ADDED / CHANGED / REMOVED deltas as the SessionService observer
// fires. Subscription happens BEFORE the snapshot so we don't miss events
// that fire mid-snapshot — clients tolerate a duplicate ADDED for an ID
// already seen in the snapshot.
func (s *Server) ListSessions(req *fleetv1.ListSessionsRequest, stream grpc.ServerStreamingServer[fleetv1.SessionUpdate]) error {
	obs := newDeltaObserver(64)
	s.svc.Subscribe(obs)
	defer s.svc.Unsubscribe(obs)

	last := map[string]sessionFingerprint{}

	for _, sess := range filterByRepo(s.svc.Sessions(), req.GetRepoRootFilter()) {
		if err := stream.Send(&fleetv1.SessionUpdate{
			Kind:    fleetv1.SessionUpdateKind_SESSION_UPDATE_KIND_SNAPSHOT,
			Session: convertSession(sess),
		}); err != nil {
			return err
		}
		last[sess.ID] = fingerprintOf(sess)
	}

	for {
		select {
		case <-stream.Context().Done():
			return nil
		case <-obs.ch:
			if err := emitSessionDeltas(stream, s.svc, req.GetRepoRootFilter(), last); err != nil {
				return err
			}
		}
	}
}

func emitSessionDeltas(
	stream grpc.ServerStreamingServer[fleetv1.SessionUpdate],
	svc service.Service,
	repoFilter string,
	last map[string]sessionFingerprint,
) error {
	current := filterByRepo(svc.Sessions(), repoFilter)
	currentIDs := make(map[string]struct{}, len(current))

	for _, sess := range current {
		currentIDs[sess.ID] = struct{}{}
		fp := fingerprintOf(sess)
		prev, existed := last[sess.ID]
		switch {
		case !existed:
			if err := stream.Send(&fleetv1.SessionUpdate{
				Kind:    fleetv1.SessionUpdateKind_SESSION_UPDATE_KIND_ADDED,
				Session: convertSession(sess),
			}); err != nil {
				return err
			}
		case prev != fp:
			if err := stream.Send(&fleetv1.SessionUpdate{
				Kind:    fleetv1.SessionUpdateKind_SESSION_UPDATE_KIND_CHANGED,
				Session: convertSession(sess),
			}); err != nil {
				return err
			}
		}
		last[sess.ID] = fp
	}

	for id := range last {
		if _, alive := currentIDs[id]; alive {
			continue
		}
		if err := stream.Send(&fleetv1.SessionUpdate{
			Kind:      fleetv1.SessionUpdateKind_SESSION_UPDATE_KIND_REMOVED,
			RemovedId: id,
		}); err != nil {
			return err
		}
		delete(last, id)
	}
	return nil
}

// ── Repos ──────────────────────────────────────────────────────────────────

// repoFingerprint mirrors sessionFingerprint for the Repo wire shape.
// Times are excluded — clients shouldn't churn on cosmetic refresh
// timestamps; clients re-fetch the timestamps via the next CHANGED that
// has a real diff.
type repoFingerprint struct {
	displayName      string
	branch           string
	isDirty          bool
	isWorktreeRepo   bool
	pinned           bool
	prNumber         int32
	prState          string
	prReviewDecision string
	prCIStatus       string
	prUnresolved     int32
	prHasConflicts   bool
}

func fingerprintRepo(r *fleetv1.Repo) repoFingerprint {
	fp := repoFingerprint{
		displayName:    r.DisplayName,
		branch:         r.Branch,
		isDirty:        r.IsDirty,
		isWorktreeRepo: r.IsWorktreeRepo,
		pinned:         r.Pinned,
	}
	if pr := r.Pr; pr != nil {
		fp.prNumber = pr.Number
		fp.prState = pr.State
		fp.prReviewDecision = pr.ReviewDecision
		fp.prCIStatus = pr.CiStatus
		fp.prUnresolved = pr.UnresolvedThreads
		fp.prHasConflicts = pr.HasConflicts
	}
	return fp
}

func (s *Server) ListRepos(_ *fleetv1.ListReposRequest, stream grpc.ServerStreamingServer[fleetv1.RepoUpdate]) error {
	obs := newDeltaObserver(64)
	s.svc.Subscribe(obs)
	defer s.svc.Unsubscribe(obs)

	last := map[string]repoFingerprint{}

	for _, r := range derivedRepos(s.svc) {
		if err := stream.Send(&fleetv1.RepoUpdate{
			Kind: fleetv1.RepoUpdateKind_REPO_UPDATE_KIND_SNAPSHOT,
			Repo: r,
		}); err != nil {
			return err
		}
		last[r.Root] = fingerprintRepo(r)
	}

	for {
		select {
		case <-stream.Context().Done():
			return nil
		case <-obs.ch:
			if err := emitRepoDeltas(stream, s.svc, last); err != nil {
				return err
			}
		}
	}
}

func emitRepoDeltas(
	stream grpc.ServerStreamingServer[fleetv1.RepoUpdate],
	svc service.Service,
	last map[string]repoFingerprint,
) error {
	current := derivedRepos(svc)
	currentRoots := make(map[string]struct{}, len(current))

	for _, r := range current {
		currentRoots[r.Root] = struct{}{}
		fp := fingerprintRepo(r)
		prev, existed := last[r.Root]
		switch {
		case !existed:
			if err := stream.Send(&fleetv1.RepoUpdate{
				Kind: fleetv1.RepoUpdateKind_REPO_UPDATE_KIND_ADDED,
				Repo: r,
			}); err != nil {
				return err
			}
		case prev != fp:
			if err := stream.Send(&fleetv1.RepoUpdate{
				Kind: fleetv1.RepoUpdateKind_REPO_UPDATE_KIND_CHANGED,
				Repo: r,
			}); err != nil {
				return err
			}
		}
		last[r.Root] = fp
	}

	for root := range last {
		if _, alive := currentRoots[root]; alive {
			continue
		}
		if err := stream.Send(&fleetv1.RepoUpdate{
			Kind:        fleetv1.RepoUpdateKind_REPO_UPDATE_KIND_REMOVED,
			RemovedRoot: root,
		}); err != nil {
			return err
		}
		delete(last, root)
	}
	return nil
}
