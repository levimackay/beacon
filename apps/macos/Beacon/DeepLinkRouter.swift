import Foundation
import Observation

/// One request to jump to a target, tagged with a fresh id every time.
///
/// Storing only the target id would mean clicking the same widget row twice
/// in a row looks like "no change" to SwiftUI's `onChange`, so the second
/// click would silently do nothing. Equatable so a view can react to it.
struct DeepLinkRequest: Equatable {
    var id = UUID()
    var targetID: String?
}

/// What the last `beacon://` URL that reached the app named, if anything.
/// `BeaconApp.onOpenURL` writes it; `DashboardView` reads it to scroll to
/// and briefly highlight the named target.
@Observable
@MainActor
final class DeepLinkRouter {
    private(set) var request: DeepLinkRequest?

    func handle(_ url: URL) {
        guard url.scheme == DeepLink.scheme else { return }
        request = DeepLinkRequest(targetID: DeepLink.targetID(from: url))
    }
}
