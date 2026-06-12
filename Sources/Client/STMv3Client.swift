import Foundation
import Compression

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

    /// Devices we know about, populated by loadProject. Used by the poll loop
    /// to emit StateUpdates when module state changes.
    private let lock = NSLock()
    private var knownDevices: [ChannelRef: UUID] = [:]   // ref → Device.id

    init(endpoint: Endpoint) {
        (self.events, self.continuation) = AsyncStream.makeStream()
        self.endpoint = endpoint
        self.session = URLSession(configuration: .ephemeral)
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
        let project = try PHCProjectParser.parse(ppfxData: ppfxData)

        // Register devices so the poll loop can emit state updates.
        registerDevices(Array(project.devices.values))
        return project
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
        switch command {
        case .stop:
            try await simInputEvent(dip: downRef.dip, event: .press, channel: downRef.channel)
            try await simInputEvent(dip: downRef.dip, event: .release, channel: downRef.channel)
        case .down:
            try await simInputEvent(dip: downRef.dip, event: .press, channel: downRef.channel)
            try await simInputEvent(dip: downRef.dip, event: .longPress, channel: downRef.channel)
        case .up:
            let target = upRef ?? downRef
            try await simInputEvent(dip: target.dip, event: .press, channel: target.channel)
            try await simInputEvent(dip: target.dip, event: .longPress, channel: target.channel)
        }
    }

    // MARK: - State polling

    /// Register devices so the poll loop can emit events for them.
    func registerDevices(_ devices: [Device]) {
        lock.lock(); defer { lock.unlock() }
        for device in devices {
            if let ref = device.ref { knownDevices[ref] = device.id }
        }
    }

    func startPolling() {
        pollTask?.cancel()
        pollTask = Task { [weak self] in
            while !Task.isCancelled {
                await self?.pollOnce()
                try? await Task.sleep(for: .seconds(1))
            }
        }
    }

    private func pollOnce() async {
        let refs = lock.withLock { Array(knownDevices.keys) }
        for ref in refs {
            guard !Task.isCancelled else { return }
            guard let state = try? await getState(ref) else { continue }
            let id = lock.withLock { knownDevices[ref] }
            if let id {
                continuation.yield(StateUpdate(deviceID: id, state: state))
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

    /// Read a module's state bitmask by sending the getState content byte (1).
    private func getState(_ ref: ChannelRef) async throws -> DeviceState {
        let response = try await sendTelegram(moduleAddr: ref.busAddress, content: 0x01)
        let bitmask = response.last ?? 0
        var state = DeviceState()
        state.isOn = (bitmask >> ref.channel) & 1 == 1
        return state
    }

    /// simInputEvent(stm_idx=0, class=2, module_dip, event_type, channel)
    private func simInputEvent(dip: Int, event: InputEvent, channel: Int) async throws {
        _ = try await call(method: "service.stm.simInputEvent",
                           params: [.int(0), .int(2), .int(dip), .int(event.rawValue), .int(channel)])
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

    /// Extracts the raw bytes of `project.ppfx` from the ZIP returned by readFile.
    /// Uses a minimal ZIP local-file-header parser — no external dependency needed.
    private func extractPPFX(from zip: Data) throws -> Data {
        return try extractZipEntry(named: "project.ppfx", from: zip)
    }

    private func extractZipEntry(named target: String, from zip: Data) throws -> Data {
        var offset = 0
        while offset + 30 < zip.count {
            guard zip[offset] == 0x50, zip[offset+1] == 0x4B,
                  zip[offset+2] == 0x03, zip[offset+3] == 0x04 else { break }

            let flags       = UInt16(zip[offset+6])  | UInt16(zip[offset+7])  << 8
            let compression = UInt16(zip[offset+8])  | UInt16(zip[offset+9])  << 8
            var cSize       = Int(UInt32(zip[offset+18]) | UInt32(zip[offset+19]) << 8
                                | UInt32(zip[offset+20]) << 16 | UInt32(zip[offset+21]) << 24)
            var uSize       = Int(UInt32(zip[offset+22]) | UInt32(zip[offset+23]) << 8
                                | UInt32(zip[offset+24]) << 16 | UInt32(zip[offset+25]) << 24)
            let nameLen     = Int(UInt16(zip[offset+26]) | UInt16(zip[offset+27]) << 8)
            let extraLen    = Int(UInt16(zip[offset+28]) | UInt16(zip[offset+29]) << 8)

            let nameStart = offset + 30
            let nameEnd   = nameStart + nameLen
            guard nameEnd <= zip.count else { break }

            let name = String(bytes: zip[nameStart..<nameEnd], encoding: .utf8) ?? ""
            let dataStart = nameEnd + extraLen

            // When bit 3 is set, cSize/uSize in the local header are 0.
            // The real values are in the data descriptor (PK\x07\x08) that follows the data.
            var nextEntryOffset: Int
            if (flags & 0x08) != 0, let ddOffset = findDataDescriptor(in: zip, from: dataStart) {
                cSize = Int(UInt32(zip[ddOffset+8])  | UInt32(zip[ddOffset+9])  << 8
                          | UInt32(zip[ddOffset+10]) << 16 | UInt32(zip[ddOffset+11]) << 24)
                uSize = Int(UInt32(zip[ddOffset+12]) | UInt32(zip[ddOffset+13]) << 8
                          | UInt32(zip[ddOffset+14]) << 16 | UInt32(zip[ddOffset+15]) << 24)
                nextEntryOffset = ddOffset + 16
            } else {
                nextEntryOffset = dataStart + cSize
            }

            let dataEnd = dataStart + cSize
            guard dataEnd <= zip.count else { break }

            if name == target {
                let compressed = Data(zip[dataStart..<dataEnd])
                if compression == 0 {
                    return compressed
                } else if compression == 8 {
                    return try inflateDeflate(compressed, expectedSize: uSize)
                } else {
                    throw PHCClientError.transport("Unsupported ZIP compression \(compression) for \(name)")
                }
            }
            offset = nextEntryOffset
        }
        throw PHCClientError.transport("project.ppfx not found in ZIP")
    }

    /// Scan forward from `start` for the data descriptor signature PK\x07\x08.
    private func findDataDescriptor(in zip: Data, from start: Int) -> Int? {
        guard start + 15 < zip.count else { return nil }
        for i in start ..< zip.count - 15 {
            if zip[i] == 0x50 && zip[i+1] == 0x4B && zip[i+2] == 0x07 && zip[i+3] == 0x08 {
                return i
            }
        }
        return nil
    }

    private func inflateDeflate(_ data: Data, expectedSize: Int) throws -> Data {
        guard !data.isEmpty else {
            throw PHCClientError.transport("ZIP deflate: empty compressed data")
        }
        // ZIP uses raw DEFLATE (RFC 1951) — no zlib header or Adler-32.
        // 0x600 = COMPRESSION_DEFLATE; the named constant isn't exported through the Swift
        // Compression module overlay, so we use the raw integer value directly.
        let bufSize = expectedSize > 0 ? expectedSize : data.count * 6
        var output = Data(count: bufSize)
        let written = output.withUnsafeMutableBytes { outPtr in
            data.withUnsafeBytes { inPtr in
                compression_decode_buffer(
                    outPtr.baseAddress!.assumingMemoryBound(to: UInt8.self),
                    bufSize,
                    inPtr.baseAddress!.assumingMemoryBound(to: UInt8.self),
                    inPtr.count,
                    nil,
                    compression_algorithm(rawValue: UInt32(0x600))  // COMPRESSION_DEFLATE
                )
            }
        }
        guard written > 0 else {
            throw PHCClientError.transport("ZIP deflate decompression failed")
        }
        return output.prefix(written)
    }
}

// MARK: - Input event types

private enum InputEvent: Int {
    case press = 2
    case longPress = 3
    case release = 4
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
