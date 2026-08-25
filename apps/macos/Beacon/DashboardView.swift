import SwiftUI

/// The full window. Deliberately the same information as the panel with
/// room to breathe, rather than a second, denser product: anything that
/// only exists here would be something the two-second glance cannot tell
/// you, and that is the glance's problem to fix, not the window's.
struct DashboardView: View {
    var poller: HubPoller

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 26) {
                overview
                if let snapshot = poller.snapshot {
                    if !snapshot.incidents.isEmpty {
                        section("Open incidents") {
                            ForEach(snapshot.incidents) { incident in
                                IncidentCard(incident: incident)
                            }
                        }
                    }
                    section("Devices") { rows(snapshot.targets(ofKind: .host)) }
                    section("Websites") { rows(snapshot.targets(ofKind: .website)) }
                    section("Services") { rows(snapshot.targets(ofKind: .service)) }
                }
            }
            .padding(28)
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .background(.background)
        .onAppear { poller.setActive(true) }
        .onDisappear { poller.setActive(false) }
    }

    private var overview: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 11) {
                StateDot(state: poller.failureMessage != nil ? .unknown : (poller.snapshot?.overall ?? .unknown), size: 13)
                Text(poller.failureMessage != nil ? "Connection unavailable" : (poller.snapshot?.overall.headline ?? "Connecting"))
                    .font(.system(size: 26, weight: .semibold))
            }
            if let failure = poller.failureMessage {
                Text(failure)
                    .font(.system(size: 12))
                    .foregroundStyle(.secondary)
            } else if let snapshot = poller.snapshot {
                Text("\(snapshot.counts.critical) critical · \(snapshot.counts.warning) warning · \(snapshot.counts.healthy) healthy · \(snapshot.counts.unknown) unknown")
                    .font(.system(size: 12))
                    .foregroundStyle(.secondary)
            }
            if let updated = poller.lastUpdate {
                Text("Updated \(Format.age(Date().timeIntervalSince(updated))) ago")
                    .font(.system(size: 11))
                    .foregroundStyle(.tertiary)
            }
        }
    }

    @ViewBuilder
    private func section<Content: View>(_ title: String, @ViewBuilder content: () -> Content) -> some View {
        VStack(alignment: .leading, spacing: 10) {
            Text(title.uppercased())
                .font(.system(size: 10, weight: .semibold))
                .tracking(0.7)
                .foregroundStyle(.tertiary)
            content()
        }
    }

    @ViewBuilder
    private func rows(_ items: [TargetStatus]) -> some View {
        if items.isEmpty {
            Text("None configured")
                .font(.system(size: 12))
                .foregroundStyle(.tertiary)
        } else {
            VStack(spacing: 0) {
                ForEach(Array(items.enumerated()), id: \.element.id) { index, item in
                    if index > 0 { Divider() }
                    DashboardRow(status: item)
                }
            }
            .background(.quaternary.opacity(0.28), in: RoundedRectangle(cornerRadius: 8))
        }
    }
}

struct DashboardRow: View {
    var status: TargetStatus

    var body: some View {
        HStack(spacing: 12) {
            StateDot(state: status.state, size: 9)
            VStack(alignment: .leading, spacing: 2) {
                Text(status.target.name)
                    .font(.system(size: 13, weight: .medium))
                Text(status.target.address.isEmpty ? status.target.kind.rawValue : status.target.address)
                    .font(.system(size: 11))
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
            Spacer(minLength: 12)
            if let error = status.error, !error.isEmpty {
                Text(error)
                    .font(.system(size: 11))
                    .foregroundStyle(status.state.tint)
                    .lineLimit(1)
            } else {
                HStack(spacing: 16) {
                    ForEach(metrics, id: \.label) { metric in
                        VStack(alignment: .trailing, spacing: 1) {
                            Text(metric.value)
                                .font(.system(size: 12, design: .monospaced))
                            Text(metric.label)
                                .font(.system(size: 9))
                                .foregroundStyle(.tertiary)
                        }
                    }
                }
            }
        }
        .padding(.horizontal, 13)
        .padding(.vertical, 10)
    }

    private var metrics: [(label: String, value: String)] {
        switch status.target.kind {
        case .host:
            var out: [(String, String)] = []
            if let v = status.metric(MetricKey.cpu) { out.append(("cpu", Format.percent(v))) }
            if let v = status.metric(MetricKey.memory) { out.append(("memory", Format.percent(v))) }
            if let v = status.metric(MetricKey.disk) { out.append(("disk", Format.percent(v))) }
            if let v = status.metric(MetricKey.temperature) { out.append(("temp", "\(Int(v))°C")) }
            if let v = status.metric(MetricKey.uptime) { out.append(("uptime", Format.age(v))) }
            return out
        case .website, .service:
            var out: [(String, String)] = []
            if status.latencyMs > 0 { out.append(("latency", Format.latency(status.latencyMs))) }
            if let v = status.metric(MetricKey.certDaysLeft) { out.append(("cert", "\(Int(v))d")) }
            return out
        }
    }
}

struct IncidentCard: View {
    var incident: Incident

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            StateDot(state: incident.state, size: 9)
            VStack(alignment: .leading, spacing: 3) {
                Text("#\(incident.id)  \(incident.targetName)")
                    .font(.system(size: 13, weight: .medium))
                Text(incident.summary)
                    .font(.system(size: 11))
                    .foregroundStyle(.secondary)
                Text(incident.startedAt.formatted(date: .abbreviated, time: .shortened))
                    .font(.system(size: 10))
                    .foregroundStyle(.tertiary)
            }
            Spacer(minLength: 12)
            Text(Format.duration(incident.duration(now: Date())))
                .font(.system(size: 12, design: .monospaced))
                .foregroundStyle(incident.state.tint)
        }
        .padding(13)
        .background(incident.state.tint.opacity(0.08), in: RoundedRectangle(cornerRadius: 8))
    }
}
