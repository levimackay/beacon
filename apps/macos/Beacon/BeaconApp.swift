import ServiceManagement
import SwiftUI
import UserNotifications

/// Starts polling as soon as the process launches.
///
/// A MenuBarExtra's content view is not instantiated until the user opens
/// the panel, and a Window scene's is not instantiated until a window
/// opens, so hanging the poller off either one means a Beacon that has been
/// running since login has still never contacted the hub and the widget has
/// nothing to show. The work has to begin at launch, independently of
/// whether any view exists yet.
@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate, UNUserNotificationCenterDelegate {
    let poller = HubPoller()

    func applicationDidFinishLaunching(_ notification: Notification) {
        UNUserNotificationCenter.current().delegate = self
        poller.start()
        registerAsLoginItem()
    }

    /// Tapping a delivered notification should do what tapping the widget
    /// does: bring the dashboard up rather than just clearing the banner. A
    /// monitor that pages you and then leaves you to go find the app
    /// yourself has only done half the job.
    ///
    /// This re-opens Beacon through the same `beacon://` URL the widget
    /// uses, via NSWorkspace, rather than reaching for SwiftUI's
    /// `openWindow` action directly: `openWindow` can create the window
    /// without actually giving it a real screen position until the app is
    /// separately activated, which for an LSUIElement process with no
    /// window yet on screen reads as the notification having done nothing.
    /// Routing through the URL means there is exactly one place -
    /// `BeaconApp.onOpenURL` - that knows how to make the window actually
    /// appear, instead of two that could disagree.
    nonisolated func userNotificationCenter(
        _ center: UNUserNotificationCenter,
        didReceive response: UNNotificationResponse,
        withCompletionHandler completionHandler: @escaping () -> Void
    ) {
        let targetID = response.notification.request.content.userInfo["targetID"] as? String
        let url = targetID.map(DeepLink.target) ?? DeepLink.open()
        NSWorkspace.shared.open(url)
        completionHandler()
    }

    /// Without this, macOS suppresses the banner whenever Beacon's own
    /// window already has focus, which is exactly backwards for an app
    /// whose entire job is telling you about a change you were not already
    /// looking at.
    nonisolated func userNotificationCenter(
        _ center: UNUserNotificationCenter,
        willPresent notification: UNNotification,
        withCompletionHandler completionHandler: @escaping (UNNotificationPresentationOptions) -> Void
    ) {
        completionHandler([.banner, .sound])
    }

    /// Registers Beacon to launch at login.
    ///
    /// The widget shows what the app last fetched, so an app that is not
    /// running means a widget that goes stale within two minutes and then
    /// reports "unknown". A monitor you have to remember to start is one
    /// you will eventually forget to start, on the morning it mattered.
    ///
    /// Failure here is not worth interrupting the user over: the app still
    /// works, it just will not come back by itself, so it is logged and
    /// left alone.
    private func registerAsLoginItem() {
        guard SMAppService.mainApp.status != .enabled else { return }
        do {
            try SMAppService.mainApp.register()
        } catch {
            NSLog("Beacon: could not register as a login item: \(error.localizedDescription)")
        }
    }

    func applicationWillTerminate(_ notification: Notification) {
        poller.stop()
    }
}

@main
struct BeaconApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var delegate
    @State private var deepLink = DeepLinkRouter()

    var body: some Scene {
        MenuBarExtra {
            MenuBarPanel(poller: delegate.poller)
                .onAppear { delegate.poller.setActive(true) }
                .onDisappear { delegate.poller.setActive(false) }
        } label: {
            // The icon carries the state, so the answer to "is everything
            // okay" is available without any click at all.
            Image(systemName: iconName)
                .symbolRenderingMode(.hierarchical)
        }
        .menuBarExtraStyle(.window)

        Window("Beacon", id: "main") {
            DashboardView(poller: delegate.poller, deepLink: deepLink)
                // A View modifier, not a Scene one - SwiftUI only exposes
                // onOpenURL on views, so it only fires once this window's
                // content actually exists. `handlesExternalEvents` below is
                // what makes that happen for a widget tap that arrives
                // while the window has never been opened.
                .onOpenURL { url in
                    deepLink.handle(url)
                    // SwiftUI has no public API to pop open a
                    // MenuBarExtra's own dropdown from outside a click on
                    // the status item, so "open the panel" from here means
                    // this window instead - the same information "with
                    // room to breathe" per DashboardView's own doc
                    // comment. Activating is required, not optional: an
                    // LSUIElement process has no Dock icon, and a widget or
                    // notification tap launches it with no window on
                    // screen and nothing to click afterward, so without
                    // this the window exists but never gets a real frame
                    // or comes forward - the click reads as having done
                    // nothing at all.
                    NSApp.activate(ignoringOtherApps: true)
                }
        }
        .defaultSize(width: 720, height: 520)
        // Tells SwiftUI this is the scene to open (or wake) for an
        // external event such as a `beacon://` URL, including when the
        // window has never been opened this run. Without it, onOpenURL
        // above never fires for a cold widget tap: there is no window yet
        // whose content could receive it.
        .handlesExternalEvents(matching: ["*"])
    }

    private var iconName: String {
        let poller = delegate.poller
        guard poller.failureMessage == nil, let snapshot = poller.snapshot else {
            return "bolt.horizontal.circle"
        }
        switch snapshot.overall {
        case .healthy: return "bolt.horizontal.circle.fill"
        case .warning: return "exclamationmark.triangle.fill"
        case .down: return "xmark.octagon.fill"
        case .unknown: return "bolt.horizontal.circle"
        }
    }
}
