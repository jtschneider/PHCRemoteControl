import Foundation

/// Builders for raw PHC bus telegrams — the payload carried inside the STM's
/// `service.stm.sendTelegram` XML-RPC call. Byte semantics are documented in
/// docs/PROTOCOL.md §2 and mirror the verified ESPHome-PHC-Controller logic.
///
/// These are pure and unit-testable; they have no networking dependency.
enum PHCTelegram {

    /// CRC-16/X-25: poly 0x1021 reflected (0x8408), init 0xFFFF, reflected in/out,
    /// final XOR 0xFFFF.
    static func crc(_ data: [UInt8]) -> UInt16 {
        var crc: UInt16 = 0xFFFF
        for byte in data {
            crc ^= UInt16(byte)
            for _ in 0..<8 {
                if crc & 0x0001 != 0 {
                    crc = (crc >> 1) ^ 0x8408
                } else {
                    crc >>= 1
                }
            }
        }
        return crc ^ 0xFFFF
    }

    /// Compose a full telegram: address, (toggle|length), content, CRC (LE).
    static func frame(address: UInt8, toggle: Bool, content: [UInt8]) -> [UInt8] {
        precondition(content.count <= 0x7F, "content too long")
        var msg: [UInt8] = [address, (toggle ? 0x80 : 0x00) | UInt8(content.count)]
        msg.append(contentsOf: content)
        let c = crc(msg)
        msg.append(UInt8(c & 0xFF))
        msg.append(UInt8((c >> 8) & 0xFF))
        return msg
    }

    /// Bus address byte for a channel reference: (class << 5) | dip.
    static func address(for ref: ChannelRef) -> UInt8 {
        let classBits: UInt8 = (ref.moduleClass == .emd) ? 0x00 : 0x40
        return classBits | UInt8(ref.dip & 0x1F)
    }

    // MARK: - Command builders

    /// AMD relay output on/off. content = (channel << 5) | (0x02 on | 0x03 off).
    static func amdSwitch(_ ref: ChannelRef, on: Bool, toggle: Bool) -> [UInt8] {
        let fn: UInt8 = on ? 0x02 : 0x03
        let content: [UInt8] = [UInt8(ref.channel) << 5 | fn]
        return frame(address: address(for: ref), toggle: toggle, content: content)
    }

    /// EMD LED output on/off. content = (channel << 4) | (0x02 on | 0x03 off).
    static func emdLight(_ ref: ChannelRef, on: Bool, toggle: Bool) -> [UInt8] {
        let fn: UInt8 = on ? 0x02 : 0x03
        let content: [UInt8] = [UInt8(ref.channel) << 4 | fn]
        return frame(address: address(for: ref), toggle: toggle, content: content)
    }

    /// JRM shutter move. content = [(channel<<5)|(0x05 up|0x06 down), 0x07 prio,
    /// time_lo, time_hi] where time is in units of 100 ms.
    static func jrmMove(_ ref: ChannelRef, up: Bool, deciSeconds time: UInt16, toggle: Bool) -> [UInt8] {
        let fn: UInt8 = UInt8(ref.channel) << 5 | (up ? 0x05 : 0x06)
        let content: [UInt8] = [fn, 0x07, UInt8(time & 0xFF), UInt8(time >> 8)]
        return frame(address: address(for: ref), toggle: toggle, content: content)
    }

    /// JRM shutter stop/idle. content = [(channel<<5)|0x02, 0xFC prio].
    static func jrmStop(_ ref: ChannelRef, toggle: Bool) -> [UInt8] {
        let content: [UInt8] = [UInt8(ref.channel) << 5 | 0x02, 0xFC]
        return frame(address: address(for: ref), toggle: toggle, content: content)
    }

    /// Acknowledgement for a module: content = [0x00], length 1.
    static func ack(address: UInt8, toggle: Bool) -> [UInt8] {
        frame(address: address, toggle: toggle, content: [0x00])
    }
}

