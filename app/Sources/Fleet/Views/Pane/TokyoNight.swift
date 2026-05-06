import AppKit
import SwiftTerm

// Tokyo Night palette — used as a placeholder until the UX designer ships
// a real visual system. Picked because it's the TUI's default and what the
// user already chose at planning time.
//
// Hex values from the canonical "Tokyo Night" theme by enkia:
//   https://github.com/enkia/tokyo-night-vscode-theme
enum TokyoNight {
    static let background = NSColor(srgbRed: 0x1a / 255, green: 0x1b / 255, blue: 0x26 / 255, alpha: 1)
    static let foreground = NSColor(srgbRed: 0xc0 / 255, green: 0xca / 255, blue: 0xf5 / 255, alpha: 1)
    static let cursor = NSColor(srgbRed: 0x7a / 255, green: 0xa2 / 255, blue: 0xf7 / 255, alpha: 1)
    static let selectionBackground = NSColor(srgbRed: 0x33 / 255, green: 0x46 / 255, blue: 0x7c / 255, alpha: 1)

    /// 16 ANSI colors: indices 0..7 dark, 8..15 bright. SwiftTerm.Color is
    /// a class (non-Sendable), so this can't be a `static let` — we mint a
    /// fresh array each call. 16 tiny allocations on view creation; cheap.
    static func ansi() -> [Color] {
        [
            // dark
            rgb(0x15, 0x16, 0x1e),  // black
            rgb(0xf7, 0x76, 0x8e),  // red
            rgb(0x9e, 0xce, 0x6a),  // green
            rgb(0xe0, 0xaf, 0x68),  // yellow
            rgb(0x7a, 0xa2, 0xf7),  // blue
            rgb(0xbb, 0x9a, 0xf7),  // magenta
            rgb(0x7d, 0xcf, 0xff),  // cyan
            rgb(0xa9, 0xb1, 0xd6),  // white
            // bright
            rgb(0x41, 0x48, 0x68),
            rgb(0xf7, 0x76, 0x8e),
            rgb(0x9e, 0xce, 0x6a),
            rgb(0xe0, 0xaf, 0x68),
            rgb(0x7a, 0xa2, 0xf7),
            rgb(0xbb, 0x9a, 0xf7),
            rgb(0x7d, 0xcf, 0xff),
            rgb(0xc0, 0xca, 0xf5),
        ]
    }

    // SwiftTerm.Color's only public init takes 0..65535 components. Mirror
    // the byte values into both halves (`value * 0x101`) so an 8-bit input
    // maps to the right 16-bit value.
    private static func rgb(_ r: UInt8, _ g: UInt8, _ b: UInt8) -> Color {
        Color(
            red: UInt16(r) * 0x101,
            green: UInt16(g) * 0x101,
            blue: UInt16(b) * 0x101
        )
    }
}
