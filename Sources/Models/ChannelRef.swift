import Foundation

/// The PHC module class (the 3 high bits of a bus address byte).
/// Kept in the model so the eventual real transport can address the bus
/// without the UI needing to know about it.
enum ModuleClass: String, Codable, Sendable {
    case emd   // input / LED output
    case amd   // relay output (lights, outlets)
    case jrm   // shutter / blind
}

/// Address of a single controllable point on the PHC bus:
/// a module (identified by its DIP-switch address) and a channel on it.
struct ChannelRef: Hashable, Codable, Sendable {
    var moduleClass: ModuleClass
    var dip: Int          // DIP-switch address of the module (0...31)
    var channel: Int      // channel index on the module

    var description: String { "\(moduleClass.rawValue.uppercased()) #\(dip).\(channel)" }
}
</content>
