import SwiftUI

// FleetMark renders the canonical brand mark on a 64×64 design grid.
// Geometry is the T0 baseline locked on 2026-05-07 in
// docs/desktop/design-system.md (and bundled at Resources/Brand/fleet-mark.svg):
//
//   Row 1  — pink #F472B6        · 32w × 6h · rx 2 · @ 14,14
//   Row 1.5 — pink @ 40%          · 24w × 2h · rx 1 · @ 18,22
//   Row 2  — body #F4F4F5        · 22w × 6h · rx 2 · @ 14,28
//   Row 2.5 — body @ 30%          · 14w × 2h · rx 1 · @ 18,36
//   Row 3  — body @ 55%          · 14w × 6h · rx 2 · @ 14,42
//
// On a light surface, the body color flips to #0A0A0B; the pink stays.
struct FleetMark: View {
    enum Surface {
        case dark, light
    }

    var size: CGFloat = 64
    var surface: Surface = .dark

    static let pink = Color(red: 0xF4 / 255, green: 0x72 / 255, blue: 0xB6 / 255)
    static let darkBody = Color(red: 0xF4 / 255, green: 0xF4 / 255, blue: 0xF5 / 255)
    static let lightBody = Color(red: 0x0A / 255, green: 0x0A / 255, blue: 0x0B / 255)

    var body: some View {
        let s = size / 64
        let body: Color = surface == .dark ? Self.darkBody : Self.lightBody

        ZStack(alignment: .topLeading) {
            // Reserve the full grid so the mark has predictable bounds even
            // though every stripe is positioned absolutely.
            Color.clear.frame(width: size, height: size)

            stripe(x: 14, y: 14, w: 32, h: 6, r: 2, color: Self.pink, scale: s)
            stripe(x: 18, y: 22, w: 24, h: 2, r: 1, color: Self.pink.opacity(0.4), scale: s)
            stripe(x: 14, y: 28, w: 22, h: 6, r: 2, color: body, scale: s)
            stripe(x: 18, y: 36, w: 14, h: 2, r: 1, color: body.opacity(0.3), scale: s)
            stripe(x: 14, y: 42, w: 14, h: 6, r: 2, color: body.opacity(0.55), scale: s)
        }
        .accessibilityElement()
        .accessibilityLabel("fleet")
    }

    private func stripe(
        x: CGFloat, y: CGFloat, w: CGFloat, h: CGFloat, r: CGFloat,
        color: Color, scale: CGFloat
    ) -> some View {
        RoundedRectangle(cornerRadius: r * scale, style: .circular)
            .fill(color)
            .frame(width: w * scale, height: h * scale)
            .offset(x: x * scale, y: y * scale)
    }
}

#Preview("Dark") {
    FleetMark(size: 160)
        .padding(40)
        .background(Color(red: 0x0A / 255, green: 0x0A / 255, blue: 0x0B / 255))
}

#Preview("Light") {
    FleetMark(size: 160, surface: .light)
        .padding(40)
        .background(Color(red: 0xF4 / 255, green: 0xF4 / 255, blue: 0xF5 / 255))
}
