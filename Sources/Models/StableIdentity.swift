import Foundation

/// Language-neutral identities shared with the Go bridge fixture contract.
/// Runtime UUIDs remain unchanged so existing project caches need no migration.
enum PHCStableIdentity {
    static func device(ref: ChannelRef) -> String {
        "device:v1:\(ref.moduleClass.rawValue):\(ref.dip):\(ref.channel)"
    }

    static func floor(name: String) -> String {
        let encoded = Data(name.utf8).base64EncodedString()
            .replacingOccurrences(of: "+", with: "-")
            .replacingOccurrences(of: "/", with: "_")
            .replacingOccurrences(of: "=", with: "")
        return "floor:v1:\(encoded)"
    }
}

extension Device {
    /// Stable across project reloads for parsed hardware-backed devices.
    var stableID: String? {
        ref.map(PHCStableIdentity.device)
    }
}

extension Room {
    var stableID: String {
        PHCStableIdentity.floor(name: name)
    }
}
