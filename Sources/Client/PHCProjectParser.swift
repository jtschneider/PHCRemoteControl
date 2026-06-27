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
    var projectName = "PHC"
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
        case "PROJECT":
            if let name = attrs["name"], !name.isEmpty { projectName = name }
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

        // The device's UI category is the verbatim TYPE from its channel name
        // (e.g. "Licht", "Steckdose", "Rollläden") — kept as project data, untranslated.

        // 1. AMD output channels → lights / outlets
        for ch in visuChannels
        where ch.moduleGroup == "Ausgangsmodule"
            && ch.channelGroup == "Ausgang"
            && ch.moduleName.hasPrefix("AMD")
        {
            guard let p = parseChannelParts(ch.text) else { continue }
            let kind = deviceKind(from: p.type)
            guard kind != .shutter else { continue }

            let ref = ChannelRef(moduleClass: .amd, dip: ch.moduleAdr, channel: ch.channelAdr)
            let device = Device(name: p.label, kind: kind, ref: ref, category: p.type)
            roomMap[p.room, default: (p.sortIdx, [])].devices.append(device)
            roomMap[p.room]?.sortIndex = p.sortIdx
        }

        // 2. EMD input channels with Rollo → shutters (pair heben/senken)
        var rolloDown: [String: (Int, Int)] = [:]   // key → (emdAdr, channelAdr)
        var rolloUp:   [String: (Int, Int)] = [:]
        var rolloType: [String: String] = [:]       // key → verbatim TYPE ("Rollo"/"Rollläden")

        for ch in visuChannels
        where ch.moduleGroup == "Eingangsmodule"
            && ch.channelGroup == "Eingang"
        {
            guard let p = parseChannelParts(ch.text), deviceKind(from: p.type) == .shutter else { continue }
            let key = rolloKey(from: ch.text)
            rolloType[key] = p.type
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
            let device = Device(name: label, kind: .shutter, ref: downRef,
                                shutterUpRef: upRef, category: rolloType[key] ?? "Rollo")
            roomMap[room, default: (sortIdx, [])].devices.append(device)
            roomMap[room]?.sortIndex = sortIdx
        }

        // 2b. EMD_VIR virtual inputs → central/group scene buttons (e.g. the
        //     "5.Zentral" commands: all lights off, close all EG shutters, …).
        //     Each is a momentary trigger fired via simInputEvent on its EMD channel.
        //     Category = TYPE ("Rollläden", "Licht", …); the label alone is the name.
        for ch in visuChannels
        where ch.moduleName == "EMD_VIR" && ch.channelGroup == "Eingang"
        {
            guard let (sortIdx, room, type, label) = parseChannelParts(ch.text) else { continue }
            let ref = ChannelRef(moduleClass: .emd, dip: ch.moduleAdr, channel: ch.channelAdr)
            let device = Device(name: label, kind: .scene, ref: ref, category: type)
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
            // Within a floor, order by kind (lights → shutters → outlets → scenes),
            // then by category name, then by device name (natural/numeric). This makes
            // the project's own categories fall out as clean, ordered sections. Wiring
            // order is ignored; floor order itself stays by sort index above.
            let roomDevices = entry.devices.sorted {
                let r0 = kindSortRank($0.kind), r1 = kindSortRank($1.kind)
                if r0 != r1 { return r0 < r1 }
                if $0.category != $1.category {
                    return $0.category.localizedStandardCompare($1.category) == .orderedAscending
                }
                return $0.name.localizedStandardCompare($1.name) == .orderedAscending
            }
            let room = Room(
                name: roomName,
                symbol: roomSymbol(for: roomName),
                deviceIDs: roomDevices.map(\.id)
            )
            rooms.append(room)
            for d in roomDevices { devices[d.id] = d }
        }

        return PHCProject(name: projectName, rooms: rooms, devices: devices)
    }

    // MARK: - Helpers

    /// Splits `"N.ROOM : TYPE > LABEL"` into its parts.
    /// e.g. `"2.EG : Licht > DL Flur"` → (2, "EG", "Licht", "DL Flur").
    private func parseChannelParts(_ text: String) -> (sortIdx: Int, room: String, type: String, label: String)? {
        let colonSplit = text.components(separatedBy: " : ")
        guard colonSplit.count == 2 else { return nil }
        let arrowSplit = colonSplit[1].components(separatedBy: " > ")
        guard arrowSplit.count == 2 else { return nil }
        let dotSplit = colonSplit[0].components(separatedBy: ".")
        let sortIdx = Int(dotSplit.first ?? "") ?? 99
        let room = dotSplit.dropFirst().joined(separator: ".")
        return (sortIdx, room, arrowSplit[0], arrowSplit[1])
    }

    /// Parses `"N.ROOM : TYPE > LABEL"` → (sortIndex, room, kind, label).
    private func parseChannelName(_ text: String) -> (Int, String, DeviceKind, String)? {
        guard let p = parseChannelParts(text) else { return nil }
        return (p.sortIdx, p.room, deviceKind(from: p.type), p.label)
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

    /// Display order of device categories within a floor: lights, then shutters,
    /// then outlets, then everything else.
    private func kindSortRank(_ kind: DeviceKind) -> Int {
        switch kind {
        case .light:   return 0
        case .dimmer:  return 1
        case .shutter: return 2
        case .outlet:  return 3
        case .scene:   return 4
        }
    }

    private func roomSymbol(for room: String) -> String {
        switch room {
        case "KG":          return "arrow.down.to.line"
        case "Einlieger":   return "house.and.flag"
        case "EG":          return "house"
        case "DG":          return "stairs"
        case "Außen":       return "sun.max"
        case "Zentral":     return "square.grid.2x2"
        default:            return "square.split.bottomrightquarter"
        }
    }
}
