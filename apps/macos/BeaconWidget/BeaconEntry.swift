import Foundation
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
