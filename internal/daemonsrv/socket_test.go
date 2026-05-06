package daemonsrv

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
)

// shortSocketDir returns a fresh tmpdir under /tmp (NOT t.TempDir) so the
// resulting socket path stays under macOS's 104-byte AF_UNIX sun_path limit.
// Same precaution as internal/ptybridge/bridge_test.go.
func shortSocketDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "fleet-daemon-sock-")
	if err != nil {
		t.Fatalf("mkdir tmpdir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func TestPrepareSocket_NoFile(t *testing.T) {
	path := filepath.Join(shortSocketDir(t), "daemon.sock")
	if err := PrepareSocket(path); err != nil {
		t.Fatalf("PrepareSocket on absent file: %v", err)
	}

	lis, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("net.Listen after PrepareSocket: %v", err)
	}
	_ = lis.Close()
}

func TestPrepareSocket_StaleFile(t *testing.T) {
	path := filepath.Join(shortSocketDir(t), "daemon.sock")
	if err := os.WriteFile(path, []byte{}, 0o600); err != nil {
		t.Fatalf("seed stale file: %v", err)
	}

	if err := PrepareSocket(path); err != nil {
		t.Fatalf("PrepareSocket on stale file: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("stale file not removed: stat err=%v", err)
	}
}

func TestPrepareSocket_LiveDaemon(t *testing.T) {
	dir := shortSocketDir(t)
	path := filepath.Join(dir, "daemon.sock")

	lis, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("seed live listener: %v", err)
	}
	t.Cleanup(func() { _ = lis.Close() })

	go func() {
		for {
			c, err := lis.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()

	err = PrepareSocket(path)
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("PrepareSocket against live daemon: want ErrAlreadyRunning, got %v", err)
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("PrepareSocket removed live socket file: %v", statErr)
	}
}
