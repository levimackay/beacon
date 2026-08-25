import SwiftUI
import WidgetKit

/// Shared reading of an entry, so all three sizes agree on what they are
/// looking at rather than each recomputing it slightly differently.
extension BeaconEntry {
    var hostStatus: TargetStatus? {
        snapshot?.targets(ofKind: .host).first
    }

    /// Targets ordered so anything unhealthy is first. The widget exists to
    /// surface the exception, not to list inventory alphabetically.
    var ranked: [TargetStatus] {
        (snapshot?.allTargets ?? []).sorted {
            $0.state.rank != $1.state.rank ? $0.state.rank > $1.state.rank : $0.target.name < $1.target.name
        }
    }

    var services: [TargetStatus] {
        ranked.filter { $0.target.kind != .host }
    }

    var ageLabel: String {
        guard let age else { return "no data" }
        return isStale ? "\(Format.age(age)) old" : "\(Format.age(age)) ago"
    }

    var headline: String {
        if problem != nil && snapshot == nil { return "NOT\nSET UP" }
        if isStale { return "NO\nSIGNAL" }
        return displayState.display
    }
}

/// The wordmark and freshness line every size carries, so the widget always
/// says what it is and how old its information is.
private struct Chrome: View {
    var entry: BeaconEntry
    var trailing: String?

    var body: some View {
        HStack(spacing: 5) {
            Micro("Beacon", color: Ink.muted)
            Spacer(minLength: 2)
            if let trailing {
                Micro(trailing, color: entry.isStale ? Ink.caution : Ink.faint)
            }
            StateDot(state: entry.displayState, size: 5)
        }
    }
}

// MARK: - Small

/// Answers one question, readable across a room, and then fills the rest of
/// its area with the numbers behind that answer rather than with padding.
struct SmallWidgetView: View {
    var entry: BeaconEntry

    var body: some View {
        // Fixed spacing rather than Spacers: the small widget has about
        // 126pt of usable height and flexible spacing quietly overflows it,
        // which crops the wordmark off the top and the footer off the
        // bottom instead of shrinking anything.
        VStack(alignment: .leading, spacing: 0) {
            Chrome(entry: entry, trailing: entry.ageLabel)

            Text(entry.headline)
                .font(.system(size: 22, weight: .heavy))
                .tracking(-0.7)
                .lineSpacing(-4)
                .foregroundStyle(entry.displayState.tint)
                .lineLimit(2)
                .minimumScaleFactor(0.55)
                .fixedSize(horizontal: false, vertical: true)
                .padding(.top, 7)

            Spacer(minLength: 4)

            if let host = entry.hostStatus {
                VStack(spacing: 4) {
                    Meter(label: "cpu", value: host.metric(MetricKey.cpu) ?? 0)
                    Meter(label: "mem", value: host.metric(MetricKey.memory) ?? 0)
                    Meter(label: "ssd", value: host.metric(MetricKey.disk) ?? 0)
                }
                // Readings from a snapshot that is no longer current are
                // dimmed rather than removed: the last known numbers are
                // still useful, but they must not look live.
                .opacity(entry.isStale ? 0.4 : 1)
            } else {
                Micro(entry.problem ?? "waiting for data", color: Ink.muted)
            }

            Spacer(minLength: 4)

            HStack(spacing: 5) {
                Micro("\(entry.ranked.count) watched")
                Spacer(minLength: 2)
                TargetTicks(states: entry.ranked.map(\.state), height: 8)
                    .frame(width: 38)
            }
        }
    }
}

// MARK: - Medium

/// Two columns: the verdict on the left at display size, the evidence on
/// the right. The split means neither half is ever mostly empty.
struct MediumWidgetView: View {
    var entry: BeaconEntry

    var body: some View {
        HStack(alignment: .top, spacing: 14) {
            VStack(alignment: .leading, spacing: 0) {
                Micro("Beacon", color: Ink.muted)
                Spacer(minLength: 4)
                Text(entry.headline)
                    .font(.system(size: 25, weight: .heavy))
                    .tracking(-0.8)
                    .lineSpacing(-3)
                    .foregroundStyle(entry.displayState.tint)
                    .lineLimit(2)
                    .minimumScaleFactor(0.6)
                    .fixedSize(horizontal: false, vertical: true)
                Spacer(minLength: 4)

                if let incident = entry.snapshot?.incidents.first {
                    VStack(alignment: .leading, spacing: 1) {
                        Micro("down for", color: Ink.faint)
                        Text(Format.duration(incident.duration(now: Date())))
                            .font(.system(size: 15, weight: .semibold, design: .monospaced))
                            .foregroundStyle(Ink.alarm)
                    }
                } else if let host = entry.hostStatus {
                    VStack(spacing: 5) {
                        Meter(label: "cpu", value: host.metric(MetricKey.cpu) ?? 0)
                        Meter(label: "mem", value: host.metric(MetricKey.memory) ?? 0)
                    }
                    .opacity(entry.isStale ? 0.4 : 1)
                }

                Spacer(minLength: 4)
                Micro(entry.ageLabel, color: entry.isStale ? Ink.caution : Ink.faint)
            }
            .frame(width: 118, alignment: .leading)

            Rectangle().fill(Ink.hairline).frame(width: 1)

            VStack(alignment: .leading, spacing: 0) {
                ForEach(Array(entry.ranked.prefix(5).enumerated()), id: \.element.id) { index, item in
                    if index > 0 { Hairline() }
                    ServiceRow(status: item)
                        .padding(.vertical, 4)
                }
                Spacer(minLength: 0)
            }
        }
    }
}

// MARK: - Large

struct LargeWidgetView: View {
    var entry: BeaconEntry

    /// The slowest service, used to scale the latency bars. Scaling against
    /// the slowest rather than a fixed ceiling means the bars stay
    /// informative whether everything answers in 20ms or 2s.
    private var latencyCeiling: Double {
        max(1, entry.services.map(\.latencyMs).max() ?? 1)
    }

    /// An incident card costs roughly two service rows worth of height, so
    /// the list gives that space back rather than pushing the footer off
    /// the bottom of the widget.
    private var serviceLimit: Int {
        (entry.snapshot?.incidents.isEmpty ?? true) ? 6 : 4
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            Chrome(entry: entry, trailing: entry.ageLabel)
                .padding(.bottom, 8)

            HStack(alignment: .lastTextBaseline, spacing: 8) {
                Text(entry.headline.replacingOccurrences(of: "\n", with: " "))
                    .font(.system(size: 27, weight: .heavy))
                    .tracking(-0.9)
                    .foregroundStyle(entry.displayState.tint)
                    .lineLimit(1)
                    .minimumScaleFactor(0.5)
                Spacer(minLength: 4)
                if let counts = entry.snapshot?.counts {
                    Text("\(counts.healthy)/\(counts.total)")
                        .font(.system(size: 15, weight: .semibold, design: .monospaced))
                        .foregroundStyle(Ink.muted)
                }
            }
            .padding(.bottom, 10)

            if let incident = entry.snapshot?.incidents.first {
                VStack(alignment: .leading, spacing: 3) {
                    HStack {
                        Micro("incident #\(incident.id)", color: Ink.alarm)
                        Spacer()
                        Text(Format.duration(incident.duration(now: Date())))
                            .font(.system(size: 11, weight: .semibold, design: .monospaced))
                            .foregroundStyle(Ink.alarm)
                    }
                    Text(incident.targetName)
                        .font(.system(size: 14, weight: .semibold))
                        .foregroundStyle(Ink.paper)
                    Text(incident.summary)
                        .font(.system(size: 11))
                        .foregroundStyle(Ink.muted)
                        .lineLimit(1)
                }
                .padding(8)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(Ink.alarm.opacity(0.12), in: RoundedRectangle(cornerRadius: 7))
                .overlay(RoundedRectangle(cornerRadius: 7).stroke(Ink.alarm.opacity(0.35), lineWidth: 1))
                .padding(.bottom, 10)
            }

            if let host = entry.hostStatus {
                Micro(host.target.name, color: Ink.muted)
                    .padding(.bottom, 7)
                    .opacity(entry.isStale ? 0.4 : 1)
                HStack(alignment: .top, spacing: 10) {
                    Meter(label: "cpu", value: host.metric(MetricKey.cpu) ?? 0)
                    Meter(label: "mem", value: host.metric(MetricKey.memory) ?? 0)
                    Meter(label: "ssd", value: host.metric(MetricKey.disk) ?? 0)
                }
                .opacity(entry.isStale ? 0.4 : 1)
                .padding(.bottom, 8)

                HStack(spacing: 0) {
                    if let temp = host.metric(MetricKey.temperature) {
                        Stat(label: "temp", value: "\(Int(temp))°")
                        Spacer(minLength: 0)
                    }
                    if let load = host.metric(MetricKey.load1) {
                        Stat(label: "load", value: String(format: "%.2f", load))
                        Spacer(minLength: 0)
                    }
                    if let up = host.metric(MetricKey.uptime) {
                        Stat(label: "uptime", value: Format.age(up))
                        Spacer(minLength: 0)
                    }
                    Stat(label: "watched", value: "\(entry.ranked.count)")
                }
                .opacity(entry.isStale ? 0.4 : 1)
                .padding(.bottom, 10)
            }

            Hairline()
            Micro("services", color: Ink.muted)
                .padding(.top, 8)
                .padding(.bottom, 2)

            VStack(alignment: .leading, spacing: 0) {
                ForEach(entry.services.prefix(serviceLimit)) { item in
                    ServiceDetailRow(status: item, ceiling: latencyCeiling)
                }
            }

            Spacer(minLength: 2)

            Hairline()
            HStack(spacing: 6) {
                Micro("hub \(entry.snapshot?.hub.version ?? "?")")
                Spacer(minLength: 2)
                Micro(entry.snapshot?.hub.os ?? "", color: Ink.faint)
            }
            .padding(.top, 5)
        }
    }
}

/// A service on the large widget: name, its number, and a bar showing how
/// it compares with everything else being watched. The bar is what turns a
/// list of numbers into something readable at a glance, and it fills the
/// row's width with information rather than whitespace.
private struct ServiceDetailRow: View {
    var status: TargetStatus
    var ceiling: Double

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(spacing: 7) {
                RoundedRectangle(cornerRadius: 1)
                    .fill(status.state.tint)
                    .frame(width: 2.5, height: 11)
                Text(status.target.name)
                    .font(.system(size: 12, weight: .medium))
                    .foregroundStyle(Ink.paper)
                    .lineLimit(1)
                Spacer(minLength: 4)
                if let days = status.metric(MetricKey.certDaysLeft), status.error == nil {
                    Micro("\(Int(days))d cert")
                }
                Text(valueLabel)
                    .font(.system(size: 11.5, weight: .semibold, design: .monospaced))
                    .foregroundStyle(status.error == nil ? Ink.paper : status.state.tint)
            }
            GeometryReader { geo in
                ZStack(alignment: .leading) {
                    Capsule().fill(Ink.track).frame(height: 2)
                    Capsule()
                        .fill(status.error == nil ? Ink.paper.opacity(0.75) : status.state.tint)
                        .frame(width: max(3, geo.size.width * fraction), height: 2)
                }
            }
            .frame(height: 2)
        }
        .padding(.vertical, 5)
    }

    private var fraction: Double {
        guard status.error == nil, ceiling > 0 else { return 1 }
        return min(1, max(0.04, status.latencyMs / ceiling))
    }

    private var valueLabel: String {
        if let error = status.error, !error.isEmpty {
            return error.count > 16 ? "DOWN" : error
        }
        return status.latencyMs > 0 ? Format.latency(status.latencyMs) : "no data"
    }
}

private struct Stat: View {
    var label: String
    var value: String

    var body: some View {
        VStack(alignment: .leading, spacing: 1) {
            Micro(label)
            Text(value)
                .font(.system(size: 12, weight: .medium, design: .monospaced))
                .foregroundStyle(Ink.paper)
        }
    }
}

/// One monitored thing: a tick for its state, its name, and its number.
struct ServiceRow: View {
    var status: TargetStatus
    var showsCert: Bool = false

    var body: some View {
        HStack(spacing: 7) {
            RoundedRectangle(cornerRadius: 1)
                .fill(status.state.tint)
                .frame(width: 2.5, height: 12)
            Text(status.target.name)
                .font(.system(size: 11.5, weight: .medium))
                .foregroundStyle(Ink.paper)
                .lineLimit(1)
            Spacer(minLength: 4)
            if showsCert, let days = status.metric(MetricKey.certDaysLeft), status.error == nil {
                Micro("\(Int(days))d")
            }
            Text(value)
                .font(.system(size: 11, weight: .medium, design: .monospaced))
                .foregroundStyle(status.error == nil ? Ink.muted : status.state.tint)
                .lineLimit(1)
        }
    }

    private var value: String {
        if let error = status.error, !error.isEmpty {
            // A truncated phrase ("connection...") reads worse than the
            // plain fact. The full reason is on the large widget and in
            // the app; here the useful signal is that it is not up.
            return error.count > 10 ? "DOWN" : error
        }
        switch status.target.kind {
        case .host:
            if let cpu = status.metric(MetricKey.cpu) { return "\(Int(cpu))%" }
            return "ok"
        case .website, .service:
            return status.latencyMs > 0 ? Format.latency(status.latencyMs) : "no data"
        }
    }
}

/// Picks the layout for the family the system asked for.
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
