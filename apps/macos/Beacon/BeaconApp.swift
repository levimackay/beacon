import CoreServices // keyDirectObject: no Swift-native equivalent exists for it.
import ServiceManagement
import SwiftUI
import UserNotifications

/// The Carbon symbols for the "open URL" Apple Event (kInternetEventClass,
/// kAEGetURLEvent) were dropped from the SDK's Swift-visible headers even
/// though the event itself is alive and well - LaunchServices still sends
/// exactly this event to open a registered URL scheme. 'GURL' for both is
/// not a guess: it is the FourCharCode Internet Config defined for this
/// event decades ago and every URL-handling app still keys off, headers or
/// not.
private let openURLEventClass: AEEventClass = 0x4755_524C // 'GURL'
private let openURLEventID: AEEventID = 0x4755_524C // 'GURL'

/// Starts polling as soon as the process launches.
///
/// A MenuBarExtra's content view is not instantiated until the user opens
/// the panel, and a Window scene's is not instantiated until a window
/// opens, so hanging the poller off either one means a Beacon that has been
/// running since login has still never contacted the hub and the widget has
/// nothing to show. The work has to begin at launch, independently of
/// whether any view exists yet.
@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate, NSWindowDelegate, UNUserNotificationCenterDelegate {
    let poller = HubPoller()
    let deepLink = DeepLinkRouter()
    private var dashboardWindow: NSWindow?

    /// Registered here rather than in `applicationDidFinishLaunching`: a
    /// `beacon://` tap that launches Beacon cold delivers its Apple Event
    /// before `didFinishLaunching` runs, and an event with no handler
    /// installed yet is dropped, not queued for later.
    ///
    /// This replaces SwiftUI's `Window` + `.handlesExternalEvents` +
    /// `.onOpenURL`, which is what the widget click was built on before and
    /// which turned out not to work against the real installed app: that
    /// stack only promises to fire once some Window scene's content view
    /// exists, and for an already-running LSUIElement process whose window
    /// has never opened this run, verified behavior was no window
    /// appearing at all, not a mispositioned one. `NSAppleEventManager` is
    /// the actual mechanism LaunchServices and `NSWorkspace.open` deliver a
    /// custom-scheme URL through underneath that SwiftUI abstraction, so
    /// handling it here does not depend on any scene ever having been
    /// opened.
    func applicationWillFinishLaunching(_ notification: Notification) {
        NSAppleEventManager.shared().setEventHandler(
            self,
            andSelector: #selector(handleGetURL(_:withReplyEvent:)),
            forEventClass: openURLEventClass,
            andEventID: openURLEventID)
    }

    func applicationDidFinishLaunching(_ notification: Notification) {
        UNUserNotificationCenter.current().delegate = self
        poller.start()
        registerAsLoginItem()
    }

    @objc private func handleGetURL(_ event: NSAppleEventDescriptor, withReplyEvent: NSAppleEventDescriptor) {
        guard let string = event.paramDescriptor(forKeyword: keyDirectObject)?.stringValue,
              let url = URL(string: string) else {
            NSLog("Beacon: got a GetURL event with no usable URL: \(event)")
            return
        }
        deepLink.handle(url)
        presentDashboard()
    }

    /// Builds and shows the dashboard window by hand rather than asking a
    /// SwiftUI `Window` scene to do it. That scene is what silently failed
    /// in the first place, so trusting it again for presentation - even
    /// with URL delivery fixed above - would leave the same failure mode
    /// sitting underneath. One instance is kept and reused rather than
    /// built fresh per call, so a second tap while the window is already
    /// open re-fronts and re-highlights it instead of stacking a duplicate.
    private func presentDashboard() {
        if dashboardWindow == nil {
            let window = NSWindow(
                contentRect: NSRect(x: 0, y: 0, width: 720, height: 520),
                styleMask: [.titled, .closable, .miniaturizable, .resizable],
                backing: .buffered,
                defer: false)
            window.title = "Beacon"
            window.center()
            // Otherwise closing the window deallocates it, and the next
            // beacon:// tap would rebuild DashboardView from scratch,
            // losing whatever poll state it was showing for no reason.
            window.isReleasedWhenClosed = false
            window.delegate = self
            window.contentView = NSHostingView(rootView: DashboardView(poller: poller, deepLink: deepLink))
            dashboardWindow = window
        }
        // An LSUIElement process is background-only by policy, and a
        // background-only app's window can be created and even ordered
        // front without ever taking key status or showing up for Cmd-Tab -
        // it just sits behind whatever the user was already looking at,
        // which is indistinguishable from the click having done nothing.
        // Restored to .accessory in windowWillClose, so Beacon goes back to
        // having no Dock icon once the dashboard is dismissed again.
        NSApp.setActivationPolicy(.regular)
        NSApp.activate(ignoringOtherApps: true)
        dashboardWindow?.makeKeyAndOrderFront(nil)
    }

    func windowWillClose(_ notification: Notification) {
        NSApp.setActivationPolicy(.accessory)
    }

    /// Tapping a delivered notification should do what tapping the widget
    /// does: bring the dashboard up rather than just clearing the banner. A
    /// monitor that pages you and then leaves you to go find the app
    /// yourself has only done half the job.
    ///
    /// This re-opens Beacon through the same `beacon://` URL the widget
    /// uses, via NSWorkspace, rather than calling `presentDashboard`
    /// directly: routing through the URL means there is exactly one place
    /// - `handleGetURL` above - that knows how to make the window actually
    /// appear, instead of two call paths that could disagree about it.
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

    // No Window scene for the dashboard: AppDelegate.presentDashboard
    // manages that NSWindow by hand, because the SwiftUI scene equivalent
    // (Window + .handlesExternalEvents + .onOpenURL) is the thing that
    // did not work against the real installed build. Two systems for
    // showing the same window would just be two places this could break.
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
