package service

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/brizzai/fleet/internal/config"
	"github.com/brizzai/fleet/internal/session"
)

// isolateLiveTmux redirects every child `tmux` invocation in the test to a
// fresh per-test socket directory so the user's real tmux server is never
// touched.
//
// Why both TMUX_TMPDIR and unsetting TMUX are required:
//
//	tmux's socket-path resolution checks TMUX (the env var that's set
//	*inside* a tmux session, with the literal socket path of the parent
//	server) BEFORE falling back to TMUX_TMPDIR. When `go test` is run
//	from a fleet-managed Claude session — i.e. from inside tmux — the
//	test process inherits TMUX=/private/tmp/tmux-501/default,..., and
//	plain `tmux list-windows -a` (called from statusWorkerCycle via
//	tmux.RefreshSessionCache) honours that inherited path, ignoring
//	whatever we set TMUX_TMPDIR to. Result: the test queries the live
//	server even with TMUX_TMPDIR pointed at a temp dir.
//
//	t.Setenv("TMUX", "") doesn't help — tmux only checks if TMUX is
//	non-NULL, and Setenv with an empty string is still NULL-distinct.
//	We need a real os.Unsetenv, restored in t.Cleanup.
//
// Mirrors the helper of the same name in internal/ptybridge/bridge_test.go.
// Kept duplicated rather than extracted because the test surface is small
// and pulling in a shared testutil package across feature areas adds more
// complexity than it removes.
func isolateLiveTmux(t *testing.T) {
	t.Helper()
	// See bridge_test.go's isolateLiveTmux for why /tmp instead of t.TempDir():
	// macOS AF_UNIX sun_path is 104 bytes and `t.TempDir()` paths blow past
	// it once tmux appends `/tmux-<uid>/default`. /tmp keeps the socket path
	// short enough to actually bind.
	dir, err := os.MkdirTemp("/tmp", "fleet-tmux-iso-")
	if err != nil {
		t.Fatalf("mkdir tmux isolation tmpdir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("TMUX_TMPDIR", dir)
	if val, ok := os.LookupEnv("TMUX"); ok {
		_ = os.Unsetenv("TMUX")
		t.Cleanup(func() { _ = os.Setenv("TMUX", val) })
	}
	if val, ok := os.LookupEnv("TMUX_PANE"); ok {
		_ = os.Unsetenv("TMUX_PANE")
		t.Cleanup(func() { _ = os.Setenv("TMUX_PANE", val) })
	}
}

// newTestService builds a SessionService backed by a fresh SQLite DB in
// t.TempDir() and pre-loads slot bindings + pinned repos. It does NOT call
// Start() — that path injects Claude hooks and spawns the background worker,
// which we don't want in unit tests.
//
// CRITICAL: isolate every service test from the user's live tmux server.
//
// Even though we don't call Start() here, tests like
// TestStatusWorkerCycle_AutoNamesFromFirstPrompt call s.statusWorkerCycle(false)
// directly, which at service.go:629 invokes tmux.RefreshSessionCache() —
// shelling out `tmux list-windows -a`. Without isolation, that subprocess
// hits the user's live tmux server (the same server hosting fleet sessions
// and manual tmux sessions).
//
// 2026-05-06 incident timeline (instructive, please don't repeat the
// false starts):
//
//   - `go test -race -count=1 ./...` on this branch silently disconnected
//     the user from unrelated tmux sessions.
//   - First fix attempt: TMUX_TMPDIR override on bridge_test.go only.
//     Insufficient — service tests still hit live tmux.
//   - Second fix attempt: TMUX_TMPDIR override on newTestService too.
//     STILL insufficient — even with TMUX_TMPDIR set, tmux clients
//     resolve socket path from TMUX env var first when it's set, which
//     it is whenever the test is launched from inside a tmux session
//     (i.e. always, when running fleet's own tests via Claude inside a
//     fleet-managed pane). TMUX_TMPDIR was being silently ignored.
//   - Actual fix (what isolateLiveTmux now does): also unset TMUX +
//     TMUX_PANE for the duration of the test. Then plain `tmux ...`
//     calls fall through to the TMUX_TMPDIR resolution and land on the
//     isolated per-test socket.
//
// isolateLiveTmux lives in this file (not in newTestService inline) so the
// rationale is anchored next to the helper, and so future regressions that
// add tests outside the newTestService path can call it directly.
func newTestService(t *testing.T) (*SessionService, *session.StateDB) {
	t.Helper()
	isolateLiveTmux(t)
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

func TestSoftDelete_RoundTrip(t *testing.T) {
	s, db := newTestService(t)
	row := &session.SessionRow{
		ID: "soft", Title: "draft", ProjectPath: "/tmp",
		CreatedAt:    time.Now().Truncate(time.Second),
		LastAccessed: time.Now().Truncate(time.Second),
	}
	seedSession(t, s, row)
	if err := s.BindSlot(3, "soft"); err != nil {
		t.Fatalf("BindSlot: %v", err)
	}

	snap, err := s.SoftDelete("soft")
	if err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}
	if snap == nil || snap.ID != "soft" {
		t.Fatalf("SoftDelete row: want id=soft, got %+v", snap)
	}
	if got := s.GetSession("soft"); got != nil {
		t.Errorf("session should be gone from memory")
	}
	if rows, _ := db.LoadSessions(); len(rows) != 0 {
		t.Errorf("session should be gone from storage, got %d rows", len(rows))
	}
	if _, ok := s.SlotBindings()[3]; ok {
		t.Errorf("slot binding should be pruned")
	}

	if _, err := s.SoftDelete("missing"); err == nil {
		t.Errorf("SoftDelete on missing id: want error, got nil")
	}

	// Round-trip via RestoreDeleted.
	if err := s.RestoreDeleted(snap); err != nil {
		t.Fatalf("RestoreDeleted: %v", err)
	}
	if got := s.GetSession("soft"); got == nil {
		t.Fatalf("session should be back in memory after restore")
	}
}

func TestEnqueuePriority_BufferAndDrop(t *testing.T) {
	s, _ := newTestService(t)
	// 256 fits, 257th drops silently — must not block or panic.
	for range 257 {
		s.EnqueuePriority("id")
	}
	if got := len(s.priorityStatusUpdates); got != 256 {
		t.Errorf("priorityStatusUpdates len: want 256, got %d", got)
	}
}

func TestStatusWorkerCycle_AutoNamesFromFirstPrompt(t *testing.T) {
	cfg := &config.Config{}
	if !cfg.IsAutoNameEnabled() {
		t.Skip("auto-name disabled by default")
	}
	s, db := newTestService(t)
	row := &session.SessionRow{
		ID: "auto", Title: "untitled", ProjectPath: "/tmp",
		CreatedAt:   time.Now(),
		FirstPrompt: "please refactor the user dashboard component",
	}
	seedSession(t, s, row)

	s.statusWorkerCycle(false)

	sess := s.GetSession("auto")
	if sess == nil {
		t.Fatalf("session vanished")
	}
	if sess.Title == "untitled" || sess.Title == "" {
		t.Errorf("Title should be auto-generated, got %q", sess.Title)
	}
	if !sess.TitleGenerated {
		t.Errorf("TitleGenerated should be true")
	}
	rows, _ := db.LoadSessions()
	if len(rows) != 1 || rows[0].Title == "untitled" || !rows[0].TitleGenerated {
		t.Errorf("storage row: title=%q TitleGenerated=%v", rows[0].Title, rows[0].TitleGenerated)
	}

	// Second cycle shouldn't change the title (TitleGenerated locks it).
	titleAfterFirst := sess.Title
	s.statusWorkerCycle(false)
	if sess.Title != titleAfterFirst {
		t.Errorf("second cycle re-titled: want %q, got %q", titleAfterFirst, sess.Title)
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
