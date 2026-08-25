import Foundation

/// The one flag that turns incident notifications off.
///
/// Backed by UserDefaults directly rather than a bespoke settings file: it
/// is the one persistence mechanism already idiomatic for a single
/// lightweight preference on macOS, and keeping the key here as a constant
/// means the panel's `@AppStorage` toggle and `IncidentNotifier` (a plain
/// object, not a view) read and write the exact same flag instead of two
/// that could drift apart.
enum NotificationSettings {
    static let key = "notificationsEnabled"

    /// Defaults to on: a monitor that never tells you anything is a
    /// dashboard, which is the exact problem this feature exists to fix.
    /// Turning that off is something the user does on purpose.
    static var isEnabled: Bool {
        UserDefaults.standard.object(forKey: key) as? Bool ?? true
    }
}
