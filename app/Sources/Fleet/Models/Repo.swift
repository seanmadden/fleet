import Foundation

struct Repo: Identifiable, Hashable {
    let id: String           // e.g. "brizzai/brizzai"
    var name: String         // display label
    var branch: String
    var dirty: Bool
    var sessions: [Session]
    var collapsed: Bool = false
}
