import Foundation

/// In-memory fake control unit. Lets the whole app run — and be demoed and
/// previewed — without any PHC hardware. Simulates believable behaviour,
/// including shutter travel that streams back position updates.
final class MockPHCClient: PHCClient, @unchecked Sendable {
    let events: AsyncStream<StateUpdate>
    private let continuation: AsyncStream<StateUpdate>.Continuation

    private let lock = NSLock()
    private var project: PHCProject

    init(project: PHCProject = MockPHCClient.sampleProject()) {
        (self.events, self.continuation) = AsyncStream.makeStream()
        self.project = project
    }

    func connect() async throws {
        try await Task.sleep(for: .milliseconds(300)) // pretend to handshake
    }

    func loadProject() async throws -> PHCProject {
        try await Task.sleep(for: .milliseconds(400))
        return withLock { project }
    }

    func setPower(_ ref: ChannelRef, on: Bool) async throws {
        try await Task.sleep(for: .milliseconds(120))
        mutate(matching: ref) { $0.isOn = on; if $0.brightness == 0 && on { $0.brightness = 100 } }
    }

    func setBrightness(_ ref: ChannelRef, _ value: Int) async throws {
        let clamped = max(0, min(100, value))
        mutate(matching: ref) { $0.brightness = clamped; $0.isOn = clamped > 0 }
    }

    func moveShutter(_ ref: ChannelRef, _ command: ShutterCommand) async throws {
        switch command {
        case .stop:
            mutate(matching: ref) { $0.shutterMoving = nil }
        case .up, .down:
            mutate(matching: ref) { $0.shutterMoving = command }
            simulateTravel(ref: ref, opening: command == .up)
        }
    }

    func activateScene(_ ref: ChannelRef) async throws {
        // No central-command automation in the mock; just acknowledge.
        try await Task.sleep(for: .milliseconds(120))
    }

    // The mock pushes state changes itself, so polling is a no-op.
    func registerDevices(_ devices: [Device]) {}
    func startPolling() {}
    func stopPolling() {}

    // MARK: - Simulation

    /// Animate a shutter toward fully open/closed, streaming position updates.
    private func simulateTravel(ref: ChannelRef, opening: Bool) {
        Task { [weak self] in
            guard let self else { return }
            while true {
                try? await Task.sleep(for: .milliseconds(250))
                var done = false
                self.mutate(matching: ref) { state in
                    guard state.shutterMoving != nil else { done = true; return }
                    let step = 8
                    if opening {
                        state.shutterPosition = min(100, state.shutterPosition + step)
                        if state.shutterPosition >= 100 { state.shutterMoving = nil; done = true }
                    } else {
                        state.shutterPosition = max(0, state.shutterPosition - step)
                        if state.shutterPosition <= 0 { state.shutterMoving = nil; done = true }
                    }
                }
                if done { break }
            }
        }
    }

    // MARK: - State helpers

    private func withLock<T>(_ body: () -> T) -> T {
        lock.lock(); defer { lock.unlock() }
        return body()
    }

    /// Mutate every device that targets `ref` and emit the resulting state.
    private func mutate(matching ref: ChannelRef, _ change: (inout DeviceState) -> Void) {
        let updates: [StateUpdate] = withLock {
            var result: [StateUpdate] = []
            for (id, var device) in project.devices where device.ref == ref {
                change(&device.state)
                project.devices[id] = device
                result.append(StateUpdate(deviceID: id, state: device.state))
            }
            return result
        }
        for update in updates { continuation.yield(update) }
    }
}

