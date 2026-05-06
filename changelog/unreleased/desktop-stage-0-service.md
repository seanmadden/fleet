---
type: changed
---
Internal: introduce `internal/service.SessionService`, `internal/ptybridge.Bridge`, and the `fleet daemon` subcommand (`internal/daemonsrv`) — the daemon-shaped foundation the upcoming Mac app and the future TUI client will both ride on. The TUI continues to drive sessions in-process for now; the daemon is a parallel runnable that holds the same service surface behind the gRPC contract at `~/.config/fleet/daemon.sock`. No user-visible behavior change in this release.
