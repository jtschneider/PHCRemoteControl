import Foundation
import XCTest
@testable import PHCRemoteControl

final class CommandFixtureTests: XCTestCase {
    func testInputEventPlans() throws {
        let data = try Data(contentsOf: fixtureURL())
        let fixture = try JSONDecoder().decode(InputEventFixture.self, from: data)

        XCTAssertEqual(fixture.schemaVersion, 1)
        XCTAssertEqual(PHCInputEventPlan.shortPress.map(\.rawValue), fixture.plans.shortPress)
        XCTAssertEqual(PHCInputEventPlan.longPress.map(\.rawValue), fixture.plans.longPress)
        XCTAssertEqual(PHCInputEventPlan.tip.map(\.rawValue), fixture.plans.tip)
    }

    private func fixtureURL() throws -> URL {
        let bundle = Bundle(for: Self.self)
        let candidates = [
            bundle.url(forResource: "input-events", withExtension: "json", subdirectory: "commands"),
            bundle.url(forResource: "input-events", withExtension: "json"),
        ]
        if let url = candidates.compactMap({ $0 }).first {
            return url
        }
        throw CommandFixtureError.missingInputEvents
    }
}

private enum CommandFixtureError: Error {
    case missingInputEvents
}

private struct InputEventFixture: Decodable {
    let schemaVersion: Int
    let plans: InputEventPlans
}

private struct InputEventPlans: Decodable {
    let shortPress: [Int]
    let longPress: [Int]
    let tip: [Int]
}
