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
                Section("Rooms") {
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
                    RoomView(room: room)
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
                Section("Rooms") {
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
            RoomView(room: room)
        } else {
            ContentUnavailableView("Select a room", systemImage: "house")
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

/// A responsive grid of device cards for one room.
struct RoomView: View {
    @Environment(HomeStore.self) private var store
    let room: Room

    private let columns = [GridItem(.adaptive(minimum: 280, maximum: 420), spacing: 16)]

    var body: some View {
        ScrollView {
            LazyVGrid(columns: columns, spacing: 16) {
                ForEach(store.devices(in: room)) { device in
                    DeviceCard(device: device)
                }
            }
            .padding()
        }
        .navigationTitle(room.name)
        .navigationBarTitleDisplayMode(.inline)
        .background(Color(.systemGroupedBackground))
    }
}

#Preview {
    let store = HomeStore(client: MockPHCClient())
    return HomeView()
        .environment(store)
        .onAppear { store.start() }
}
