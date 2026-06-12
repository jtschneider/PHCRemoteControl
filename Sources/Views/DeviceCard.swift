import SwiftUI

/// One card per device, rendering the right control for its kind.
struct DeviceCard: View {
    @Environment(HomeStore.self) private var store
    let device: Device

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            header
            control
        }
        .padding()
        .background(Color(.secondarySystemGroupedBackground), in: RoundedRectangle(cornerRadius: 18))
    }

    private var header: some View {
        HStack(spacing: 12) {
            Image(systemName: device.systemImage)
                .font(.title2)
                .foregroundStyle(isActive ? Color.accentColor : .secondary)
                .frame(width: 32)
            VStack(alignment: .leading, spacing: 2) {
                Text(device.name).font(.headline)
                if let ref = device.ref {
                    Text(ref.description).font(.caption2).foregroundStyle(.tertiary)
                }
            }
            Spacer()
        }
    }

    @ViewBuilder
    private var control: some View {
        switch device.kind {
        case .light, .outlet:
            Toggle("On", isOn: Binding(
                get: { device.state.isOn },
                set: { _ in store.togglePower(device) }
            ))
            .labelsHidden()
            .toggleStyle(.switch)
            .frame(maxWidth: .infinity, alignment: .trailing)

        case .dimmer:
            DimmerControl(device: device)

        case .shutter:
            ShutterControl(device: device)

        case .scene:
            Button("Activate") { store.togglePower(device) }
                .buttonStyle(.borderedProminent)
                .frame(maxWidth: .infinity)
        }
    }

    private var isActive: Bool {
        switch device.kind {
        case .light, .outlet: return device.state.isOn
        case .dimmer: return device.state.brightness > 0
        case .shutter: return device.state.shutterMoving != nil
        case .scene: return false
        }
    }
}

/// Brightness slider for a dimmer.
private struct DimmerControl: View {
    @Environment(HomeStore.self) private var store
    let device: Device

    var body: some View {
        VStack(spacing: 4) {
            Slider(
                value: Binding(
                    get: { Double(device.state.brightness) },
                    set: { store.setBrightness(device, Int($0)) }
                ),
                in: 0...100,
                step: 1
            )
            HStack {
                Text("Brightness").font(.caption).foregroundStyle(.secondary)
                Spacer()
                Text("\(device.state.brightness)%").font(.caption.monospacedDigit())
            }
        }
    }
}

/// Up / stop / down controls for a shutter.
///
/// PHC JRM shutter modules are plain up/down/stop relays with no position
/// feedback, so there is no percentage to show — only the last command sent
/// (Opening…/Closing…), which clears when you press Stop.
private struct ShutterControl: View {
    @Environment(HomeStore.self) private var store
    let device: Device

    var body: some View {
        VStack(spacing: 10) {
            HStack(spacing: 12) {
                shutterButton(.up, "chevron.up")
                shutterButton(.stop, "stop.fill")
                shutterButton(.down, "chevron.down")
            }
            if let statusText {
                HStack {
                    ProgressView().controlSize(.mini)
                    Text(statusText).font(.caption).foregroundStyle(.secondary)
                    Spacer()
                }
            }
        }
    }

    private func shutterButton(_ command: ShutterCommand, _ symbol: String) -> some View {
        Button {
            store.moveShutter(device, command)
        } label: {
            Image(systemName: symbol)
                .frame(maxWidth: .infinity)
                .padding(.vertical, 10)
        }
        .buttonStyle(.bordered)
        .tint(device.state.shutterMoving == command ? .accentColor : nil)
        .accessibilityLabel(accessibilityLabel(command))
    }

    private func accessibilityLabel(_ command: ShutterCommand) -> LocalizedStringKey {
        switch command {
        case .up:   return "Open shutter"
        case .stop: return "Stop shutter"
        case .down: return "Close shutter"
        }
    }

    /// Last command in flight, or nil when idle (no sensor to report otherwise).
    private var statusText: LocalizedStringKey? {
        switch device.state.shutterMoving {
        case .up: return "Opening…"
        case .down: return "Closing…"
        case .stop, .none: return nil
        }
    }
}

#Preview {
    let store = HomeStore(client: MockPHCClient())
    return ScrollView {
        VStack {
            DeviceCard(device: Device(name: "Ceiling", kind: .dimmer,
                                      ref: ChannelRef(moduleClass: .amd, dip: 6, channel: 5),
                                      state: DeviceState(isOn: true, brightness: 60)))
            DeviceCard(device: Device(name: "Terrace Blind", kind: .shutter,
                                      ref: ChannelRef(moduleClass: .jrm, dip: 0, channel: 0),
                                      state: DeviceState(shutterPosition: 80)))
        }
        .padding()
    }
    .environment(store)
}

