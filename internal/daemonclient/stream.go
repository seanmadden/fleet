package daemonclient

import (
	"context"
	"errors"
	"io"
	"sort"
	"time"

	fleetv1 "github.com/brizzai/fleet/gen/proto/fleet/v1"
	"github.com/brizzai/fleet/internal/debuglog"
	"github.com/brizzai/fleet/internal/service"
	"github.com/brizzai/fleet/internal/session"
	"google.golang.org/grpc"
)

// Reconnect backoff. Caps at 5s — long enough that a daemon respawning
// through `fleet daemon --detach` (which takes ~200ms in the cold-start
// path) won't hammer the socket.
var reconnectBackoffSchedule = []time.Duration{
	250 * time.Millisecond,
	1 * time.Second,
	4 * time.Second,
	5 * time.Second,
}

// runSessionStream owns the ListSessions stream and its reconnect loop.
// On EOF or transport error it fires EventError, sleeps with exponential
// backoff, and re-opens the stream. On reconnect the server re-snapshots,
// at which point we *replace* the session cache wholesale so dropped
// REMOVED events don't leave ghost entries behind.
func (c *Client) runSessionStream() {
	defer c.wg.Done()
	attempt := 0
	for {
		if c.ctx.Err() != nil {
			return
		}
		err := c.consumeSessionStream()
		if errors.Is(err, context.Canceled) || c.ctx.Err() != nil {
			return
		}
		debuglog.Logger.Warn("daemonclient: session stream broken, reconnecting", "err", err, "attempt", attempt)
		c.notify(service.Event{Type: service.EventError, Message: "daemon disconnected — reconnecting"})
		if !sleepBackoff(c.ctx, attempt) {
			return
		}
		attempt++
	}
}

// consumeSessionStream opens one ListSessions stream and consumes it until
// EOF or error. Returns nil only on graceful shutdown (caller-cancelled
// context); any other return is a reason to reconnect. Snapshot semantics:
// the first run of SNAPSHOT-kind messages replaces the cache; subsequent
// non-SNAPSHOT kinds apply as deltas. On reconnect the same sequence
// repeats — `seenIDs` tracks what the snapshot delivered so we can drop
// pre-disconnect entries the new snapshot didn't carry.
func (c *Client) consumeSessionStream() error {
	stream, err := c.api.ListSessions(c.ctx, &fleetv1.ListSessionsRequest{})
	if err != nil {
		return err
	}

	seenIDs := map[string]bool{}
	inSnapshot := true

	for {
		msg, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
				return err
			}
			return err
		}

		switch msg.GetKind() {
		case fleetv1.SessionUpdateKind_SESSION_UPDATE_KIND_SNAPSHOT:
			c.applySnapshotSession(msg.GetSession())
			seenIDs[msg.GetSession().GetId()] = true

		case fleetv1.SessionUpdateKind_SESSION_UPDATE_KIND_ADDED,
			fleetv1.SessionUpdateKind_SESSION_UPDATE_KIND_CHANGED:
			if inSnapshot {
				c.finalizeSnapshot(seenIDs)
				inSnapshot = false
			}
			c.applyDeltaSession(msg.GetSession(), msg.GetKind() == fleetv1.SessionUpdateKind_SESSION_UPDATE_KIND_CHANGED)

		case fleetv1.SessionUpdateKind_SESSION_UPDATE_KIND_REMOVED:
			if inSnapshot {
				c.finalizeSnapshot(seenIDs)
				inSnapshot = false
			}
			c.removeSessionFromStream(msg.GetRemovedId())

		default:
			debuglog.Logger.Debug("daemonclient: unknown session update kind", "kind", msg.GetKind())
		}
	}
}

// finalizeSnapshot prunes any pre-disconnect cache entries the new snapshot
// didn't carry. Called once the stream transitions from SNAPSHOT-kind
// messages to delta kinds.
func (c *Client) finalizeSnapshot(seenIDs map[string]bool) {
	c.mu.Lock()
	pruned := false
	for id := range c.sessions {
		if !seenIDs[id] {
			delete(c.sessions, id)
			pruned = true
		}
	}
	if pruned {
		c.rebuildSessionList()
	}
	c.mu.Unlock()
	if pruned {
		c.notify(service.Event{Type: service.EventSessionsChanged})
	}
}

// applySnapshotSession upserts during the initial snapshot phase. It does
// NOT fire individual events for each entry — the snapshot is a bulk
// rebuild, and the UI only needs one EventSessionsChanged once the
// snapshot completes (handled by the first delta kind, or by Start's
// caller via observer subscription before stream open).
func (c *Client) applySnapshotSession(p *fleetv1.Session) {
	if p == nil {
		return
	}
	sess := protoToSession(p)
	c.mu.Lock()
	c.sessions[sess.ID] = sess
	c.rebuildSessionList()
	c.mu.Unlock()
	// Fire EventSessionsChanged on every snapshot entry — the UI's observer
	// is bounded-channel + coalesced, so this is cheap.
	c.notify(service.Event{Type: service.EventSessionsChanged})
}

func (c *Client) applyDeltaSession(p *fleetv1.Session, isChange bool) {
	if p == nil {
		return
	}
	sess := protoToSession(p)
	c.mu.Lock()
	prev, existed := c.sessions[sess.ID]
	c.sessions[sess.ID] = sess
	c.rebuildSessionList()
	c.mu.Unlock()

	// Fire the appropriate event for downstream observers. CHANGED with a
	// status flip mirrors the in-process worker firing
	// EventSessionStatusChanged, so the UI's status-driven highlights
	// react identically.
	if isChange && existed && prev != nil && prev.Status != sess.Status {
		c.notify(service.Event{Type: service.EventSessionStatusChanged})
		return
	}
	c.notify(service.Event{Type: service.EventSessionsChanged})
}

func (c *Client) removeSessionFromStream(id string) {
	if id == "" {
		return
	}
	c.mu.Lock()
	if _, existed := c.sessions[id]; !existed {
		c.mu.Unlock()
		return
	}
	delete(c.sessions, id)
	c.rebuildSessionList()
	for slot, sid := range c.slotBind {
		if sid == id {
			delete(c.slotBind, slot)
		}
	}
	c.mu.Unlock()
	c.notify(service.Event{Type: service.EventSessionsChanged})
}

// runRepoStream is the same shape as runSessionStream but for ListRepos.
// Repo state is derived from sessions + pinned set on the daemon, so a
// repo CHANGED almost always reflects a git/PR refresh — we fan out as
// EventSessionsChanged because that's the event the UI uses to redraw
// the sidebar (there is no consumer of EventGitInfoChanged in internal/ui).
func (c *Client) runRepoStream() {
	defer c.wg.Done()
	attempt := 0
	for {
		if c.ctx.Err() != nil {
			return
		}
		err := c.consumeRepoStream()
		if errors.Is(err, context.Canceled) || c.ctx.Err() != nil {
			return
		}
		debuglog.Logger.Warn("daemonclient: repo stream broken, reconnecting", "err", err, "attempt", attempt)
		// Don't double-fire EventError if the session stream already did —
		// they reconnect in lockstep. Just back off quietly.
		if !sleepBackoff(c.ctx, attempt) {
			return
		}
		attempt++
	}
}

func (c *Client) consumeRepoStream() error {
	stream, err := c.api.ListRepos(c.ctx, &fleetv1.ListReposRequest{})
	if err != nil {
		return err
	}

	seenRoots := map[string]bool{}
	inSnapshot := true

	for {
		msg, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
				return err
			}
			return err
		}

		switch msg.GetKind() {
		case fleetv1.RepoUpdateKind_REPO_UPDATE_KIND_SNAPSHOT:
			c.applySnapshotRepo(msg.GetRepo())
			seenRoots[msg.GetRepo().GetRoot()] = true

		case fleetv1.RepoUpdateKind_REPO_UPDATE_KIND_ADDED,
			fleetv1.RepoUpdateKind_REPO_UPDATE_KIND_CHANGED:
			if inSnapshot {
				c.finalizeRepoSnapshot(seenRoots)
				inSnapshot = false
			}
			c.applyDeltaRepo(msg.GetRepo())

		case fleetv1.RepoUpdateKind_REPO_UPDATE_KIND_REMOVED:
			if inSnapshot {
				c.finalizeRepoSnapshot(seenRoots)
				inSnapshot = false
			}
			c.removeRepoFromStream(msg.GetRemovedRoot())

		default:
			debuglog.Logger.Debug("daemonclient: unknown repo update kind", "kind", msg.GetKind())
		}
	}
}

func (c *Client) finalizeRepoSnapshot(seenRoots map[string]bool) {
	c.mu.Lock()
	for root := range c.repos {
		if !seenRoots[root] {
			delete(c.repos, root)
		}
	}
	for root := range c.pinned {
		if !seenRoots[root] {
			delete(c.pinned, root)
		}
	}
	c.mu.Unlock()
}

func (c *Client) applySnapshotRepo(p *fleetv1.Repo) {
	if p == nil {
		return
	}
	c.mu.Lock()
	c.repos[p.GetRoot()] = protoToRepoInfo(p)
	if p.GetPinned() {
		c.pinned[p.GetRoot()] = true
	} else {
		delete(c.pinned, p.GetRoot())
	}
	c.mu.Unlock()
	c.notify(service.Event{Type: service.EventSessionsChanged})
}

func (c *Client) applyDeltaRepo(p *fleetv1.Repo) {
	if p == nil {
		return
	}
	c.mu.Lock()
	c.repos[p.GetRoot()] = protoToRepoInfo(p)
	if p.GetPinned() {
		c.pinned[p.GetRoot()] = true
	} else {
		delete(c.pinned, p.GetRoot())
	}
	c.mu.Unlock()
	c.notify(service.Event{Type: service.EventSessionsChanged})
}

func (c *Client) removeRepoFromStream(root string) {
	if root == "" {
		return
	}
	c.mu.Lock()
	delete(c.repos, root)
	delete(c.pinned, root)
	c.mu.Unlock()
	c.notify(service.Event{Type: service.EventSessionsChanged})
}

// sleepBackoff blocks for the schedule entry corresponding to attempt,
// returning false when the context is cancelled mid-sleep. attempts beyond
// the schedule cap at the last entry.
func sleepBackoff(ctx context.Context, attempt int) bool {
	idx := attempt
	if idx >= len(reconnectBackoffSchedule) {
		idx = len(reconnectBackoffSchedule) - 1
	}
	t := time.NewTimer(reconnectBackoffSchedule[idx])
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// sortSessionsByCreatedAt orders sessions oldest-first, with ID as the
// tiebreaker. Mirrors the in-process service's insertion-order semantics
// closely enough for the UI's repo grouping to render identically.
func sortSessionsByCreatedAt(out []*session.Session) {
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
}

// streamRecvType pins the message types so consumeSessionStream/Repo can be
// expressed without a generic adapter — used only as documentation here.
var (
	_ grpc.ServerStreamingClient[fleetv1.SessionUpdate] = (grpc.ServerStreamingClient[fleetv1.SessionUpdate])(nil)
	_ grpc.ServerStreamingClient[fleetv1.RepoUpdate]    = (grpc.ServerStreamingClient[fleetv1.RepoUpdate])(nil)
)
