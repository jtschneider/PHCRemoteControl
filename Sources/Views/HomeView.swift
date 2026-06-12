import SwiftUI

/// Top-level adaptive layout.
///
/// • iPhone (compact): a `NavigationStack` showing the floor overview that
///   pushes to a room's device grid — classic slide transition, correct back
///   behaviour, and no floor pre-selected.
/// • iPad (regular): a `NavigationSplitView` with the floors in a persistent
///   sidebar and the selected room's devices in the detail pane.
struct HomeView: View {
    @Environment(HomeStore.self) private var store
    @Environment(\.horizontalSizeClass) private var sizeClass
    @State private var selectedRoom: Room.ID?

    /// Invoked when the user taps Disconnect; the App tears down the store.
    var onDisconnect: () -> Void = {}

    var body: some View {
        if sizeClass == .compact {
            NavigationStack { stackSidebar }
        } else {
            NavigationSplitView {
                splitSidebar
            } detail: {
                detail
            }
        }
    }

    private var disconnectButton: some ToolbarContent {
        ToolbarItem(placement: .topBarLeading) {
            Button("Disconnect", systemImage: "xmark.circle", action: onDisconnect)
        }
    }

    // MARK: iPhone — navigation stack

    @ViewBuilder
    private var stackSidebar: some View {
        phaseContent { project in
            List {
                Section("Floors") {
                    ForEach(project.rooms) { room in
                        NavigationLink(value: room.id) {
                            Label(room.name, systemImage: room.symbol)
                        }
                    }
                }
            }
            .navigationTitle(project.name)
            .navigationDestination(for: Room.ID.self) { id in
                if let room = project.rooms.first(where: { $0.id == id }) {
                    FloorView(floor: room)
                }
            }
            .toolbar { disconnectButton }
        }
    }

    // MARK: iPad — split view

    @ViewBuilder
    private var splitSidebar: some View {
        phaseContent { project in
            List(selection: $selectedRoom) {
                Section("Floors") {
                    ForEach(project.rooms) { room in
                        Label(room.name, systemImage: room.symbol).tag(room.id)
                    }
                }
            }
            .navigationTitle(project.name)
            .toolbar { disconnectButton }
            // Pre-selecting a room only makes sense on iPad, where the detail
            // pane is always visible alongside the sidebar.
            .onAppear { selectedRoom = selectedRoom ?? project.rooms.first?.id }
        }
    }

    @ViewBuilder
    private var detail: some View {
        if let project = store.project,
           let room = project.rooms.first(where: { $0.id == selectedRoom }) {
            FloorView(floor: room)
        } else {
            ContentUnavailableView("Select a floor", systemImage: "house")
        }
    }

    // MARK: Shared phase handling

    @ViewBuilder
    private func phaseContent<Content: View>(@ViewBuilder _ content: (PHCProject) -> Content) -> some View {
        switch store.phase {
        case .connecting:
            ProgressView("Connecting…")
        case .failed(let message):
            ContentUnavailableView("Can't reach the PHC", systemImage: "wifi.exclamationmark", description: Text(message))
        case .ready:
            if let project = store.project {
                content(project)
            }
        }
    }
}

/// A responsive grid of device cards for one floor.
/// (The underlying model is still `Room`; a finer room grouping comes later.)
struct FloorView: View {
    @Environment(HomeStore.self) private var store
    let floor: Room

    /// Categories the user has collapsed (default: all expanded).
    @State private var collapsed: Set<String> = []

    private let columns = [GridItem(.adaptive(minimum: 280, maximum: 420), spacing: 16)]

    private var groups: [DeviceGroup] { DeviceGroup.grouped(store.devices(in: floor)) }

    var body: some View {
        ScrollView {
            LazyVStack(alignment: .leading, spacing: 20) {
                ForEach(groups) { group in
                    Section {
                        if !collapsed.contains(group.id) {
                            LazyVGrid(columns: columns, spacing: 16) {
                                ForEach(group.devices) { DeviceCard(device: $0) }
                            }
                        }
                    } header: {
                        header(for: group)
                    }
                }
            }
            .padding()
            .animation(.snappy, value: collapsed)
        }
        .navigationTitle(floor.name)
        .navigationBarTitleDisplayMode(.inline)
        .background(Color(.systemGroupedBackground))
        .toolbar {
            ToolbarItem(placement: .topBarTrailing) {
                Menu {
                    Button("Expand All", systemImage: "rectangle.expand.vertical") {
                        collapsed.removeAll()
                    }
                    Button("Collapse All", systemImage: "rectangle.compress.vertical") {
                        collapsed = Set(groups.map(\.id))
                    }
                } label: {
                    Image(systemName: "line.3.horizontal.decrease.circle")
                }
            }
        }
    }

    /// A tappable category header that collapses/expands its section.
    private func header(for group: DeviceGroup) -> some View {
        let isCollapsed = collapsed.contains(group.id)
        return Button {
            if isCollapsed { collapsed.remove(group.id) } else { collapsed.insert(group.id) }
        } label: {
            HStack(spacing: 8) {
                Label("\(group.title) (\(group.devices.count))", systemImage: group.symbol)
                    .font(.headline)
                Spacer()
                Image(systemName: "chevron.down")
                    .font(.subheadline.weight(.semibold))
                    .foregroundStyle(.secondary)
                    .rotationEffect(.degrees(isCollapsed ? -90 : 0))
            }
            .contentShape(.rect)
        }
        .buttonStyle(.plain)
        .padding(.top, 4)
    }
}

/// Devices in a room grouped into a display category (lights, shutters, …).
struct DeviceGroup: Identifiable {
    let id: String          // category title, also the stable identity
    let title: String
    let symbol: String
    let devices: [Device]

    /// Buckets pre-sorted devices into ordered category groups, preserving the
    /// incoming order (lights → shutters → outlets …, already sorted by name).
    static func grouped(_ devices: [Device]) -> [DeviceGroup] {
        var order: [String] = []
        var buckets: [String: [Device]] = [:]
        for device in devices {
            let title = categoryTitle(for: device.kind)
            if buckets[title] == nil { order.append(title) }
            buckets[title, default: []].append(device)
        }
        return order.map { DeviceGroup(id: $0, title: $0, symbol: symbol(for: $0), devices: buckets[$0]!) }
    }

    private static func categoryTitle(for kind: DeviceKind) -> String {
        switch kind {
        case .light, .dimmer: return "Lights"
        case .shutter:        return "Shutters"
        case .outlet:         return "Outlets"
        case .scene:          return "Scenes"
        }
    }

    private static func symbol(for title: String) -> String {
        switch title {
        case "Lights":   return "lightbulb.fill"
        case "Shutters": return "blinds.horizontal.closed"
        case "Outlets":  return "powerplug.fill"
        default:         return "play.circle"
        }
    }
}

#Preview {
    let store = HomeStore(client: MockPHCClient())
    return HomeView()
        .environment(store)
        .onAppear { store.start() }
}
