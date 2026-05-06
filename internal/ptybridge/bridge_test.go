//go:build !windows

package ptybridge

import (
	"fmt"
	"os"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"
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
//	test process inherits TMUX=/private/tmp/tmux-501/default,..., and a
//	plain `tmux new-session` honours that inherited path, ignoring
//	whatever we set TMUX_TMPDIR to. Result: the test runs on the live
//	server even with TMUX_TMPDIR pointed at a temp dir.
//
//	t.Setenv("TMUX", "") doesn't help — tmux only checks if TMUX is
//	non-NULL, and Setenv with an empty string is still NULL-distinct.
//	We need a real os.Unsetenv, restored in t.Cleanup.
//
// This combo (unset TMUX + set TMUX_TMPDIR) makes plain `tmux new-session`
// land in the per-test directory under the default socket name, isolated
// from the user's live server.
func isolateLiveTmux(t *testing.T) {
	t.Helper()
	// CRITICAL: must NOT use t.TempDir() here. macOS AF_UNIX sun_path is
	// 104 bytes; tmux composes its socket path as `<TMUX_TMPDIR>/tmux-<uid>/default`
	// (≈17 trailing chars), and `t.TempDir()` produces paths under
	// `/var/folders/.../T/<TestName><digits>/<seq>` that are ~70-90 bytes
	// before the suffix. Long test names (e.g. TestBridge_AttachWriteDetach_RoundTrip)
	// push the resulting socket path past 104 bytes and `tmux new-session`
	// fails with a bare `exit status 1`. Short `/tmp/...` keeps us safe.
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

func TestBridge_DetachWithoutAttach_ReturnsError(t *testing.T) {
	b := NewBridge()
	if err := b.Detach("nope"); err == nil {
		t.Errorf("Detach on missing session: want error, got nil")
	}
	if err := b.WriteInput("nope", []byte("x")); err == nil {
		t.Errorf("WriteInput on missing session: want error, got nil")
	}
	if err := b.Resize("nope", 24, 80); err == nil {
		t.Errorf("Resize on missing session: want error, got nil")
	}
}

func TestBridge_AttachWriteDetach_RoundTrip(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed; skipping bridge roundtrip")
	}

	// CRITICAL: isolate this test from the user's live tmux server.
	//
	// See the long-form note on isolateLiveTmux above. Short version:
	//   1. Without isolation, every `tmux ...` here (the new-session
	//      below, the attach-session inside Bridge.Attach, the
	//      kill-session in cleanup) targets the same default server
	//      hosting the user's real fleet sessions and manual tmux sessions.
	//   2. Bridge.Detach historically SIGKILL'd the attached tmux client.
	//      SIGKILL'ing an attached client mid-protocol leaves the server
	//      with half-flushed write queues; some tmux builds recover by
	//      detaching *every* client on the socket — so the blast radius
	//      grew from "our PTY" to "every human or fleet session sharing
	//      this server." (Detach is now fixed in bridge.go to do a clean
	//      `tmux detach-client -s <name>` first.)
	//   3. The 2026-05-06 incident: `go test -race ./...` on this branch
	//      silently disconnected the user from unrelated tmux sessions.
	//      The first attempted fix used only TMUX_TMPDIR — that wasn't
	//      enough, because when tests run from inside a tmux session the
	//      inherited TMUX env var beats TMUX_TMPDIR in tmux's resolution
	//      order, so plain `tmux new-session` still hit the live socket.
	//      isolateLiveTmux unsets TMUX too, which is the actual fix.
	isolateLiveTmux(t)

	tmuxName := fmt.Sprintf("ptybridge-test-%d", time.Now().UnixNano())
	if err := exec.Command("tmux", "new-session", "-d", "-s", tmuxName, "-x", "80", "-y", "24", "cat").Run(); err != nil {
		t.Fatalf("tmux new-session: %v", err)
	}
	defer func() { _ = exec.Command("tmux", "kill-session", "-t", tmuxName).Run() }()

	b := NewBridge()
	var bytesSeen, exits int32
	exited := make(chan struct{}, 1)

	err := b.Attach("s1", tmuxName, 24, 80,
		func(_ string, data []byte) { atomic.AddInt32(&bytesSeen, int32(len(data))) },
		func(_ string) {
			atomic.AddInt32(&exits, 1)
			select {
			case exited <- struct{}{}:
			default:
			}
		},
	)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}

	// Re-attach same id should refuse.
	if err := b.Attach("s1", tmuxName, 24, 80, func(string, []byte) {}, func(string) {}); err == nil {
		t.Errorf("double Attach: want error, got nil")
	}

	// Resize while attached should succeed.
	if err := b.Resize("s1", 30, 100); err != nil {
		t.Errorf("Resize: %v", err)
	}

	// WriteInput should not error. Real keystrokes get attach-mode interception
	// from tmux, so we don't assert on echoed content — just that the write
	// reaches the PTY without error.
	if err := b.WriteInput("s1", []byte{0x20}); err != nil {
		t.Errorf("WriteInput: %v", err)
	}

	// Wait briefly for the read goroutine to pick up tmux's status-bar/init bytes.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && atomic.LoadInt32(&bytesSeen) == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	if atomic.LoadInt32(&bytesSeen) == 0 {
		t.Errorf("expected onOutput to receive bytes from tmux, got none")
	}

	if err := b.Detach("s1"); err != nil {
		t.Errorf("Detach: %v", err)
	}

	select {
	case <-exited:
	case <-time.After(2 * time.Second):
		t.Errorf("onExit not fired within 2s of Detach")
	}
	if got := atomic.LoadInt32(&exits); got != 1 {
		t.Errorf("onExit calls: want 1, got %d", got)
	}
}
