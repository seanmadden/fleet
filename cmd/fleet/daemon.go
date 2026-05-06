package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
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

// runDaemon serves the fleet gRPC API on a Unix domain socket. Foreground-only
// for Stage 0 PR 4 — `--detach` and admin subcommands (status/stop) are
// deferred to PR 5 / Stage 0.5.
//
// The TUI is unaffected: it still runs `*service.SessionService` in-process.
// `fleet daemon` is a parallel runnable that holds the same service shape
// behind the wire-level contract; PR 5 turns the TUI into a client.
func runDaemon() {
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
