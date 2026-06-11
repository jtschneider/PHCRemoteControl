import Foundation

/// Real transport to a networked **STM v3** control unit.
///
/// NOT FUNCTIONAL YET. The STM v3 speaks a proprietary XML-RPC interface over
/// the LAN that still has to be captured (see docs/PROTOCOL.md). This file marks
/// every place that needs the real protocol so that, once captured, wiring it up
/// touches nothing outside this type.
///
/// Discovery is expected to be a UDP broadcast on the LAN; control is expected to
/// be XML-RPC over HTTP on a fixed port. Fill in `Endpoint` and the method bodies
/// when the capture is done.
final class STMv3Client: PHCClient, @unchecked Sendable {
    struct Endpoint {
        var host: String
        var port: Int = 0          // TODO: real XML-RPC port (capture)
        var path: String = "/"     // TODO: real endpoint path
        var projectPassword: String?
    }

    let events: AsyncStream<StateUpdate>
    private let continuation: AsyncStream<StateUpdate>.Continuation
    private let endpoint: Endpoint

    init(endpoint: Endpoint) {
        (self.events, self.continuation) = AsyncStream.makeStream()
        self.endpoint = endpoint
    }

    func connect() async throws {
        // TODO: open session / authenticate with project password.
        throw PHCClientError.notImplemented("STM v3 transport")
    }

    func loadProject() async throws -> PHCProject {
        // TODO: XML-RPC call that returns the module list, then map modules →
        //       rooms/devices. The PHC bus semantics in docs/PROTOCOL.md (§2)
        //       describe how channels map to lights/dimmers/shutters.
        throw PHCClientError.notImplemented("loadProject")
    }

    func setPower(_ ref: ChannelRef, on: Bool) async throws {
        throw PHCClientError.notImplemented("setPower")
    }

    func setBrightness(_ ref: ChannelRef, _ value: Int) async throws {
        throw PHCClientError.notImplemented("setBrightness")
    }

    func moveShutter(_ ref: ChannelRef, _ command: ShutterCommand) async throws {
        throw PHCClientError.notImplemented("moveShutter")
    }
}
</content>
