---
type: changed
---
Internal: complete the daemon-shaped Stage 0 foundation. `internal/service.SessionService`, `internal/ptybridge.Bridge`, the `fleet daemon` subcommand (`internal/daemonsrv`, with `--detach` for background launch), and the new gRPC client `internal/daemonclient` are all in place — the upcoming Mac app and the TUI both ride this contract. The TUI now defaults to driving sessions through the daemon (autospawned on first launch); pass `--standalone` to keep the in-process path. The daemon socket lives at `~/.config/fleet/daemon.sock` and its log at `~/.config/fleet/daemon.log`. No visible behavior change for default users.
