import Foundation
import GRPCCore
import GRPCNIOTransportHTTP2

// DaemonClientRunner owns the daemon-side lifecycle for the Mac app: it
// resolves the socket path (autospawning the Go daemon if needed), opens a
// long-lived gRPC client against it, and runs the session/repo/slot-binding
// consumers concurrently. The runner is one-shot — it owns the GRPCClient
// for as long as `run(model:)` is awaited; cancel the surrounding Task to
// shut everything down.
//
// Mirrors the behaviour of `internal/daemonclient.Client`: ListSessions and
// ListRepos are server-streaming and stay open for the life of the client;
// ListSlotBindings is unary and re-fetched on session changes (V1
// simplification — refresh on a slow timer rather than diff-tracking).
enum DaemonClientRunner {
    static func run(model: AppModel) async {
        // Phase 1 — make sure the daemon is reachable. If anything here fails,
        // surface a clear error and bail; the user can retry by re-launching.
        let socketPath: String
        do {
            socketPath = try await EnsureRunning.ensure()
        } catch {
            await model.set(connectionState: .disconnected,
                            error: "Daemon unavailable: \(error)")
            return
        }

        await model.set(connectionState: .connecting, error: nil)

        // Phase 2 — open the gRPC client. `withGRPCClient` takes care of
        // running the connection event loop for as long as the closure is
        // alive. Stream consumers run inside the closure as a TaskGroup so
        // a fatal error in one tears them all down and bubbles up.
        do {
            try await withGRPCClient(
                transport: .http2NIOPosix(
                    target: .unixDomainSocket(path: socketPath),
                    transportSecurity: .plaintext
                )
            ) { grpcClient in
                let fleet = FleetFleet.Client(wrapping: grpcClient)
                await model.set(connectionState: .connected, error: nil)

                try await withThrowingTaskGroup(of: Void.self) { group in
                    group.addTask {
                        try await runSessionStream(fleet, model: model)
                    }
                    group.addTask {
                        try await runRepoStream(fleet, model: model)
                    }
                    group.addTask {
                        try await runSlotBindingsRefresher(fleet, model: model)
                    }
                    // Wait for the first child to finish. Streams should run
                    // forever; if one returns it means the Task was cancelled
                    // (app shutdown) — cancel the rest.
                    for try await _ in group {
                        group.cancelAll()
                        break
                    }
                }
            }
        } catch is CancellationError {
            // Graceful app shutdown.
        } catch {
            await model.set(connectionState: .disconnected,
                            error: "Daemon disconnected: \(error)")
        }
    }
}
