import Foundation

/// Parses a `project.ppfx` XML document (from the STM's `readFile` response)
/// into a `PHCProject` with rooms and devices.
///
/// Channel names follow the pattern `"N.ROOM : TYPE > LABEL"`:
///   - N      = sort index (0-9)
///   - ROOM   = room/floor name (KG, Einlieger, EG, DG, Außen, …)
///   - TYPE   = device type (Licht, Steckdose, Pumpe, Rollo, …)
///   - LABEL  = human-readable device name
///
/// AMD `Ausgang` channels with `visu="true"` become lights/outlets.
/// EMD `Eingang` channels with `visu="true"` and type `Rollo` become shutters,
/// paired by stripping the `heben`/`senken` suffix.
enum PHCProjectParser {

    struct ParseError: Error { let message: String }

    static func parse(ppfxData: Data) throws -> PHCProject {
        let parser = Parser()
        let xml = XMLParser(data: ppfxData)
        xml.delegate = parser
        xml.parse()
        if let err = parser.error { throw err }
        return parser.buildProject()
    }

    static func parse(ppfxString: String) throws -> PHCProject {
        guard let data = ppfxString.data(using: .utf8) else {
            throw ParseError(message: "Could not encode ppfx string as UTF-8")
        }
        return try parse(ppfxData: data)
    }
}

// MARK: - Internal XML delegate

private final class Parser: NSObject, XMLParserDelegate {

    // --- raw collected data ---

    struct RawChannel {
        let moduleGroup: String   // "Eingangsmodule" or "Ausgangsmodule"
        let moduleName: String    // "AMD230_4", "JRM", "EMD_RUE", …
        let moduleAdr: Int
        let channelGroup: String  // "Eingang" or "Ausgang"
        let channelAdr: Int
        let text: String
    }

    var visuChannels: [RawChannel] = []
    var error: Error?

    // --- parser state ---

    private var moduleGroupStack: [String] = []
    private var currentModuleGroup = ""
    private var currentModuleName = ""
    private var currentModuleAdr = -1
    private var currentChannelGroup = ""
    private var currentChannelAdr = -1
    private var currentVisu = false
    private var collectingText = false
    private var textBuffer = ""

    // MARK: XMLParserDelegate

    func parser(_ parser: XMLParser,
                didStartElement element: String,
                namespaceURI: String?,
                qualifiedName _: String?,
                attributes attrs: [String: String]) {
        switch element {
        case "MODS":
            currentModuleGroup = attrs["grp"] ?? ""
        case "MOD":
            currentModuleAdr = Int(attrs["adr"] ?? "") ?? -1
            currentModuleName = attrs["name"] ?? ""
        case "CHAS":
            currentChannelGroup = attrs["grp"] ?? ""
        case "CHA":
            currentChannelAdr = Int(attrs["adr"] ?? "") ?? -1
            currentVisu = attrs["visu"] == "true"
            if currentVisu {
                collectingText = true
                textBuffer = ""
            }
        default: break
        }
    }

    func parser(_ parser: XMLParser, foundCharacters string: String) {
        if collectingText { textBuffer += string }
    }

    func parser(_ parser: XMLParser,
                didEndElement element: String,
                namespaceURI: String?,
                qualifiedName _: String?) {
        if element == "CHA" && collectingText {
            let text = textBuffer.trimmingCharacters(in: .whitespaces)
            if !text.isEmpty {
                visuChannels.append(RawChannel(
                    moduleGroup: currentModuleGroup,
                    moduleName: currentModuleName,
                    moduleAdr: currentModuleAdr,
                    channelGroup: currentChannelGroup,
                    channelAdr: currentChannelAdr,
                    text: text
                ))
            }
            collectingText = false
            textBuffer = ""
        }
    }

    func parser(_ parser: XMLParser, parseErrorOccurred err: Error) {
        error = err
    }

    // MARK: Project building

    func buildProject() -> PHCProject {
        var roomMap: [String: (sortIndex: Int, devices: [Device])] = [:]

        // 1. AMD output channels → lights / outlets
        for ch in visuChannels
        where ch.moduleGroup == "Ausgangsmodule"
            && ch.channelGroup == "Ausgang"
            && ch.moduleName.hasPrefix("AMD")
        {
            guard let (sortIdx, room, kind, label) = parseChannelName(ch.text),
                  kind != .shutter else { continue }

            let ref = ChannelRef(moduleClass: .amd, dip: ch.moduleAdr, channel: ch.channelAdr)
            let device = Device(name: label, kind: kind, ref: ref)
            roomMap[room, default: (sortIdx, [])].devices.append(device)
            roomMap[room]?.sortIndex = sortIdx
        }

        // 2. EMD input channels with Rollo → shutters (pair heben/senken)
        var rolloDown: [String: (Int, Int)] = [:]   // key → (emdAdr, channelAdr)
        var rolloUp:   [String: (Int, Int)] = [:]

        for ch in visuChannels
        where ch.moduleGroup == "Eingangsmodule"
            && ch.channelGroup == "Eingang"
        {
            guard let (_, _, kind, _) = parseChannelName(ch.text), kind == .shutter else { continue }
            let key = rolloKey(from: ch.text)
            if ch.text.lowercased().contains("senken") || ch.text.lowercased().contains("lower") {
                rolloDown[key] = (ch.moduleAdr, ch.channelAdr)
            } else if ch.text.lowercased().contains("heben") || ch.text.lowercased().contains("raise") {
                rolloUp[key] = (ch.moduleAdr, ch.channelAdr)
            }
        }

        for (key, downInfo) in rolloDown {
            guard let (sortIdx, room, _, label) = parseRolloName(key) else { continue }
            let downRef = ChannelRef(moduleClass: .emd, dip: downInfo.0, channel: downInfo.1)
            var upRef: ChannelRef?
            if let up = rolloUp[key] {
                upRef = ChannelRef(moduleClass: .emd, dip: up.0, channel: up.1)
            }
            let device = Device(name: label, kind: .shutter, ref: downRef, shutterUpRef: upRef)
            roomMap[room, default: (sortIdx, [])].devices.append(device)
            roomMap[room]?.sortIndex = sortIdx
        }

        // 3. Assemble rooms sorted by sort index then name
        let sortedRooms = roomMap
            .sorted { lhs, rhs in
                if lhs.value.sortIndex != rhs.value.sortIndex {
                    return lhs.value.sortIndex < rhs.value.sortIndex
                }
                return lhs.key < rhs.key
            }

        var rooms: [Room] = []
        var devices: [UUID: Device] = [:]

        for (roomName, entry) in sortedRooms {
            // Within a floor, order devices by name (natural/numeric) rather than by
            // wiring order — devices on adjacent module channels aren't necessarily
            // physically close. Floor order itself stays by sort index above.
            let roomDevices = entry.devices.sorted {
                $0.name.localizedStandardCompare($1.name) == .orderedAscending
            }
            let room = Room(
                name: roomName,
                symbol: roomSymbol(for: roomName),
                deviceIDs: roomDevices.map(\.id)
            )
            rooms.append(room)
            for d in roomDevices { devices[d.id] = d }
        }

        return PHCProject(name: "PHC", rooms: rooms, devices: devices)
    }

    // MARK: - Helpers

    /// Parses `"N.ROOM : TYPE > LABEL"` → (sortIndex, room, kind, label).
    private func parseChannelName(_ text: String) -> (Int, String, DeviceKind, String)? {
        // Split on " : " then " > "
        let colonSplit = text.components(separatedBy: " : ")
        guard colonSplit.count == 2 else { return nil }
        let roomPart = colonSplit[0]   // "2.EG"
        let rest = colonSplit[1]       // "Licht > DL Flur"

        let arrowSplit = rest.components(separatedBy: " > ")
        guard arrowSplit.count == 2 else { return nil }
        let typeStr = arrowSplit[0]    // "Licht"
        let label = arrowSplit[1]      // "DL Flur"

        // Extract sort index and room name from "N.ROOM"
        let dotSplit = roomPart.components(separatedBy: ".")
        let sortIdx = Int(dotSplit.first ?? "") ?? 99
        let room = dotSplit.dropFirst().joined(separator: ".")

        let kind = deviceKind(from: typeStr)
        return (sortIdx, room, kind, label)
    }

    /// Returns the key used to pair heben/senken: strips direction suffix.
    private func rolloKey(from text: String) -> String {
        let colonSplit = text.components(separatedBy: " : ")
        guard colonSplit.count == 2 else { return text }
        let rest = colonSplit[1]   // "Rollo > Bad heben"
        let arrowSplit = rest.components(separatedBy: " > ")
        guard arrowSplit.count == 2 else { return text }
        var label = arrowSplit[1]
        for suffix in [" heben", " senken", " heben ", " senken "] {
            if label.hasSuffix(suffix.trimmingCharacters(in: .whitespaces)) {
                label = String(label.dropLast(suffix.trimmingCharacters(in: .whitespaces).count))
            }
        }
        return colonSplit[0] + " : Rollo > " + label.trimmingCharacters(in: .whitespaces)
    }

    /// Parses a rollo key like `"2.EG : Rollo > Bad"` into (sortIdx, room, kind, label).
    private func parseRolloName(_ key: String) -> (Int, String, DeviceKind, String)? {
        return parseChannelName(key)
    }

    private func deviceKind(from typeStr: String) -> DeviceKind {
        switch typeStr {
        case "Licht":      return .light
        case "Steckdose":  return .outlet
        case "Pumpe":      return .outlet
        case "Rollo":      return .shutter
        default:           return .light
        }
    }

    private func roomSymbol(for room: String) -> String {
        switch room {
        case "KG":          return "arrow.down.to.line"
        case "Einlieger":   return "house.and.flag"
        case "EG":          return "house"
        case "DG":          return "stairs"
        case "Außen":       return "sun.max"
        default:            return "square.split.bottomrightquarter"
        }
    }
}
