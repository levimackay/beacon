import Foundation

/// The `beacon://` URL scheme that ties the widget and notifications back
/// to the app. Every caller - the widget, and Beacon itself re-opening its
/// own window from a notification tap - goes through this one scheme
/// instead of each inventing its own way to say "show me", so there is
/// exactly one code path (`BeaconApp.onOpenURL`) that has to know how to
/// actually surface the window.
enum DeepLink {
    static let scheme = "beacon"
    private static let targetQueryItem = "target"

    /// Opens the app without naming anything in particular. This is the
    /// widget's tap target: WidgetKit views have no real interaction, so a
    /// tap anywhere on one just means "bring Beacon up".
    static func open() -> URL {
        URL(string: "\(scheme)://open")!
    }

    /// Opens the app naming one target, so the dashboard can scroll to and
    /// highlight it. Used when a notification is tapped: the notification
    /// already knows which target it was about, and routing that back
    /// through the same URL scheme Beacon re-opens itself with means there
    /// is still only one place that decides what "open the dashboard"
    /// does.
    static func target(_ id: String) -> URL {
        var components = URLComponents()
        components.scheme = scheme
        components.host = "open"
        components.queryItems = [URLQueryItem(name: targetQueryItem, value: id)]
        // Force-unwrapped: every part above is a fixed literal or a
        // percent-encoded query item, which URLComponents always resolves.
        return components.url!
    }

    static func targetID(from url: URL) -> String? {
        guard url.scheme == scheme else { return nil }
        return URLComponents(url: url, resolvingAgainstBaseURL: false)?
            .queryItems?.first(where: { $0.name == targetQueryItem })?.value
    }
}
