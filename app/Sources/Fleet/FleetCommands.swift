import SwiftUI

// FleetCommands wires the macOS menu bar's "Session" menu and exposes the
// V1 mutation shortcuts: ⌘Y approve, ⌘R restart, ⌘⇧R rename, ⌘⌫ delete. Each
// item is enabled only when a session is selected, and Approve additionally
// requires the session to be in the Waiting state — matching the TUI's
// gating so users can't fire `y\r` at a Running pane and pollute it.
struct FleetCommands: Commands {
    @Bindable var model: AppModel

    var body: some Commands {
        // Empty File menu (drop the default New / Open / Save chrome — the
        // app has no documents, only sessions, and "New Session" lives in
        // the Session menu).
        CommandGroup(replacing: .newItem) {}

        CommandMenu("Session") {
            Button("Approve") {
                Task { await model.dispatchQuickApprove() }
            }
            .keyboardShortcut("y", modifiers: .command)
            .disabled(!canApprove)

            Divider()

            Button("Rename…") { startRename() }
                .keyboardShortcut("r", modifiers: [.command, .shift])
                .disabled(!hasSelection)

            Button("Restart") { restart() }
                .keyboardShortcut("r", modifiers: .command)
                .disabled(!hasSelection)

            Divider()

            Button("Delete…") { promptDelete() }
                .keyboardShortcut(.delete, modifiers: .command)
                .disabled(!hasSelection)

            Divider()

            Button("Save Diagnostics Snapshot") {
                Task { await model.dispatchSnapshot() }
            }
            .keyboardShortcut("d", modifiers: [.command, .shift])
        }
    }

    private var hasSelection: Bool {
        model.selectedSessionID != nil
    }

    private var canApprove: Bool {
        guard let id = model.selectedSessionID,
              let session = model.sessionsByID[id]
        else { return false }
        return session.status == .waiting
    }

    private func startRename() {
        guard let id = model.selectedSessionID else { return }
        model.renamingSessionID = id
    }

    private func restart() {
        guard let id = model.selectedSessionID else { return }
        Task { await model.dispatchRestart(sessionID: id) }
    }

    private func promptDelete() {
        guard let id = model.selectedSessionID,
              let session = model.sessionsByID[id]
        else { return }
        model.pendingDeletion = session
    }
}
