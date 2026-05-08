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
    var selectedSessionID: String? {
        didSet { onSelectionChange(from: oldValue) }
    }
    var filterText: String = ""

    // Pending rename session id; SidebarView swaps the row for a TextField
    // when this matches a row's id. nil means no row is in edit mode.
    var renamingSessionID: String?

    // Pending delete confirmation. SwiftUI binds to this for the .alert.
    var pendingDeletion: Session?

    // ─── Connection state ────────────────────────────────────────────
    private(set) var connectionState: ConnectionState = .connecting
    private(set) var lastError: String?

    // Transient red banner for failed mutations; cleared by `errorToastClearer`.
    private(set) var errorToast: String?
    private var errorToastClearer: Task<Void, Never>?

    // ─── Lifecycle ───────────────────────────────────────────────────
    private var runnerTask: Task<Void, Never>?
    private(set) var mutator: Mutator?

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

    // ─── Mutator wiring (set by DaemonClientRunner) ──────────────────

    func attach(mutator: Mutator?) {
        self.mutator = mutator
    }

    // ─── Selection side-effect: ack on focus ─────────────────────────

    private func onSelectionChange(from old: String?) {
        guard let id = selectedSessionID, id != old,
              let session = sessionsByID[id],
              session.status == .finished, !session.acknowledged
        else { return }
        Task { await dispatchAcknowledge(sessionID: id) }
    }

    // ─── Mutation dispatch (called from views) ───────────────────────

    func dispatchQuickApprove() async {
        guard let id = selectedSessionID,
              let session = sessionsByID[id],
              session.status == .waiting,
              let m = mutator
        else { return }
        // Optimistic flip: drop the row out of `waiting` immediately so the
        // user gets feedback that their Y landed. The next SessionUpdate
        // from the daemon (sub-second once we wired TriggerRefresh into the
        // hook watcher) reconciles to the real status — running while the
        // turn continues, finished when Stop fires.
        var optimistic = session
        optimistic.status = .running
        sessionsByID[id] = optimistic
        await run(label: "send keys") {
            try await m.sendKeys(sessionID: id, keys: ["y"], submit: true)
        }
    }

    func dispatchDelete(sessionID: String) async {
        guard let m = mutator else { return }
        await run(label: "delete") {
            try await m.delete(sessionID: sessionID)
        }
    }

    func dispatchRestart(sessionID: String) async {
        guard let m = mutator else { return }
        await run(label: "restart") {
            try await m.restart(sessionID: sessionID)
        }
    }

    func dispatchRename(sessionID: String, title: String) async {
        let trimmed = title.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty, let m = mutator else { return }
        await run(label: "rename") {
            try await m.rename(sessionID: sessionID, title: trimmed)
        }
    }

    func dispatchAcknowledge(sessionID: String) async {
        guard let m = mutator else { return }
        // Acknowledge is best-effort; failures aren't worth a toast.
        try? await m.acknowledge(sessionID: sessionID)
    }

    func dispatchPinRepo(root: String, pinned: Bool) async {
        guard let m = mutator else { return }
        await run(label: pinned ? "unpin" : "pin") {
            if pinned {
                try await m.unpinRepo(root: root)
            } else {
                try await m.pinRepo(root: root)
            }
        }
    }

    // Cmd-Shift-D: capture the daemon's status-detection snapshot and dump
    // it to ~/.config/fleet/snapshots/. Surfaces a toast with the saved
    // path on success, or the error string on failure.
    func dispatchSnapshot() async {
        guard let m = mutator else {
            showErrorToast("snapshot: daemon not connected")
            return
        }
        do {
            let markdown = try await m.diagnostics()
            let url = try SnapshotWriter.write(markdown: markdown)
            self.errorToast = "Snapshot saved: \(url.path)"
            errorToastClearer?.cancel()
            errorToastClearer = Task { [weak self] in
                try? await Task.sleep(for: .seconds(8))
                guard !Task.isCancelled else { return }
                self?.clearErrorToast()
            }
        } catch {
            showErrorToast("snapshot: \(error)")
        }
    }

    // ─── Toast helpers ───────────────────────────────────────────────

    private func run(label: String, op: () async throws -> Void) async {
        do {
            try await op()
        } catch {
            showErrorToast("\(label): \(error)")
        }
    }

    func showErrorToast(_ message: String) {
        self.errorToast = message
        errorToastClearer?.cancel()
        errorToastClearer = Task { [weak self] in
            try? await Task.sleep(for: .seconds(5))
            guard !Task.isCancelled else { return }
            self?.clearErrorToast()
        }
    }

    private func clearErrorToast() {
        self.errorToast = nil
        self.errorToastClearer = nil
    }
}
