//go:build !windows

package ptybridge

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/creack/pty"
)

// TerminalSession represents one active PTY connection to a tmux session.
type TerminalSession struct {
	ptmx     *os.File
	cmd      *exec.Cmd
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	tmuxName string // session name, kept so Detach can issue a clean `tmux detach-client -s <name>`
}

// Bridge manages multiple PTY-backed terminal sessions.
// Unlike pty.go (which uses os.Stdin/os.Stdout for TUI attach), Bridge
// uses callbacks for output — suitable for Wails/web frontends where the
// browser handles rendering and input natively.
type Bridge struct {
	sessions map[string]*TerminalSession
	mu       sync.RWMutex
}

// NewBridge creates a new PTY bridge.
func NewBridge() *Bridge {
	return &Bridge{
		sessions: make(map[string]*TerminalSession),
	}
}

// Attach starts a tmux attach-session via a PTY.
// Output bytes are delivered to onOutput; onExit is called when the process ends.
// Multiple sessions can be attached simultaneously.
func (b *Bridge) Attach(sessionID, tmuxName string, rows, cols int, onOutput func(sid string, data []byte), onExit func(sid string)) error {
	b.mu.Lock()
	if _, exists := b.sessions[sessionID]; exists {
		b.mu.Unlock()
		return fmt.Errorf("session %s already attached", sessionID)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "tmux", "attach-session", "-t", tmuxName)

	ptmx, err := pty.Start(cmd)
	if err != nil {
		cancel()
		b.mu.Unlock()
		return fmt.Errorf("failed to start pty: %w", err)
	}

	// Set initial size.
	_ = pty.Setsize(ptmx, &pty.Winsize{
		Rows: uint16(rows),
		Cols: uint16(cols),
	})

	ts := &TerminalSession{
		ptmx:     ptmx,
		cmd:      cmd,
		ctx:      ctx,
		cancel:   cancel,
		tmuxName: tmuxName,
	}
	b.sessions[sessionID] = ts
	b.mu.Unlock()

	// Read PTY output in a goroutine.
	ts.wg.Add(1)
	go func() {
		defer ts.wg.Done()
		buf := make([]byte, 32*1024)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				// Copy the data to avoid buffer reuse issues.
				data := make([]byte, n)
				copy(data, buf[:n])
				onOutput(sessionID, data)
			}
			if err != nil {
				if err != io.EOF {
					_ = err // swallow
				}
				return
			}
		}
	}()

	// Wait for process exit in a goroutine.
	ts.wg.Add(1)
	go func() {
		defer ts.wg.Done()
		_ = cmd.Wait()
		_ = ptmx.Close()

		b.mu.Lock()
		delete(b.sessions, sessionID)
		b.mu.Unlock()

		onExit(sessionID)
	}()

	return nil
}

// Detach disconnects from a terminal session.
//
// Uses tmux's own client-detach protocol rather than killing the child
// process. This matters because the child here is `tmux attach-session`,
// which is *connected* to a real tmux server (the user's default server,
// or a test-isolated one — see bridge_test.go). The historical
// implementation just called ts.cancel(), but exec.CommandContext cancels
// with os.Kill (SIGKILL); SIGKILL'ing an attached tmux client mid-protocol
// leaves the server with half-flushed write queues for that client. On
// some tmux builds the server recovers by detaching *every* client on the
// socket — i.e. blast radius escalates from "just our PTY" to "every
// human or fleet session sharing this server." That actually shipped: on
// 2026-05-06, running `go test ./...` on this branch would silently
// disconnect the user from unrelated tmux sessions, both fleet-managed
// and manual ones, because bridge_test ran against the live default
// socket and Detach SIGKILL'd the test client.
//
// The clean path is `tmux detach-client -s <name>` — it goes through the
// tmux protocol, the server flushes pending writes to that client, sends
// a detach message, the client returns from attach-session normally,
// cmd.Wait completes, the read goroutine exits on EOF. We give that path
// a short window (detachGraceWindow) before falling back to ts.cancel(),
// so a hung server can't block Detach forever.
//
// detachGraceWindow is short on purpose: real tmux detaches in <50ms
// locally; if we're past 500ms something is wrong and we should just
// SIGKILL and move on.
func (b *Bridge) Detach(sessionID string) error {
	const detachGraceWindow = 500 * time.Millisecond

	b.mu.RLock()
	ts, exists := b.sessions[sessionID]
	b.mu.RUnlock()
	if !exists {
		return fmt.Errorf("session %s not attached", sessionID)
	}

	// Best-effort clean detach. Errors here (e.g. server already gone,
	// session already killed externally) are non-fatal — we'll fall
	// through to ts.cancel() which guarantees the child dies.
	_ = exec.Command("tmux", "detach-client", "-s", ts.tmuxName).Run()

	// Wait for the read+wait goroutines to finish naturally, but cap it.
	done := make(chan struct{})
	go func() {
		ts.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-time.After(detachGraceWindow):
		// Clean detach didn't take. Fall back to SIGKILL via context.
		ts.cancel()
		ts.wg.Wait()
		return nil
	}
}

// WriteInput sends data to the PTY stdin of a terminal session.
func (b *Bridge) WriteInput(sessionID string, data []byte) error {
	b.mu.RLock()
	ts, exists := b.sessions[sessionID]
	b.mu.RUnlock()
	if !exists {
		return fmt.Errorf("session %s not attached", sessionID)
	}
	_, err := ts.ptmx.Write(data)
	return err
}

// Resize changes the terminal dimensions for a session.
func (b *Bridge) Resize(sessionID string, rows, cols int) error {
	b.mu.RLock()
	ts, exists := b.sessions[sessionID]
	b.mu.RUnlock()
	if !exists {
		return fmt.Errorf("session %s not attached", sessionID)
	}
	return pty.Setsize(ts.ptmx, &pty.Winsize{
		Rows: uint16(rows),
		Cols: uint16(cols),
	})
}

// DetachAll disconnects all active terminal sessions.
func (b *Bridge) DetachAll() {
	b.mu.Lock()
	sessions := make(map[string]*TerminalSession, len(b.sessions))
	for k, v := range b.sessions {
		sessions[k] = v
	}
	b.mu.Unlock()

	for _, ts := range sessions {
		ts.cancel()
		ts.wg.Wait()
	}
}
