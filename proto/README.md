# proto/

gRPC schema for the fleet daemon. The daemon and all clients (Mac app, TUI, CLI) use this contract to communicate over `~/.config/fleet/daemon.sock`.

## Layout

```
proto/
└── fleet/
    └── v1/
        └── fleet.proto    # the schema
```

We use semver-style versioning in the package name (`fleet.v1`). Breaking changes go to `fleet.v2` in a sibling directory; the daemon supports both for one minor release before dropping the older version.

## Codegen

We use [buf](https://buf.build/) (recommended over raw `protoc` — it handles dep resolution and lint).

### Go (daemon, TUI, CLI)

```bash
buf generate                              # generates into gen/proto/fleet/v1/
```

`buf.gen.yaml` (when added) writes Go code with `protoc-gen-go` and gRPC code with `protoc-gen-go-grpc`. Output lands at `gen/proto/fleet/v1/`.

### Swift (Mac app)

```bash
buf generate --template buf.gen.swift.yaml   # writes into macapp/Fleet/DaemonClient/Generated/
```

We use `grpc-swift` (Apple's gRPC stack) and `SwiftProtobuf`. Output is committed to the repo so Xcode builds without invoking buf.

## What's NOT in this schema

- **PTY data.** The daemon never proxies PTY bytes. Clients spawn a local `tmux attach` against the shared tmux socket at `~/.config/fleet/tmux.sock` and render the PTY locally. This service is purely a control plane.
- **Chrome native messaging.** Stays on its own socket (`~/.config/fleet/chrome.sock`). Decision deferred to whether to fold it into the daemon eventually.
- **V2 features** (diff streaming, cost analytics, suggested actions). Placeholder comments in `fleet.proto`; flesh out when V2 lands.

## Style conventions

- **snake_case** for field names (proto convention; generates correctly in both Go and Swift).
- **`STATUS_X` enum prefix** with `STATUS_UNSPECIFIED = 0` (proto3 best practice).
- **`google.protobuf.Empty`** for void responses; **`google.protobuf.Timestamp`** for times.
- **Streaming** for live data (sessions, repos, hook events). Snapshot is delivered as the first messages, then deltas.
- **No fields removed without deprecating first** — even within v1.
