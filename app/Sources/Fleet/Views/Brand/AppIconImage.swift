import SwiftUI

// AppIconImage composes the FleetMark on a dark squircle background sized
// for a 1024×1024 macOS app-icon source. We render this view into an
// NSImage at launch and assign it to NSApp.applicationIconImage so the
// Dock shows the brand mark while the app is running. (Once we ship a
// real .app bundle with an Assets.xcassets AppIcon, the OS will use that
// instead and this runtime path becomes redundant.)
struct AppIconImage: View {
    var size: CGFloat = 1024

    var body: some View {
        // Apple's macOS app-icon grid sits the squircle inside ~824/1024
        // of the canvas — the rest is transparent bleed that gives every
        // app the same visual size in the Dock and Cmd-Tab. Filling the
        // full canvas makes our icon look outsized next to neighbours.
        let squircleSize = size * 0.8047
        let cornerRadius = squircleSize * 0.2237

        // The mark's ink lives in 32×34 of its 64×64 grid with asymmetric
        // padding (14/14 on top-left, 18/16 on right-bottom). Scaling the
        // grid to 1.1× the squircle puts the visible ink at ~55% of the
        // squircle. The grid's transparent bleed extends past the squircle,
        // but the stripes themselves are absolutely positioned at x:14–46,
        // y:14–48 of the grid — so the visible ink stays inside.
        let markSize = squircleSize * 1.10
        let unit = markSize / 64

        ZStack {
            RoundedRectangle(cornerRadius: cornerRadius, style: .continuous)
                .fill(Color(red: 0x17 / 255, green: 0x17 / 255, blue: 0x1A / 255))
                .frame(width: squircleSize, height: squircleSize)
            FleetMark(size: markSize)
                .offset(x: 2 * unit, y: 1 * unit)
        }
        .frame(width: size, height: size)
    }
}

enum AppIconRenderer {
    /// Renders AppIconImage to an NSImage suitable for
    /// NSApp.applicationIconImage. Returns nil only if SwiftUI's
    /// ImageRenderer fails to produce a backing image.
    @MainActor
    static func makeNSImage(size: CGFloat = 1024) -> NSImage? {
        let renderer = ImageRenderer(content: AppIconImage(size: size))
        renderer.scale = 1
        return renderer.nsImage
    }
}

#Preview {
    AppIconImage(size: 256)
        .padding(40)
        .background(Color.gray.opacity(0.2))
}
