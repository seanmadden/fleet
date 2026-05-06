//go:build !windows

package ptybridge

import (
	"fmt"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"
)

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
