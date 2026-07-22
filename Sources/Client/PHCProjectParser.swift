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
/// EMD `Eingang` channels with `visu="true"` and a motor-like type (`Rollo`,
/// `Lüftung Fenster`, …) become shutter-style controls, paired by stripping the
/// directional suffix (`heben`/`senken`, `Auf`/`Zu`, …).
enum PHCProjectParser {

    struct ParseError: LocalizedError {
        let message: String

        var errorDescription: String? { message }
    }

    fileprivate enum Limits {
        static let maxProjectXMLBytes = 8 * 1024 * 1024
        static let maxVisuChannels = 4096
        static let maxChannelTextLength = 512
        static let maxToolActions = 256
        static let maxToolCandidates = 4096
        static let maxLocationDepth = 64
    }

    static func parse(ppfxData: Data, tpfxData: Data? = nil) throws -> PHCProject {
        try validateXMLSize(ppfxData, fileName: "project.ppfx")

        let parser = Parser()
        let xml = XMLParser(data: ppfxData)
        xml.delegate = parser
        guard xml.parse() else {
            throw parser.error ?? ParseError(message: PHCLocalization.string("Could not parse %@", "project.ppfx"))
        }
        if let tpfxData {
            try validateXMLSize(tpfxData, fileName: "project.tpfx")
            let toolParser = ToolActionParser()
            let toolXML = XMLParser(data: tpfxData)
            toolXML.delegate = toolParser
            guard toolXML.parse() else {
                throw toolParser.error ?? ParseError(message: PHCLocalization.string("Could not parse %@", "project.tpfx"))
            }
            parser.toolActions = toolParser.actions
        }
        return parser.buildProject()
    }

    static func parse(ppfxString: String) throws -> PHCProject {
        guard let data = ppfxString.data(using: .utf8) else {
            throw ParseError(message: PHCLocalization.string("Could not encode project XML as UTF-8"))
        }
        return try parse(ppfxData: data)
    }

    private static func validateXMLSize(_ data: Data, fileName: String) throws {
        guard !data.isEmpty else {
            throw ParseError(message: PHCLocalization.string("%@ is empty", fileName))
        }
        guard data.count <= Limits.maxProjectXMLBytes else {
            throw ParseError(message: PHCLocalization.string("%@ exceeds %d MB", fileName, Limits.maxProjectXMLBytes / 1024 / 1024))
        }
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
    var toolActions: [ToolActionParser.Action] = []
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
        guard collectingText else { return }
        guard textBuffer.count + string.count <= PHCProjectParser.Limits.maxChannelTextLength else {
            fail(PHCLocalization.string("Project channel label exceeds %d characters", PHCProjectParser.Limits.maxChannelTextLength), parser)
            return
        }
        textBuffer += string
    }

    func parser(_ parser: XMLParser,
                didEndElement element: String,
                namespaceURI: String?,
                qualifiedName _: String?) {
        if element == "CHA" && collectingText {
            let text = textBuffer.trimmingCharacters(in: .whitespaces)
            if !text.isEmpty {
                guard visuChannels.count < PHCProjectParser.Limits.maxVisuChannels else {
                    fail(PHCLocalization.string("Project contains more than %d visible channels", PHCProjectParser.Limits.maxVisuChannels), parser)
                    return
                }
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
        if error == nil { error = err }
    }

    private func fail(_ message: String, _ parser: XMLParser) {
        error = PHCProjectParser.ParseError(message: message)
        parser.abortParsing()
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

        // 2. EMD input channels with motor-like categories → shutter controls
        //    (pair heben/senken, Auf/Zu, open/close, …).
        var motorDown: [String: (Int, Int)] = [:]   // key → (emdAdr, channelAdr)
        var motorUp:   [String: (Int, Int)] = [:]
        var motorType: [String: String] = [:]       // key → verbatim TYPE ("Rollo", "Lüftung Fenster", …)
        var representedInputRefs = Set<ChannelRef>()

        for ch in visuChannels
        where ch.moduleGroup == "Eingangsmodule"
            && ch.channelGroup == "Eingang"
        {
            guard let p = parseChannelParts(ch.text), deviceKind(from: p.type) == .shutter else { continue }
            let key = motorKey(from: ch.text)
            motorType[key] = p.type
            switch motorDirection(from: ch.text) {
            case .down:
                motorDown[key] = (ch.moduleAdr, ch.channelAdr)
            case .up:
                motorUp[key] = (ch.moduleAdr, ch.channelAdr)
            case nil:
                break
            }
        }

        for (key, downInfo) in motorDown {
            guard let (sortIdx, room, _, label) = parseMotorName(key) else { continue }
            guard let up = motorUp[key] else { continue }
            let downRef = ChannelRef(moduleClass: .emd, dip: downInfo.0, channel: downInfo.1)
            let upRef = ChannelRef(moduleClass: .emd, dip: up.0, channel: up.1)
            let device = Device(name: label, kind: .shutter, ref: downRef,
                                shutterUpRef: upRef, category: motorType[key] ?? "Rollo")
            roomMap[room, default: (sortIdx, [])].devices.append(device)
            roomMap[room]?.sortIndex = sortIdx
            representedInputRefs.insert(downRef)
            representedInputRefs.insert(upRef)
        }

        // 2b. EMD_VIR virtual inputs → central/group scene buttons (e.g. the
        //     "5.Zentral" commands: all lights off, close all EG shutters, …).
        //     Each is a momentary trigger fired via simInputEvent on its EMD channel.
        //     Category = TYPE ("Rollläden", "Licht", …); the label alone is the name.
        for ch in visuChannels
        where ch.moduleName == "EMD_VIR" && ch.channelGroup == "Eingang"
        {
            guard let (sortIdx, room, type, label) = parseChannelParts(ch.text) else { continue }
            guard deviceKind(from: type) != .shutter else { continue }
            let ref = ChannelRef(moduleClass: .emd, dip: ch.moduleAdr, channel: ch.channelAdr)
            let device = Device(name: label, kind: .scene, ref: ref, category: type)
            roomMap[room, default: (sortIdx, [])].devices.append(device)
            roomMap[room]?.sortIndex = sortIdx
            representedInputRefs.insert(ref)
        }

        // 2c. Total input fallback. Every remaining visible EMD channel gets its
        //     own card with the project label left untouched. A short activation
        //     and a long press cover both event paths without guessing the wiring.
        for ch in visuChannels
        where ch.moduleGroup == "Eingangsmodule"
            && ch.channelGroup == "Eingang"
        {
            guard let p = parseChannelParts(ch.text) else { continue }
            let ref = ChannelRef(moduleClass: .emd, dip: ch.moduleAdr, channel: ch.channelAdr)
            guard !representedInputRefs.contains(ref) else { continue }
            let device = Device(name: p.label, kind: .button, ref: ref, category: p.type)
            roomMap[p.room, default: (p.sortIdx, [])].devices.append(device)
            roomMap[p.room]?.sortIndex = p.sortIdx
            representedInputRefs.insert(ref)
        }

        // 2d. Selected automation tools from project.tpfx (e.g. panic buttons or
        //     presence simulation) can be controllable even when they are not
        //     exported as EMD_VIR visu channels in project.ppfx. Surface them as
        //     momentary scene/action buttons by simulating their activation input.
        let existingSceneRefs = Set(roomMap.values.flatMap { entry in
            entry.devices.compactMap { device -> ChannelRef? in
                (device.kind == .scene || device.kind == .button) ? device.ref : nil
            }
        })
        for action in toolActions where !existingSceneRefs.contains(action.ref) {
            let device = Device(name: action.name, kind: .scene, ref: action.ref, category: action.category)
            roomMap[action.room, default: (action.sortIndex, [])].devices.append(device)
            roomMap[action.room]?.sortIndex = action.sortIndex
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

    /// Splits `"N.ROOM : TYPE > LABEL"` into its parts. The category is always
    /// the project text between the first `:` and the following `>`, trimmed but
    /// otherwise kept verbatim for the UI.
    /// e.g. `"2.EG : Licht > DL Flur"` → (2, "EG", "Licht", "DL Flur").
    private func parseChannelParts(_ text: String) -> (sortIdx: Int, room: String, type: String, label: String)? {
        guard let colon = text.firstIndex(of: ":"),
              let arrow = text[colon...].firstIndex(of: ">")
        else { return nil }

        let prefix = text[..<colon].trimmingCharacters(in: .whitespaces)
        let type = text[text.index(after: colon)..<arrow].trimmingCharacters(in: .whitespaces)
        let label = text[text.index(after: arrow)...].trimmingCharacters(in: .whitespaces)
        guard !type.isEmpty, !label.isEmpty else { return nil }

        let dotSplit = prefix.components(separatedBy: ".")
        let sortIdx = Int(dotSplit.first ?? "") ?? 99
        let room = dotSplit.dropFirst().joined(separator: ".").trimmingCharacters(in: .whitespaces)
        guard !room.isEmpty else { return nil }
        return (sortIdx, room, type, label)
    }

    /// Parses `"N.ROOM : TYPE > LABEL"` → (sortIndex, room, kind, label).
    private func parseChannelName(_ text: String) -> (Int, String, DeviceKind, String)? {
        guard let p = parseChannelParts(text) else { return nil }
        return (p.sortIdx, p.room, deviceKind(from: p.type), p.label)
    }

    /// Returns the key used to pair directional motor channels: strips direction suffix.
    private func motorKey(from text: String) -> String {
        guard let p = parseChannelParts(text) else { return text }
        let label = stripMotorDirectionSuffix(from: p.label)
        return "\(p.sortIdx).\(p.room) : \(p.type) > \(label)"
    }

    /// Parses a motor key like `"2.EG : Rollo > Bad"` into (sortIdx, room, kind, label).
    private func parseMotorName(_ key: String) -> (Int, String, DeviceKind, String)? {
        return parseChannelName(key)
    }

    /// Maps a channel TYPE (Rollo, Rollladen, Jalousie, Licht, Steckdose, …) to a
    /// control kind via German/English keywords (see `PHCKeywords`). Defaults to
    /// light for anything unrecognised.
    private func deviceKind(from typeStr: String) -> DeviceKind {
        if PHCKeywords.matches(PHCKeywords.shutter, typeStr)
            || PHCKeywords.matches(PHCKeywords.motorizedWindow, typeStr) { return .shutter }
        if PHCKeywords.matches(PHCKeywords.outlet, typeStr)  { return .outlet }
        return .light
    }

    private enum MotorDirection { case up, down }

    private func motorDirection(from text: String) -> MotorDirection? {
        guard let label = parseChannelParts(text)?.label else { return nil }
        let words = normalizedWords(label)
        guard let last = words.last else { return nil }
        if ["senken", "lower", "down", "zu", "close", "closing", "schliessen", "schließen"].contains(last) {
            return .down
        }
        if ["heben", "raise", "up", "auf", "open", "opening", "offnen", "oeffnen", "öffnen"].contains(last) {
            return .up
        }
        return nil
    }

    private func stripMotorDirectionSuffix(from label: String) -> String {
        var parts = label.split(separator: " ").map(String.init)
        guard let last = parts.last else { return label.trimmingCharacters(in: .whitespaces) }
        let normalizedLast = normalize(last)
        let directionWords = [
            "heben", "raise", "up", "auf", "open", "opening", "offnen", "oeffnen", "öffnen",
            "senken", "lower", "down", "zu", "close", "closing", "schliessen", "schließen",
        ]
        if directionWords.contains(normalizedLast) {
            parts.removeLast()
            return parts.joined(separator: " ").trimmingCharacters(in: .whitespaces)
        }
        return label.trimmingCharacters(in: .whitespaces)
    }

    private func normalizedWords(_ text: String) -> [String] {
        text.split(separator: " ").map { normalize(String($0)) }
    }

    private func normalize(_ text: String) -> String {
        text.trimmingCharacters(in: .whitespacesAndNewlines)
            .folding(options: [.caseInsensitive, .diacriticInsensitive], locale: .current)
            .lowercased()
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
        case .button:  return 5
        }
    }

    /// Floor/room name → SF Symbol, matched by German/English synonyms. Short codes
    /// (EG, DG, KG, OG …) match exactly; longer words (Erdgeschoss, Keller …) match
    /// as a substring, so the icon survives different naming conventions. The
    /// symbol choices are unchanged from the original hand-picked set.
    private func roomSymbol(for room: String) -> String {
        let groups: [(symbol: String, synonyms: [String])] = [
            ("exclamationmark.triangle", ["panik", "panic", "alarm", "notfall", "emergency", "sicherheit", "security"]),
            ("person.crop.circle.badge.checkmark", ["anwesenheit", "presence", "simulation", "urlaub", "vacation", "abwesend", "away"]),
            ("arrow.down.to.line", ["kg", "ug", "keller", "untergeschoss", "souterrain", "basement", "cellar"]),
            ("house.and.flag",     ["einlieger", "einliegerwohnung", "elw", "annex", "granny", "gästewohnung", "apartment"]),
            ("house",              ["eg", "erdgeschoss", "parterre", "ground", "groundfloor"]),
            ("stairs",             ["og", "dg", "obergeschoss", "dachgeschoss", "dach", "stock", "etage", "upper", "attic", "loft"]),
            ("sun.max",            ["außen", "aussen", "garten", "outdoor", "outside", "terrasse", "balkon", "garden"]),
            ("square.grid.2x2",    ["zentral", "central", "gesamt", "global"]),
        ]
        let name = room.lowercased()
        for group in groups where group.synonyms.contains(where: { name == $0 || ($0.count >= 4 && name.contains($0)) }) {
            return group.symbol
        }
        return "square.split.bottomrightquarter"
    }
}

// MARK: - Automation/action tools from project.tpfx

private final class ToolActionParser: NSObject, XMLParserDelegate {
    struct Action {
        let name: String
        let category: String
        let room: String
        let sortIndex: Int
        let ref: ChannelRef
    }

    private struct Candidate {
        let index: Int
        let ref: ChannelRef
        let isPushButton: Bool
    }

    var actions: [Action] = []
    var error: Error?

    private var currentLocation: [String] = []
    private var currentTool: [String: String]?
    private var candidates: [Candidate] = []
    private var currentNode: [String: String]?

    func parser(_ parser: XMLParser,
                didStartElement element: String,
                namespaceURI: String?,
                qualifiedName _: String?,
                attributes attrs: [String: String]) {
        switch element {
        case "PAGE", "LAYER":
            if let name = attrs["name"], !name.isEmpty {
                guard currentLocation.count < PHCProjectParser.Limits.maxLocationDepth else {
                    fail(PHCLocalization.string("%@ nesting exceeds %d levels", "project.tpfx", PHCProjectParser.Limits.maxLocationDepth), parser)
                    return
                }
                currentLocation.append(name)
            }
        case "TOOL":
            currentTool = attrs
            candidates.removeAll()
        case "NODE":
            currentNode = attrs
        case "VAR":
            guard let tool = currentTool,
                  isSupportedActionTool(tool),
                  let node = currentNode,
                  node["ntype"] == "ntInput",
                  attrs["modGrp"] == "Eingangsmodule",
                  attrs["chGrp"] == "Eingang",
                  let module = Int(attrs["mod"] ?? ""),
                  let channel = Int(attrs["cha"] ?? "")
            else { return }
            guard candidates.count < PHCProjectParser.Limits.maxToolCandidates else {
                fail(PHCLocalization.string("%@ contains too many tool input candidates", "project.tpfx"), parser)
                return
            }

            let index = Int(node["index"] ?? "") ?? 99
            let objects = (node["comfortSoftwareObjects"] ?? "").lowercased()
            let name = (node["name"] ?? "").lowercased()
            let isPushButton = objects.contains("pushbutton") || name.contains("taster")
            candidates.append(Candidate(
                index: index,
                ref: ChannelRef(moduleClass: .emd, dip: module, channel: channel),
                isPushButton: isPushButton
            ))
        default:
            break
        }
    }

    func parser(_ parser: XMLParser,
                didEndElement element: String,
                namespaceURI: String?,
                qualifiedName _: String?) {
        switch element {
        case "TOOL":
            finishTool(parser)
        case "NODE":
            currentNode = nil
        case "PAGE", "LAYER":
            if !currentLocation.isEmpty { currentLocation.removeLast() }
        default:
            break
        }
    }

    func parser(_ parser: XMLParser, parseErrorOccurred err: Error) {
        if error == nil { error = err }
    }

    private func finishTool(_ parser: XMLParser) {
        defer {
            currentTool = nil
            candidates.removeAll()
        }
        guard let tool = currentTool,
              isSupportedActionTool(tool),
              let kind = actionKind(tool),
              let candidate = preferredCandidate()
        else { return }
        guard actions.count < PHCProjectParser.Limits.maxToolActions else {
            fail(PHCLocalization.string("%@ contains more than %d supported actions", "project.tpfx", PHCProjectParser.Limits.maxToolActions), parser)
            return
        }

        let room = currentLocation.last ?? defaultRoom(for: kind)
        let rawName = tool["bez"].flatMap(nonEmpty)
            ?? tool["name"].flatMap(nonEmpty)
            ?? defaultName(for: kind)
        let name = cleanedName(rawName, kind: kind)

        actions.append(Action(
            name: name,
            category: category(for: kind),
            room: room,
            sortIndex: sortIndex(for: kind),
            ref: candidate.ref
        ))
    }

    private func preferredCandidate() -> Candidate? {
        candidates
            .sorted {
                if $0.isPushButton != $1.isPushButton { return $0.isPushButton && !$1.isPushButton }
                return $0.index < $1.index
            }
            .first
    }

    private enum ActionKind { case panic, presence }

    private func isSupportedActionTool(_ attrs: [String: String]) -> Bool {
        guard attrs["enable"] != "false" else { return false }
        return actionKind(attrs) != nil
    }

    private func actionKind(_ attrs: [String: String]) -> ActionKind? {
        let haystack = [
            attrs["internalName"],
            attrs["name"],
            attrs["bez"],
            attrs["grp"],
            attrs["comfortSoftwareGroupType"],
        ]
        .compactMap { $0 }
        .joined(separator: " ")
        .lowercased()

        if PHCKeywords.matches(PHCKeywords.panic, haystack) { return .panic }
        if PHCKeywords.matches(PHCKeywords.presenceSimulation, haystack)
            || haystack.contains("infratec.tools.anwesenheitssimulation") {
            return .presence
        }
        return nil
    }

    private func category(for kind: ActionKind) -> String {
        switch kind {
        case .panic: return "Security"
        case .presence: return "Presence Simulation"
        }
    }

    private func defaultRoom(for kind: ActionKind) -> String {
        switch kind {
        case .panic: return "Security"
        case .presence: return "Automation"
        }
    }

    private func defaultName(for kind: ActionKind) -> String {
        switch kind {
        case .panic: return "Panic Button"
        case .presence: return "Presence Simulation"
        }
    }

    private func sortIndex(for kind: ActionKind) -> Int {
        switch kind {
        case .panic: return 0
        case .presence: return 90
        }
    }

    private func cleanedName(_ raw: String, kind: ActionKind) -> String {
        let trimmed = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        switch kind {
        case .panic:
            return trimmed
        case .presence:
            for prefix in ["Anwesenheitssimulation ", "Presence Simulation "] where trimmed.hasPrefix(prefix) {
                return String(trimmed.dropFirst(prefix.count))
            }
            return trimmed
        }
    }

    private func nonEmpty(_ value: String) -> String? {
        let trimmed = value.trimmingCharacters(in: .whitespacesAndNewlines)
        return trimmed.isEmpty ? nil : trimmed
    }

    private func fail(_ message: String, _ parser: XMLParser) {
        error = PHCProjectParser.ParseError(message: message)
        parser.abortParsing()
    }
}
