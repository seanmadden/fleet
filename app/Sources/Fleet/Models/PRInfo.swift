import SwiftUI

struct PRInfo: Hashable {
    let number: Int
    let state: PRState
}

enum PRState {
    case approvedGreen     // approved + CI passed
    case pending           // CI pending
    case ciFailed          // CI failed
    case changesRequested  // changes requested or unresolved threads
    case merged

    var glyph: String {
        switch self {
        case .approvedGreen: "✓"
        case .pending: "•"
        case .ciFailed: "✕"
        case .changesRequested: "↩"
        case .merged: "⇡"
        }
    }

    var tint: Color {
        switch self {
        case .approvedGreen: .green
        case .pending: .yellow
        case .ciFailed: .red
        case .changesRequested: .red
        case .merged: .purple
        }
    }
}
