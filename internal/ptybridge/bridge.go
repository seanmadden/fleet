//go:build !windows

package ptybridge

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"

	"github.com/creack/pty"
)

// TerminalSession represents one active PTY connection to a tmux session.
type TerminalSession struct {
	ptmx   *os.File
	cmd    *exec.Cmd
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
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
		ptmx:   ptmx,
		cmd:    cmd,
		ctx:    ctx,
		cancel: cancel,
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
func (b *Bridge) Detach(sessionID string) error {
	b.mu.RLock()
	ts, exists := b.sessions[sessionID]
	b.mu.RUnlock()
	if !exists {
		return fmt.Errorf("session %s not attached", sessionID)
	}
	ts.cancel()
	ts.wg.Wait()
	return nil
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
