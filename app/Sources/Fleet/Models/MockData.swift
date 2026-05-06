import Foundation

enum MockData {
    static let repos: [Repo] = [
        Repo(
            id: "brizzai/brizzai",
            name: "brizzai/brizzai",
            branch: "main",
            dirty: true,
            sessions: [
                Session(
                    id: "a1b2c3d4-1715000000",
                    title: "Refactor analytics ingest pipeline",
                    status: .running,
                    branch: "feature/analytics-ingest",
                    pr: PRInfo(number: 1234, state: .pending),
                    slot: 1
                ),
                Session(
                    id: "e5f6g7h8-1715000050",
                    title: "Fix tenant lookup race",
                    status: .waiting,
                    branch: "fix/tenant-race",
                    pr: PRInfo(number: 1240, state: .changesRequested),
                    slot: nil
                ),
                Session(
                    id: "i9j0k1l2-1715000100",
                    title: "Update onboarding docs",
                    status: .idle,
                    branch: "docs/onboarding",
                    pr: nil,
                    slot: nil
                ),
            ]
        ),
        Repo(
            id: "brizzai/fleet",
            name: "brizzai/fleet",
            branch: "fleet-ui",
            dirty: false,
            sessions: [
                Session(
                    id: "m3n4o5p6-1715000200",
                    title: "Desktop app prototype",
                    status: .running,
                    branch: "fleet-ui",
                    pr: PRInfo(number: 67, state: .approvedGreen),
                    slot: 2
                ),
                Session(
                    id: "q7r8s9t0-1715000300",
                    title: "Stage 0 daemon process",
                    status: .finished,
                    branch: "stage0/daemon",
                    pr: PRInfo(number: 68, state: .pending),
                    slot: nil
                ),
            ]
        ),
        Repo(
            id: "brizzai/morebrizzai",
            name: "brizzai/morebrizzai",
            branch: "main",
            dirty: false,
            sessions: [
                Session(
                    id: "u1v2w3x4-1715000400",
                    title: "Investigate crash on startup",
                    status: .error,
                    branch: "bug/crash-startup",
                    pr: nil,
                    slot: nil
                ),
            ]
        ),
    ]
}
