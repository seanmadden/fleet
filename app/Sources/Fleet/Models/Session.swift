import Foundation

struct Session: Identifiable, Hashable {
    let id: String
    var title: String
    var status: SessionStatus
    var branch: String
    var pr: PRInfo?
    var slot: Int?

    var repoRoot: String        // absolute project path; matches Repo.id
    var projectPath: String     // working directory (often == repoRoot for non-worktree)
    var tmuxName: String?       // nil when the session has no live tmux pane
    var isAlive: Bool
    var acknowledged: Bool
    var workspaceName: String   // worktree name; empty for non-worktree sessions
}
