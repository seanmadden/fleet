package service

import (
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/brizzai/fleet/internal/config"
	"github.com/brizzai/fleet/internal/session"
)

// newTestService builds a SessionService backed by a fresh SQLite DB in
// t.TempDir() and pre-loads slot bindings + pinned repos. It does NOT call
// Start() — that path injects Claude hooks and spawns the background worker,
// which we don't want in unit tests.
func newTestService(t *testing.T) (*SessionService, *session.StateDB) {
	t.Helper()
	db, err := session.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("session.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	s := NewSessionService(db, &config.Config{})
	if b, _ := s.storage.LoadSlotBindings(); b != nil {
		s.slotBindings = b
	}
	if p, _ := s.storage.LoadPinnedRepos(); p != nil {
		for _, path := range p {
			s.pinnedRepos[path] = true
		}
	}
	return s, db
}

// seedSession persists a row to storage and reflects it into the service's
// in-memory state. Mirrors what Start() does for one row.
func seedSession(t *testing.T, s *SessionService, row *session.SessionRow) {
	t.Helper()
	if err := s.storage.SaveSession(row); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	sess := session.FromRow(row)
	s.mu.Lock()
	s.sessions = append(s.sessions, sess)
	s.rebuildSessionMap()
	s.mu.Unlock()
}

type recordingObserver struct{ count int32 }

func (r *recordingObserver) OnEvent(_ Event) { atomic.AddInt32(&r.count, 1) }

func TestNewSessionService_Empty(t *testing.T) {
	s, _ := newTestService(t)
	if got := s.Sessions(); len(got) != 0 {
		t.Errorf("Sessions on fresh service: want 0, got %d", len(got))
	}
	if got := s.SlotBindings(); len(got) != 0 {
		t.Errorf("SlotBindings on fresh service: want 0, got %d", len(got))
	}
	if got := s.PinnedRepos(); len(got) != 0 {
		t.Errorf("PinnedRepos on fresh service: want 0, got %d", len(got))
	}
}

func TestRenameSession_PersistsAndMarksManual(t *testing.T) {
	s, db := newTestService(t)
	row := &session.SessionRow{ID: "abc", Title: "old", ProjectPath: "/tmp", CreatedAt: time.Now()}
	seedSession(t, s, row)

	s.RenameSession("abc", "new")

	if got := s.GetSession("abc").Title; got != "new" {
		t.Errorf("in-memory Title: want 'new', got %q", got)
	}
	if got := s.GetSession("abc").ManuallyRenamed; !got {
		t.Errorf("ManuallyRenamed should be true after RenameSession")
	}
	rows, _ := db.LoadSessions()
	if len(rows) != 1 || rows[0].Title != "new" || !rows[0].ManuallyRenamed {
		t.Errorf("storage: want title='new' manuallyRenamed=true, got %+v", rows[0])
	}
}

func TestSubscribe_FanOutAndUnsubscribe(t *testing.T) {
	s, _ := newTestService(t)
	seedSession(t, s, &session.SessionRow{ID: "a", Title: "x", ProjectPath: "/tmp", CreatedAt: time.Now()})

	o1, o2 := &recordingObserver{}, &recordingObserver{}
	s.Subscribe(o1)
	s.Subscribe(o2)

	s.RenameSession("a", "y")
	if c1, c2 := atomic.LoadInt32(&o1.count), atomic.LoadInt32(&o2.count); c1 != 1 || c2 != 1 {
		t.Errorf("after rename: want o1=1 o2=1, got o1=%d o2=%d", c1, c2)
	}

	s.Unsubscribe(o1)
	s.RenameSession("a", "z")
	if c1, c2 := atomic.LoadInt32(&o1.count), atomic.LoadInt32(&o2.count); c1 != 1 || c2 != 2 {
		t.Errorf("after unsub: want o1=1 o2=2, got o1=%d o2=%d", c1, c2)
	}
}

func TestBindSlot_PersistsExposesAndUnbinds(t *testing.T) {
	s, db := newTestService(t)
	seedSession(t, s, &session.SessionRow{ID: "abc", Title: "x", ProjectPath: "/tmp", CreatedAt: time.Now()})

	if err := s.BindSlot(1, "abc"); err != nil {
		t.Fatalf("BindSlot: %v", err)
	}
	if got := s.SlotBindings()[1]; got != "abc" {
		t.Errorf("in-memory SlotBindings[1]: want 'abc', got %q", got)
	}
	persisted, _ := db.LoadSlotBindings()
	if persisted[1] != "abc" {
		t.Errorf("storage SlotBindings[1]: want 'abc', got %q", persisted[1])
	}

	if err := s.BindSlot(2, "missing"); err == nil {
		t.Errorf("BindSlot for missing session: want error, got nil")
	}

	if err := s.UnbindSlot(1); err != nil {
		t.Fatalf("UnbindSlot: %v", err)
	}
	if _, ok := s.SlotBindings()[1]; ok {
		t.Errorf("SlotBindings[1] after unbind: should be gone")
	}
}

func TestPinRepo_DedupesAndPersists(t *testing.T) {
	s, db := newTestService(t)
	if err := s.PinRepo("/repo"); err != nil {
		t.Fatalf("PinRepo: %v", err)
	}
	if err := s.PinRepo("/repo"); err != nil {
		t.Fatalf("PinRepo (dedupe): %v", err)
	}
	if got := s.PinnedRepos(); len(got) != 1 {
		t.Errorf("PinnedRepos: want 1, got %d", len(got))
	}
	persisted, _ := db.LoadPinnedRepos()
	if len(persisted) != 1 {
		t.Errorf("storage PinnedRepos: want 1, got %d", len(persisted))
	}

	if err := s.UnpinRepo("/repo"); err != nil {
		t.Fatalf("UnpinRepo: %v", err)
	}
	if got := s.PinnedRepos(); len(got) != 0 {
		t.Errorf("PinnedRepos after unpin: want 0, got %d", len(got))
	}
}

func TestSnapshotRestore_RoundTrip(t *testing.T) {
	s, db := newTestService(t)
	row := &session.SessionRow{
		ID: "abc", Title: "snap", ProjectPath: "/tmp",
		CreatedAt:    time.Now().Truncate(time.Second),
		LastAccessed: time.Now().Truncate(time.Second),
	}
	seedSession(t, s, row)

	snap, err := s.SnapshotForUndo("abc")
	if err != nil {
		t.Fatalf("SnapshotForUndo: %v", err)
	}

	s.DeleteSession("abc")
	if got := s.Sessions(); len(got) != 0 {
		t.Fatalf("after delete: want 0 sessions in memory, got %d", len(got))
	}
	if got, _ := db.LoadSessions(); len(got) != 0 {
		t.Fatalf("after delete: want 0 in storage, got %d", len(got))
	}

	if err := s.RestoreDeleted(snap); err != nil {
		t.Fatalf("RestoreDeleted: %v", err)
	}
	if got := s.GetSession("abc"); got == nil {
		t.Fatalf("after restore: session not in memory")
	}
	rows, _ := db.LoadSessions()
	if len(rows) != 1 || rows[0].Title != "snap" {
		t.Errorf("after restore: storage row wrong: %+v", rows)
	}

	if _, err := s.SnapshotForUndo("not-a-session"); err == nil {
		t.Errorf("SnapshotForUndo for missing session: want error, got nil")
	}
	if err := s.RestoreDeleted(nil); err == nil {
		t.Errorf("RestoreDeleted(nil): want error, got nil")
	}
}

func TestOnFirstPrompt_GeneratesTitleOnceAndRespectsManual(t *testing.T) {
	s, _ := newTestService(t)
	seedSession(t, s, &session.SessionRow{ID: "auto", Title: "untitled", ProjectPath: "/tmp", CreatedAt: time.Now()})

	s.OnFirstPrompt("auto", "please add a logout button to the navbar")
	sess := s.GetSession("auto")
	if !sess.TitleGenerated {
		t.Errorf("TitleGenerated should be true after first prompt")
	}
	firstTitle := sess.Title
	if firstTitle == "untitled" || firstTitle == "" {
		t.Errorf("Title should be auto-generated, got %q", firstTitle)
	}

	s.OnFirstPrompt("auto", "totally different second prompt")
	if sess.Title != firstTitle {
		t.Errorf("second OnFirstPrompt should no-op: want %q, got %q", firstTitle, sess.Title)
	}

	seedSession(t, s, &session.SessionRow{ID: "manual", Title: "fixed", ProjectPath: "/tmp", ManuallyRenamed: true, CreatedAt: time.Now()})
	s.OnFirstPrompt("manual", "anything")
	manual := s.GetSession("manual")
	if manual.Title != "fixed" || manual.TitleGenerated {
		t.Errorf("manually-renamed session should not auto-title: Title=%q TitleGenerated=%v", manual.Title, manual.TitleGenerated)
	}
}

func TestOnPromptCount_ResetsTitleGenerated(t *testing.T) {
	s, db := newTestService(t)
	seedSession(t, s, &session.SessionRow{
		ID: "abc", Title: "auto", ProjectPath: "/tmp", CreatedAt: time.Now(),
		TitleGenerated: true, PromptCount: 1,
	})

	s.OnPromptCount("abc", 2)
	sess := s.GetSession("abc")
	if sess.PromptCount != 2 {
		t.Errorf("PromptCount: want 2, got %d", sess.PromptCount)
	}
	if sess.TitleGenerated {
		t.Errorf("TitleGenerated should reset on new prompt count")
	}

	persisted, _ := db.LoadSessions()
	if persisted[0].PromptCount != 2 || persisted[0].TitleGenerated {
		t.Errorf("storage row: want count=2 generated=false, got count=%d generated=%v",
			persisted[0].PromptCount, persisted[0].TitleGenerated)
	}

	// Lower-or-equal counts are no-ops.
	s.OnPromptCount("abc", 1)
	if sess.PromptCount != 2 {
		t.Errorf("regression on lower count: want PromptCount=2, got %d", sess.PromptCount)
	}
}
