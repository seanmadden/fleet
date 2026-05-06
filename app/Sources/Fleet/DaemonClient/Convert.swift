import Foundation

// Proto → domain transforms. Keeps the gRPC-generated `Fleet*` types pinned
// inside `DaemonClient/`; the rest of the app sees the SwiftUI-friendly
// structs declared in `Models/`. Mirrors `internal/daemonclient/convert.go`.

enum Convert {
    static func toSession(_ p: FleetSession) -> Session {
        Session(
            id: p.id,
            title: p.title,
            status: SessionStatus(proto: p.status),
            branch: p.workspaceName,        // best-effort; the proto carries no branch field on Session
            pr: nil,                        // PR info is on the Repo, not the session
            slot: nil,                      // populated separately from ListSlotBindings
            repoRoot: p.repoRoot,
            projectPath: p.projectPath,
            tmuxName: p.tmuxSessionName.isEmpty ? nil : p.tmuxSessionName,
            isAlive: p.isAlive,
            acknowledged: p.acknowledged,
            workspaceName: p.workspaceName
        )
    }

    static func toRepo(_ p: FleetRepo) -> Repo {
        Repo(
            id: p.root,
            displayName: p.displayName,
            branch: p.branch,
            dirty: p.isDirty,
            pinned: p.pinned,
            isWorktreeRepo: p.isWorktreeRepo,
            prInfo: p.hasPr ? toPR(p.pr) : nil,
            sessions: []                    // grouped in AppModel from the live session cache
        )
    }

    static func toPR(_ p: FleetPR) -> PRInfo {
        PRInfo(number: Int(p.number), state: derivePRState(p))
    }

    // Mirrors the icon/style derivation in internal/ui/sidebar.go:366-392.
    // `pending` is the safe default — yellow until we have evidence of a more
    // specific state.
    private static func derivePRState(_ p: FleetPR) -> PRState {
        if p.state == "MERGED" { return .merged }

        let ciFail = p.ciStatus == "FAILURE"
        let changesReq = p.reviewDecision == "CHANGES_REQUESTED"
        let hasThreads = p.unresolvedThreads > 0
        let hasConflicts = p.hasConflicts_p   // SwiftProtobuf appends `_p` to disambiguate from the `has*` accessor convention.

        if ciFail || changesReq || hasThreads || hasConflicts {
            return ciFail ? .ciFailed : .changesRequested
        }

        let approved = p.reviewDecision == "APPROVED"
        let ciPass = p.ciStatus == "SUCCESS"
        if approved && ciPass { return .approvedGreen }

        return .pending
    }
}
