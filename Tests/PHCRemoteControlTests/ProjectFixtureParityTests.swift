import Foundation
import XCTest
@testable import PHCRemoteControl

final class ProjectFixtureParityTests: XCTestCase {
    func testAMDBasicFixture() throws {
        try assertFixture(named: "amd-basic")
    }

    func testFullProjectFixture() throws {
        try assertFixture(named: "full-project", includesTPFX: true)
    }

    private func assertFixture(named fixtureName: String, includesTPFX: Bool = false) throws {
        let ppfx = try Data(contentsOf: fixtureURL(name: fixtureName, extension: "ppfx"))
        let tpfx = includesTPFX
            ? try Data(contentsOf: fixtureURL(name: fixtureName, extension: "tpfx"))
            : nil
        let expectedData = try Data(contentsOf: fixtureURL(name: "\(fixtureName).expected", extension: "json"))
        let expected = try JSONDecoder().decode(ExpectedFixture.self, from: expectedData)

        XCTAssertEqual(expected.schemaVersion, 1)

        let parsed = try PHCProjectParser.parse(ppfxData: ppfx, tpfxData: tpfx)
        let normalized = ExpectedProject(
            name: parsed.name,
            floors: parsed.rooms.map { room in
                ExpectedFloor(
                    id: room.stableID,
                    name: room.name,
                    devices: parsed.devices(in: room).map { device in
                        ExpectedDevice(
                            id: device.stableID ?? "missing-hardware-reference",
                            name: device.name,
                            kind: device.kind.rawValue,
                            category: device.category,
                            ref: device.ref.map {
                                ExpectedRef(
                                    moduleClass: $0.moduleClass.rawValue,
                                    dip: $0.dip,
                                    channel: $0.channel
                                )
                            },
                            upRef: device.shutterUpRef.map {
                                ExpectedRef(
                                    moduleClass: $0.moduleClass.rawValue,
                                    dip: $0.dip,
                                    channel: $0.channel
                                )
                            }
                        )
                    }
                )
            }
        )

        XCTAssertEqual(normalized, expected.project)
    }

    private func fixtureURL(name: String, extension ext: String) throws -> URL {
        let bundle = Bundle(for: Self.self)
        let candidates = [
            bundle.url(forResource: name, withExtension: ext, subdirectory: "protocol-fixtures/project"),
            bundle.url(forResource: name, withExtension: ext, subdirectory: "project"),
            bundle.url(forResource: name, withExtension: ext),
        ]
        if let url = candidates.compactMap({ $0 }).first {
            return url
        }
        throw FixtureError.missing("\(name).\(ext)")
    }
}

private enum FixtureError: Error {
    case missing(String)
}

private struct ExpectedFixture: Decodable {
    let schemaVersion: Int
    let project: ExpectedProject
}

private struct ExpectedProject: Codable, Equatable {
    let name: String
    let floors: [ExpectedFloor]
}

private struct ExpectedFloor: Codable, Equatable {
    let id: String
    let name: String
    let devices: [ExpectedDevice]
}

private struct ExpectedDevice: Codable, Equatable {
    let id: String
    let name: String
    let kind: String
    let category: String
    let ref: ExpectedRef?
    let upRef: ExpectedRef?
}

private struct ExpectedRef: Codable, Equatable {
    let moduleClass: String
    let dip: Int
    let channel: Int
}
