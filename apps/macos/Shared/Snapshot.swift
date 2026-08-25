import Foundation

/// Health of a monitored target. Mirrors protocol.State on the hub.
enum HealthState: String, Codable, Sendable {
    case healthy, warning, down, unknown

    /// Severity order, matching the hub's own ranking: an unobservable
    /// target is a worse position than a degraded one, but not as bad as a
    /// confirmed failure.
    var rank: Int {
        switch self {
        case .down: 3
        case .unknown: 2
        case .warning: 1
        case .healthy: 0
        }
    }

    var symbolName: String {
        switch self {
        case .healthy: "checkmark.circle.fill"
        case .warning: "exclamationmark.triangle.fill"
        case .down: "xmark.octagon.fill"
        case .unknown: "questionmark.circle.fill"
        }
    }

    var headline: String {
        switch self {
        case .healthy: "Everything is healthy"
        case .warning: "Needs attention"
        case .down: "Problem detected"
        case .unknown: "Status unknown"
        }
    }
}

enum TargetKind: String, Codable, Sendable {
    case host, website, service
}

struct MonitoredTarget: Codable, Hashable, Sendable {
    var id: String
    var kind: TargetKind
    var name: String
    var address: String
    var intervalSeconds: Int
    var expectStatus: Int?
    var enabled: Bool
    var allowPrivate: Bool?
}

struct TargetStatus: Codable, Hashable, Sendable, Identifiable {
    var target: MonitoredTarget
    var state: HealthState
    var latencyMs: Double
    var metrics: [String: Double]?
    var lastCheck: Date
    var error: String?
    var certExpiry: Date?

    var id: String { target.id }

    func metric(_ key: String) -> Double? { metrics?[key] }
}

struct Incident: Codable, Hashable, Sendable, Identifiable {
    var id: Int
    var targetId: String
    var targetName: String
    var state: HealthState
    var startedAt: Date
    var resolvedAt: Date?
    var summary: String

    var isOpen: Bool { resolvedAt == nil }

    func duration(now: Date) -> TimeInterval {
        (resolvedAt ?? now).timeIntervalSince(startedAt)
    }
}

struct Counts: Codable, Hashable, Sendable {
    var critical: Int
    var warning: Int
    var healthy: Int
    var unknown: Int

    var total: Int { critical + warning + healthy + unknown }
}

struct HubInfo: Codable, Hashable, Sendable {
    var version: String
    var host: String
    var os: String
    var kernel: String
    var startedAt: Date
    var uptimeSeconds: Int
}

struct Snapshot: Codable, Hashable, Sendable {
    var generatedAt: Date
    var overall: HealthState
    var hub: HubInfo
    var counts: Counts
    var targets: [TargetStatus]?
    var openIncidents: [Incident]?

    var allTargets: [TargetStatus] { targets ?? [] }
    var incidents: [Incident] { openIncidents ?? [] }

    func targets(ofKind kind: TargetKind) -> [TargetStatus] {
        allTargets.filter { $0.target.kind == kind }.sorted { $0.target.name < $1.target.name }
    }
}

/// Metric keys the hub emits. These are wire identifiers and must match
/// internal/protocol/sample.go exactly.
enum MetricKey {
    static let cpu = "cpu_percent"
    static let memory = "mem_percent"
    static let disk = "disk_percent"
    static let load1 = "load1"
    static let uptime = "uptime_seconds"
    static let temperature = "temp_c"
    static let certDaysLeft = "cert_days_left"
}

extension JSONDecoder {
    /// A decoder matching the hub's wire format. Go emits RFC 3339 with a
    /// variable number of fractional-second digits, which the fixed
    /// .iso8601 strategy rejects, so parsing is done with a formatter that
    /// accepts both forms.
    static var hub: JSONDecoder {
        let d = JSONDecoder()
        d.dateDecodingStrategy = .custom { decoder in
            let raw = try decoder.singleValueContainer().decode(String.self)
            if let date = ISO8601DateFormatter.withFractional.date(from: raw) {
                return date
            }
            if let date = ISO8601DateFormatter.plain.date(from: raw) {
                return date
            }
            throw DecodingError.dataCorrupted(
                .init(codingPath: decoder.codingPath, debugDescription: "unrecognised timestamp \(raw)"))
        }
        return d
    }
}

extension JSONEncoder {
    static var hub: JSONEncoder {
        let e = JSONEncoder()
        e.dateEncodingStrategy = .custom { date, encoder in
            var c = encoder.singleValueContainer()
            try c.encode(ISO8601DateFormatter.withFractional.string(from: date))
        }
        return e
    }
}

extension ISO8601DateFormatter {
    static let withFractional: ISO8601DateFormatter = {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return f
    }()

    static let plain: ISO8601DateFormatter = {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime]
        return f
    }()
}
