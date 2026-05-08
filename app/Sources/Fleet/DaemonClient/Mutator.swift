import Foundation
import GRPCCore
import SwiftProtobuf

// Mutator is the read-write counterpart to the streaming consumers in
// `StreamConsumers.swift`: it wraps a live `FleetFleet.ClientProtocol`
// reference and exposes the mutation RPCs the V1 UI needs as plain async
// throws methods. AppModel holds a `Mutator?` once the gRPC client is open,
// and clears it when the daemon link drops.
//
// Errors are surfaced to the UI by the caller (see AppModel.dispatch*) — we
// don't translate them here so the toast can show the raw daemon message,
// which is the same shape the TUI's error history captures.
final class Mutator: Sendable {
    private let client: any FleetFleet.ClientProtocol

    init(client: any FleetFleet.ClientProtocol) {
        self.client = client
    }

    func sendKeys(sessionID: String, keys: [String], submit: Bool) async throws {
        var req = FleetSendKeysRequest()
        req.sessionID = sessionID
        req.keys = keys
        req.submit = submit
        _ = try await client.sendKeys(req)
    }

    func delete(sessionID: String,
                option: FleetDeleteOption = .sessionOnly,
                deferTmuxKill: Bool = false) async throws {
        var req = FleetDeleteSessionRequest()
        req.id = sessionID
        req.option = option
        req.deferTmuxKill = deferTmuxKill
        _ = try await client.deleteSession(req)
    }

    func restart(sessionID: String) async throws {
        var req = FleetRestartSessionRequest()
        req.id = sessionID
        _ = try await client.restartSession(req)
    }

    func rename(sessionID: String, title: String) async throws {
        var req = FleetRenameSessionRequest()
        req.id = sessionID
        req.title = title
        _ = try await client.renameSession(req)
    }

    func acknowledge(sessionID: String) async throws {
        var req = FleetAcknowledgeSessionRequest()
        req.id = sessionID
        _ = try await client.acknowledgeSession(req)
    }

    func pinRepo(root: String) async throws {
        var req = FleetPinRepoRequest()
        req.root = root
        _ = try await client.pinRepo(req)
    }

    func unpinRepo(root: String) async throws {
        var req = FleetUnpinRepoRequest()
        req.root = root
        _ = try await client.unpinRepo(req)
    }

    // ─── Diagnostics ─────────────────────────────────────────────────
    // GetDiagnostics is read-only and lives here purely so consumers have
    // a single object to call into. Returns the markdown blob the daemon
    // prepared (per-session anti-flicker state, recent worker cycles,
    // hook events, status transitions). Tied to the Mac app's Cmd-Shift-D.
    func diagnostics() async throws -> String {
        let resp = try await client.getDiagnostics(Google_Protobuf_Empty())
        return resp.markdown
    }
}
