import Foundation

/// German + English keywords used to classify a channel's category (the `TYPE`
/// segment of its project channel name) into a device kind and a section icon.
/// Matching is case-insensitive substring, so plurals and compounds are covered
/// ("roll" matches Rollo / Rollladen / Rollläden; "licht" matches Deckenlicht).
///
/// Shared by `PHCProjectParser` (which picks the control type) and the UI (which
/// picks the section icon) so the two never drift apart.
enum PHCKeywords {
    static let shutter = ["roll", "jalousie", "shutter", "blind", "raffstore",
                          "markise", "beschattung", "sonnenschutz", "store"]
    static let outlet  = ["steckdose", "dose", "schuko", "stecker", "outlet",
                          "socket", "receptacle", "plug", "pumpe", "pump",
                          "zirkulation"]
    static let light   = ["licht", "lampe", "leuchte", "beleuchtung", "strahler",
                          "spot", "fluter", "kronleuchter", "lüster", "luster",
                          "pendel", "birne", "led", "light", "lamp", "bulb",
                          "sconce", "chandelier", "downlight"]
    static let vent    = ["lüftung", "luftung", "fenster", "klima", "ventil", "window"]

    /// True if `text` contains any of `words` (case-insensitive).
    static func matches(_ words: [String], _ text: String) -> Bool {
        let t = text.lowercased()
        return words.contains { t.contains($0) }
    }
}
