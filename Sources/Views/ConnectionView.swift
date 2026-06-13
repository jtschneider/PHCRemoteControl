import SwiftUI

/// Shown on first launch (or when no STM is configured).
/// Lets the user enter the STM's IP address or run in demo mode.
struct ConnectionView: View {
    @AppStorage("stm.host") private var host: String = ""
    let onConnect: (String?) -> Void   // nil = use demo/mock

    @State private var editing: String = ""
    @FocusState private var focused: Bool
    @State private var showAcknowledgments = false

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    Image("Logo")
                        .resizable()
                        .scaledToFit()
                        .frame(width: 104, height: 104)
                        .clipShape(RoundedRectangle(cornerRadius: 23, style: .continuous))
                        .shadow(color: .black.opacity(0.15), radius: 5, y: 2)
                        .frame(maxWidth: .infinity)
                        .padding(.vertical, 8)
                }
                .listRowBackground(Color.clear)

                Section {
                    HStack {
                        Image(systemName: "network")
                            .foregroundStyle(.secondary)
                        TextField("192.168.x.x", text: $editing)
                            .keyboardType(.numbersAndPunctuation)
                            .autocorrectionDisabled()
                            .focused($focused)
                    }
                } header: {
                    Text("STM IP Address")
                } footer: {
                    Text("Enter the IP address of your PHC control unit (STM). Port 6680 is used automatically.")
                }

                Section {
                    Button {
                        let trimmed = editing.trimmingCharacters(in: .whitespaces)
                        guard !trimmed.isEmpty else { return }
                        host = trimmed
                        onConnect(trimmed)
                    } label: {
                        Label("Connect to STM", systemImage: "bolt.fill")
                            .frame(maxWidth: .infinity)
                    }
                    .buttonStyle(.borderedProminent)
                    .disabled(editing.trimmingCharacters(in: .whitespaces).isEmpty)

                    Button {
                        onConnect(nil)
                    } label: {
                        Label("Demo Mode (no hardware)", systemImage: "wand.and.stars")
                            .frame(maxWidth: .infinity)
                    }
                    .buttonStyle(.bordered)
                }
                .listRowBackground(Color.clear)

                Section {
                    Button("Acknowledgments") { showAcknowledgments = true }
                        .font(.footnote)
                        .frame(maxWidth: .infinity)
                }
                .listRowBackground(Color.clear)
            }
            .navigationTitle("PHC Remote")
            .onAppear {
                editing = host
                focused = host.isEmpty
            }
            .sheet(isPresented: $showAcknowledgments) {
                AcknowledgmentsView()
            }
        }
    }
}

#Preview {
    ConnectionView { _ in }
}
