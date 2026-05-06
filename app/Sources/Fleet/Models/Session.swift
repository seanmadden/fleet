import Foundation

struct Session: Identifiable, Hashable {
    let id: String
    var title: String
    var status: SessionStatus
    var branch: String
    var pr: PRInfo?
    var slot: Int?
    var tmuxName: String?  // when set, the terminal pane can attach to a real tmux session
}
