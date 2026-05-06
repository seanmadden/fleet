import SwiftUI

enum SessionStatus: String, CaseIterable {
    case starting
    case idle
    case running
    case waiting
    case finished
    case error

    var icon: String {
        switch self {
        case .running, .finished: "●"
        case .waiting: "◐"
        case .idle, .starting: "○"
        case .error: "✕"
        }
    }

    var tint: Color {
        switch self {
        case .running: .green
        case .waiting: .yellow
        case .finished: .blue
        case .idle: .secondary
        case .starting: .secondary
        case .error: .red
        }
    }

    var label: String {
        switch self {
        case .starting: "Starting"
        case .idle: "Idle"
        case .running: "Running"
        case .waiting: "Waiting"
        case .finished: "Finished"
        case .error: "Error"
        }
    }
}
