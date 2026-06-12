import SwiftUI

@main
struct PHCRemoteControlApp: App {
    @AppStorage("stm.host") private var savedHost: String = ""
    @State private var store: HomeStore?

    var body: some Scene {
        WindowGroup {
            if let store {
                HomeView()
                    .environment(store)
                    .onAppear { store.start() }
                    .toolbar {
                        ToolbarItem(placement: .topBarLeading) {
                            Button("Disconnect", systemImage: "xmark.circle") {
                                self.store = nil
                            }
                        }
                    }
            } else {
                ConnectionView { host in
                    if let host {
                        self.store = HomeStore(
                            client: STMv3Client(endpoint: .init(host: host))
                        )
                    } else {
                        self.store = HomeStore(client: MockPHCClient())
                    }
                }
            }
        }
    }
}
