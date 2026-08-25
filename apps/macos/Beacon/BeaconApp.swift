import SwiftUI

/// Starts polling as soon as the process launches.
///
/// A MenuBarExtra's content view is not instantiated until the user opens
/// the panel, and a Window scene's is not instantiated until a window
/// opens, so hanging the poller off either one means a Beacon that has been
/// running since login has still never contacted the hub and the widget has
/// nothing to show. The work has to begin at launch, independently of
/// whether any view exists yet.
@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate {
    let poller = HubPoller()

    func applicationDidFinishLaunching(_ notification: Notification) {
        poller.start()
    }

    func applicationWillTerminate(_ notification: Notification) {
        poller.stop()
    }
}

@main
struct BeaconApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var delegate

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
            DashboardView(poller: delegate.poller)
        }
        .defaultSize(width: 720, height: 520)
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
