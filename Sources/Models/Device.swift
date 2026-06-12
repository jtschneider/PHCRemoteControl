import Foundation

/// What kind of control a device exposes in the UI.
enum DeviceKind: String, Codable, Sendable {
    case light     // simple on/off relay output
    case dimmer    // brightness 0...100
    case outlet    // on/off, shown as a socket
    case shutter   // up / stop / down, with optional position
    case scene     // momentary trigger (no persistent state)
}

/// Direction command for a shutter/blind.
enum ShutterCommand: String, Codable, Sendable {
    case up, stop, down
}

/// The live state of a device. Only the fields relevant to its `kind` are used.
struct DeviceState: Equatable, Codable, Sendable {
    var isOn: Bool = false
    /// 0...100 for dimmers.
    var brightness: Int = 0
    /// 0 (closed) ... 100 (open) for shutters, when position is known.
    var shutterPosition: Int = 100
    var shutterMoving: ShutterCommand? = nil
}

/// A user-facing controllable thing in the home.
struct Device: Identifiable, Codable, Sendable {
    let id: UUID
    var name: String
    var kind: DeviceKind
    /// Bus address this device drives. `scene` devices may drive several, so the
    /// primary ref is optional.
    var ref: ChannelRef?
    var state: DeviceState

    /// For shutters only: the EMD input channel that raises (heben).
    /// `ref` holds the lower (senken) channel; both use simInputEvent.
    var shutterUpRef: ChannelRef?

    init(id: UUID = UUID(), name: String, kind: DeviceKind, ref: ChannelRef? = nil,
         shutterUpRef: ChannelRef? = nil, state: DeviceState = .init()) {
        self.id = id
        self.name = name
        self.kind = kind
        self.ref = ref
        self.shutterUpRef = shutterUpRef
        self.state = state
    }

    var systemImage: String {
        switch kind {
        case .light:   return state.isOn ? "lightbulb.fill" : "lightbulb"
        case .dimmer:  return state.isOn ? "lightbulb.led.fill" : "lightbulb.led"
        case .outlet:  return "powerplug.fill"
        case .shutter: return "blinds.horizontal.closed"
        case .scene:   return "play.circle"
        }
    }
}

