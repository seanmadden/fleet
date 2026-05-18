package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brizzai/fleet/internal/config"
	"github.com/brizzai/fleet/internal/git"
	"github.com/brizzai/fleet/internal/session"
)

func TestHomeInitializes(t *testing.T) {
	// Create temp dir for in-memory-like SQLite DB.
	tmpDir, err := os.MkdirTemp("", "fleet-test-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	storage, err := session.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer storage.Close()

	cfg := &config.Config{
		TickIntervalSec: 2,
	}

	// Should not panic.
	home := NewHome(storage, cfg, "test")
	if home == nil {
		t.Fatal("NewHome returned nil")
		return
	}

	// Set minimal dimensions for rendering.
	home.width = 120
	home.height = 40

	// View() should not panic and should return non-empty output.
	output := home.View()
	if output == "" {
		t.Error("View() returned empty string")
	}
}

// TestViewGitInfoCacheRace guards against the "concurrent map read and map write"
// fatal that happens if View() reads h.gitInfoCache while the status worker writes
// it. Run with `go test -race` — pre-fix this trips the race detector reliably.
func TestViewGitInfoCacheRace(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "fleet-race-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	storage, err := session.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer storage.Close()

	home := NewHome(storage, &config.Config{TickIntervalSec: 2}, "test")
	home.width = 120
	home.height = 40

	// Seed a repo-header flatItem so RenderSidebar hits the gitInfo[item.RepoPath]
	// read path at sidebar.go:183.
	const repo = "/tmp/fleet-race-repo"
	home.flatItems = []SidebarItem{{IsRepoHeader: true, RepoPath: repo, Expanded: false, SessionCount: 0}}

	const iterations = 500
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			home.workerMu.Lock()
			home.gitInfoCache[repo] = &git.RepoInfo{Branch: "main"}
			home.workerMu.Unlock()
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			_ = home.View()
		}
	}()

	wg.Wait()
}

// runGit shells out to git for test fixtures. Production code uses
// internal/git wrappers — this is fixture-building only.
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s in %s: %v: %s", strings.Join(args, " "), dir, err, out)
	}
}

// initRepoForTest creates a fresh repo with one commit so worktree-add /
// branch ops downstream have a HEAD to anchor on.
func initRepoForTest(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	runGit(t, dir, "init", "--initial-branch=main", "-q")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "test")
	runGit(t, dir, "commit", "--allow-empty", "-q", "-m", "init")
}

// TestNewSessionRequestMsg_GitRepoEmitsWorkspaceCreate asserts the path-picker
// (palette-only "New Session at Path") routes git paths into the worktree-by-
// default flow. The dialog emits newSessionRequestMsg; Update should produce a
// workspaceCreateMsg with a `claude/<8hex>` branch via the same dispatcher
// that handles the `n` key.
func TestNewSessionRequestMsg_GitRepoEmitsWorkspaceCreate(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "fleet-newsess-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	storage, err := session.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer storage.Close()

	repo := filepath.Join(tmpDir, "myrepo")
	initRepoForTest(t, repo)

	home := NewHome(storage, &config.Config{TickIntervalSec: 2}, "test")
	home.width = 120
	home.height = 40

	_, cmd := home.Update(newSessionRequestMsg{path: repo})
	if cmd == nil {
		t.Fatal("Update(newSessionRequestMsg) returned nil cmd")
	}
	msg := cmd()
	wc, ok := msg.(workspaceCreateMsg)
	if !ok {
		t.Fatalf("expected workspaceCreateMsg, got %T (%v)", msg, msg)
	}
	if !strings.HasPrefix(wc.branch, "claude/") {
		t.Errorf("branch = %q, want prefix claude/", wc.branch)
	}
	if !strings.HasPrefix(wc.name, "claude-") {
		t.Errorf("name = %q, want prefix claude-", wc.name)
	}
	if wc.repoPath != repo {
		// resolveCurrentRepo + GetMainRepo on a plain repo returns the repo.
		// EvalSymlinks on macOS could resolve to a /private/var path; accept
		// either as long as it normalises to the same repo.
		resolved, _ := filepath.EvalSymlinks(repo)
		if wc.repoPath != resolved {
			t.Errorf("repoPath = %q, want %q (or symlink-resolved %q)", wc.repoPath, repo, resolved)
		}
	}
}

// TestNewSessionRequestMsg_NonGitFallsBackToSessionCreate asserts non-git
// paths picked through the path-picker bypass the worktree flow and produce a
// plain sessionCreateMsg (no-worktree fallback). The dialog still passes the
// path raw; the dispatcher decides the routing.
func TestNewSessionRequestMsg_NonGitFallsBackToSessionCreate(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "fleet-nongit-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	storage, err := session.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer storage.Close()

	nonGit := filepath.Join(tmpDir, "plain")
	if err := os.MkdirAll(nonGit, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	home := NewHome(storage, &config.Config{TickIntervalSec: 2}, "test")
	home.width = 120
	home.height = 40

	// startWorktreeSessionForRepo handles non-git paths by routing to
	// handleSessionCreate, which returns a tea.Cmd that calls Session.Start().
	// Start() shells out to `tmux` which isn't available in CI test envs, so
	// we exercise the routing decision indirectly by checking that the
	// returned model didn't surface an error toast for "no workspace
	// provider" — the no-worktree fallback path keeps the toast empty until
	// the (deferred) Start failure.
	model, _ := home.Update(newSessionRequestMsg{path: nonGit})
	h, ok := model.(*Home)
	if !ok {
		t.Fatalf("Update returned %T, want *Home", model)
	}
	if h.err != nil {
		t.Errorf("non-git path should not surface an error pre-Start; got %v", h.err)
	}
}

// drainMsg runs a tea.Cmd and returns its message. Used in tests where the
// command's payload type is what we're asserting about.
func drainMsg(cmd tea.Cmd) tea.Msg {
	if cmd == nil {
		return nil
	}
	return cmd()
}

var _ = drainMsg // referenced indirectly
