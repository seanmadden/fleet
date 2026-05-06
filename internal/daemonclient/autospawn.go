package daemonclient

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/brizzai/fleet/internal/daemonsrv"
	"github.com/brizzai/fleet/internal/debuglog"
)

// daemonStartTimeout caps how long EnsureRunning waits for a freshly
// spawned daemon to start accepting connections. 3s comfortably covers
// config load + storage open + service.Start + listen on a warm machine.
const daemonStartTimeout = 3 * time.Second

// EnsureRunning makes sure a fleet daemon is listening at SocketPath. If
// one is already up it returns immediately; otherwise it spawns
// `<self> daemon --detach` (Setsid + log redirect to ~/.config/fleet/daemon.log)
// and waits up to daemonStartTimeout for the socket to become connectable.
//
// Callers should use this to gate TUI startup in daemon mode and fall back
// to in-process service if it fails.
func EnsureRunning(ctx context.Context) (string, error) {
	sock := daemonsrv.SocketPath()
	if isAlive(sock) {
		return sock, nil
	}

	if err := spawnDetached(); err != nil {
		return sock, fmt.Errorf("spawn daemon: %w", err)
	}

	deadlineCtx, cancel := context.WithTimeout(ctx, daemonStartTimeout)
	defer cancel()
	if err := waitForSocket(deadlineCtx, sock); err != nil {
		return sock, fmt.Errorf("daemon did not start within %s: %w", daemonStartTimeout, err)
	}
	return sock, nil
}

// isAlive returns true if a daemon is currently accepting connections at
// path. Mirrors daemonsrv.PrepareSocket's liveness probe so EnsureRunning
// and the daemon's stale-socket cleanup share the same definition of
// "live."
func isAlive(path string) bool {
	conn, err := net.DialTimeout("unix", path, 200*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// spawnDetached starts a new `<self> daemon --detach` process via Setsid
// so it survives the parent TUI's exit, with stdout/stderr redirected to
// ~/.config/fleet/daemon.log. Does not Wait — the child is intentionally
// orphaned so its lifetime is independent.
func spawnDetached() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate self binary: %w", err)
	}

	logPath := filepath.Join(filepath.Dir(daemonsrv.SocketPath()), "daemon.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return fmt.Errorf("mkdir for daemon log: %w", err)
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open daemon log %s: %w", logPath, err)
	}
	// We deliberately do NOT close logFile here — the child inherits the
	// fd and writes to it for its lifetime. The OS reaps it when the
	// child exits.

	cmd := exec.Command(exe, "daemon", "--detach")
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	// Mark the child so it knows it's the spawned half of the re-exec
	// pair. cmd/fleet/daemon.go reads this env var and, if set, skips
	// the re-exec and runs the daemon body directly.
	cmd.Env = append(os.Environ(), "FLEET_DAEMON_DETACHED=1")

	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return fmt.Errorf("start daemon: %w", err)
	}
	debuglog.Logger.Info("daemonclient: spawned daemon", "pid", cmd.Process.Pid, "log", logPath)
	// Release the child so we don't accumulate a zombie when it eventually
	// exits — cmd.Wait would otherwise be required to reap.
	if err := cmd.Process.Release(); err != nil {
		debuglog.Logger.Warn("daemonclient: process release failed", "err", err)
	}
	return nil
}

// waitForSocket polls every 100ms until the socket becomes connectable or
// ctx fires. Returns nil on success, ctx.Err() on timeout/cancellation.
func waitForSocket(ctx context.Context, path string) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if isAlive(path) {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.Join(errors.New("socket never became ready"), ctx.Err())
		case <-ticker.C:
		}
	}
}
