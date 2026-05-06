---
type: changed
---
Internal: introduce `internal/service.SessionService` and `internal/ptybridge.Bridge` packages — the foundation the upcoming Mac-app daemon will wrap over gRPC. No user-visible behavior change; the TUI continues to drive sessions directly in this release.
