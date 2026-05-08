import Foundation

// SnapshotWriter persists the daemon's diagnostics blob to a fixed location
// on disk — `~/.config/fleet/snapshots/snapshot-YYYYMMDD-HHMMSS.md`.
// Cmd-Shift-D in the Mac app calls into here; the user can then ask "look
// at the last snapshot" and a tool can read the newest file in that dir.
//
// Intentionally does not open Finder or copy to clipboard — the path is
// stable and predictable, so workflows that want the file can just read
// it directly.
enum SnapshotWriter {
    static let directory: URL = {
        FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent(".config/fleet/snapshots", isDirectory: true)
    }()

    static func write(markdown: String) throws -> URL {
        try FileManager.default.createDirectory(at: directory,
                                                withIntermediateDirectories: true)
        let formatter = DateFormatter()
        formatter.dateFormat = "yyyyMMdd-HHmmss"
        formatter.timeZone = TimeZone.current
        let stamp = formatter.string(from: Date())
        let url = directory.appendingPathComponent("snapshot-\(stamp).md")
        try markdown.write(to: url, atomically: true, encoding: .utf8)
        return url
    }
}
