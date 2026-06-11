import SwiftUI

@main
struct PHCRemoteControlApp: App {
    // Today the store runs on the mock client. To use a real STM v3 once the
    // transport is implemented, construct it with:
    //   HomeStore(client: STMv3Client(endpoint: .init(host: "192.168.1.x")))
    @State private var store = HomeStore(client: MockPHCClient())

    var body: some Scene {
        WindowGroup {
            HomeView()
                .environment(store)
                .onAppear { store.start() }
        }
    }
}
</content>
