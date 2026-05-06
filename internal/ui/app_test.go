package ui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/brizzai/fleet/internal/config"
	"github.com/brizzai/fleet/internal/service"
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

	svc := service.NewSessionService(storage, cfg)

	// Should not panic.
	home := NewHome(svc, storage, cfg, "test")
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

// TestViewGitInfoCacheRace was a regression guard for the pre-PR-3c
// architecture where the UI's status worker wrote to h.gitInfoCache from a
// background goroutine while View() read it from the bubbletea loop. Stage 0
// PR 3c moved all polling into SessionService and made gitInfoCache a single-
// writer mirror updated only on the bubbletea goroutine via service events,
// so the original race scenario can no longer occur.
