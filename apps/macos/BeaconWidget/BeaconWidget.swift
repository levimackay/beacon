import SwiftUI
import WidgetKit

struct BeaconEntry: TimelineEntry {
    var date: Date
    var snapshot: Snapshot?
    var age: TimeInterval?
    var problem: String?

    /// What the widget should actually show. When the data is stale or
    /// absent, the answer is "unknown", never the last healthy reading:
    /// a widget that says everything is fine because it cannot see
    /// anything is worse than one that admits it does not know.
    var displayState: HealthState {
        guard problem == nil, let snapshot else { return .unknown }
        if let age, age > CachedSnapshot.freshnessLimit { return .unknown }
        return snapshot.overall
    }

    var isStale: Bool {
        problem != nil || (age.map { $0 > CachedSnapshot.freshnessLimit } ?? true)
    }
}

struct BeaconProvider: TimelineProvider {
    func placeholder(in context: Context) -> BeaconEntry {
        BeaconEntry(date: Date(), snapshot: nil, age: nil, problem: nil)
    }

    func getSnapshot(in context: Context, completion: @escaping (BeaconEntry) -> Void) {
        Task { completion(await load()) }
    }

    func getTimeline(in context: Context, completion: @escaping (Timeline<BeaconEntry>) -> Void) {
        Task {
            let entry = await load()
            // Refreshed often enough to be worth glancing at, rarely
            // enough that WidgetKit keeps honouring the requests.
            let next = Date().addingTimeInterval(entry.problem == nil ? 300 : 900)
            completion(Timeline(entries: [entry], policy: .after(next)))
        }
    }

    /// Reads the snapshot the app cached into the shared container.
    ///
    /// The widget deliberately does not talk to the hub itself. It is
    /// sandboxed, so it cannot read the hub's token, and copying that
    /// token somewhere the widget could read would widen the blast radius
    /// of the one secret the whole security model rests on. The app polls;
    /// the widget reports what the app last saw, and says plainly how old
    /// that is.
    private func load() async -> BeaconEntry {
        guard let cached = SnapshotCache.read() else {
            return BeaconEntry(date: Date(), snapshot: nil, age: nil,
                               problem: "Open Beacon to start monitoring.")
        }
        return BeaconEntry(
            date: Date(),
            snapshot: cached.snapshot,
            age: cached.age,
            problem: cached.isStale ? "Beacon is not running." : nil)
    }
}

// MARK: - Small

/// Answers exactly one question, readable at a glance from across a desk.
struct SmallWidgetView: View {
    var entry: BeaconEntry

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: 6) {
                StateDot(state: entry.displayState, size: 9)
                Text("Beacon")
                    .font(.system(size: 11, weight: .semibold))
                    .foregroundStyle(.secondary)
            }
            Spacer(minLength: 6)
            Text(headline)
                .font(.system(size: 19, weight: .semibold))
                .foregroundStyle(entry.displayState.tint)
                .lineLimit(2)
                .minimumScaleFactor(0.7)
                .fixedSize(horizontal: false, vertical: true)
            Spacer(minLength: 6)
            Text(detail)
                .font(.system(size: 10))
                .foregroundStyle(.tertiary)
                .lineLimit(2)
        }
    }

    private var headline: String {
        if entry.problem != nil { return "No connection" }
        guard let snapshot = entry.snapshot else { return "Not set up" }
        if entry.isStale { return "Stale" }
        switch snapshot.overall {
        case .healthy: return "Healthy"
        case .warning: return "\(snapshot.counts.warning) warning"
        case .down: return "\(snapshot.counts.critical) down"
        case .unknown: return "Unknown"
        }
    }

    private var detail: String {
        guard let snapshot = entry.snapshot else { return "Start the hub" }
        if let age = entry.age, entry.isStale {
            return "Last seen \(Format.age(age)) ago"
        }
        let devices = snapshot.targets(ofKind: .host).count
        let sites = snapshot.targets(ofKind: .website).count
        var parts: [String] = []
        if devices > 0 { parts.append("\(devices) device\(devices == 1 ? "" : "s")") }
        if sites > 0 { parts.append("\(sites) site\(sites == 1 ? "" : "s")") }
        let summary = parts.isEmpty ? "Nothing monitored" : parts.joined(separator: ", ")
        if let age = entry.age { return summary + " · \(Format.age(age)) ago" }
        return summary
    }
}

// MARK: - Medium

struct MediumWidgetView: View {
    var entry: BeaconEntry

    var body: some View {
        VStack(alignment: .leading, spacing: 7) {
            HStack(spacing: 6) {
                Text("Beacon")
                    .font(.system(size: 11, weight: .semibold))
                    .foregroundStyle(.secondary)
                Spacer()
                StateDot(state: entry.displayState, size: 7)
                Text(statusLabel)
                    .font(.system(size: 11, weight: .medium))
                    .foregroundStyle(entry.displayState.tint)
            }

            if let snapshot = entry.snapshot {
                if let incident = snapshot.incidents.first {
                    problemRow(incident)
                } else {
                    ForEach(rows(snapshot).prefix(4), id: \.id) { row in
                        WidgetRow(status: row, compact: true)
                    }
                }
            } else {
                Text(entry.problem ?? "Not set up")
                    .font(.system(size: 12))
                    .foregroundStyle(.secondary)
            }

            Spacer(minLength: 0)
            if let age = entry.age {
                Text(entry.isStale ? "Last seen \(Format.age(age)) ago" : "Updated \(Format.age(age)) ago")
                    .font(.system(size: 9))
                    .foregroundStyle(.tertiary)
            }
        }
    }

    private var statusLabel: String {
        entry.problem != nil ? "No connection" : (entry.isStale ? "Stale" : entry.displayState.headline)
    }

    private func rows(_ snapshot: Snapshot) -> [TargetStatus] {
        // Anything unhealthy first: the widget's job is to surface the
        // exception, not to list inventory alphabetically.
        snapshot.allTargets.sorted {
            $0.state.rank != $1.state.rank ? $0.state.rank > $1.state.rank : $0.target.name < $1.target.name
        }
    }

    private func problemRow(_ incident: Incident) -> some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(incident.targetName)
                .font(.system(size: 15, weight: .semibold))
                .foregroundStyle(incident.state.tint)
            Text(incident.summary)
                .font(.system(size: 11))
                .foregroundStyle(.secondary)
                .lineLimit(2)
            Text("Down for \(Format.duration(incident.duration(now: Date())))")
                .font(.system(size: 11, design: .monospaced))
                .foregroundStyle(.secondary)
        }
    }
}

// MARK: - Large

struct LargeWidgetView: View {
    var entry: BeaconEntry

    var body: some View {
        VStack(alignment: .leading, spacing: 9) {
            HStack(spacing: 7) {
                StateDot(state: entry.displayState, size: 9)
                Text(entry.problem != nil ? "No connection" : (entry.isStale ? "Stale data" : entry.displayState.headline))
                    .font(.system(size: 15, weight: .semibold))
                Spacer()
                if let age = entry.age {
                    Text(Format.age(age) + " ago")
                        .font(.system(size: 10))
                        .foregroundStyle(.tertiary)
                }
            }

            if let snapshot = entry.snapshot {
                if !snapshot.incidents.isEmpty {
                    ForEach(snapshot.incidents.prefix(2)) { incident in
                        HStack(alignment: .top, spacing: 7) {
                            StateDot(state: incident.state, size: 6)
                            VStack(alignment: .leading, spacing: 1) {
                                Text(incident.targetName)
                                    .font(.system(size: 12, weight: .medium))
                                Text(incident.summary)
                                    .font(.system(size: 10))
                                    .foregroundStyle(.secondary)
                                    .lineLimit(1)
                            }
                            Spacer(minLength: 4)
                            Text(Format.duration(incident.duration(now: Date())))
                                .font(.system(size: 10, design: .monospaced))
                                .foregroundStyle(incident.state.tint)
                        }
                        .padding(7)
                        .background(incident.state.tint.opacity(0.1), in: RoundedRectangle(cornerRadius: 6))
                    }
                }

                group("Devices", snapshot.targets(ofKind: .host))
                group("Websites", snapshot.targets(ofKind: .website))
                group("Services", snapshot.targets(ofKind: .service))
            } else {
                Text(entry.problem ?? "Not set up")
                    .font(.system(size: 12))
                    .foregroundStyle(.secondary)
            }
            Spacer(minLength: 0)
        }
    }

    @ViewBuilder
    private func group(_ title: String, _ items: [TargetStatus]) -> some View {
        if !items.isEmpty {
            VStack(alignment: .leading, spacing: 4) {
                Text(title.uppercased())
                    .font(.system(size: 9, weight: .semibold))
                    .tracking(0.5)
                    .foregroundStyle(.tertiary)
                ForEach(items.prefix(4)) { item in
                    WidgetRow(status: item, compact: false)
                }
            }
        }
    }
}

struct WidgetRow: View {
    var status: TargetStatus
    var compact: Bool

    var body: some View {
        HStack(spacing: 6) {
            StateDot(state: status.state, size: 6)
            Text(status.target.name)
                .font(.system(size: compact ? 12 : 11))
                .lineLimit(1)
            Spacer(minLength: 6)
            Text(detail)
                .font(.system(size: compact ? 11 : 10, design: .monospaced))
                .foregroundStyle(status.error == nil ? .secondary : status.state.tint)
                .lineLimit(1)
        }
    }

    private var detail: String {
        if let error = status.error, !error.isEmpty { return error }
        switch status.target.kind {
        case .host:
            var parts: [String] = []
            if let cpu = status.metric(MetricKey.cpu) { parts.append("\(Format.percent(cpu)) cpu") }
            if let mem = status.metric(MetricKey.memory) { parts.append("\(Format.percent(mem)) ram") }
            return parts.isEmpty ? "no data" : parts.joined(separator: "  ")
        case .website, .service:
            return status.latencyMs > 0 ? Format.latency(status.latencyMs) : "no data"
        }
    }
}

// MARK: - Widget

struct BeaconWidgetView: View {
    @Environment(\.widgetFamily) private var family
    var entry: BeaconEntry

    var body: some View {
        switch family {
        case .systemSmall: SmallWidgetView(entry: entry)
        case .systemLarge: LargeWidgetView(entry: entry)
        default: MediumWidgetView(entry: entry)
        }
    }
}

@main
struct BeaconWidget: Widget {
    var body: some WidgetConfiguration {
        StaticConfiguration(kind: "com.levimackay.beacon.widget", provider: BeaconProvider()) { entry in
            BeaconWidgetView(entry: entry)
                .containerBackground(.fill.tertiary, for: .widget)
        }
        .configurationDisplayName("Beacon")
        .description("Whether your machines, services and websites are healthy.")
        .supportedFamilies([.systemSmall, .systemMedium, .systemLarge])
    }
}
