import Foundation
import WidgetKit

/// One rendered moment of the widget.
///
/// Everything the widget claims is derived from `date` against `storedAt`,
/// never from a verdict reached when the timeline was built. That matters
/// because a timeline entry is rendered long after it is created: a "stale
/// or not" decision baked in at build time would keep insisting the data is
/// current for as long as the entry is on screen. Deriving it here instead
/// lets a single extra entry, placed at the moment the cache expires, flip
/// the widget to "no signal" exactly when that becomes true.
struct BeaconEntry: TimelineEntry {
    var date: Date
    var snapshot: Snapshot?
    /// When the app last wrote the snapshot. Nil when nothing is cached.
    var storedAt: Date?

    var age: TimeInterval? { storedAt.map { date.timeIntervalSince($0) } }

    var isStale: Bool { (age ?? .infinity) > CachedSnapshot.freshnessLimit }

    /// What the widget should actually show. When the data is stale or
    /// absent, the answer is "unknown", never the last healthy reading:
    /// a widget that says everything is fine because it cannot see
    /// anything is worse than one that admits it does not know.
    var displayState: HealthState {
        guard let snapshot, !isStale else { return .unknown }
        return snapshot.overall
    }

    /// Why the widget cannot vouch for what it is showing, if it cannot.
    var note: String? {
        // Short enough to survive the small widget's width; the app
        // itself carries the longer explanation.
        if snapshot == nil { return "open beacon to begin" }
        if isStale { return "beacon is not running" }
        return nil
    }
}
