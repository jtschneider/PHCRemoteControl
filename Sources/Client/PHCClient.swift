import Foundation

/// Transport boundary between the app and a PHC control unit.
///
/// Everything above this protocol (models, store, views) is independent of how
/// we actually reach the STM. Today the app runs on `MockPHCClient`; the real
/// `STMv3Client` implements the same surface once the wire protocol is captured
/// (see docs/PROTOCOL.md).
protocol PHCClient: AnyObject, Sendable {
    /// Establish the connection / session with the control unit.
    func connect() async throws

    /// Load the installation (rooms + devices) from the control unit.
    func loadProject() async throws -> PHCProject

    /// Turn a light/outlet on or off.
    func setPower(_ ref: ChannelRef, on: Bool) async throws

    /// Set a dimmer's brightness (0...100).
    func setBrightness(_ ref: ChannelRef, _ value: Int) async throws

    /// Drive a shutter up / stop / down.
    func moveShutter(_ ref: ChannelRef, _ command: ShutterCommand) async throws

    /// Begin periodically polling output state (lights/outlets) from the control
    /// unit. No-op for clients that already push their own state.
    func startPolling()

    /// Stop the polling loop.
    func stopPolling()

    /// Live state changes pushed from the bus.
    var events: AsyncStream<StateUpdate> { get }
}

/// Errors surfaced from a client.
enum PHCClientError: LocalizedError {
    case notConnected
    case notImplemented(String)
    case transport(String)

    var errorDescription: String? {
        switch self {
        case .notConnected: return "Not connected to a PHC control unit."
        case .notImplemented(let what): return "\(what) is not implemented yet."
        case .transport(let msg): return msg
        }
    }
}

