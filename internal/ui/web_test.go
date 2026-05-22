package ui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/brizzai/fleet/internal/config"
	"github.com/brizzai/fleet/internal/session"
	"github.com/brizzai/fleet/internal/web"
)

func newWebTestHome(t *testing.T) *Home {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "fleet-web-ui-*")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(tmpDir) })

	storage, err := session.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { storage.Close() })

	h := NewHome(storage, &config.Config{TickIntervalSec: 2}, "test")
	h.width = 120
	h.height = 40
	return h
}

func TestSessionsSnapshot_Empty(t *testing.T) {
	h := newWebTestHome(t)
	snap := h.SessionsSnapshot()
	if snap == nil {
		t.Fatal("SessionsSnapshot returned nil")
	}
	if len(snap) != 0 {
		t.Errorf("expected empty snapshot, got %d sessions", len(snap))
	}
}

func TestSessionsSnapshot_ReturnsAddedSession(t *testing.T) {
	h := newWebTestHome(t)

	// Insert a session bypassing tmux Start() — we only need a populated
	// pointer in h.sessions/sessionByID so the snapshot can read it.
	s := session.NewSession("hello world", "/tmp/repo")
	s.WorkspaceName = "feature/x"
	h.workerMu.Lock()
	h.sessions = append(h.sessions, s)
	h.rebuildSessionMap()
	h.workerMu.Unlock()

	snap := h.SessionsSnapshot()
	if len(snap) != 1 {
		t.Fatalf("got %d sessions, want 1", len(snap))
	}
	if snap[0].ID != s.ID || snap[0].Title != "hello world" || snap[0].WorkspaceName != "feature/x" {
		t.Errorf("snapshot mismatch: %+v", snap[0])
	}
}

func TestPaneSnapshot_NotFound(t *testing.T) {
	h := newWebTestHome(t)
	if _, err := h.PaneSnapshot("missing"); err == nil {
		t.Fatal("expected error for missing id, got nil")
	}
}

func TestApproveSessionByID_NotFound(t *testing.T) {
	h := newWebTestHome(t)
	if _, err := h.approveSessionByID("nope"); err == nil {
		t.Fatal("expected error for missing id")
	}
}

func TestRestartSessionByID_NotFound(t *testing.T) {
	h := newWebTestHome(t)
	if _, err := h.restartSessionByID("nope"); err == nil {
		t.Fatal("expected error for missing id")
	}
}

func TestSendKeysToSessionByID_NotFound(t *testing.T) {
	h := newWebTestHome(t)
	if _, err := h.sendKeysToSessionByID("nope", "Enter"); err == nil {
		t.Fatal("expected error for missing id")
	}
}

// TestDispatch_NoProgramRepliesWithError — when Home has no *tea.Program
// wired (e.g. test environments), Dispatch must not block; it must reply
// on the Mutation.Reply channel with an error.
func TestDispatch_NoProgramRepliesWithError(t *testing.T) {
	h := newWebTestHome(t)
	reply := make(chan error, 1)
	h.Dispatch(web.Mutation{Kind: web.MutationApprove, ID: "x", Reply: reply})
	select {
	case err := <-reply:
		if err == nil {
			t.Fatal("expected non-nil error reply")
		}
	default:
		t.Fatal("expected reply to be sent synchronously when program is nil")
	}
}

// TestPublishSessionEvent_NoPublisherIsNoop — publishing without a wired
// publisher must not panic.
func TestPublishSessionEvent_NoPublisherIsNoop(t *testing.T) {
	h := newWebTestHome(t)
	h.publishSessionEvent("refresh", "")
}

// TestSetWebPublisher_NilSafe — setting nil and publishing must not panic.
func TestSetWebPublisher_NilSafe(t *testing.T) {
	h := newWebTestHome(t)
	h.SetWebPublisher(nil)
	h.publishSessionEvent("refresh", "")
}

// TestPublishSessionEvent_DeliversToHub — when a publisher is wired, the
// event reaches it with the kind and (optionally) snapshot populated.
func TestPublishSessionEvent_DeliversToHub(t *testing.T) {
	h := newWebTestHome(t)
	cap := &capturingPublisher{}
	h.SetWebPublisher(cap)

	s := session.NewSession("captured", "/tmp/r")
	h.workerMu.Lock()
	h.sessions = append(h.sessions, s)
	h.rebuildSessionMap()
	h.workerMu.Unlock()

	h.publishSessionEvent("updated", s.ID)
	if len(cap.events) != 1 {
		t.Fatalf("got %d events, want 1", len(cap.events))
	}
	if cap.events[0].Kind != "updated" || cap.events[0].SessionID != s.ID || cap.events[0].Snapshot == nil {
		t.Errorf("unexpected event: %+v", cap.events[0])
	}
	if cap.events[0].Snapshot.Title != "captured" {
		t.Errorf("snapshot title = %q", cap.events[0].Snapshot.Title)
	}
}

type capturingPublisher struct {
	events []web.SessionEvent
}

func (c *capturingPublisher) Publish(e web.SessionEvent) { c.events = append(c.events, e) }
