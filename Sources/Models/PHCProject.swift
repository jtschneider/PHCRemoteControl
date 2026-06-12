import Foundation

/// A named grouping of devices (typically a room).
struct Room: Identifiable, Codable, Sendable {
    let id: UUID
    var name: String
    var symbol: String          // SF Symbol used in the sidebar
    var deviceIDs: [UUID]

    init(id: UUID = UUID(), name: String, symbol: String = "square.split.bottomrightquarter", deviceIDs: [UUID]) {
        self.id = id
        self.name = name
        self.symbol = symbol
        self.deviceIDs = deviceIDs
    }
}

/// The whole installation as handed to us by the STM: rooms + devices.
/// Mirrors the "project" the official app loads from the control unit.
struct PHCProject: Codable, Sendable {
    var name: String
    var rooms: [Room]
    var devices: [UUID: Device]

    func devices(in room: Room) -> [Device] {
        room.deviceIDs.compactMap { devices[$0] }
    }
}

/// A live state change pushed from the STM/bus for a single device.
struct StateUpdate: Sendable {
    let deviceID: UUID
    let state: DeviceState
}

