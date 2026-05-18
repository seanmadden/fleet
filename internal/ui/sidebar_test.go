package ui

import (
	"testing"

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
