import Foundation
import Observation
import WidgetKit

/// Polls the hub and publishes the result to the UI, the cache and the
/// widget.
///
/// Polling rate is deliberately tied to whether anyone is looking. A menu
/// bar app that hammers a local HTTP server every second while its panel is
/// closed is exactly the kind of background cost that gets an app deleted,
/// and the hub is already sampling on its own schedule regardless.
@Observable
@MainActor
final class HubPoller {
    enum Status: Equatable {
        case waiting
        case live(Snapshot)
        case failing(String, last: Snapshot?)
    }

    private(set) var status: Status = .waiting
    private(set) var lastUpdate: Date?

    /// Interval used while the panel or a window is visible.
    private let activeInterval: TimeInterval = 15
    /// Interval used while nothing is on screen.
    private let idleInterval: TimeInterval = 60

    private var task: Task<Void, Never>?
    private var isActive = false

    var snapshot: Snapshot? {
        switch status {
        case .live(let s): s
        case .failing(_, let last): last
        case .waiting: nil
        }
    }

    var failureMessage: String? {
        if case .failing(let message, _) = status { return message }
        return nil
    }

    func start() {
        guard task == nil else { return }
        // Show whatever the last run left behind straight away, so opening
        // the panel never begins with an empty box.
        if let cached = SnapshotCache.read() {
            status = .live(cached.snapshot)
            lastUpdate = cached.storedAt
        }
        task = Task { [weak self] in
            while !Task.isCancelled {
                await self?.refresh()
                let interval = await self?.currentInterval ?? 60
                try? await Task.sleep(for: .seconds(interval))
            }
        }
    }

    func stop() {
        task?.cancel()
        task = nil
    }

    private var currentInterval: TimeInterval { isActive ? activeInterval : idleInterval }

    func setActive(_ active: Bool) {
        let wasActive = isActive
        isActive = active
        if active && !wasActive {
            Task { await refresh() }
        }
    }

    func refresh() async {
        guard let client = HubClient() else {
            status = .failing("Beacon is not set up on this Mac yet.", last: snapshot)
            return
        }
        do {
            let snapshot = try await client.snapshot()
            status = .live(snapshot)
            lastUpdate = Date()
            SnapshotCache.write(snapshot)
            WidgetCenter.shared.reloadAllTimelines()
        } catch {
            let message = (error as? HubError)?.errorDescription ?? error.localizedDescription
            status = .failing(message, last: snapshot)
        }
    }
}
