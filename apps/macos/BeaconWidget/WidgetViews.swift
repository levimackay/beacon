import SwiftUI
import WidgetKit

/// Shared reading of an entry, so all three sizes agree on what they are
/// looking at rather than each recomputing it slightly differently.
extension BeaconEntry {
    /// Targets ordered so anything unhealthy is first. The widget exists to
    /// surface the exception, not to list inventory alphabetically.
    var ranked: [TargetStatus] {
        (snapshot?.allTargets ?? []).sorted {
            $0.state.rank != $1.state.rank ? $0.state.rank > $1.state.rank : $0.target.name < $1.target.name
        }
    }

    /// The machine given the detailed treatment: the worst one, not the
    /// alphabetically first. If one of several machines is in trouble, that
    /// is the one whose numbers are worth the space.
    var hostStatus: TargetStatus? {
        ranked.first { $0.target.kind == .host }
    }

    /// Everything the detailed host panel does not already cover. Filtering
    /// by kind would silently drop a second or third machine off the widget
    /// entirely, which is the one thing a monitor must never do.
    var others: [TargetStatus] {
        ranked.filter { $0.id != hostStatus?.id }
    }

    var headline: String {
        if snapshot == nil { return "NOT\nSET UP" }
        if isStale { return "NO\nSIGNAL" }
        return displayState.display
    }

    /// One sentence covering everything the widget draws, for VoiceOver.
    /// A glanceable widget read out element by element is a jumble of
    /// numbers; the summary is the thing a person actually wanted.
    var spoken: String {
        guard let snapshot else { return "Beacon. Not set up yet." }
        if isStale { return "Beacon. Status unknown, Beacon is not running." }
        var parts = [displayState.headline]
        let counts = snapshot.counts
        if counts.critical > 0 { parts.append("\(counts.critical) down") }
        if counts.warning > 0 { parts.append("\(counts.warning) needing attention") }
        parts.append("\(counts.healthy) of \(counts.total) healthy")
        if let incident = snapshot.incidents.first {
            parts.append("\(incident.targetName), \(incident.summary)")
        }
        return "Beacon. " + parts.joined(separator: ". ") + "."
    }
}

/// The wordmark and freshness line every size carries, so the widget always
/// says what it is and how old its information is.
private struct Chrome: View {
    var entry: BeaconEntry

    var body: some View {
        HStack(spacing: 5) {
            Micro("Beacon", color: Ink.muted)
            Spacer(minLength: 2)
            AgeLabel(storedAt: entry.storedAt, stale: entry.isStale)
            StateDot(state: entry.displayState, size: 5)
        }
    }
}

/// How long the oldest open incident has been running, counting up rather
/// than frozen at whatever it read when the entry was built.
private struct IncidentClock: View {
    var incident: Incident
    var size: CGFloat

    var body: some View {
        Text(incident.startedAt, style: .timer)
            .font(.system(size: size, weight: .semibold, design: .monospaced))
            .foregroundStyle(Ink.alarm)
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
            Chrome(entry: entry)

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
            } else if entry.note == nil {
                Micro("waiting for data", color: Ink.muted)
            }

            Spacer(minLength: 4)

            // The footer carries the shape of the estate when there is
            // nothing to explain, and the explanation when there is. Swapping
            // rather than stacking keeps the column's height fixed.
            if let note = entry.note {
                Micro(note, color: Ink.caution)
                    .lineLimit(1)
                    .minimumScaleFactor(0.7)
            } else {
                HStack(spacing: 5) {
                    Micro("\(entry.ranked.count) watched")
                    Spacer(minLength: 2)
                    TargetTicks(states: entry.ranked.map(\.state), height: 8)
                        .frame(width: 38)
                }
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

                if let note = entry.note {
                    Micro(note, color: Ink.caution)
                        .lineLimit(2)
                        .fixedSize(horizontal: false, vertical: true)
                } else if let incident = entry.snapshot?.incidents.first {
                    VStack(alignment: .leading, spacing: 1) {
                        Micro("down for", color: Ink.faint)
                        IncidentClock(incident: incident, size: 15)
                    }
                } else if let host = entry.hostStatus {
                    VStack(spacing: 5) {
                        Meter(label: "cpu", value: host.metric(MetricKey.cpu) ?? 0)
                        Meter(label: "mem", value: host.metric(MetricKey.memory) ?? 0)
                    }
                }

                Spacer(minLength: 4)
                AgeLabel(storedAt: entry.storedAt, stale: entry.isStale)
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
            .opacity(entry.isStale ? 0.4 : 1)
        }
    }
}

// MARK: - Large

struct LargeWidgetView: View {
    var entry: BeaconEntry

    /// The slowest thing being watched, used to scale the latency bars.
    /// Scaling against the slowest rather than a fixed ceiling means the
    /// bars stay informative whether everything answers in 20ms or 2s.
    private var latencyCeiling: Double {
        max(1, entry.others.map(\.latencyMs).max() ?? 1)
    }

    /// An incident card costs roughly two rows worth of height, so the list
    /// gives that space back rather than pushing the footer off the bottom
    /// of the widget.
    private var rowLimit: Int {
        (entry.snapshot?.incidents.isEmpty ?? true) ? 6 : 4
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            Chrome(entry: entry)
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
                        // As much a claim about right now as the meters are.
                        .opacity(entry.isStale ? 0.4 : 1)
                }
            }
            .padding(.bottom, entry.note == nil ? 10 : 3)

            // "NO SIGNAL" says the widget cannot see; the note says why, which
            // is the difference between a puzzle and an instruction.
            if let note = entry.note {
                Micro(note, color: Ink.caution)
                    .padding(.bottom, 10)
            }

            if let incident = entry.snapshot?.incidents.first {
                VStack(alignment: .leading, spacing: 3) {
                    HStack {
                        Micro("incident #\(incident.id)", color: Ink.alarm)
                        Spacer()
                        IncidentClock(incident: incident, size: 11)
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
                Group {
                    Micro(host.target.name, color: Ink.muted)
                        .padding(.bottom, 7)
                    HStack(alignment: .top, spacing: 10) {
                        Meter(label: "cpu", value: host.metric(MetricKey.cpu) ?? 0)
                        Meter(label: "mem", value: host.metric(MetricKey.memory) ?? 0)
                        Meter(label: "ssd", value: host.metric(MetricKey.disk) ?? 0)
                    }
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
                    .padding(.bottom, 10)
                }
                .opacity(entry.isStale ? 0.4 : 1)
            }

            Hairline()
            Micro("targets", color: Ink.muted)
                .padding(.top, 8)
                .padding(.bottom, 2)

            VStack(alignment: .leading, spacing: 0) {
                ForEach(entry.others.prefix(rowLimit)) { item in
                    TargetDetailRow(status: item, ceiling: latencyCeiling)
                }
            }
            .opacity(entry.isStale ? 0.4 : 1)

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

/// A target on the large widget: name, its number, and a bar showing how it
/// compares with everything else being watched. The bar is what turns a list
/// of numbers into something readable at a glance, and it fills the row's
/// width with information rather than whitespace.
private struct TargetDetailRow: View {
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
                Text(status.reading(maxErrorLength: 16))
                    .font(.system(size: 11.5, weight: .semibold, design: .monospaced))
                    .foregroundStyle(status.error == nil ? Ink.paper : status.state.tint)
            }
            GeometryReader { geo in
                ZStack(alignment: .leading) {
                    Capsule().fill(Ink.track).frame(height: 2)
                    // A target with no latency to report (a machine, say)
                    // keeps the empty track so every row is the same height,
                    // but draws no fill it cannot justify.
                    if let fraction {
                        Capsule()
                            .fill(status.error == nil ? Ink.paper.opacity(0.75) : status.state.tint)
                            .frame(width: max(3, geo.size.width * fraction), height: 2)
                    }
                }
            }
            .frame(height: 2)
        }
        .padding(.vertical, 5)
    }

    private var fraction: Double? {
        if status.error != nil { return 1 }
        guard status.latencyMs > 0, ceiling > 0 else { return nil }
        return min(1, max(0.04, status.latencyMs / ceiling))
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
            Text(status.reading(maxErrorLength: 10))
                .font(.system(size: 11, weight: .medium, design: .monospaced))
                .foregroundStyle(status.error == nil ? Ink.muted : status.state.tint)
                .lineLimit(1)
        }
    }
}

/// Picks the layout for the family the system asked for.
struct BeaconWidgetView: View {
    @Environment(\.widgetFamily) private var family
    var entry: BeaconEntry

    var body: some View {
        layout
            // Read out as one sentence rather than as a run of loose numbers.
            .accessibilityElement(children: .ignore)
            .accessibilityLabel(entry.spoken)
            // A tap anywhere on the widget opens Beacon. This is the one
            // widgetURL for the whole thing rather than a Link per row:
            // WidgetKit views are a static snapshot with no real
            // interaction, and the widget names which target is wrong via
            // colour and text, not via which pixel was tapped.
            .widgetURL(DeepLink.open())
    }

    @ViewBuilder private var layout: some View {
        switch family {
        case .systemSmall: SmallWidgetView(entry: entry)
        case .systemLarge: LargeWidgetView(entry: entry)
        default: MediumWidgetView(entry: entry)
        }
    }
}
