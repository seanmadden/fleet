// Package daemonsrv implements the gRPC server that exposes
// *service.SessionService over a Unix domain socket. The TUI does not
// consume it yet (Stage 0 PR 5 wires that up); for PR 4 the daemon is a
// separate runnable that ships the contract while leaving in-process TUI
// behavior untouched.
package daemonsrv

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"time"
)

// ErrAlreadyRunning is returned by PrepareSocket when another daemon is
// already accepting connections on the target socket.
var ErrAlreadyRunning = errors.New("daemon already running on this socket")

// SocketPath returns the default daemon socket location:
// ~/.config/fleet/daemon.sock. Mirrors session.DefaultDBPath /
// config.DefaultConfigPath conventions — there's deliberately no
// XDG_CONFIG_HOME indirection, since the rest of the project doesn't use it
// either.
func SocketPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "fleet", "daemon.sock")
}

// PrepareSocket cleans up a stale socket file at path so a subsequent
// net.Listen("unix", path) call can succeed. If a live daemon is already
// listening, returns ErrAlreadyRunning instead of removing it.
//
// "Stale" means the file exists but no process is accepting on it — common
// after a SIGKILL or crash where Go's deferred os.Remove never ran. Distinguished
// from "live" by attempting a short Dial: a connection refused / timeout means
// stale, a successful dial means live.
func PrepareSocket(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}

	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	conn, err := net.DialTimeout("unix", path, 200*time.Millisecond)
	if err == nil {
		_ = conn.Close()
		return ErrAlreadyRunning
	}

	if rmErr := os.Remove(path); rmErr != nil && !os.IsNotExist(rmErr) {
		return rmErr
	}
	return nil
}
