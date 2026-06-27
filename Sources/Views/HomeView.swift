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

    /// Force a fresh project download from the STM (the structure is otherwise
    /// served from the local cache for instant startup).
    private var reloadButton: some ToolbarContent {
        ToolbarItem(placement: .topBarTrailing) {
            Button("Reload from STM", systemImage: "arrow.clockwise") {
                store.reloadProject()
            }
        }
    }

    // MARK: iPhone — navigation stack

    @ViewBuilder
    private var stackSidebar: some View {
        phaseContent { project in
            List {
                Section {
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
            .toolbar { disconnectButton; reloadButton }
        }
    }

    // MARK: iPad — split view

    @ViewBuilder
    private var splitSidebar: some View {
        phaseContent { project in
            List(selection: $selectedRoom) {
                Section {
                    ForEach(project.rooms) { room in
                        Label(room.name, systemImage: room.symbol).tag(room.id)
                    }
                }
            }
            .navigationTitle(project.name)
            .toolbar { disconnectButton; reloadButton }
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
                Label {
                    Text(group.title) + Text(verbatim: " (\(group.devices.count))")
                } icon: {
                    Image(systemName: group.symbol)
                }
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

/// Devices in a room grouped into a display category.
struct DeviceGroup: Identifiable {
    /// The section heading. For real devices this is the verbatim project category
    /// (e.g. "Licht", "Rollläden"); for mock devices it falls back to a kind name.
    let id: String
    let symbol: String
    let devices: [Device]

    /// Heading text. Project categories render verbatim (they aren't catalog keys);
    /// the mock's fallback names ("Lights", …) get localized via the String Catalog.
    var title: LocalizedStringKey { LocalizedStringKey(id) }

    /// Buckets the pre-sorted devices by their project category, preserving order.
    static func grouped(_ devices: [Device]) -> [DeviceGroup] {
        var order: [String] = []
        var buckets: [String: [Device]] = [:]
        for device in devices {
            let category = device.category.isEmpty ? fallbackCategory(device.kind) : device.category
            if buckets[category] == nil { order.append(category) }
            buckets[category, default: []].append(device)
        }
        return order.map { cat in
            let devs = buckets[cat]!
            return DeviceGroup(id: cat, symbol: symbol(for: cat, kind: devs[0].kind), devices: devs)
        }
    }

    /// Used only for mock/sample devices, which carry no project category.
    private static func fallbackCategory(_ kind: DeviceKind) -> String {
        switch kind {
        case .light, .dimmer: return "Lights"
        case .shutter:        return "Shutters"
        case .outlet:         return "Outlets"
        case .scene:          return "Scenes"
        }
    }

    /// Icon for a section: keyword-match the (German) category, else fall back to kind.
    private static func symbol(for category: String, kind: DeviceKind) -> String {
        let c = category.lowercased()
        if c.contains("licht") || c.contains("light") || c.contains("lampe") { return "lightbulb.fill" }
        if c.contains("roll")  || c.contains("shutter") || c.contains("jalousie") { return "blinds.horizontal.closed" }
        if c.contains("steckdose") || c.contains("outlet") || c.contains("pumpe") { return "powerplug.fill" }
        if c.contains("lüftung") || c.contains("fenster") { return "wind" }
        switch kind {
        case .light, .dimmer: return "lightbulb.fill"
        case .shutter:        return "blinds.horizontal.closed"
        case .outlet:         return "powerplug.fill"
        case .scene:          return "play.circle"
        }
    }
}

#Preview {
    let store = HomeStore(client: MockPHCClient())
    return HomeView()
        .environment(store)
        .onAppear { store.start() }
}
