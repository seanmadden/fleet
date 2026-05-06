import Foundation
import Observation

enum ConnectionState: Equatable, Sendable {
    case connecting    // first dial, no socket yet
    case connected     // streams active
    case reconnecting  // stream broken; backoff loop running
    case disconnected  // gave up (daemon binary missing, etc.)
}

// AppModel is the SwiftUI source of truth and the sole sink for daemon
// stream updates. SwiftUI observes it from the main thread; daemon
// consumers run on background tasks and call into the @MainActor methods
// to mutate state. This keeps writes serialised on the main actor without
// our daemon code having to reach for `MainActor.run` each call.
@Observable
@MainActor
final class AppModel {
    // ─── Stream-driven state ─────────────────────────────────────────
    private(set) var sessionsByID: [String: Session] = [:]
    private(set) var reposByRoot: [String: Repo] = [:]
    private(set) var slotBindings: [Int: String] = [:]

    // ─── UI state ────────────────────────────────────────────────────
    var selectedSessionID: String?
    var filterText: String = ""

    // ─── Connection state ────────────────────────────────────────────
    private(set) var connectionState: ConnectionState = .connecting
    private(set) var lastError: String?

    // ─── Lifecycle ───────────────────────────────────────────────────
    private var runnerTask: Task<Void, Never>?

    func start() {
        guard runnerTask == nil else { return }
        runnerTask = Task { [self] in
            await DaemonClientRunner.run(model: self)
        }
    }

    func stop() {
        runnerTask?.cancel()
        runnerTask = nil
    }

    // ─── Derived views (consumed by the sidebar) ─────────────────────

    /// Sessions sorted in a stable display order. The TUI groups by repo
    /// then sorts by created-time inside each group; we mirror that in
    /// `sessionsForRepo(root:)` rather than sorting the global list.
    var allSessions: [Session] {
        Array(sessionsByID.values)
    }

    /// Repos in the same order the TUI sidebar uses: pinned and/or with
    /// sessions first, alphabetised by display name. Filtering by
    /// `filterText` is applied in the view layer, not here.
    var displayedRepos: [Repo] {
        var repos = Array(reposByRoot.values)
        repos.sort { lhs, rhs in
            if lhs.pinned != rhs.pinned { return lhs.pinned && !rhs.pinned }
            return lhs.displayName.localizedCompare(rhs.displayName) == .orderedAscending
        }
        // Hydrate `sessions` so views can consume `repo.sessions` directly.
        return repos.map { repo in
            var copy = repo
            copy.sessions = sessionsForRepo(root: repo.id)
            return copy
        }
    }

    func sessionsForRepo(root: String) -> [Session] {
        sessionsByID.values
            .filter { $0.repoRoot == root }
            .sorted { lhs, rhs in
                lhs.title.localizedCompare(rhs.title) == .orderedAscending
            }
            .map { sess in
                var copy = sess
                copy.slot = slotForSession(id: sess.id)
                return copy
            }
    }

    private func slotForSession(id: String) -> Int? {
        for (slot, sid) in slotBindings where sid == id { return slot }
        return nil
    }

    // ─── Daemon-driven mutators (called from streams) ────────────────

    func set(connectionState state: ConnectionState, error: String?) {
        self.connectionState = state
        if let error { self.lastError = error } // keep last meaningful error
        else if state == .connected { self.lastError = nil }
    }

    func applySession(_ session: Session) {
        sessionsByID[session.id] = session
    }

    func removeSession(id: String) {
        sessionsByID.removeValue(forKey: id)
        if selectedSessionID == id { selectedSessionID = nil }
    }

    /// Drops any cached sessions whose IDs were not delivered by the
    /// snapshot we just finished consuming.
    func finalizeSessionsSnapshot(seenIDs: Set<String>) {
        for id in sessionsByID.keys where !seenIDs.contains(id) {
            sessionsByID.removeValue(forKey: id)
        }
        if let sel = selectedSessionID, !seenIDs.contains(sel) {
            selectedSessionID = nil
        }
    }

    func applyRepo(_ repo: Repo) {
        reposByRoot[repo.id] = repo
    }

    func removeRepo(root: String) {
        reposByRoot.removeValue(forKey: root)
    }

    func finalizeReposSnapshot(seenRoots: Set<String>) {
        for root in reposByRoot.keys where !seenRoots.contains(root) {
            reposByRoot.removeValue(forKey: root)
        }
    }

    func applySlotBindings(_ bindings: [Int: String]) {
        self.slotBindings = bindings
    }
}
