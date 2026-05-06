import SwiftUI

// FleetLockup pairs the FleetMark with the wordmark "fleet" set in
// Inter Semibold, lowercase, letter-spacing -0.02em. The reference
// proportion (logo-final.html) is mark 56pt + wordmark 40pt with a 16pt
// gap; this view keeps that ratio for any wordmark size you pass in.
struct FleetLockup: View {
    var wordmarkSize: CGFloat = 40
    var surface: FleetMark.Surface = .dark

    var body: some View {
        let markSize = wordmarkSize * 1.4   // 56 / 40 = 1.4
        let gap = wordmarkSize * 0.4        // 16 / 40 = 0.4
        let textColor: Color = surface == .dark ? FleetMark.darkBody : FleetMark.lightBody

        HStack(spacing: gap) {
            FleetMark(size: markSize, surface: surface)
            Text("fleet")
                .font(.custom(FontRegistration.sansFamily, size: wordmarkSize).weight(.semibold))
                .tracking(-wordmarkSize * 0.02)
                .foregroundStyle(textColor)
        }
        .accessibilityElement()
        .accessibilityLabel("fleet")
    }
}

#Preview("Dark") {
    FleetLockup()
        .padding(40)
        .background(Color(red: 0x0A / 255, green: 0x0A / 255, blue: 0x0B / 255))
}

#Preview("Small") {
    FleetLockup(wordmarkSize: 16)
        .padding(20)
        .background(Color(red: 0x11 / 255, green: 0x11 / 255, blue: 0x13 / 255))
}
