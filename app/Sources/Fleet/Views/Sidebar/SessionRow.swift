import SwiftUI

struct SessionRow: View {
    let session: Session
    let model: AppModel

    @State private var draftTitle: String = ""
    @FocusState private var renameFocused: Bool

    private var isRenaming: Bool {
        model.renamingSessionID == session.id
    }

    var body: some View {
        HStack(spacing: 8) {
            Text(session.status.icon)
                .foregroundStyle(session.status.tint)
                .font(.system(.body, design: .monospaced))
                .frame(width: 12, alignment: .center)

            if isRenaming {
                TextField("", text: $draftTitle)
                    .textFieldStyle(.roundedBorder)
                    .focused($renameFocused)
                    .onSubmit { commitRename() }
                    .onExitCommand { cancelRename() }
                    .onAppear {
                        draftTitle = session.title
                        renameFocused = true
                    }
            } else {
                Text(session.title)
                    .lineLimit(1)
                    .truncationMode(.tail)
            }

            if let slot = session.slot {
                Text("[\(slot)]")
                    .font(.caption2.monospaced())
                    .foregroundStyle(.secondary)
            }

            Spacer(minLength: 0)

            if !session.isAlive {
                Text("✕")
                    .font(.caption2)
                    .foregroundStyle(.red.opacity(0.8))
            }
        }
        .contentShape(Rectangle())
        .contextMenu { contextMenuItems }
    }

    @ViewBuilder
    private var contextMenuItems: some View {
        if session.status == .waiting {
            Button("Approve (y + Enter)") {
                Task { await sendQuickApprove() }
            }
        }
        Button("Rename…") { startRename() }
        Button("Restart") {
            Task { await model.dispatchRestart(sessionID: session.id) }
        }
        Divider()
        Button("Delete…", role: .destructive) {
            model.pendingDeletion = session
        }
    }

    private func startRename() {
        model.selectedSessionID = session.id
        model.renamingSessionID = session.id
    }

    private func commitRename() {
        let title = draftTitle
        let id = session.id
        model.renamingSessionID = nil
        Task { await model.dispatchRename(sessionID: id, title: title) }
    }

    private func cancelRename() {
        model.renamingSessionID = nil
    }

    private func sendQuickApprove() async {
        // Match selection so dispatchQuickApprove targets this row.
        model.selectedSessionID = session.id
        await model.dispatchQuickApprove()
    }
}
