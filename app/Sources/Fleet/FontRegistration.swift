import AppKit
import CoreText
import Foundation

// Registers the bundled font files (Inter + JetBrains Mono) with the
// process so SwiftUI / NSFont can find them by family name. SwiftPM
// `.process("Resources")` copies the .ttf files into the app bundle, but
// macOS doesn't auto-register fonts inside an executable's resources
// directory — we have to call `CTFontManagerRegisterFontsForURLs` once
// at launch.
enum FontRegistration {
    /// Family names that callers should use after `register()`.
    static let monospaceFamily = "JetBrains Mono"
    static let sansFamily = "Inter"

    private static let registered: Bool = {
        registerFonts()
        return true
    }()

    static func register() {
        _ = registered
    }

    private static func registerFonts() {
        guard let resourceURL = Bundle.module.resourceURL else { return }
        let fontsDir = resourceURL.appendingPathComponent("Fonts", isDirectory: true)
        let urls = (try? FileManager.default.contentsOfDirectory(
            at: fontsDir, includingPropertiesForKeys: nil, options: []
        )) ?? []
        let ttfURLs = urls.filter { $0.pathExtension.lowercased() == "ttf" }
        guard !ttfURLs.isEmpty else { return }

        CTFontManagerRegisterFontURLs(
            ttfURLs as CFArray,
            .process,
            true,
            nil
        )
        // Errors here are usually "already registered" if the app got
        // re-launched in the same process group during dev — non-fatal.
    }
}
