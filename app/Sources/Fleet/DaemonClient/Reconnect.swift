import Foundation

// Mirrors `internal/daemonclient/stream.go:17-25`. Caps at 5s so a daemon
// respawning through `fleet daemon --detach` (~200ms cold start) does not
// hammer the socket, but we still notice quickly when it comes back.
let reconnectBackoffSchedule: [Duration] = [
    .milliseconds(250),
    .seconds(1),
    .seconds(4),
    .seconds(5),
]

/// Sleeps for the backoff matching `attempt` (0-indexed), capped at the
/// last entry of the schedule. Cancellable via Task cancellation.
func sleepBackoff(attempt: Int) async throws {
    let idx = min(attempt, reconnectBackoffSchedule.count - 1)
    try await Task.sleep(for: reconnectBackoffSchedule[idx])
}
