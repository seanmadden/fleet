import Foundation

struct Repo: Identifiable, Hashable {
    let id: String           // absolute path on disk; same as Session.repoRoot
    var displayName: String  // "brizzai/brizzai" if a remote was parsed, else basename
    var branch: String
    var dirty: Bool
    var pinned: Bool
    var isWorktreeRepo: Bool
    var prInfo: PRInfo?
    var sessions: [Session]   // populated by AppModel after grouping; not from the proto
    var collapsed: Bool = false
}
