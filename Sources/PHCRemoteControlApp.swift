import SwiftUI

@main
struct PHCRemoteControlApp: App {
    @AppStorage("stm.host") private var savedHost: String = ""
    @State private var store: HomeStore?

    var body: some Scene {
        WindowGroup {
            if let store {
                HomeView(onDisconnect: { self.store = nil })
                    .environment(store)
                    .onAppear { store.start() }
            } else {
                ConnectionView { host in
                    if let host {
                        self.store = HomeStore(
                            client: STMv3Client(endpoint: .init(host: host)),
                            cacheKey: host
                        )
                    } else {
                        self.store = HomeStore(client: MockPHCClient())
                    }
                }
            }
        }
    }
}
