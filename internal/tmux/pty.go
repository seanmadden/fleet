//go:build !windows

package tmux

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"golang.org/x/term"
)

const terminalStyleReset = "\x1b]8;;\x1b\\\x1b[0m\x1b[24m\x1b[39m\x1b[49m"

// Attach attaches to the tmux session with full PTY support. Detach uses
// tmux's native prefix-d chord — Ctrl+B D by default, or whatever the user's
// tmux prefix is configured to. The chord exits the attach-session subprocess,
// which we catch via cmdDone and return cleanly to the caller (fleet TUI).
func (s *Session) Attach(ctx context.Context) error {
	if !s.Exists() {
		return fmt.Errorf("session %s does not exist", s.Name)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Start tmux attach with PTY.
	cmd := exec.CommandContext(ctx, "tmux", "attach-session", "-t", s.Name)
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return fmt.Errorf("failed to start pty: %w", err)
	}
	defer ptmx.Close()

	// Save terminal state and set raw mode.
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return fmt.Errorf("failed to set raw mode: %w", err)
	}
	defer func() { _ = term.Restore(int(os.Stdin.Fd()), oldState) }()

	// Handle window resize signals.
	sigwinch := make(chan os.Signal, 1)
	signal.Notify(sigwinch, syscall.SIGWINCH)
	sigwinchDone := make(chan struct{})
	defer func() {
		signal.Stop(sigwinch)
		close(sigwinchDone)
	}()

	var wg sync.WaitGroup

	// SIGWINCH handler.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-sigwinchDone:
				return
			case _, ok := <-sigwinch:
				if !ok {
					return
				}
				if ws, err := pty.GetsizeFull(os.Stdin); err == nil {
					_ = pty.Setsize(ptmx, ws)
				}
			}
		}
	}()
	// Initial resize.
	sigwinch <- syscall.SIGWINCH

	outputDone := make(chan struct{})

	// Goroutine: copy PTY output to stdout.
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(outputDone)
		_, _ = io.Copy(os.Stdout, ptmx)
	}()

	// Goroutine: forward stdin to PTY. tmux handles its own detach chord, so
	// fleet doesn't intercept anything — every byte passes through.
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(ptmx, os.Stdin)
	}()

	// Wait for the attach-session command to finish (detach or exit).
	cmdDone := make(chan error, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		cmdDone <- cmd.Wait()
	}()

	cleanupAttach := func() {
		cancel()
		_ = ptmx.Close()
		select {
		case <-outputDone:
		case <-time.After(20 * time.Millisecond):
		}
		_, _ = os.Stdout.WriteString(terminalStyleReset)
	}

	attachErr := waitForDetach(ctx, cmdDone)
	cleanupAttach()
	return attachErr
}

// waitForDetach waits for the attach-session command to exit or context cancellation.
func waitForDetach(ctx context.Context, cmdDone <-chan error) error {
	select {
	case err := <-cmdDone:
		return classifyExitError(ctx, err)
	case <-ctx.Done():
		return nil
	}
}

// classifyExitError returns nil for expected exit codes (0, 1) or context cancellation.
func classifyExitError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		if exitErr.ExitCode() == 0 || exitErr.ExitCode() == 1 {
			return nil
		}
	}
	return err
}
