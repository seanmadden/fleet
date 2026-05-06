import Foundation

// EnsureRunning probes the daemon's Unix socket and, if absent, spawns
// `fleet daemon --detach` from $PATH. Mirrors `internal/daemonclient/autospawn.go`.
//
// The Go daemon re-execs itself with Setsid on `--detach`, so we don't need
// to do any of that ourselves — `Process.run()` then drop the handle.
enum EnsureRunning {
    enum Error: Swift.Error, CustomStringConvertible {
        case fleetBinaryNotFound
        case spawnFailed(String)
        case socketTimeout

        var description: String {
            switch self {
            case .fleetBinaryNotFound:
                return "Could not locate `fleet` on $PATH. Install it (e.g. `brew install brizzai/tap/fleet`)."
            case .spawnFailed(let msg):
                return "Failed to spawn `fleet daemon --detach`: \(msg)"
            case .socketTimeout:
                return "fleet daemon did not begin listening within 3s."
            }
        }
    }

    /// Returns the daemon socket path. Spawns the daemon if no live socket is found.
    static func ensure() async throws -> String {
        let path = defaultSocketPath()
        if isAlive(path) { return path }
        guard let exe = locateFleetBinary() else { throw Error.fleetBinaryNotFound }
        try spawnDetached(exe: exe)
        try await waitForSocket(path, timeout: .seconds(3))
        return path
    }

    static func defaultSocketPath() -> String {
        let home = NSHomeDirectory()
        return "\(home)/.config/fleet/daemon.sock"
    }

    private static func isAlive(_ path: String) -> Bool {
        // A 200ms connect-and-close is the cheapest liveness probe that
        // distinguishes a stale socket file from an actively-listening one.
        let fd = socket(AF_UNIX, SOCK_STREAM, 0)
        if fd < 0 { return false }
        defer { close(fd) }

        var addr = sockaddr_un()
        addr.sun_family = sa_family_t(AF_UNIX)
        let pathBytes = path.utf8CString
        guard pathBytes.count <= MemoryLayout.size(ofValue: addr.sun_path) else { return false }
        withUnsafeMutablePointer(to: &addr.sun_path) { dest in
            pathBytes.withUnsafeBufferPointer { src in
                _ = memcpy(dest, src.baseAddress, src.count)
            }
        }

        let socklen = socklen_t(MemoryLayout<sockaddr_un>.size)
        let rc = withUnsafePointer(to: &addr) { ptr -> Int32 in
            ptr.withMemoryRebound(to: sockaddr.self, capacity: 1) { sa in
                connect(fd, sa, socklen)
            }
        }
        return rc == 0
    }

    private static func locateFleetBinary() -> String? {
        // `which` is the simplest, most predictable lookup. We rely on the
        // user's login PATH having Homebrew (/opt/homebrew/bin or /usr/local/bin).
        let proc = Process()
        proc.executableURL = URL(fileURLWithPath: "/usr/bin/env")
        proc.arguments = ["which", "fleet"]
        let pipe = Pipe()
        proc.standardOutput = pipe
        proc.standardError = Pipe()
        do { try proc.run() } catch { return nil }
        proc.waitUntilExit()
        guard proc.terminationStatus == 0 else { return nil }
        let data = pipe.fileHandleForReading.readDataToEndOfFile()
        let path = String(data: data, encoding: .utf8)?.trimmingCharacters(in: .whitespacesAndNewlines)
        return (path?.isEmpty == false) ? path : nil
    }

    private static func spawnDetached(exe: String) throws {
        let proc = Process()
        proc.executableURL = URL(fileURLWithPath: exe)
        proc.arguments = ["daemon", "--detach"]
        // The Go daemon redirects its own stdout/stderr to ~/.config/fleet/daemon.log
        // after re-exec, so we don't need to wire them here. Pipes still need to
        // be closed on the parent side or `Process` keeps the fd alive.
        proc.standardInput = Pipe()
        proc.standardOutput = Pipe()
        proc.standardError = Pipe()
        do {
            try proc.run()
        } catch {
            throw Error.spawnFailed(String(describing: error))
        }
        // Don't wait — the Go daemon backgrounds itself via Setsid re-exec
        // and the original child exits immediately, leaving the grandchild
        // owning the socket.
    }

    private static func waitForSocket(_ path: String, timeout: Duration) async throws {
        let deadline = ContinuousClock.now.advanced(by: timeout)
        while ContinuousClock.now < deadline {
            if isAlive(path) { return }
            try await Task.sleep(for: .milliseconds(100))
        }
        throw Error.socketTimeout
    }
}
