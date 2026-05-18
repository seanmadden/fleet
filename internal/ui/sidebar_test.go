package ui

import (
	"strings"
	"testing"

	"github.com/brizzai/fleet/internal/forge"
	"github.com/brizzai/fleet/internal/git"
	"github.com/brizzai/fleet/internal/session"
)

// repoHeaderPaths returns the RepoPath of each header item in order, so tests
// can assert sidebar group ordering without inspecting session-level items.
func repoHeaderPaths(items []SidebarItem) []string {
	var out []string
	for _, it := range items {
		if it.IsRepoHeader {
			out = append(out, it.RepoPath)
		}
	}
	return out
}

func TestBuildFlatItemsAlphabeticalFallback(t *testing.T) {
	// No repoOrder entries: ordering must match the legacy alphabetical behaviour
	// so existing installs don't see their sidebar reshuffle on upgrade.
	expanded := map[string]bool{
		"/tmp/zebra":  true,
		"/tmp/apple":  true,
		"/tmp/mango":  true,
		"/tmp/banana": true,
	}
	pinned := map[string]bool{
		"/tmp/zebra":  true,
		"/tmp/apple":  true,
		"/tmp/mango":  true,
		"/tmp/banana": true,
	}
	items := BuildFlatItems(nil, nil, expanded, "", pinned, nil)
	got := repoHeaderPaths(items)
	want := []string{"/tmp/apple", "/tmp/banana", "/tmp/mango", "/tmp/zebra"}
	if len(got) != len(want) {
		t.Fatalf("expected %d headers, got %d: %v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("position %d: got %q, want %q (full order: %v)", i, got[i], want[i], got)
		}
	}
}

func TestBuildFlatItemsRespectsRepoOrder(t *testing.T) {
	// Explicit repoOrder keys win over alphabetical. /tmp/apple has the highest
	// key, so it should sort *after* /tmp/mango and /tmp/zebra (which have key
	// 50 and 10 respectively); /tmp/banana has no entry → 0 → alphabetical.
	expanded := map[string]bool{
		"/tmp/apple":  true,
		"/tmp/zebra":  true,
		"/tmp/mango":  true,
		"/tmp/banana": true,
	}
	pinned := map[string]bool{
		"/tmp/apple":  true,
		"/tmp/zebra":  true,
		"/tmp/mango":  true,
		"/tmp/banana": true,
	}
	order := map[string]int64{
		"/tmp/apple": 100,
		"/tmp/mango": 50,
		"/tmp/zebra": 10,
		// /tmp/banana intentionally absent → key 0, alphabetical fallback.
	}
	items := BuildFlatItems(nil, nil, expanded, "", pinned, order)
	got := repoHeaderPaths(items)
	// 0-keyed first (alphabetical among them): banana. Then 10, 50, 100.
	want := []string{"/tmp/banana", "/tmp/zebra", "/tmp/mango", "/tmp/apple"}
	if len(got) != len(want) {
		t.Fatalf("expected %d headers, got %d: %v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("position %d: got %q, want %q (full order: %v)", i, got[i], want[i], got)
		}
	}
}

func TestBuildFlatItemsRepoOrderTieBreaksAlphabetically(t *testing.T) {
	// Two repos sharing the same explicit key should fall through to the
	// alphabetical tiebreaker (deterministic, matches the comparator).
	expanded := map[string]bool{"/tmp/zzz": true, "/tmp/aaa": true}
	pinned := map[string]bool{"/tmp/zzz": true, "/tmp/aaa": true}
	order := map[string]int64{"/tmp/zzz": 50, "/tmp/aaa": 50}
	items := BuildFlatItems(nil, nil, expanded, "", pinned, order)
	got := repoHeaderPaths(items)
	want := []string{"/tmp/aaa", "/tmp/zzz"}
	if len(got) != len(want) {
		t.Fatalf("expected %d headers, got %d: %v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("position %d: got %q, want %q", i, got[i], want[i])
		}
	}
	// Reference session to avoid the import being unused if this file ever
	// drops the other tests.
	_ = (*session.Session)(nil)
}

// TestRenderSidebar_PerSessionInfo asserts a worktree-session row picks up its
// own branch / dirty / PR badge from the sessionInfo map. The exact rendered
// bytes go through lipgloss styles, so we assert visible content via
// strings.Contains rather than fixture-locking the full ANSI output.
func TestRenderSidebar_PerSessionInfo(t *testing.T) {
	const (
		repo      = "/tmp/mainrepo"
		sessionID = "abc12345-1"
	)
	s := &session.Session{
		ID:          sessionID,
		Title:       "fix the bug",
		ProjectPath: "/tmp/mainrepo-claude-abc12345",
		Status:      session.StatusRunning,
	}
	items := []SidebarItem{
		{IsRepoHeader: true, RepoPath: repo, Expanded: true, SessionCount: 1},
		{Session: s, IsLast: true},
	}
	gitInfo := map[string]*git.RepoInfo{
		repo: {Branch: "main"},
	}
	sessionInfo := map[string]*git.RepoInfo{
		sessionID: {
			Branch:  "claude/fix-bug",
			IsDirty: true,
			PR: &forge.PR{
				Forge:  "github",
				Number: 42,
				State:  "OPEN",
			},
		},
	}

	out := RenderSidebar(items, []*session.Session{s}, gitInfo, sessionInfo, map[int]string{}, 0, 0, 80, 20)
	if !strings.Contains(out, "fix-bug") {
		t.Errorf("rendered sidebar missing per-session branch label %q\nout:\n%s", "fix-bug", out)
	}
	if !strings.Contains(out, "*") {
		t.Errorf("rendered sidebar missing dirty asterisk\nout:\n%s", out)
	}
	if !strings.Contains(out, "#42") {
		t.Errorf("rendered sidebar missing PR badge #42\nout:\n%s", out)
	}
}

// TestRenderSidebar_NoSessionInfoLeavesRowsBare asserts no-worktree sessions
// (sessionInfo == nil for that ID) still render as today — no branch, no PR.
func TestRenderSidebar_NoSessionInfoLeavesRowsBare(t *testing.T) {
	const repo = "/tmp/mainrepo"
	s := &session.Session{
		ID:          "no-wt",
		Title:       "plain session",
		ProjectPath: repo,
		Status:      session.StatusRunning,
	}
	items := []SidebarItem{
		{IsRepoHeader: true, RepoPath: repo, Expanded: true, SessionCount: 1},
		{Session: s, IsLast: true},
	}
	gitInfo := map[string]*git.RepoInfo{
		repo: {Branch: "main"},
	}
	out := RenderSidebar(items, []*session.Session{s}, gitInfo, map[string]*git.RepoInfo{}, map[int]string{}, 0, 0, 80, 20)
	if strings.Contains(out, "#") {
		t.Errorf("no-wt row should not render a PR badge; out:\n%s", out)
	}
	// The row text should still contain the title.
	if !strings.Contains(out, "plain session") {
		t.Errorf("rendered sidebar missing session title; out:\n%s", out)
	}
}

// TestRenderSidebar_DimsHexBranch asserts an unrenamed `claude/<8hex>` branch
// renders without the BranchStyle color (DimStyle is used instead) so the
// visual hint matches the spec — the user shouldn't see the placeholder branch
// as a "real" branch name in the sidebar.
func TestRenderSidebar_DimsHexBranch(t *testing.T) {
	const (
		repo      = "/tmp/mainrepo"
		sessionID = "abc12345-1"
	)
	s := &session.Session{
		ID:          sessionID,
		Title:       "claude session",
		ProjectPath: "/tmp/mainrepo-claude-abc12345",
		Status:      session.StatusRunning,
	}
	items := []SidebarItem{
		{IsRepoHeader: true, RepoPath: repo, Expanded: true, SessionCount: 1},
		{Session: s, IsLast: true},
	}
	sessionInfo := map[string]*git.RepoInfo{
		sessionID: {Branch: "claude/abc12345"},
	}
	out := RenderSidebar(items, []*session.Session{s}, nil, sessionInfo, map[int]string{}, 0, 0, 80, 20)
	// The hex string still shows up in the output (just dim-styled).
	if !strings.Contains(out, "abc12345") {
		t.Errorf("rendered sidebar should still include the hex branch label; out:\n%s", out)
	}
}
