package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"

	fleetv1 "github.com/brizzai/fleet/gen/proto/fleet/v1"
	"github.com/brizzai/fleet/internal/config"
	"github.com/brizzai/fleet/internal/daemonsrv"
	"github.com/brizzai/fleet/internal/debuglog"
	"github.com/brizzai/fleet/internal/migration"
	"github.com/brizzai/fleet/internal/service"
	"github.com/brizzai/fleet/internal/session"
	"github.com/brizzai/fleet/internal/tmux"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

// detachedEnvKey marks the spawned-child half of the --detach re-exec.
// When set, runDaemon skips the re-exec branch and runs the daemon body
// directly. The TUI's autospawn path (internal/daemonclient.spawnDetached)
// also sets this so it doesn't need to know whether the child will fork
// itself again.
const detachedEnvKey = "FLEET_DAEMON_DETACHED"

// runDaemon serves the fleet gRPC API on a Unix domain socket. Defaults to
// foreground; `fleet daemon --detach` re-execs into a Setsid'd child whose
// stdout/stderr land in ~/.config/fleet/daemon.log so a TUI client can fork
// it from autospawn and exit cleanly.
//
// The detach handshake uses an env var (FLEET_DAEMON_DETACHED) to mark the
// child so it doesn't recurse: the parent re-execs with --detach + env set,
// the child sees the env var and skips the re-exec branch, running the
// daemon body in the foreground of its own session.
func runDaemon() {
	if shouldDetach() {
		if err := redetach(); err != nil {
			fmt.Fprintf(os.Stderr, "fleet daemon: failed to detach: %v\n", err)
			os.Exit(1)
		}
		return // parent exits; child has taken over.
	}

	migration.Run()
	debuglog.Init()
	defer debuglog.Close()
	debuglog.Logger.Info("fleet daemon starting", "version", version)

	if err := tmux.IsTmuxAvailable(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	cfg := config.Load()

	storage, err := session.Open(session.DefaultDBPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open database: %v\n", err)
		os.Exit(1)
	}
	defer storage.Close()

	svc := service.NewSessionService(storage, cfg)
	warning, err := svc.Start()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start session service: %v\n", err)
		os.Exit(1)
	}
	defer svc.Stop()
	if warning != "" {
		fmt.Fprintln(os.Stderr, warning)
	}

	sockPath := daemonsrv.SocketPath()
	if err := daemonsrv.PrepareSocket(sockPath); err != nil {
		if errors.Is(err, daemonsrv.ErrAlreadyRunning) {
			fmt.Fprintf(os.Stderr, "fleet daemon: %v (socket: %s)\n", err, sockPath)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Failed to prepare socket %s: %v\n", sockPath, err)
		os.Exit(1)
	}

	lis, err := net.Listen("unix", sockPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to listen on %s: %v\n", sockPath, err)
		os.Exit(1)
	}
	if err := os.Chmod(sockPath, 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to chmod socket: %v\n", err)
		_ = lis.Close()
		os.Exit(1)
	}
	defer os.Remove(sockPath)

	grpcServer := grpc.NewServer()
	fleetv1.RegisterFleetServer(grpcServer, daemonsrv.NewServer(svc))
	// Reflection lets `grpcurl -unix … list` enumerate the schema for
	// debugging without re-distributing .proto files.
	reflection.Register(grpcServer)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		debuglog.Logger.Info("daemon received signal, shutting down", "signal", sig)
		grpcServer.GracefulStop()
	}()

	fmt.Fprintf(os.Stderr, "fleet daemon listening on %s\n", sockPath)
	if err := grpcServer.Serve(lis); err != nil {
		fmt.Fprintf(os.Stderr, "gRPC server error: %v\n", err)
		os.Exit(1)
	}
}

// shouldDetach inspects argv for `--detach` and reports whether the current
// process is the parent half of the re-exec pair. The child sees
// FLEET_DAEMON_DETACHED=1 in its environment and short-circuits to the
// daemon body; the parent does not, and proceeds into redetach().
func shouldDetach() bool {
	if os.Getenv(detachedEnvKey) == "1" {
		return false
	}
	for _, a := range os.Args[1:] {
		if a == "--detach" {
			return true
		}
	}
	return false
}

// redetach re-execs the current binary with the same args plus the marker
// env var, redirects stdout/stderr to ~/.config/fleet/daemon.log, and
// asks the OS to make the child a session leader (Setsid) so it survives
// the parent's exit. The parent prints the new PID to its own stderr and
// returns; the child runs runDaemon's body.
func redetach() error {
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
	// Don't close logFile in this process — the child inherits the fd
	// and writes to it for its lifetime. The kernel reaps it on exit.

	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = append(os.Environ(), detachedEnvKey+"=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return fmt.Errorf("start detached daemon: %w", err)
	}
	// Detach from the child so we don't leave a zombie when it exits;
	// we never call cmd.Wait.
	if rerr := cmd.Process.Release(); rerr != nil {
		fmt.Fprintf(os.Stderr, "fleet daemon: process release: %v\n", rerr)
	}
	fmt.Fprintf(os.Stderr, "fleet daemon: detached (pid %d, log %s)\n", cmd.Process.Pid, logPath)
	return nil
}
