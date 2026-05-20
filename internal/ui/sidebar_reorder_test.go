package ui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/brizzai/fleet/internal/config"
	"github.com/brizzai/fleet/internal/session"
)

// newTestHome opens a fresh on-disk SQLite (modernc.org/sqlite doesn't expose
// :memory: through this storage layer) and returns a Home wired to it. Cleanup
// is registered via t.Cleanup.
func newTestHome(t *testing.T) *Home {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "fleet-reorder-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(tmpDir) })

	dbPath := filepath.Join(tmpDir, "test.db")
	storage, err := session.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { storage.Close() })

	cfg := &config.Config{TickIntervalSec: 2}
	h := NewHome(storage, cfg, "test")
	h.width = 120
	h.height = 40
	return h
}

// seedSessions installs sessions on Home (in memory + persisted), then rebuilds
// the flat sidebar. ProjectPath is used as the repo root via GetRepoRoot's
// fallback for non-git paths.
func seedSessions(t *testing.T, h *Home, rows []*session.SessionRow) {
	t.Helper()
	for _, r := range rows {
		if err := h.storage.SaveSession(r); err != nil {
			t.Fatalf("SaveSession %s: %v", r.ID, err)
		}
		s := session.FromRow(r)
		h.sessions = append(h.sessions, s)
		// Auto-expand each repo so its sessions appear in flatItems.
		h.repoExpanded[r.ProjectPath] = true
	}
	h.rebuildSessionMap()
	h.rebuildFlatItems()
}

// findFlatSessionIdx returns the index of a session in h.flatItems, or -1.
func findFlatSessionIdx(h *Home, id string) int {
	for i, it := range h.flatItems {
		if !it.IsRepoHeader && it.Session != nil && it.Session.ID == id {
			return i
		}
	}
	return -1
}

// findFlatRepoIdx returns the index of a repo header in h.flatItems, or -1.
func findFlatRepoIdx(h *Home, repoPath string) int {
	for i, it := range h.flatItems {
		if it.IsRepoHeader && it.RepoPath == repoPath {
			return i
		}
	}
	return -1
}

func TestMoveCursorItemSessionReorder(t *testing.T) {
	h := newTestHome(t)
	now := time.Now().Truncate(time.Second)
	seedSessions(t, h, []*session.SessionRow{
		{ID: "s1", Title: "First", ProjectPath: "/tmp/repo-a", Status: "idle", CreatedAt: now},
		{ID: "s2", Title: "Second", ProjectPath: "/tmp/repo-a", Status: "idle", CreatedAt: now.Add(time.Second)},
	})

	// Cursor on s1 (first session).
	idx := findFlatSessionIdx(h, "s1")
	if idx < 0 {
		t.Fatalf("s1 not in flatItems: %+v", h.flatItems)
	}
	h.cursor = idx

	// Move s1 down.
	h.moveCursorItem(1)

	// In memory: s2 should now precede s1.
	if h.sessions[0].ID != "s2" || h.sessions[1].ID != "s1" {
		t.Errorf("after move-down, expected [s2, s1] in memory, got [%s, %s]",
			h.sessions[0].ID, h.sessions[1].ID)
	}

	// On disk: LoadSessions should yield the same order.
	loaded, err := h.storage.LoadSessions()
	if err != nil {
		t.Fatalf("LoadSessions: %v", err)
	}
	if loaded[0].ID != "s2" || loaded[1].ID != "s1" {
		t.Errorf("after move-down, expected [s2, s1] on disk, got [%s, %s]",
			loaded[0].ID, loaded[1].ID)
	}

	// Cursor should follow s1 to its new slot.
	newIdx := findFlatSessionIdx(h, "s1")
	if newIdx < 0 {
		t.Fatalf("s1 vanished from flatItems after move: %+v", h.flatItems)
	}
	if h.cursor != newIdx {
		t.Errorf("cursor did not follow s1: cursor=%d, s1 at %d", h.cursor, newIdx)
	}

	// Move back: with non-zero keys now, swap should be symmetric.
	h.moveCursorItem(-1)
	if h.sessions[0].ID != "s1" || h.sessions[1].ID != "s2" {
		t.Errorf("after move-up, expected [s1, s2] in memory, got [%s, %s]",
			h.sessions[0].ID, h.sessions[1].ID)
	}
}

// TestMoveCursorItemSessionReorderThreePlus reproduces the "stuck shift+↑/↓"
// session bug in a group of 3+ sessions. The old "seed only the swap pair"
// code left the other group members at SortKey=0; after the global re-sort,
// those zero-key siblings sorted *before* the seeded pair, so the first
// reorder shoved both swapped sessions to the bottom of the group instead of
// performing a local swap. With four sessions [a, b, c, d] and Shift+↓ on a,
// the bug produced [c, d, b, a] instead of [b, a, c, d].
func TestMoveCursorItemSessionReorderThreePlus(t *testing.T) {
	h := newTestHome(t)
	now := time.Now().Truncate(time.Second)
	seedSessions(t, h, []*session.SessionRow{
		{ID: "a", Title: "A", ProjectPath: "/tmp/repo", Status: "idle", CreatedAt: now},
		{ID: "b", Title: "B", ProjectPath: "/tmp/repo", Status: "idle", CreatedAt: now.Add(1 * time.Second)},
		{ID: "c", Title: "C", ProjectPath: "/tmp/repo", Status: "idle", CreatedAt: now.Add(2 * time.Second)},
		{ID: "d", Title: "D", ProjectPath: "/tmp/repo", Status: "idle", CreatedAt: now.Add(3 * time.Second)},
	})

	// Cursor on a (first in group). Move down — expect [b, a, c, d].
	h.cursor = findFlatSessionIdx(h, "a")
	h.moveCursorItem(1)

	groupAfter := session.GroupByRepo(h.sessions)["/tmp/repo"]
	gotIDs := []string{groupAfter[0].ID, groupAfter[1].ID, groupAfter[2].ID, groupAfter[3].ID}
	wantIDs := []string{"b", "a", "c", "d"}
	for i, want := range wantIDs {
		if gotIDs[i] != want {
			t.Errorf("after move-down on a: position %d = %q, want %q (full order %v)", i, gotIDs[i], want, gotIDs)
		}
	}

	// Disk should reflect the same order under SortKey sort.
	loaded, err := h.storage.LoadSessions()
	if err != nil {
		t.Fatalf("LoadSessions: %v", err)
	}
	var loadedIDs []string
	for _, r := range loaded {
		if r.ProjectPath == "/tmp/repo" {
			loadedIDs = append(loadedIDs, r.ID)
		}
	}
	for i, want := range wantIDs {
		if loadedIDs[i] != want {
			t.Errorf("disk position %d = %q, want %q (full order %v)", i, loadedIDs[i], want, loadedIDs)
		}
	}

	// Cursor follows a.
	newIdx := findFlatSessionIdx(h, "a")
	if h.cursor != newIdx {
		t.Errorf("cursor did not follow a: cursor=%d, a at %d", h.cursor, newIdx)
	}
}

func TestMoveCursorItemRepoReorder(t *testing.T) {
	h := newTestHome(t)
	now := time.Now().Truncate(time.Second)
	// Two repos; "aaa" sorts before "zzz" alphabetically.
	seedSessions(t, h, []*session.SessionRow{
		{ID: "sa", Title: "A", ProjectPath: "/tmp/aaa", Status: "idle", CreatedAt: now},
		{ID: "sz", Title: "Z", ProjectPath: "/tmp/zzz", Status: "idle", CreatedAt: now},
	})

	// Cursor on the /tmp/aaa header.
	idx := findFlatRepoIdx(h, "/tmp/aaa")
	if idx < 0 {
		t.Fatalf("/tmp/aaa header not in flatItems: %+v", h.flatItems)
	}
	h.cursor = idx

	// Move /tmp/aaa down → swaps with /tmp/zzz.
	h.moveCursorItem(1)

	// repoOrder map should now contain both repos with /tmp/zzz < /tmp/aaa.
	keyA, okA := h.repoOrder["/tmp/aaa"]
	keyZ, okZ := h.repoOrder["/tmp/zzz"]
	if !okA || !okZ {
		t.Fatalf("expected both repos in repoOrder, got %v", h.repoOrder)
	}
	if !(keyZ < keyA) {
		t.Errorf("expected zzz key < aaa key after swap, got aaa=%d, zzz=%d", keyA, keyZ)
	}

	// flatItems ordering: zzz header should now precede aaa header.
	zIdx := findFlatRepoIdx(h, "/tmp/zzz")
	aIdx := findFlatRepoIdx(h, "/tmp/aaa")
	if !(zIdx < aIdx) {
		t.Errorf("expected /tmp/zzz before /tmp/aaa in flatItems, got zzz=%d aaa=%d", zIdx, aIdx)
	}

	// Cursor should follow /tmp/aaa to its new slot.
	if h.cursor != aIdx {
		t.Errorf("cursor did not follow /tmp/aaa: cursor=%d, aaa at %d", h.cursor, aIdx)
	}

	// On disk: repo_order persists.
	loaded, err := h.storage.LoadRepoOrder()
	if err != nil {
		t.Fatalf("LoadRepoOrder: %v", err)
	}
	if loaded["/tmp/aaa"] != keyA || loaded["/tmp/zzz"] != keyZ {
		t.Errorf("on-disk repo_order mismatch: got %v, want aaa=%d zzz=%d", loaded, keyA, keyZ)
	}
}

// TestMoveCursorItemRepoReorderSeededCollision reproduces the "stuck shift+↑/↓"
// repo bug: when one repo has a stored sort_key (e.g. from a prior reorder) and
// neighbouring repos are still on the alphabetical fallback (key=0), the
// seeding formula (idx+1)*100 used by moveRepoGroup can mint a key equal to an
// existing stored key. After the swap both repos share the same key, the
// alphabetical tiebreaker takes over, and the move is a visual no-op.
func TestMoveCursorItemRepoReorderSeededCollision(t *testing.T) {
	h := newTestHome(t)
	now := time.Now().Truncate(time.Second)
	// Three repos. /tmp/ccc has a pre-existing stored key of 100 (simulating
	// a prior reorder); /tmp/aaa and /tmp/bbb are unseeded.
	seedSessions(t, h, []*session.SessionRow{
		{ID: "sa", Title: "A", ProjectPath: "/tmp/aaa", Status: "idle", CreatedAt: now},
		{ID: "sb", Title: "B", ProjectPath: "/tmp/bbb", Status: "idle", CreatedAt: now},
		{ID: "sc", Title: "C", ProjectPath: "/tmp/ccc", Status: "idle", CreatedAt: now},
	})
	if err := h.storage.UpsertRepoOrder("/tmp/ccc", 100); err != nil {
		t.Fatalf("UpsertRepoOrder: %v", err)
	}
	h.repoOrder["/tmp/ccc"] = 100
	h.rebuildFlatItems()

	// Visual order should be aaa (0/alpha), bbb (0/alpha), ccc (100).
	aIdx := findFlatRepoIdx(h, "/tmp/aaa")
	bIdx := findFlatRepoIdx(h, "/tmp/bbb")
	cIdx := findFlatRepoIdx(h, "/tmp/ccc")
	if !(aIdx >= 0 && bIdx >= 0 && cIdx >= 0 && aIdx < bIdx && bIdx < cIdx) {
		t.Fatalf("expected aaa<bbb<ccc, got aaa=%d bbb=%d ccc=%d", aIdx, bIdx, cIdx)
	}

	// Move /tmp/bbb up — swap with /tmp/aaa. Expected order: bbb, aaa, ccc.
	h.cursor = bIdx
	h.moveCursorItem(-1)

	aIdx2 := findFlatRepoIdx(h, "/tmp/aaa")
	bIdx2 := findFlatRepoIdx(h, "/tmp/bbb")
	cIdx2 := findFlatRepoIdx(h, "/tmp/ccc")
	if !(bIdx2 < aIdx2 && aIdx2 < cIdx2) {
		t.Errorf("expected bbb<aaa<ccc after move-up, got bbb=%d aaa=%d ccc=%d", bIdx2, aIdx2, cIdx2)
	}

	// Cursor should follow /tmp/bbb to its new slot.
	if h.cursor != bIdx2 {
		t.Errorf("cursor did not follow /tmp/bbb: cursor=%d, bbb at %d", h.cursor, bIdx2)
	}

	// All three repos should now have distinct stored keys (no collision).
	loaded, err := h.storage.LoadRepoOrder()
	if err != nil {
		t.Fatalf("LoadRepoOrder: %v", err)
	}
	keys := map[string]int64{
		"/tmp/aaa": loaded["/tmp/aaa"],
		"/tmp/bbb": loaded["/tmp/bbb"],
		"/tmp/ccc": loaded["/tmp/ccc"],
	}
	if keys["/tmp/aaa"] == 0 && keys["/tmp/bbb"] == 0 {
		// At least the two swapped repos must be persisted with non-zero keys
		// so the swap survives the next collision-prone formula application.
		t.Errorf("expected swapped repos to have non-zero stored keys, got %v", keys)
	}
	seen := make(map[int64]string)
	for repo, k := range keys {
		if k == 0 {
			continue
		}
		if other, dup := seen[k]; dup {
			t.Errorf("duplicate stored key %d for %s and %s (will collide on next move)", k, repo, other)
		}
		seen[k] = repo
	}
}

func TestMoveCursorItemBoundaryNoOp(t *testing.T) {
	h := newTestHome(t)
	now := time.Now().Truncate(time.Second)
	// Two sessions in two different repos. First session in repo-a + dir=-1
	// must be a no-op (does NOT migrate to repo-b).
	seedSessions(t, h, []*session.SessionRow{
		{ID: "a1", Title: "A1", ProjectPath: "/tmp/repo-a", Status: "idle", CreatedAt: now},
		{ID: "b1", Title: "B1", ProjectPath: "/tmp/repo-b", Status: "idle", CreatedAt: now},
	})

	// Snapshot in-memory + on-disk state.
	prevA := h.sessions[0].ID
	prevB := h.sessions[1].ID
	prevSorted, _ := h.storage.LoadSessions()
	var prevDiskIDs []string
	for _, r := range prevSorted {
		prevDiskIDs = append(prevDiskIDs, r.ID)
	}

	// Cursor on a1 (only session in /tmp/repo-a). Up is a no-op.
	h.cursor = findFlatSessionIdx(h, "a1")
	h.moveCursorItem(-1)

	if h.sessions[0].ID != prevA || h.sessions[1].ID != prevB {
		t.Errorf("expected in-memory order unchanged, got [%s, %s] (was [%s, %s])",
			h.sessions[0].ID, h.sessions[1].ID, prevA, prevB)
	}

	// Down is also a no-op (a1 is the only session in /tmp/repo-a).
	h.moveCursorItem(1)
	if h.sessions[0].ID != prevA || h.sessions[1].ID != prevB {
		t.Errorf("expected in-memory order unchanged after move-down, got [%s, %s]",
			h.sessions[0].ID, h.sessions[1].ID)
	}

	// Disk untouched.
	loaded, _ := h.storage.LoadSessions()
	if len(loaded) != len(prevDiskIDs) {
		t.Fatalf("expected %d sessions on disk, got %d", len(prevDiskIDs), len(loaded))
	}
	for i, want := range prevDiskIDs {
		if loaded[i].ID != want {
			t.Errorf("disk position %d: got %q, want %q", i, loaded[i].ID, want)
		}
		if loaded[i].SortKey != 0 {
			t.Errorf("disk position %d: sort_key should still be 0, got %d", i, loaded[i].SortKey)
		}
	}
}

func TestMoveCursorItemPhantomIgnored(t *testing.T) {
	h := newTestHome(t)
	// Inject a phantom "creating..." pending workspace; no real sessions.
	pw := &PendingWorkspace{ID: "pw-1", Name: "newbranch", RepoPath: "/tmp/repo-a"}
	h.pendingWorkspaces = []*PendingWorkspace{pw}
	h.repoExpanded["/tmp/repo-a"] = true
	h.rebuildFlatItems()

	// Find the phantom in flatItems and put the cursor on it.
	phantomIdx := -1
	for i, it := range h.flatItems {
		if it.Pending != nil && it.Pending.ID == "pw-1" {
			phantomIdx = i
			break
		}
	}
	if phantomIdx < 0 {
		t.Fatalf("phantom not in flatItems: %+v", h.flatItems)
	}
	h.cursor = phantomIdx

	// Move down — should be a silent no-op (no toast, no DB write, no error).
	h.moveCursorItem(1)
	if h.infoMsg != "" {
		t.Errorf("expected no info toast on phantom move, got %q", h.infoMsg)
	}
	if h.err != nil {
		t.Errorf("expected no error on phantom move, got %v", h.err)
	}
	// Phantom should still be where it was (only one item, anyway).
	if findFlatSessionIdx(h, "pw-1") != -1 {
		// (No real sessions to test against, but the phantom slot is unchanged.)
		t.Errorf("phantom unexpectedly turned into a session")
	}
}

func TestMoveCursorItemFilteredGuard(t *testing.T) {
	h := newTestHome(t)
	now := time.Now().Truncate(time.Second)
	seedSessions(t, h, []*session.SessionRow{
		{ID: "s1", Title: "alpha", ProjectPath: "/tmp/repo-a", Status: "idle", CreatedAt: now},
		{ID: "s2", Title: "beta", ProjectPath: "/tmp/repo-a", Status: "idle", CreatedAt: now.Add(time.Second)},
	})

	// Activate filter.
	h.filterText = "alpha"
	h.rebuildFlatItems()

	idx := findFlatSessionIdx(h, "s1")
	if idx < 0 {
		t.Fatalf("s1 not in filtered flatItems: %+v", h.flatItems)
	}
	h.cursor = idx

	h.moveCursorItem(1)

	// In-memory order must be unchanged.
	if h.sessions[0].ID != "s1" || h.sessions[1].ID != "s2" {
		t.Errorf("expected order unchanged under filter, got [%s, %s]",
			h.sessions[0].ID, h.sessions[1].ID)
	}
	if h.infoMsg == "" {
		t.Errorf("expected info toast when reordering under filter, got empty")
	}
	// Sort keys on disk should still be 0.
	loaded, _ := h.storage.LoadSessions()
	for _, r := range loaded {
		if r.SortKey != 0 {
			t.Errorf("session %s: sort_key should still be 0 under filter guard, got %d",
				r.ID, r.SortKey)
		}
	}
}
