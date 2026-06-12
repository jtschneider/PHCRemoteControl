import Foundation
import ZIPFoundation

/// Real transport to a networked STM control unit over its XML-RPC interface.
///
/// Wire format confirmed via packet capture (mitmproxy, 2026-06-11):
///   • POST http://<host>:6680/  HTTP/1.1
///   • sendTelegram(stm_idx, module_addr_byte, content_byte) — 3 integer params
///   • simInputEvent(stm_idx, class=2, module_dip, event_type, channel)
///   • No connect/activate handshake observed for LAN connections
///   • Response: 5-integer array [stm_idx, addr, toggle_echo, ?, state_bitmask]
///     state_bitmask has bit N set when output channel N is active
final class STMv3Client: PHCClient, @unchecked Sendable {

    struct Endpoint {
        var host: String
        var port: Int = 6680
    }

    // MARK: - PHCClient

    let events: AsyncStream<StateUpdate>
    private let continuation: AsyncStream<StateUpdate>.Continuation

    private let endpoint: Endpoint
    private let session: URLSession
    private var pollTask: Task<Void, Never>?

    /// One pollable output channel on an AMD module.
    private struct PolledChannel { let channel: Int; let id: UUID }

    /// AMD output modules to poll, keyed by bus address → its channels.
    /// Populated by loadProject; one telegram per module reads all its channels.
    private let lock = NSLock()
    private var polledModules: [Int: [PolledChannel]] = [:]

    init(endpoint: Endpoint) {
        (self.events, self.continuation) = AsyncStream.makeStream()
        self.endpoint = endpoint
        self.session = URLSession(configuration: .ephemeral)
    }

    deinit {
        pollTask?.cancel()
        continuation.finish()
    }

    func connect() async throws {
        // whoAreYou confirms reachability and returns STM identity (no auth needed for LAN).
        _ = try await call(method: "service.stm.whoAreYou", params: [])
    }

    func loadProject() async throws -> PHCProject {
        // Download the project ZIP via chunked readFile calls, then parse the ppfx.
        var chunks: [Data] = []
        var chunkIdx = 0
        while true {
            let result = try await call(
                method: "service.stm.readFile",
                params: [.int(0), .int(chunkIdx), .int(1)]
            )
            guard let b64 = result.base64,
                  let data = Data(base64Encoded: b64, options: .ignoreUnknownCharacters)
            else { throw PHCClientError.transport("readFile chunk \(chunkIdx): missing or invalid base64") }

            chunks.append(data)

            if result.cur >= result.total - 1 { break }
            chunkIdx += 1
        }

        let zipData = chunks.reduce(Data(), +)
        let ppfxData = try extractPPFX(from: zipData)
        return try PHCProjectParser.parse(ppfxData: ppfxData)
    }

    func setPower(_ ref: ChannelRef, on: Bool) async throws {
        let com: Int = on ? 0x02 : 0x03
        let content = (ref.channel << 5) | com
        try await sendTelegram(moduleAddr: ref.busAddress, content: content)
    }

    func setBrightness(_ ref: ChannelRef, _ value: Int) async throws {
        try await setPower(ref, on: value > 0)
    }

    func moveShutter(_ ref: ChannelRef, _ command: ShutterCommand) async throws {
        // ref = senken (down) EMD channel; shutterUpRef = heben (up) EMD channel.
        // Stored on the Device; STMv3Client receives only the ref here, so the
        // HomeStore must resolve the upRef and call moveShutterFull instead.
        try await moveShutterFull(downRef: ref, upRef: nil, command: command)
    }

    func moveShutterFull(downRef: ChannelRef, upRef: ChannelRef?, command: ShutterCommand) async throws {
        // Verified on real hardware (shutter A + shutter B):
        //   MOVE (short tap):  press(2) → release(4) → doublePress(5) on the direction's
        //                      channel — starts the motor in that direction.
        //   STOP (long hold):  press(2) → longPress(3) on either channel — halts the
        //                      motor; a no-op when the blind is already idle.
        switch command {
        case .down:
            try await tapMove(downRef)
        case .up:
            try await tapMove(upRef ?? downRef)
        case .stop:
            // A long press on the senken (down) channel stops motion in either direction.
            try await simInputEvent(emdModule: downRef.dip, channel: downRef.channel, event: .press)
            try await simInputEvent(emdModule: downRef.dip, channel: downRef.channel, event: .longPress)
        }
    }

    /// A short tap on an EMD channel: press → release → doublePress, which starts the
    /// shutter motor in that channel's direction.
    private func tapMove(_ ref: ChannelRef) async throws {
        try await simInputEvent(emdModule: ref.dip, channel: ref.channel, event: .press)
        try await simInputEvent(emdModule: ref.dip, channel: ref.channel, event: .release)
        try await simInputEvent(emdModule: ref.dip, channel: ref.channel, event: .doublePress)
    }

    // MARK: - State polling

    /// Register devices so the poll loop can read their module state. Only AMD
    /// outputs (lights/outlets) report on/off via the bitmask telegram; shutters
    /// (EMD inputs) and scenes have no pollable state and are skipped.
    func registerDevices(_ devices: [Device]) {
        lock.lock(); defer { lock.unlock() }
        polledModules.removeAll()
        for device in devices {
            guard let ref = device.ref, ref.moduleClass == .amd else { continue }
            polledModules[ref.busAddress, default: []].append(
                PolledChannel(channel: ref.channel, id: device.id))
        }
    }

    func startPolling() {
        pollTask?.cancel()
        pollTask = Task { [weak self] in
            while !Task.isCancelled {
                await self?.pollOnce()
                try? await Task.sleep(for: .seconds(2.5))
            }
        }
    }

    func stopPolling() {
        pollTask?.cancel()
        pollTask = nil
    }

    /// Poll each AMD module once and emit on/off updates for all its channels.
    private func pollOnce() async {
        let modules = lock.withLock { polledModules }
        for (busAddress, channels) in modules {
            guard !Task.isCancelled else { return }
            guard let bitmask = try? await readModuleState(busAddress) else { continue }
            for ch in channels {
                var state = DeviceState()
                state.isOn = (bitmask >> ch.channel) & 1 == 1
                continuation.yield(StateUpdate(deviceID: ch.id, state: state))
            }
        }
    }

    // MARK: - Core calls

    /// sendTelegram(stm_idx=0, module_addr, content_byte) → [0, addr, toggle, ?, state_bitmask]
    @discardableResult
    private func sendTelegram(moduleAddr: Int, content: Int) async throws -> [Int] {
        let result = try await call(method: "service.stm.sendTelegram",
                                    params: [.int(0), .int(moduleAddr), .int(content)])
        return result.intArray
    }

    /// sendTelegram(0, busAddr, 1) → [..., state_bitmask]; bit N = channel N active.
    private func readModuleState(_ busAddress: Int) async throws -> Int {
        let response = try await sendTelegram(moduleAddr: busAddress, content: 0x01)
        return response.last ?? 0
    }

    /// simInputEvent(stm_idx=0, emd_module, channel, event_type, key_type=4)
    ///
    /// Param layout confirmed by captures of the official app on two shutters:
    ///   • shutter A → module 2, channels 4/5: [0, 2, 5|4, event, 4]
    ///   • shutter B → module 3, channels 10/11: [0, 3, 11|10, event, 4]
    /// So param2 = EMD module adr (raw ppfx adr), param3 = channel (CHA adr),
    /// param5 = 4 (a constant EMD key-type for EMD_RUE rocker inputs).
    private func simInputEvent(emdModule: Int, channel: Int, event: InputEvent) async throws {
        _ = try await call(method: "service.stm.simInputEvent",
                           params: [.int(0), .int(emdModule), .int(channel), .int(event.rawValue), .int(4)])
    }

    // MARK: - XML-RPC transport

    private enum Param {
        case int(Int)

        var xml: String {
            switch self { case .int(let v): return "<param><value><i4>\(v)</i4></value></param>" }
        }
    }

    private struct RPCResult {
        let intArray: [Int]
        var base64: String?
        var cur: Int = 0
        var total: Int = 1
    }

    private func call(method: String, params: [Param]) async throws -> RPCResult {
        let body = """
            <?xml version="1.0" encoding="UTF-8"?>
            <methodCall>
            <methodName>\(method)</methodName>
            <params>
            \(params.map(\.xml).joined(separator: "\n"))
            </params>
            </methodCall>
            """
        var req = URLRequest(url: URL(string: "http://\(endpoint.host):\(endpoint.port)/")!)
        req.httpMethod = "POST"
        req.setValue("application/x-www-form-urlencoded", forHTTPHeaderField: "Content-Type")
        req.httpBody = body.data(using: .utf8)

        let (data, _) = try await session.data(for: req)
        return try parseResponse(data)
    }

    private func parseResponse(_ data: Data) throws -> RPCResult {
        guard let xml = String(data: data, encoding: .isoLatin1) ?? String(data: data, encoding: .utf8) else {
            throw PHCClientError.transport("Non-text XML-RPC response")
        }
        if xml.contains("<fault>") {
            let msg = xml.between("<string>", and: "</string>") ?? "unknown fault"
            throw PHCClientError.transport("STM fault: \(msg)")
        }
        let ints = xml.allMatches(between: "<i4>", and: "</i4>").compactMap(Int.init)

        // struct members for readFile responses
        let cur   = xml.structMember(named: "cur").flatMap(Int.init) ?? 0
        let total = xml.structMember(named: "total").flatMap(Int.init) ?? 1
        let b64   = xml.structMember(named: "bin")

        return RPCResult(intArray: ints, base64: b64, cur: cur, total: total)
    }

    // MARK: - ZIP extraction

    /// Extracts `project.ppfx` from the ZIP returned by the STM's readFile calls.
    /// Uses ZIPFoundation which reads the central directory (end of file) and handles
    /// data descriptors, bit-3 flags, and raw DEFLATE correctly.
    private func extractPPFX(from zip: Data) throws -> Data {
        guard let archive = Archive(data: zip, accessMode: .read) else {
            throw PHCClientError.transport("Could not open project ZIP archive")
        }
        guard let entry = archive["project.ppfx"] else {
            throw PHCClientError.transport("project.ppfx not found in ZIP")
        }
        var result = Data()
        _ = try archive.extract(entry) { result.append($0) }
        return result
    }
}

// MARK: - Input event types

private enum InputEvent: Int {
    case press = 2
    case longPress = 3       // hold → start movement
    case release = 4
    case doublePress = 5     // "click confirmed" → stop pulse
}

// MARK: - ChannelRef extensions for bus addressing

extension ChannelRef {
    /// Module bus address byte sent in sendTelegram param 2.
    var busAddress: Int {
        switch moduleClass {
        case .emd: return dip                    // class bits 000
        case .amd: return 0x40 | dip             // class bits 010
        case .jrm: return 0x60 | dip             // class bits 011
        }
    }

}

// MARK: - NSLock convenience

extension NSLock {
    func withLock<T>(_ body: () -> T) -> T {
        lock(); defer { unlock() }
        return body()
    }
}

// MARK: - String parsing helpers

private extension String {
    func between(_ start: String, and end: String) -> String? {
        guard let s = range(of: start)?.upperBound,
              let e = self[s...].range(of: end)?.lowerBound else { return nil }
        return String(self[s..<e])
    }

    func allMatches(between start: String, and end: String) -> [String] {
        var results: [String] = []
        var search = self
        while let s = search.range(of: start)?.upperBound,
              let e = search[s...].range(of: end)?.lowerBound {
            results.append(String(search[s..<e]))
            search = String(search[e...])
        }
        return results
    }

    /// Extract the value of a named struct member from XML-RPC XML.
    /// Looks for `<name>key</name>...<value>...<i4>N</i4>` or `<base64>...</base64>`.
    func structMember(named key: String) -> String? {
        guard let nameRange = range(of: "<name>\(key)</name>") else { return nil }
        let rest = String(self[nameRange.upperBound...])
        if let v = rest.between("<i4>", and: "</i4>") { return v }
        if let v = rest.between("<string>", and: "</string>") { return v }
        if let v = rest.between("<base64>", and: "</base64>") {
            return v.components(separatedBy: .whitespacesAndNewlines).joined()
        }
        return nil
    }
}
