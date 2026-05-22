package session

import (
	"sync"
	"testing"
	"time"
)

// fakePaneCapturer lets tests construct a Session that won't shell out
// to tmux on IsAlive(). Same pattern other session tests use via the
// paneCapturer override.
type fakePaneCapturer struct {
	dead bool
}

func (f *fakePaneCapturer) CapturePane() (string, error) { return "", nil }
func (f *fakePaneCapturer) IsPaneDead() bool             { return f.dead }

func newWebTestSession(t *testing.T) *Session {
	t.Helper()
	s := &Session{
		ID:              "abc-12345",
		Title:           "test-session",
		ProjectPath:     "/tmp/repo",
		Status:          StatusRunning,
		WorkspaceName:   "feature/x",
		CreatedAt:       time.Date(2026, 5, 21, 9, 0, 0, 0, time.UTC),
		LastAccessedAt:  time.Date(2026, 5, 21, 10, 0, 0, 0, time.UTC),
		ClaudeSessionID: "claude-1",
		FirstPrompt:     "hello",
		PromptCount:     3,
		paneCapturer:    &fakePaneCapturer{dead: false},
	}
	return s
}

func TestSnapshotForWeb_CapturesAllFields(t *testing.T) {
	s := newWebTestSession(t)
	snap := s.SnapshotForWeb()

	checks := []struct {
		name string
		got  any
		want any
	}{
		{"ID", snap.ID, "abc-12345"},
		{"Title", snap.Title, "test-session"},
		{"ProjectPath", snap.ProjectPath, "/tmp/repo"},
		{"Status", snap.Status, StatusRunning},
		{"WorkspaceName", snap.WorkspaceName, "feature/x"},
		{"ClaudeSessionID", snap.ClaudeSessionID, "claude-1"},
		{"FirstPrompt", snap.FirstPrompt, "hello"},
		{"PromptCount", snap.PromptCount, 3},
		{"IsAlive", snap.IsAlive, true},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, c.got, c.want)
		}
	}
	if !snap.CreatedAt.Equal(s.CreatedAt) {
		t.Errorf("CreatedAt: got %v, want %v", snap.CreatedAt, s.CreatedAt)
	}
	if !snap.LastAccessedAt.Equal(s.LastAccessedAt) {
		t.Errorf("LastAccessedAt: got %v, want %v", snap.LastAccessedAt, s.LastAccessedAt)
	}
}

func TestSnapshotForWeb_IsAliveReflectsDeadCapturer(t *testing.T) {
	s := newWebTestSession(t)
	s.paneCapturer = &fakePaneCapturer{dead: true}
	if snap := s.SnapshotForWeb(); snap.IsAlive {
		t.Error("IsAlive=true; want false when capturer reports pane dead")
	}
}

// TestSnapshotForWeb_RacesWithMutators exercises the SnapshotForWeb path
// concurrently with MarkAccessed and UpdateHookStatus. Run with -race
// to catch any direct struct read that bypasses s.mu — that's exactly
// the regression NB4 surfaced (LastAccessedAt, PromptCount, FirstPrompt,
// ClaudeSessionID were being read raw from internal/ui/web.go).
func TestSnapshotForWeb_RacesWithMutators(t *testing.T) {
	s := newWebTestSession(t)

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Reader.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_ = s.SnapshotForWeb()
			}
		}
	}()

	// Writers.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				s.MarkAccessed()
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		i := 0
		for {
			select {
			case <-stop:
				return
			default:
				i++
				s.UpdateHookStatus(&HookStatus{
					Status:      "running",
					SessionID:   "claude-2",
					UpdatedAt:   time.Now(),
					UserPrompt:  "p",
					PromptCount: i,
				})
			}
		}
	}()

	// Let them race for a short window — with -race enabled this is enough
	// to flag any unprotected field read.
	time.Sleep(50 * time.Millisecond)
	close(stop)
	wg.Wait()
}

func TestGetTmuxSession_RaceWithRestartSwap(t *testing.T) {
	// GetTmuxSession now takes s.mu.RLock; this test confirms that
	// concurrent swaps under s.mu.Lock don't race with GetTmuxSession
	// callers. Run with -race to verify.
	s := &Session{
		ID:          "race",
		Title:       "race-session",
		ProjectPath: "/tmp/repo",
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Reader.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_ = s.GetTmuxSession()
			}
		}
	}()

	// Writer mimics Session.Restart's pointer swap under s.mu.Lock.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				s.mu.Lock()
				s.tmuxSession = nil // alternating writes are enough for -race
				s.mu.Unlock()
			}
		}
	}()

	time.Sleep(50 * time.Millisecond)
	close(stop)
	wg.Wait()
}
