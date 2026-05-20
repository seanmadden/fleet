package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseWorktreePorcelain(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		want   int // expected count
		checks func(t *testing.T, result []WorkspaceInfo)
	}{
		{
			"single worktree",
			"worktree /code/myrepo\nbranch refs/heads/main\n\n",
			1,
			func(t *testing.T, result []WorkspaceInfo) {
				if result[0].Path != "/code/myrepo" {
					t.Errorf("path = %q, want /code/myrepo", result[0].Path)
				}
				if result[0].Branch != "main" {
					t.Errorf("branch = %q, want main", result[0].Branch)
				}
				if result[0].Name != "myrepo" {
					t.Errorf("name = %q, want myrepo", result[0].Name)
				}
			},
		},
		{
			"multiple worktrees",
			"worktree /code/myrepo\nbranch refs/heads/main\n\nworktree /code/myrepo-feat\nbranch refs/heads/feat\n\n",
			2,
			func(t *testing.T, result []WorkspaceInfo) {
				if result[0].Branch != "main" {
					t.Errorf("first branch = %q, want main", result[0].Branch)
				}
				if result[1].Branch != "feat" {
					t.Errorf("second branch = %q, want feat", result[1].Branch)
				}
			},
		},
		{
			"no branch (detached HEAD)",
			"worktree /code/myrepo\nHEAD abc123\ndetached\n\n",
			1,
			func(t *testing.T, result []WorkspaceInfo) {
				if result[0].Branch != "" {
					t.Errorf("branch = %q, want empty", result[0].Branch)
				}
			},
		},
		{"empty input", "", 0, nil},
		{"whitespace only", "\n\n", 0, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseWorktreePorcelain(tt.input)
			if len(result) != tt.want {
				t.Fatalf("len(result) = %d, want %d", len(result), tt.want)
			}
			if tt.checks != nil {
				tt.checks(t, result)
			}
		})
	}
}

func TestSanitizeBranchInput(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"space becomes dash", "my branch", "my-branch"},
		{"multiple spaces", "a b c d", "a-b-c-d"},
		{"strip tilde", "feat~bad", "featbad"},
		{"strip caret", "feat^bad", "featbad"},
		{"strip colon", "feat:bad", "featbad"},
		{"strip question", "feat?bad", "featbad"},
		{"strip star", "feat*bad", "featbad"},
		{"strip bracket", "feat[bad", "featbad"},
		{"strip backslash", "feat\\bad", "featbad"},
		{"strip backtick", "feat`bad", "featbad"},
		{"strip tab control", "feat\tbad", "featbad"},
		{"strip del control", "feat\x7fbad", "featbad"},
		{"keep slash", "feature/login", "feature/login"},
		{"keep dots", "v1.2.3", "v1.2.3"},
		{"keep underscore", "fix_bug_123", "fix_bug_123"},
		{"empty stays empty", "", ""},
		{"mixed", "my cool~branch/v2 ", "my-coolbranch/v2-"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SanitizeBranchInput(tt.input)
			if got != tt.want {
				t.Errorf("SanitizeBranchInput(%q) = %q, want %q", tt.input, got, tt.want)
			}
			// Idempotence.
			if twice := SanitizeBranchInput(got); twice != got {
				t.Errorf("SanitizeBranchInput not idempotent: %q -> %q -> %q", tt.input, got, twice)
			}
		})
	}
}

func TestSanitizeBranchInputWithCursor(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		cursor     int
		wantOut    string
		wantCursor int
	}{
		{"no change", "feat", 2, "feat", 2},
		{"space before cursor replaced (cursor unchanged)", "a b", 3, "a-b", 3},
		{"drop char before cursor", "a~bc", 4, "abc", 3},
		{"drop char after cursor", "ab~c", 2, "abc", 2},
		{"drop char at cursor", "ab~c", 3, "abc", 2},
		{"multiple drops before cursor", "~a~b~c", 6, "abc", 3},
		{"cursor past end clamps", "abc", 10, "abc", 3},
		{"negative cursor clamps to 0", "~abc", -1, "abc", 0},
		{"empty string", "", 0, "", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotOut, gotCursor := SanitizeBranchInputWithCursor(tt.input, tt.cursor)
			if gotOut != tt.wantOut {
				t.Errorf("out = %q, want %q", gotOut, tt.wantOut)
			}
			if gotCursor != tt.wantCursor {
				t.Errorf("cursor = %d, want %d", gotCursor, tt.wantCursor)
			}
		})
	}
}

func TestValidateBranchName(t *testing.T) {
	invalid := []struct {
		name  string
		input string
	}{
		{"empty", ""},
		{"at sign alone", "@"},
		{"leading dash", "-bad"},
		{"leading slash", "/bad"},
		{"leading dot", ".bad"},
		{"component leading dot", "feature/.bad"},
		{"trailing dot", "bad."},
		{"component trailing dot", "a./b"},
		{"trailing slash", "bad/"},
		{"trailing .lock", "feature.lock"},
		{"component .lock", "a.lock/b"},
		{"double dot", "a..b"},
		{"at brace", "a@{b"},
		{"double slash", "a//b"},
	}
	for _, tt := range invalid {
		t.Run("invalid/"+tt.name, func(t *testing.T) {
			if got := ValidateBranchName(tt.input); got == "" {
				t.Errorf("ValidateBranchName(%q) = \"\", want error message", tt.input)
			}
		})
	}

	valid := []string{
		"feature-login",
		"feature/login",
		"fix-bug-123",
		"v1.2.3",
		"release/2026-04-16",
		"a",
	}
	for _, v := range valid {
		t.Run("valid/"+v, func(t *testing.T) {
			if got := ValidateBranchName(v); got != "" {
				t.Errorf("ValidateBranchName(%q) = %q, want \"\"", v, got)
			}
		})
	}
}

func TestSanitizeBranchName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"slashes", "feature/login", "feature-login"},
		{"spaces", "my branch", "my-branch"},
		{"double dots", "v1..v2", "v1-v2"},
		{"clean name", "fix-bug-123", "fix-bug-123"},
		{"multiple slashes", "user/feature/thing", "user-feature-thing"},
		{"mixed", "feat/my branch..v2", "feat-my-branch-v2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SanitizeBranchName(tt.input)
			if got != tt.want {
				t.Errorf("SanitizeBranchName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestDeriveWorktreePath_FromWorktree exercises the bug fix where calling
// deriveWorktreePath from inside a linked worktree used to produce a sibling
// of the *worktree* instead of being anchored at the *main* repo. The current
// behaviour resolves through `git rev-parse --git-common-dir` so the result is
// always rooted at the main repo's `.claude/worktrees/<name>` regardless of
// which worktree fleet happened to be invoked in.
func TestDeriveWorktreePath_FromWorktree(t *testing.T) {
	root := t.TempDir()
	main := filepath.Join(root, "myrepo")
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	runCmd := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s in %s: %v: %s", strings.Join(args, " "), dir, err, out)
		}
	}
	runCmd(main, "init", "--initial-branch=main", "-q")
	runCmd(main, "config", "user.email", "test@example.com")
	runCmd(main, "config", "user.name", "test")
	runCmd(main, "commit", "--allow-empty", "-q", "-m", "init")

	wt := filepath.Join(main, ".claude", "worktrees", "claude-abc12345")
	runCmd(main, "worktree", "add", "-b", "claude/abc12345", wt)

	got := deriveWorktreePath(wt, "feature-x")
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	want := filepath.Join(resolvedRoot, "myrepo", ".claude", "worktrees", "feature-x")
	if got != want {
		t.Errorf("deriveWorktreePath(worktree) = %q, want %q (anchored at main repo's .claude/worktrees, not at the worktree)", got, want)
	}
}

func TestDeriveWorktreePath(t *testing.T) {
	tests := []struct {
		name     string
		repoPath string
		wtName   string
		wantTail string // trailing path the result should end with (after the repo root)
	}{
		{"basic", "/code/myrepo", "feature-login", "myrepo/.claude/worktrees/feature-login"},
		{"nested repo", "/home/user/projects/app", "hotfix", "app/.claude/worktrees/hotfix"},
		{"claude session shape", "/code/myrepo", "claude-abc12345", "myrepo/.claude/worktrees/claude-abc12345"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := deriveWorktreePath(tt.repoPath, tt.wtName)
			if !strings.HasSuffix(got, tt.wantTail) {
				t.Errorf("deriveWorktreePath(%q, %q) = %q, want suffix %q", tt.repoPath, tt.wtName, got, tt.wantTail)
			}
			// Worktree must live inside the repo, not as a sibling of it.
			if filepath.Dir(filepath.Dir(filepath.Dir(got))) != tt.repoPath {
				t.Errorf("deriveWorktreePath result %q is not under %q/.claude/worktrees/", got, tt.repoPath)
			}
		})
	}
}

func TestDeriveWorktreePathPreview(t *testing.T) {
	got := DeriveWorktreePathPreview("/code/myrepo", "claude-abc12345")
	want := ".claude/worktrees/claude-abc12345"
	if got != want {
		t.Errorf("DeriveWorktreePathPreview = %q, want %q", got, want)
	}
}

// TestEnsureWorktreeExcluded covers the .git/info/exclude maintenance that
// keeps `.claude/worktrees/` invisible to `git status` on the main checkout.
// Idempotent: running twice should add the entry exactly once.
func TestEnsureWorktreeExcluded(t *testing.T) {
	root := t.TempDir()
	main := filepath.Join(root, "myrepo")
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	runCmd := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s in %s: %v: %s", strings.Join(args, " "), dir, err, out)
		}
	}
	runCmd(main, "init", "--initial-branch=main", "-q")

	excludePath := filepath.Join(main, ".git", "info", "exclude")
	entry := "/" + WorktreesSubdir + "/"

	// First call: should add the entry.
	if err := ensureWorktreeExcluded(main); err != nil {
		t.Fatalf("ensureWorktreeExcluded (first call): %v", err)
	}
	contents, err := os.ReadFile(excludePath)
	if err != nil {
		t.Fatalf("read exclude after first call: %v", err)
	}
	if !strings.Contains(string(contents), entry+"\n") {
		t.Fatalf("exclude missing %q after first call:\n%s", entry, contents)
	}

	// Second call: should be a no-op (exact-line match).
	if err := ensureWorktreeExcluded(main); err != nil {
		t.Fatalf("ensureWorktreeExcluded (second call): %v", err)
	}
	contents2, err := os.ReadFile(excludePath)
	if err != nil {
		t.Fatalf("read exclude after second call: %v", err)
	}
	if string(contents2) != string(contents) {
		t.Errorf("exclude changed on second call (not idempotent):\nbefore:\n%s\nafter:\n%s", contents, contents2)
	}

	// Count occurrences to make sure idempotence holds line-exactly.
	if got := strings.Count(string(contents2), entry); got != 1 {
		t.Errorf("entry appears %d times, want exactly 1", got)
	}
}

// TestLegacyDeriveWorktreePath verifies the legacy sibling path is still
// computable for Destroy's back-compat fallback.
func TestLegacyDeriveWorktreePath(t *testing.T) {
	got := legacyDeriveWorktreePath("/code/myrepo", "claude-abc12345")
	want := "/code/myrepo-claude-abc12345"
	if got != want {
		t.Errorf("legacyDeriveWorktreePath = %q, want %q", got, want)
	}
}
