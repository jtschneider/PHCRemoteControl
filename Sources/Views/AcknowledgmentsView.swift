import SwiftUI

/// Open-source attributions shown inside the app. The short list links to each
/// project; "Full license texts" renders the bundled THIRD_PARTY_NOTICES.md.
struct AcknowledgmentsView: View {
    @Environment(\.dismiss) private var dismiss

    private struct Credit: Identifiable {
        let id = UUID()
        let name: String
        let role: LocalizedStringKey
        let license: String
        let url: URL
    }

    private let credits: [Credit] = [
        Credit(name: "ZIPFoundation", role: "ZIP extraction", license: "MIT",
               url: URL(string: "https://github.com/weichsel/ZIPFoundation")!),
        Credit(name: "Mono Icons", role: "App icon artwork", license: "MIT",
               url: URL(string: "https://github.com/mono-company/mono-icons")!),
        Credit(name: "Material Design Icons", role: "App icon artwork", license: "Apache 2.0",
               url: URL(string: "https://github.com/Templarian/MaterialDesign")!),
    ]

    var body: some View {
        NavigationStack {
            List {
                Section("Open-source licenses") {
                    ForEach(credits) { credit in
                        Link(destination: credit.url) {
                            VStack(alignment: .leading, spacing: 2) {
                                HStack {
                                    Text(credit.name).font(.headline)
                                    Spacer()
                                    Text(credit.license)
                                        .font(.caption).foregroundStyle(.secondary)
                                }
                                Text(credit.role).font(.caption).foregroundStyle(.secondary)
                            }
                        }
                    }
                }
                Section {
                    NavigationLink("Full license texts") { NoticesTextView() }
                }
            }
            .navigationTitle("Acknowledgments")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button("Done") { dismiss() }
                }
            }
        }
    }
}

/// Renders the bundled THIRD_PARTY_NOTICES.md verbatim.
private struct NoticesTextView: View {
    var body: some View {
        ScrollView {
            Text(noticesText)
                .font(.system(.footnote, design: .monospaced))
                .textSelection(.enabled)
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding()
        }
        .navigationTitle("Full license texts")
        .navigationBarTitleDisplayMode(.inline)
    }

    private var noticesText: String {
        guard let url = Bundle.main.url(forResource: "THIRD_PARTY_NOTICES", withExtension: "md"),
              let text = try? String(contentsOf: url, encoding: .utf8)
        else { return "See THIRD_PARTY_NOTICES.md in the project repository." }
        return text
    }
}

#Preview {
    AcknowledgmentsView()
}
