import Foundation

extension MockPHCClient {
    /// A believable demo installation used by the mock client and SwiftUI previews.
    static func sampleProject() -> PHCProject {
        func light(_ name: String, dip: Int, ch: Int, on: Bool = false) -> Device {
            Device(name: name, kind: .light,
                   ref: ChannelRef(moduleClass: .amd, dip: dip, channel: ch),
                   state: DeviceState(isOn: on))
        }
        func dimmer(_ name: String, dip: Int, ch: Int, brightness: Int = 0) -> Device {
            Device(name: name, kind: .dimmer,
                   ref: ChannelRef(moduleClass: .amd, dip: dip, channel: ch),
                   state: DeviceState(isOn: brightness > 0, brightness: brightness))
        }
        func outlet(_ name: String, dip: Int, ch: Int, on: Bool = false) -> Device {
            Device(name: name, kind: .outlet,
                   ref: ChannelRef(moduleClass: .amd, dip: dip, channel: ch),
                   state: DeviceState(isOn: on))
        }
        func shutter(_ name: String, dip: Int, ch: Int, position: Int = 100) -> Device {
            Device(name: name, kind: .shutter,
                   ref: ChannelRef(moduleClass: .jrm, dip: dip, channel: ch),
                   state: DeviceState(shutterPosition: position))
        }

        let living = [
            dimmer("Ceiling", dip: 6, ch: 5, brightness: 60),
            light("Reading Lamp", dip: 6, ch: 4, on: true),
            outlet("TV Socket", dip: 7, ch: 0),
            shutter("Terrace Blind", dip: 0, ch: 0, position: 80),
            shutter("Window Blind", dip: 0, ch: 1, position: 100),
        ]
        let kitchen = [
            light("Worktop", dip: 8, ch: 0, on: true),
            dimmer("Dining", dip: 8, ch: 1, brightness: 0),
            shutter("Kitchen Blind", dip: 0, ch: 2, position: 40),
        ]
        let bedroom = [
            dimmer("Ceiling", dip: 9, ch: 0, brightness: 0),
            light("Bedside Left", dip: 9, ch: 1),
            light("Bedside Right", dip: 9, ch: 2),
            shutter("Bedroom Blind", dip: 1, ch: 0, position: 0),
        ]
        let office = [
            light("Desk", dip: 10, ch: 0, on: true),
            outlet("Charger", dip: 10, ch: 1, on: true),
        ]

        let rooms = [
            Room(name: "Living Room", symbol: "sofa", deviceIDs: living.map(\.id)),
            Room(name: "Kitchen", symbol: "fork.knife", deviceIDs: kitchen.map(\.id)),
            Room(name: "Bedroom", symbol: "bed.double", deviceIDs: bedroom.map(\.id)),
            Room(name: "Office", symbol: "desktopcomputer", deviceIDs: office.map(\.id)),
        ]

        let all = living + kitchen + bedroom + office
        let devices = Dictionary(uniqueKeysWithValues: all.map { ($0.id, $0) })
        return PHCProject(name: "My House", rooms: rooms, devices: devices)
    }
}
</content>
