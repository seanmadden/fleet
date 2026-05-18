package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runGit is a tiny test helper that runs `git -C dir args...` and fails the
// test loudly with the command output on error. Used by the fixture builders
// below — production code never shells out via this helper.
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s in %s: %v: %s", strings.Join(args, " "), dir, err, out)
	}
}

// initRepo creates a minimal git repo with a single empty commit and a
// non-default initial branch so tests can read it back.
func initRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	runGit(t, dir, "init", "--initial-branch=main", "-q")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "test")
	runGit(t, dir, "commit", "--allow-empty", "-q", "-m", "init")
}

// TestGitCommonDir asserts the common-dir resolution distinguishes a plain
// repo (where common-dir is `<repo>/.git`) from a linked worktree (where it
// resolves back to the main repo's `.git`). session.GetMainRepo relies on this.
func TestGitCommonDir(t *testing.T) {
	root := t.TempDir()
	main := filepath.Join(root, "myrepo")
	initRepo(t, main)

	resolvedMain, err := filepath.EvalSymlinks(main)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}

	// Plain repo: common-dir == <main>/.git.
	got := GitCommonDir(main)
	wantPlain := filepath.Join(resolvedMain, ".git")
	if got != wantPlain {
		t.Errorf("GitCommonDir(plain) = %q, want %q", got, wantPlain)
	}

	// Worktree: common-dir still resolves to the main repo's .git.
	wt := filepath.Join(root, "myrepo-wt")
	runGit(t, main, "worktree", "add", "-b", "claude/abcd1234", wt)
	got = GitCommonDir(wt)
	if got != wantPlain {
		t.Errorf("GitCommonDir(worktree) = %q, want %q (main's .git)", got, wantPlain)
	}

	// Non-git path returns empty.
	if got := GitCommonDir(t.TempDir()); got != "" {
		t.Errorf("GitCommonDir(non-git) = %q, want empty", got)
	}
}

// TestRenameBranch covers the happy path (rename succeeds, current branch
// changes) and the collision path (rename to an existing branch fails with an
// informative "already exists" error that the auto-rename worker keys off).
func TestRenameBranch(t *testing.T) {
	repo := t.TempDir()
	initRepo(t, repo)
	runGit(t, repo, "checkout", "-q", "-b", "claude/abcd1234")

	// Happy path.
	if err := RenameBranch(repo, "claude/abcd1234", "claude/fix-bug"); err != nil {
		t.Fatalf("RenameBranch happy: %v", err)
	}
	if branch := GetBranchName(repo); branch != "claude/fix-bug" {
		t.Errorf("after rename, GetBranchName = %q, want claude/fix-bug", branch)
	}

	// Collision: create another branch, then try to rename onto it.
	runGit(t, repo, "branch", "claude/other")
	err := RenameBranch(repo, "claude/fix-bug", "claude/other")
	if err == nil {
		t.Fatal("RenameBranch onto existing branch should error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "already exists") {
		t.Errorf("RenameBranch collision: error %q should mention 'already exists'", err)
	}
	// Branch unchanged after the failure.
	if branch := GetBranchName(repo); branch != "claude/fix-bug" {
		t.Errorf("after failed rename, GetBranchName = %q, want claude/fix-bug", branch)
	}
}
