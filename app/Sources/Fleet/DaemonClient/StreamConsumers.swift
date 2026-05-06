import Foundation
import GRPCCore
import SwiftProtobuf

// Stream state lives in a reference-typed box so the @Sendable onResponse
// closure can mutate it. A real concurrency model isn't needed: each box is
// touched only by the single Task running its consumer, and that task uses
// `for try await` for sequential message delivery.
private final class StreamState<ID: Hashable>: @unchecked Sendable {
    var seenIDs = Set<ID>()
    var inSnapshot = true
}

// Stream consumers — one per server-streaming RPC. Each runs forever in a
// reconnect loop with the documented exp-backoff, mirroring
// `internal/daemonclient/stream.go`.
//
// The state machine inside each consumer:
//   • SNAPSHOT messages populate `seenIDs` and apply to the cache.
//   • The first non-SNAPSHOT message (ADDED/CHANGED/REMOVED) ends the
//     snapshot phase: any cache entries whose IDs were not delivered by
//     the snapshot get evicted (ghost-pruning on reconnect — the daemon
//     could have lost a session while we were disconnected).
//   • Subsequent deltas mutate the cache directly.
//   • On stream error or EOF, sleep with backoff and retry. The whole
//     state — including `seenIDs` — resets each iteration so the next
//     snapshot starts clean.

func runSessionStream<C: FleetFleet.ClientProtocol>(
    _ fleet: C,
    model: AppModel
) async throws {
    var attempt = 0
    while !Task.isCancelled {
        do {
            try await consumeSessionStream(fleet, model: model)
            // Stream ended cleanly (server closed). Reset attempt and
            // immediately try to reconnect; this is rare but happens during
            // daemon graceful shutdown.
            attempt = 0
        } catch is CancellationError {
            throw CancellationError()
        } catch {
            await model.set(connectionState: .reconnecting,
                            error: "session stream broken: \(error)")
        }
        try await sleepBackoff(attempt: attempt)
        attempt = min(attempt + 1, reconnectBackoffSchedule.count - 1)
    }
}

private func consumeSessionStream<C: FleetFleet.ClientProtocol>(
    _ fleet: C,
    model: AppModel
) async throws {
    let state = StreamState<String>()

    try await fleet.listSessions(FleetListSessionsRequest()) { resp in
        // First message implies the connection is live.
        await model.set(connectionState: .connected, error: nil)

        for try await update in resp.messages {
            switch update.kind {
            case .snapshot:
                state.seenIDs.insert(update.session.id)
                await model.applySession(Convert.toSession(update.session))
            case .added, .changed:
                if state.inSnapshot {
                    await model.finalizeSessionsSnapshot(seenIDs: state.seenIDs)
                    state.inSnapshot = false
                }
                await model.applySession(Convert.toSession(update.session))
            case .removed:
                if state.inSnapshot {
                    await model.finalizeSessionsSnapshot(seenIDs: state.seenIDs)
                    state.inSnapshot = false
                }
                await model.removeSession(id: update.removedID)
            case .unspecified, .UNRECOGNIZED:
                break
            }
        }
    }
}

func runRepoStream<C: FleetFleet.ClientProtocol>(
    _ fleet: C,
    model: AppModel
) async throws {
    var attempt = 0
    while !Task.isCancelled {
        do {
            try await consumeRepoStream(fleet, model: model)
            attempt = 0
        } catch is CancellationError {
            throw CancellationError()
        } catch {
            await model.set(connectionState: .reconnecting,
                            error: "repo stream broken: \(error)")
        }
        try await sleepBackoff(attempt: attempt)
        attempt = min(attempt + 1, reconnectBackoffSchedule.count - 1)
    }
}

private func consumeRepoStream<C: FleetFleet.ClientProtocol>(
    _ fleet: C,
    model: AppModel
) async throws {
    let state = StreamState<String>()

    try await fleet.listRepos(FleetListReposRequest()) { resp in
        for try await update in resp.messages {
            switch update.kind {
            case .snapshot:
                state.seenIDs.insert(update.repo.root)
                await model.applyRepo(Convert.toRepo(update.repo))
            case .added, .changed:
                if state.inSnapshot {
                    await model.finalizeReposSnapshot(seenRoots: state.seenIDs)
                    state.inSnapshot = false
                }
                await model.applyRepo(Convert.toRepo(update.repo))
            case .removed:
                if state.inSnapshot {
                    await model.finalizeReposSnapshot(seenRoots: state.seenIDs)
                    state.inSnapshot = false
                }
                await model.removeRepo(root: update.removedRoot)
            case .unspecified, .UNRECOGNIZED:
                break
            }
        }
    }
}

// Slot bindings change rarely (only on Alt+0..9 hotkey assignment) and the
// daemon doesn't expose a streaming RPC for them. V1 polls every 5s, which
// is fast enough that a binding made elsewhere is reflected before the user
// notices. If this proves visibly laggy we can spawn a streaming RPC later.
func runSlotBindingsRefresher<C: FleetFleet.ClientProtocol>(
    _ fleet: C,
    model: AppModel
) async throws {
    while !Task.isCancelled {
        do {
            let resp = try await fleet.listSlotBindings(Google_Protobuf_Empty())
            var bindings: [Int: String] = [:]
            for binding in resp.bindings {
                bindings[Int(binding.slot)] = binding.sessionID
            }
            await model.applySlotBindings(bindings)
        } catch is CancellationError {
            throw CancellationError()
        } catch {
            // Non-fatal. Keep the previous bindings and try again.
        }
        try await Task.sleep(for: .seconds(5))
    }
}
