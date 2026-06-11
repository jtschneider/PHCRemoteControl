import Foundation

/// PHC output function codes (`com` values), derived from the Systemsoftware's
/// `functions.xml`. The function code is the low part of a telegram content byte:
/// `content = (channel << shift) | com` (shift = 5 for AMD/DIM/JRM, 4 for EMD-LED).
/// See docs/PROTOCOL.md §1 (command model) and §2 (telegram bytes).
enum PHCFunction {
    // AMD relay output (lights, outlets) — module type AMD24_AUS / UTM_AUS.
    static let amdOn: UInt8  = 2
    static let amdOff: UInt8 = 3

    // EMD LED output — module type EMD24_LED.
    static let emdLedOn: UInt8  = 2
    static let emdLedOff: UInt8 = 3

    // Dimmer — module type DIM_AUS. Base on/off; ramp/scene variants are com 4...25
    // and carry extended bytes (brightness/time) still to be confirmed via capture.
    static let dimOn: UInt8  = 2
    static let dimOff: UInt8 = 3

    // JRM shutter — module type JRM_AUS. The raw bus uses 0x05 up / 0x06 down /
    // 0x02 stop with a priority + time payload (see PHCTelegram.jrm*). The
    // higher-level `com` variants (2...15) in functions.xml are parameterised
    // (position/time presets) and resolve down to these bus operations.
}
