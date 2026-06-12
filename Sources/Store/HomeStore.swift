import Foundation
import Observation

/// Owns the active `PHCClient`, holds the loaded project, and mediates all user
/// actions. Views observe this and send intents to it; it applies optimistic
/// updates immediately and folds in live `StateUpdate`s from the client.
@MainActor
@Observable
final class HomeStore {
    enum Phase: Equatable {
        case connecting
        case ready
        case failed(String)
    }

    private(set) var phase: Phase = .connecting
    private(set) var project: PHCProject?

    private let client: PHCClient
    private var eventTask: Task<Void, Never>?
    /// Pending "command sent" indicator clears, keyed by device id.
    private var shutterClearTasks: [UUID: Task<Void, Never>] = [:]

    init(client: PHCClient = MockPHCClient()) {
        self.client = client
    }

    func start() {
        phase = .connecting
        Task { await load() }
        listenForEvents()
    }

    private func load() async {
        do {
            try await client.connect()
            project = try await client.loadProject()
            phase = .ready
        } catch {
            phase = .failed(error.localizedDescription)
        }
    }

    private func listenForEvents() {
        eventTask?.cancel()
        eventTask = Task { [weak self] in
            guard let self else { return }
            for await update in client.events {
                self.apply(update)
            }
        }
    }

    private func apply(_ update: StateUpdate) {
        project?.devices[update.deviceID]?.state = update.state
    }

    // MARK: - Reads

    func devices(in room: Room) -> [Device] {
        project?.devices(in: room) ?? []
    }

    func device(_ id: UUID) -> Device? { project?.devices[id] }

    // MARK: - Intents (optimistic, then fire the command)

    func togglePower(_ device: Device) {
        guard let ref = device.ref else { return }
        let newValue = !device.state.isOn
        project?.devices[device.id]?.state.isOn = newValue
        Task { try? await client.setPower(ref, on: newValue) }
    }

    func setBrightness(_ device: Device, _ value: Int) {
        guard let ref = device.ref else { return }
        project?.devices[device.id]?.state.brightness = value
        project?.devices[device.id]?.state.isOn = value > 0
        Task { try? await client.setBrightness(ref, value) }
    }

    func moveShutter(_ device: Device, _ command: ShutterCommand) {
        guard let ref = device.ref else { return }
        let id = device.id

        // Optimistic "command sent" indicator. There is no position/movement
        // feedback from PHC shutters, so for up/down we show the indicator
        // briefly and auto-clear it; stop clears immediately.
        shutterClearTasks[id]?.cancel()
        project?.devices[id]?.state.shutterMoving = (command == .stop) ? nil : command
        if command != .stop {
            shutterClearTasks[id] = Task { [weak self] in
                try? await Task.sleep(for: .seconds(4))
                guard !Task.isCancelled else { return }
                self?.project?.devices[id]?.state.shutterMoving = nil
            }
        }

        let upRef = device.shutterUpRef
        Task {
            do {
                if let real = client as? STMv3Client {
                    try await real.moveShutterFull(downRef: ref, upRef: upRef, command: command)
                } else {
                    try await client.moveShutter(ref, command)
                }
            } catch {
                phase = .failed("Shutter \(command): \(error.localizedDescription)")
            }
        }
    }
}

