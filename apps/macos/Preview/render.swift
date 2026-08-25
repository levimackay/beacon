import AppKit
import SwiftUI

/// Renders the widget views to PNG at the real widget point sizes, so the
/// design can be looked at without placing anything on a desktop.
@MainActor
func render<V: View>(_ view: V, size: CGSize, to path: String) {
    // Matches what WidgetKit puts behind the view via containerBackground,
    // so a preview is not prettier or uglier than the real thing.
    let host = ZStack {
        Ink.background
        view.padding(16)
    }
    .frame(width: size.width, height: size.height)
    .environment(\.colorScheme, .dark)

    let renderer = ImageRenderer(content: host)
    renderer.scale = 2
    guard let image = renderer.nsImage,
          let tiff = image.tiffRepresentation,
          let rep = NSBitmapImageRep(data: tiff),
          let png = rep.representation(using: .png, properties: [:]) else {
        print("render failed for \(path)")
        return
    }
    try? png.write(to: URL(fileURLWithPath: path))
    print("wrote \(path)")
}

func sampleEntry(state: HealthState = .healthy, withIncident: Bool = false) -> BeaconEntry {
    let now = Date()
    func target(_ name: String, _ kind: TargetKind, _ st: HealthState,
                _ latency: Double, _ metrics: [String: Double], _ err: String? = nil) -> TargetStatus {
        TargetStatus(
            target: MonitoredTarget(id: name, kind: kind, name: name, address: "https://\(name.lowercased()).com",
                                    intervalSeconds: 60, expectStatus: 200, enabled: true, allowPrivate: false),
            state: st, latencyMs: latency, metrics: metrics, lastCheck: now, error: err, certExpiry: nil)
    }

    var targets = [
        target("Mac.localdomain", .host, .healthy, 0,
               [MetricKey.cpu: 29.8, MetricKey.memory: 71.9, MetricKey.disk: 82.3,
                MetricKey.temperature: 52, MetricKey.uptime: 1_586_000, MetricKey.load1: 2.1]),
        target("Portfolio", .website, .healthy, 91, [MetricKey.certDaysLeft: 85]),
        target("GitHub", .website, .healthy, 125, [MetricKey.certDaysLeft: 36]),
        target("GitHub API", .website, .healthy, 275, [MetricKey.certDaysLeft: 35]),
    ]
    var incidents: [Incident] = []

    if withIncident {
        targets[1] = target("Portfolio", .website, .down, 0, [:], "connection refused")
        incidents = [Incident(id: 104, targetId: "Portfolio", targetName: "Portfolio", state: .down,
                              startedAt: now.addingTimeInterval(-253), resolvedAt: nil,
                              summary: "connection refused")]
    }

    let counts = Counts(critical: withIncident ? 1 : 0, warning: 0,
                        healthy: withIncident ? 3 : 4, unknown: 0)
    let snapshot = Snapshot(
        generatedAt: now, overall: withIncident ? .down : state,
        hub: HubInfo(version: "0.1.0", host: "Mac", os: "darwin 26.5", kernel: "25.5.0",
                     startedAt: now.addingTimeInterval(-8000), uptimeSeconds: 8000),
        counts: counts, targets: targets, openIncidents: incidents)
    return BeaconEntry(date: now, snapshot: snapshot, storedAt: now.addingTimeInterval(-12))
}

@main
struct PreviewMain {
    @MainActor
    static func main() {
        let small = CGSize(width: 158, height: 158)
        let medium = CGSize(width: 338, height: 158)
        let large = CGSize(width: 338, height: 354)
        let outDir = CommandLine.arguments.count > 1 ? CommandLine.arguments[1] : "."





            let ok = sampleEntry()
            let bad = sampleEntry(withIncident: true)
            let stale = BeaconEntry(date: Date(), snapshot: ok.snapshot,
                                    storedAt: Date().addingTimeInterval(-900))
            let unset = BeaconEntry(date: Date(), snapshot: nil, storedAt: nil)

            render(SmallWidgetView(entry: ok), size: small, to: "\(outDir)/small-healthy.png")
            render(SmallWidgetView(entry: bad), size: small, to: "\(outDir)/small-down.png")
            render(SmallWidgetView(entry: stale), size: small, to: "\(outDir)/small-stale.png")
            render(MediumWidgetView(entry: ok), size: medium, to: "\(outDir)/medium-healthy.png")
            render(MediumWidgetView(entry: bad), size: medium, to: "\(outDir)/medium-down.png")
            render(LargeWidgetView(entry: ok), size: large, to: "\(outDir)/large-healthy.png")
            render(LargeWidgetView(entry: bad), size: large, to: "\(outDir)/large-down.png")
            render(MediumWidgetView(entry: stale), size: medium, to: "\(outDir)/medium-stale.png")
            render(LargeWidgetView(entry: stale), size: large, to: "\(outDir)/large-stale.png")
            render(SmallWidgetView(entry: unset), size: small, to: "\(outDir)/small-unset.png")
    }
}
