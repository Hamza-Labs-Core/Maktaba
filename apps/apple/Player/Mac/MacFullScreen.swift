#if os(macOS)
import AppKit
import Observation

/// Full screen on the Mac (FR-42, FR-43): View ▸ Enter Full Screen (⌃⌘F) puts the focused
/// window on a space of its own, where the player fills it as it fills the window. A SwiftUI
/// app's View menu gets no full-screen item of its own when its commands make that menu, so
/// the Player names it, and says Exit while the window is full screen.
@MainActor @Observable
final class MacFullScreen {
    static let shared = MacFullScreen()

    /// The window the menu acts on: the key window, else the main one (a sheet may be key).
    var window: () -> NSWindow? = { NSApp.keyWindow ?? NSApp.mainWindow }
    private(set) var isFullScreen = false

    private init() {
        let center = NotificationCenter.default
        for name in [NSWindow.didEnterFullScreenNotification, NSWindow.didExitFullScreenNotification,
                     NSWindow.didBecomeKeyNotification] {
            center.addObserver(forName: name, object: nil, queue: .main) { _ in
                MainActor.assumeIsolated { MacFullScreen.shared.refresh() }
            }
        }
    }

    func refresh() {
        isFullScreen = window()?.styleMask.contains(.fullScreen) ?? false
    }

    /// Enters or leaves full screen; a window that cannot (a panel) is left as it is.
    func toggle() {
        guard let w = window(), Self.canFullScreen(w) else { return }
        w.toggleFullScreen(nil)
    }

    /// A window that may go full screen on a space of its own: resizable, and not marked
    /// auxiliary or none (AppKit's default for a resizable window is primary).
    static func canFullScreen(_ w: NSWindow) -> Bool {
        let b = w.collectionBehavior
        if b.contains(.fullScreenNone) || b.contains(.fullScreenAuxiliary) { return false }
        return b.contains(.fullScreenPrimary) || w.styleMask.contains(.resizable)
    }
}
#endif
