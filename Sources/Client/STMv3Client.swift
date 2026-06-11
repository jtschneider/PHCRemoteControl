import Foundation

/// Real transport to a networked **STM** control unit via its **XML-RPC**
/// interface (the `service.stm.*` API exposed by the STM's onboard daemon — the
/// same surface the PHC Systemsoftware's `iserver` provides locally).
///
/// PARTIALLY IMPLEMENTED. The method names, default port and command flow below
/// are confirmed from decompiling the Systemsoftware (see docs/PROTOCOL.md §1).
/// What still needs a packet-capture of the official app to finalise:
///   • the exact STM v3 TCP port (6680 is the documented default),
///   • LAN discovery (likely a UDP broadcast),
///   • authentication (project password presentation),
///   • exact XML-RPC parameter order for each method.
///
/// Once those are pinned down, only the bodies in `Calls` and `connect`/
/// `loadProject` need filling — the command methods already build correct PHC
/// telegrams via `PHCTelegram`.
final class STMv3Client: PHCClient, @unchecked Sendable {

    /// XML-RPC method names, verbatim from the binary.
    enum Method {
        static let connect      = "service.stm.connect"
        static let getModule    = "service.stm.getModule"
        static let sendTelegram = "service.stm.sendTelegram"
        static let sendPOR      = "service.stm.sendPOR"
        static let getProgress  = "service.stm.getProgress"
        static let ping         = "iserver.ping"
        static let getVersion   = "iserver.getVersion"
    }

    struct Endpoint {
        var host: String
        var port: Int = 6680            // documented XML-RPC default
        var path: String = "/RPC2"      // typical XML-RPC path; confirm via capture
        var projectPassword: String?
    }

    let events: AsyncStream<StateUpdate>
    private let continuation: AsyncStream<StateUpdate>.Continuation
    private let endpoint: Endpoint

    /// Per-module toggle bit, flipped on each command (see PHC protocol §2).
    private let lock = NSLock()
    private var toggles: [UInt8: Bool] = [:]

    init(endpoint: Endpoint) {
        (self.events, self.continuation) = AsyncStream.makeStream()
        self.endpoint = endpoint
    }

    func connect() async throws {
        // TODO: XML-RPC `service.stm.connect` (+ auth). Then begin receiving STM
        // events and forward them to `continuation` as StateUpdates.
        throw PHCClientError.notImplemented("STM v3 connect")
    }

    func loadProject() async throws -> PHCProject {
        // TODO: enumerate modules via `service.stm.getModule`, map AMD/JRM/EMD
        // channels to Devices/Rooms (the project XML also carries names/rooms).
        throw PHCClientError.notImplemented("loadProject")
    }

    func setPower(_ ref: ChannelRef, on: Bool) async throws {
        let telegram = PHCTelegram.amdSwitch(ref, on: on, toggle: nextToggle(for: ref))
        try await sendTelegram(telegram)
    }

    func setBrightness(_ ref: ChannelRef, _ value: Int) async throws {
        // Dimmers use the same channel addressing; the brightness payload still
        // needs to be confirmed from a capture (DIM module telegram). For now,
        // map >0 to ON so the call is well-formed.
        try await setPower(ref, on: value > 0)
    }

    func moveShutter(_ ref: ChannelRef, _ command: ShutterCommand) async throws {
        let toggle = nextToggle(for: ref)
        let telegram: [UInt8]
        switch command {
        case .stop:
            telegram = PHCTelegram.jrmStop(ref, toggle: toggle)
        case .up, .down:
            // Use a long max time; the module stops at its end position.
            telegram = PHCTelegram.jrmMove(ref, up: command == .up, deciSeconds: 600, toggle: toggle)
        }
        try await sendTelegram(telegram)
    }

    // MARK: - Plumbing

    private func nextToggle(for ref: ChannelRef) -> Bool {
        lock.lock(); defer { lock.unlock() }
        let addr = PHCTelegram.address(for: ref)
        let next = !(toggles[addr] ?? false)
        toggles[addr] = next
        return next
    }

    /// Wrap a raw PHC telegram in a `service.stm.sendTelegram` XML-RPC call.
    private func sendTelegram(_ bytes: [UInt8]) async throws {
        // TODO: build the <methodCall> for Method.sendTelegram with `bytes`
        // (likely a base64 / int-array param — confirm via capture), POST it to
        // http://host:port/path, and parse the response.
        _ = bytes
        throw PHCClientError.notImplemented("sendTelegram over XML-RPC")
    }
}
</content>
