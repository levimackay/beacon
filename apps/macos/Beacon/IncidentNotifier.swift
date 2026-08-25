import Foundation
import UserNotifications

/// Turns a change in the hub's confirmed incidents into a native
/// notification.
///
/// The hub already runs every sample through a flap-suppressing state
/// machine (internal/incident) before an incident row is ever created, so
/// an incident id appearing in or dropping out of `openIncidents` already
/// IS the transition. This type only diffs two incident sets; it never
/// re-derives "did anything change" from raw target state, because doing
/// that here too would give Beacon two different opinions about what counts
/// as a real change, and the client's opinion is not the one with the
/// confirmation window.
@MainActor
final class IncidentNotifier {
    /// The open incidents we last told the user about, by id.
    ///
    /// This is compared against on every call, never against whatever
    /// snapshot happened to be "current" a moment ago on some other stack:
    /// the menu bar panel opening and the poll timer can both trigger a
    /// refresh for nearly the same instant, and `handle` has no `await` in
    /// it, so MainActor runs the two calls fully one after another. The
    /// second one sees exactly what the first just announced and finds
    /// nothing new, instead of paging the user twice for one transition.
    ///
    /// Nil until the first snapshot arrives, and that first snapshot only
    /// seeds this dictionary silently. Without that, every relaunch would
    /// read every already-open incident as a brand new transition, which
    /// is the same over-notifying failure the hub itself avoids by
    /// persisting incident state across its own restarts.
    private var known: [Int: Incident]?

    func handle(_ snapshot: Snapshot) {
        let current = Dictionary(uniqueKeysWithValues: snapshot.incidents.map { ($0.id, $0) })
        defer { known = current }

        guard let known else { return }

        for (id, incident) in current where known[id] == nil {
            notify(opened: incident)
        }
        for (id, incident) in known where current[id] == nil {
            notify(recovered: incident)
        }
    }

    private func notify(opened incident: Incident) {
        let verb: String
        switch incident.state {
        case .down: verb = "is down"
        case .warning: verb = "needs attention"
        case .unknown: verb = "can't be reached"
        case .healthy: return // the hub never opens an incident for healthy
        }
        send(
            title: "\(incident.targetName) \(verb)",
            body: incident.summary.isEmpty ? "No further detail from the hub." : sentence(incident.summary),
            targetID: incident.targetId)
    }

    private func notify(recovered incident: Incident) {
        var body = "Back to healthy."
        if !incident.summary.isEmpty {
            // Lowercase and mid-sentence, unlike the opened case: this is
            // "after connection refused", not a sentence of its own.
            let reason = incident.summary.hasSuffix(".") ? String(incident.summary.dropLast()) : incident.summary
            body = "Back to healthy after \(reason)."
        }
        send(title: "\(incident.targetName) recovered", body: body, targetID: incident.targetId)
    }

    /// The hub's error strings are lowercase fragments meant to sit next to
    /// a state label ("connection refused"), not stand alone. Read out by
    /// VoiceOver as the whole of a notification body, an uncapitalized
    /// fragment reads as broken speech rather than a sentence.
    private func sentence(_ s: String) -> String {
        guard let first = s.first else { return s }
        return first.uppercased() + s.dropFirst() + (s.hasSuffix(".") ? "" : ".")
    }

    private func send(title: String, body: String, targetID: String) {
        guard NotificationSettings.isEnabled else { return }
        Task {
            guard await Self.isAuthorized() else { return }
            let content = UNMutableNotificationContent()
            content.title = title
            content.body = body
            content.sound = .default
            content.userInfo = ["targetID": targetID]
            let request = UNNotificationRequest(identifier: UUID().uuidString, content: content, trigger: nil)
            try? await UNUserNotificationCenter.current().add(request)
        }
    }

    /// Asks for permission the moment there is an actual notification to
    /// show, rather than at launch: a Mac that never has an incident should
    /// never see the system prompt at all. A prior refusal is remembered by
    /// macOS itself and never re-prompts, so treating `.denied` as "send
    /// nothing" here is permanent by construction, not something that needs
    /// its own re-ask throttle.
    static func isAuthorized() async -> Bool {
        let center = UNUserNotificationCenter.current()
        switch await center.notificationSettings().authorizationStatus {
        case .authorized, .provisional:
            return true
        case .notDetermined:
            return (try? await center.requestAuthorization(options: [.alert, .sound])) ?? false
        default:
            return false
        }
    }

    /// Called when the user flips the setting on from the panel, so the
    /// system prompt (if one is still owed) appears at that deliberate
    /// moment instead of silently waiting for the first real incident.
    static func requestAuthorizationIfNeeded() {
        Task { _ = await isAuthorized() }
    }
}
