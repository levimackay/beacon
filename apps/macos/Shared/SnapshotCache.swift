import Foundation

/// The snapshot the app last fetched, persisted so the widget has something
/// to show without the app running and without holding a second connection
/// to the hub.
///
/// The cache records when it was written as well as what it contains. A
/// widget that cannot say how old its data is would be worse than one
/// showing nothing: the entire point of glancing at it is to trust what it
/// says, and silently presenting a stale reading as current health is the
/// one failure mode a monitor must not have.
struct CachedSnapshot: Codable, Sendable {
    var snapshot: Snapshot
    var storedAt: Date

    /// Data older than this is presented as stale rather than as current.
    static let freshnessLimit: TimeInterval = 120

    var age: TimeInterval { Date().timeIntervalSince(storedAt) }
    var isStale: Bool { age > Self.freshnessLimit }
}

enum SnapshotCache {
    static func write(_ snapshot: Snapshot) {
        let payload = CachedSnapshot(snapshot: snapshot, storedAt: Date())
        guard let data = try? JSONEncoder.hub.encode(payload) else { return }
        try? FileManager.default.createDirectory(
            at: HubPaths.sharedDirectory, withIntermediateDirectories: true)
        // Written atomically so a widget reading concurrently never sees a
        // half-written file and decides the infrastructure is unknown.
        try? data.write(to: HubPaths.cacheFile, options: [.atomic])
    }

    static func read() -> CachedSnapshot? {
        guard let data = try? Data(contentsOf: HubPaths.cacheFile) else { return nil }
        return try? JSONDecoder.hub.decode(CachedSnapshot.self, from: data)
    }
}

/// Formatting shared by the app and the widget.
enum Format {
    static func age(_ interval: TimeInterval) -> String {
        let s = max(0, Int(interval))
        if s < 60 { return "\(s)s" }
        if s < 3600 { return "\(s / 60)m" }
        if s < 86400 { return "\(s / 3600)h" }
        return "\(s / 86400)d"
    }

    static func duration(_ interval: TimeInterval) -> String {
        let s = max(0, Int(interval))
        if s < 60 { return "\(s)s" }
        if s < 3600 { return "\(s / 60)m \(s % 60)s" }
        if s < 86400 { return "\(s / 3600)h \((s % 3600) / 60)m" }
        return "\(s / 86400)d \((s % 86400) / 3600)h"
    }

    static func percent(_ value: Double) -> String {
        "\(Int(value.rounded()))%"
    }

    static func latency(_ ms: Double) -> String {
        ms >= 1000 ? String(format: "%.1fs", ms / 1000) : "\(Int(ms.rounded()))ms"
    }
}
