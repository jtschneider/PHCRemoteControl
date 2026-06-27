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
    /// Cache key (the STM host) for persisting the project; nil disables caching.
    private let cacheKey: String?
    private var eventTask: Task<Void, Never>?
    private var cacheSaveTask: Task<Void, Never>?
    /// Pending "command sent" indicator clears, keyed by device id.
    private var shutterClearTasks: [UUID: Task<Void, Never>] = [:]

    /// Favourited devices, by stable `favouriteKey` (hardware address), per host.
    private(set) var favouriteKeys: Set<String> = []
    private var favouritesDefaultsKey: String { "favourites.\(cacheKey ?? "demo")" }

    init(client: PHCClient = MockPHCClient(), cacheKey: String? = nil) {
        self.client = client
        self.cacheKey = cacheKey
        favouriteKeys = Set(UserDefaults.standard.stringArray(forKey: "favourites.\(cacheKey ?? "demo")") ?? [])
    }

    func start() {
        phase = .connecting
        Task { await load() }
        listenForEvents()
    }

    private func load() async {
        // Start instantly from the cached project if we have one for this host;
        // the structure rarely changes, so we skip the expensive ZIP download.
        if let cacheKey, let cached = ProjectCache.load(key: cacheKey) {
            project = cached
            phase = .ready
        }
        do {
            try await client.connect()
            if project == nil {
                // First run for this host: download and cache the full project.
                let loaded = try await client.loadProject()
                project = loaded
                phase = .ready
                if let cacheKey { ProjectCache.save(loaded, key: cacheKey) }
            }
            if let project {
                client.registerDevices(Array(project.devices.values))
                client.startPolling()   // keep light/outlet state in sync with the bus
            }
        } catch {
            // Only fail hard if there's nothing cached to show.
            if project == nil { phase = .failed(error.localizedDescription) }
        }
    }

    /// Force a fresh download of the project structure from the STM (e.g. after
    /// the installation changed), replacing the cache.
    func reloadProject() {
        guard client is STMv3Client else { return }
        Task {
            do {
                let loaded = try await client.loadProject()
                project = loaded
                if let cacheKey { ProjectCache.save(loaded, key: cacheKey) }
                client.registerDevices(Array(loaded.devices.values))
            } catch {
                phase = .failed(error.localizedDescription)
            }
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
        // Ignore no-op polls; only react (and persist) when state actually changes.
        guard let current = project?.devices[update.deviceID]?.state, current != update.state else { return }
        project?.devices[update.deviceID]?.state = update.state
        scheduleCacheSave()
    }

    /// Persist the project (incl. last-known states) shortly after the last
    /// change, coalescing bursts into a single write.
    private func scheduleCacheSave() {
        guard let cacheKey else { return }
        cacheSaveTask?.cancel()
        cacheSaveTask = Task { [weak self] in
            try? await Task.sleep(for: .seconds(3))
            guard !Task.isCancelled, let project = self?.project else { return }
            ProjectCache.save(project, key: cacheKey)
        }
    }

    // MARK: - Reads

    func devices(in room: Room) -> [Device] {
        project?.devices(in: room) ?? []
    }

    func device(_ id: UUID) -> Device? { project?.devices[id] }

    /// Favourited devices, in project order (floor → category → name).
    var favourites: [Device] {
        guard let project else { return [] }
        return project.rooms
            .flatMap { $0.deviceIDs.compactMap { project.devices[$0] } }
            .filter { isFavourite($0) }
    }

    func isFavourite(_ device: Device) -> Bool {
        guard let key = device.favouriteKey else { return false }
        return favouriteKeys.contains(key)
    }

    func toggleFavourite(_ device: Device) {
        guard let key = device.favouriteKey else { return }
        if favouriteKeys.contains(key) { favouriteKeys.remove(key) } else { favouriteKeys.insert(key) }
        UserDefaults.standard.set(Array(favouriteKeys), forKey: favouritesDefaultsKey)
    }

    // MARK: - Intents (optimistic, then fire the command)

    func togglePower(_ device: Device) {
        guard let ref = device.ref else { return }
        let newValue = !device.state.isOn
        project?.devices[device.id]?.state.isOn = newValue
        Task { try? await client.setPower(ref, on: newValue) }
    }

    /// Fire a central/virtual command (scene). Momentary — no persistent state.
    func activateScene(_ device: Device) {
        guard let ref = device.ref else { return }
        Task {
            do { try await client.activateScene(ref) }
            catch { phase = .failed("Scene \(device.name): \(error.localizedDescription)") }
        }
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

