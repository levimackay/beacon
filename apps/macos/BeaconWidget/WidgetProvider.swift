import SwiftUI
import WidgetKit

struct BeaconProvider: TimelineProvider {
    func placeholder(in context: Context) -> BeaconEntry {
        entry(at: Date())
    }

    func getSnapshot(in context: Context, completion: @escaping (BeaconEntry) -> Void) {
        completion(entry(at: Date()))
    }

    func getTimeline(in context: Context, completion: @escaping (Timeline<BeaconEntry>) -> Void) {
        let now = Date()
        let current = entry(at: now)
        var entries = [current]

        // WidgetKit renders one entry and leaves it up until the next one is
        // due, so a widget with a single entry goes on asserting the data is
        // current for minutes after the app has died. A second entry placed
        // at the exact moment the cache expires costs nothing and makes the
        // widget admit it the second it is true.
        if let storedAt = current.storedAt, !current.isStale {
            let expiry = storedAt.addingTimeInterval(CachedSnapshot.freshnessLimit + 1)
            entries.append(entry(at: expiry))
        }

        // Refreshed often enough to be worth glancing at, rarely enough that
        // WidgetKit keeps honouring the requests. Nothing is arriving while
        // the app is down, so back off rather than burning the budget.
        let next = now.addingTimeInterval(current.isStale ? 900 : 300)
        completion(Timeline(entries: entries, policy: .after(next)))
    }

    /// Reads the snapshot the app cached into the shared container.
    ///
    /// The widget deliberately does not talk to the hub itself. It is
    /// sandboxed, so it cannot read the hub's token, and copying that
    /// token somewhere the widget could read would widen the blast radius
    /// of the one secret the whole security model rests on. The app polls;
    /// the widget reports what the app last saw, and says plainly how old
    /// that is.
    private func entry(at date: Date) -> BeaconEntry {
        let cached = SnapshotCache.read()
        return BeaconEntry(date: date, snapshot: cached?.snapshot, storedAt: cached?.storedAt)
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
