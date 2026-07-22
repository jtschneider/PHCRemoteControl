import Foundation

/// Event codes accepted by `service.stm.simInputEvent`.
enum PHCInputEvent: Int, Codable, Sendable {
    case press = 2
    case longPress = 3
    case release = 4
    case doublePress = 5
}

/// Pure, fixture-tested event plans used by the STM transport.
enum PHCInputEventPlan {
    static let shortPress: [PHCInputEvent] = [.press, .release, .doublePress]
    static let longPress: [PHCInputEvent] = [.press, .longPress]
    static let tip: [PHCInputEvent] = [.press, .release]
}
