import SwiftUI
import WidgetKit

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

@main
struct BeaconWidget: Widget {
    var body: some WidgetConfiguration {
        StaticConfiguration(kind: "com.levimackay.beacon.widget", provider: BeaconProvider()) { entry in
            BeaconWidgetView(entry: entry)
                // A fixed near-black ground rather than the system fill:
                // the design is monochrome so that colour means a problem,
                // and a background that shifts with the desktop tint would
                // undo that.
                .containerBackground(Ink.background, for: .widget)
        }
        .configurationDisplayName("Beacon")
        .description("Whether your machines, services and websites are healthy.")
        .supportedFamilies([.systemSmall, .systemMedium, .systemLarge])
    }
}
