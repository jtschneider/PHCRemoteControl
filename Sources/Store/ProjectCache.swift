import Foundation

/// Persists the last-loaded `PHCProject` to disk so the app can start instantly
/// without re-downloading and parsing the project ZIP from the STM on every
/// launch. Keyed by host so different control units don't collide.
enum ProjectCache {
    private static var directory: URL {
        let base = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
        let dir = base.appendingPathComponent("PHCRemoteControl", isDirectory: true)
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        return dir
    }

    private static func fileURL(for key: String) -> URL {
        let safe = key.replacingOccurrences(of: "[^A-Za-z0-9._-]", with: "_", options: .regularExpression)
        return directory.appendingPathComponent("project-\(safe).json")
    }

    static func load(key: String) -> PHCProject? {
        guard let data = try? Data(contentsOf: fileURL(for: key)) else { return nil }
        return try? JSONDecoder().decode(PHCProject.self, from: data)
    }

    static func save(_ project: PHCProject, key: String) {
        guard let data = try? JSONEncoder().encode(project) else { return }
        try? data.write(to: fileURL(for: key), options: .atomic)
    }

    static func clear(key: String) {
        try? FileManager.default.removeItem(at: fileURL(for: key))
    }
}
