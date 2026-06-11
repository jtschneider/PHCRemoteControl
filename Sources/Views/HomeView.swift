import SwiftUI

/// Top-level adaptive layout: rooms in the sidebar, a grid of device cards in
/// the detail pane. Uses `NavigationSplitView` so it feels right on iPad and
/// collapses to a navigation stack on iPhone.
struct HomeView: View {
    @Environment(HomeStore.self) private var store
    @State private var selectedRoom: Room.ID?

    var body: some View {
        NavigationSplitView {
            sidebar
        } detail: {
            detail
        }
    }

    @ViewBuilder
    private var sidebar: some View {
        Group {
            switch store.phase {
            case .connecting:
                ProgressView("Connecting…")
            case .failed(let message):
                ContentUnavailableView("Can't reach the PHC", systemImage: "wifi.exclamationmark", description: Text(message))
            case .ready:
                if let project = store.project {
                    List(selection: $selectedRoom) {
                        Section("Rooms") {
                            ForEach(project.rooms) { room in
                                Label(room.name, systemImage: room.symbol).tag(room.id)
                            }
                        }
                    }
                    .navigationTitle(project.name)
                    .onAppear { selectedRoom = selectedRoom ?? project.rooms.first?.id }
                }
            }
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
        .background(Color(.systemGroupedBackground))
    }
}

#Preview {
    let store = HomeStore(client: MockPHCClient())
    return HomeView()
        .environment(store)
        .onAppear { store.start() }
}
</content>
