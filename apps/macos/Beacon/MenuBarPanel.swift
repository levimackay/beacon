import SwiftUI

/// The compact panel behind the menu bar icon. It answers one question,
/// "is everything okay", and then gives the detail behind that answer
/// without becoming a window.
struct MenuBarPanel: View {
    var poller: HubPoller

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            header
            Divider()

            if let snapshot = poller.snapshot {
                ScrollView {
                    VStack(alignment: .leading, spacing: 14) {
                        if !snapshot.incidents.isEmpty {
                            incidents(snapshot.incidents)
                        }
                        group("Devices", snapshot.targets(ofKind: .host))
                        group("Websites", snapshot.targets(ofKind: .website))
                        group("Services", snapshot.targets(ofKind: .service))
                    }
                    .padding(14)
                }
                .frame(maxHeight: 380)
            } else {
                empty
            }

            Divider()
            footer
        }
        .frame(width: 300)
    }

    private var header: some View {
        HStack(spacing: 9) {
            StateDot(state: displayState, size: 10)
            VStack(alignment: .leading, spacing: 1) {
                Text(headline)
                    .font(.system(size: 13, weight: .semibold))
                if let detail = subhead {
                    Text(detail)
                        .font(.system(size: 11))
                        .foregroundStyle(.secondary)
                }
            }
            Spacer()
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 12)
    }

    /// When the hub is unreachable the panel reports that, rather than the
    /// health of whatever was last seen. Presenting old data as current is
    /// the one thing a monitor must never do.
    private var displayState: HealthState {
        poller.failureMessage != nil ? .unknown : (poller.snapshot?.overall ?? .unknown)
    }

    private var headline: String {
        if poller.failureMessage != nil { return "Connection unavailable" }
        return poller.snapshot?.overall.headline ?? "Connecting"
    }

    private var subhead: String? {
        if let failure = poller.failureMessage {
            guard let updated = poller.lastUpdate else { return failure }
            return "Last updated \(Format.age(Date().timeIntervalSince(updated))) ago"
        }
        guard let snapshot = poller.snapshot else { return nil }
        var parts: [String] = []
        if snapshot.counts.critical > 0 { parts.append("\(snapshot.counts.critical) critical") }
        if snapshot.counts.warning > 0 { parts.append("\(snapshot.counts.warning) warning") }
        if snapshot.counts.healthy > 0 { parts.append("\(snapshot.counts.healthy) healthy") }
        if snapshot.counts.unknown > 0 { parts.append("\(snapshot.counts.unknown) unknown") }
        return parts.isEmpty ? "Nothing monitored yet" : parts.joined(separator: ", ")
    }

    private var empty: some View {
        VStack(alignment: .leading, spacing: 6) {
            Text(poller.failureMessage ?? "Waiting for the hub")
                .font(.system(size: 12))
            Text("Start it with: beaconhub install")
                .font(.system(size: 11, design: .monospaced))
                .foregroundStyle(.secondary)
                .textSelection(.enabled)
        }
        .padding(14)
    }

    @ViewBuilder
    private func group(_ title: String, _ items: [TargetStatus]) -> some View {
        if !items.isEmpty {
            VStack(alignment: .leading, spacing: 7) {
                Text(title.uppercased())
                    .font(.system(size: 10, weight: .semibold))
                    .foregroundStyle(.tertiary)
                    .tracking(0.6)
                ForEach(items) { item in
                    TargetRow(status: item)
                }
            }
        }
    }

    @ViewBuilder
    private func incidents(_ items: [Incident]) -> some View {
        VStack(alignment: .leading, spacing: 7) {
            Text("OPEN INCIDENTS")
                .font(.system(size: 10, weight: .semibold))
                .foregroundStyle(.tertiary)
                .tracking(0.6)
            ForEach(items) { incident in
                HStack(alignment: .firstTextBaseline, spacing: 8) {
                    StateDot(state: incident.state)
                    VStack(alignment: .leading, spacing: 2) {
                        Text(incident.targetName)
                            .font(.system(size: 12, weight: .medium))
                        Text(incident.summary)
                            .font(.system(size: 11))
                            .foregroundStyle(.secondary)
                            .lineLimit(2)
                    }
                    Spacer(minLength: 6)
                    Text(Format.duration(incident.duration(now: Date())))
                        .font(.system(size: 11, design: .monospaced))
                        .foregroundStyle(incident.state.tint)
                }
            }
        }
    }

    private var footer: some View {
        HStack(spacing: 10) {
            if let updated = poller.lastUpdate {
                Text("Updated \(Format.age(Date().timeIntervalSince(updated))) ago")
                    .font(.system(size: 11))
                    .foregroundStyle(.secondary)
            }
            Spacer()
            Button("Refresh") { Task { await poller.refresh() } }
                .buttonStyle(.accessoryBar)
            Button("Quit") { NSApplication.shared.terminate(nil) }
                .buttonStyle(.accessoryBar)
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 8)
    }
}

/// One target: its state, its name, and the numbers that matter for its
/// kind. Nothing else earns a place on a 300pt-wide panel.
struct TargetRow: View {
    var status: TargetStatus

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 8) {
            StateDot(state: status.state)
            Text(status.target.name)
                .font(.system(size: 12))
                .lineLimit(1)
            Spacer(minLength: 8)
            Text(detail)
                .font(.system(size: 11, design: .monospaced))
                .foregroundStyle(status.error == nil ? .secondary : status.state.tint)
                .lineLimit(1)
                .truncationMode(.tail)
        }
        .help(status.error ?? status.target.address)
    }

    private var detail: String {
        if let error = status.error, !error.isEmpty { return error }
        switch status.target.kind {
        case .host:
            var parts: [String] = []
            if let cpu = status.metric(MetricKey.cpu) { parts.append("cpu \(Format.percent(cpu))") }
            if let mem = status.metric(MetricKey.memory) { parts.append("mem \(Format.percent(mem))") }
            if let disk = status.metric(MetricKey.disk) { parts.append("disk \(Format.percent(disk))") }
            return parts.isEmpty ? "no data yet" : parts.joined(separator: "  ")
        case .website, .service:
            var parts: [String] = []
            if status.latencyMs > 0 { parts.append(Format.latency(status.latencyMs)) }
            if let days = status.metric(MetricKey.certDaysLeft) { parts.append("cert \(Int(days))d") }
            return parts.isEmpty ? "no data yet" : parts.joined(separator: "  ")
        }
    }
}
