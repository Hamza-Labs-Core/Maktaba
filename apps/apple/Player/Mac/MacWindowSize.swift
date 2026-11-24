#if os(macOS) && DEBUG
import AppKit

/// The UI tests' window size (`-maktaba-window-size 1440x900`, in points, the title bar
/// included): the product screenshots' window (story 60.14), 2880 x 1800 pixels on the 2x
/// display CI gives them (tools/apple/hidpi-display.m), the Mac App Store's size. The window
/// keeps its top left corner at the screen's, below the menu bar. Only a Debug build reads it.
@MainActor
enum MacWindowSize {
    static func applyFromLaunchArguments(_ arguments: [String] = CommandLine.arguments) {
        guard let i = arguments.firstIndex(of: "-maktaba-window-size"), i + 1 < arguments.count else { return }
        let parts = arguments[i + 1].split(separator: "x").compactMap { Double($0) }
        guard parts.count == 2, parts[0] > 0, parts[1] > 0 else { return }
        let size = NSSize(width: parts[0], height: parts[1])
        // each time a window comes forward (the first, and again should SwiftUI resize it)
        NotificationCenter.default.addObserver(forName: NSWindow.didBecomeKeyNotification, object: nil, queue: .main) { _ in
            MainActor.assumeIsolated {
                if let window = NSApp.keyWindow { fit(window, size) }
            }
        }
    }

    static func fit(_ window: NSWindow, _ size: NSSize) {
        guard window.styleMask.contains(.titled), !window.styleMask.contains(.fullScreen),
              window.sheetParent == nil, window.frame.size != size,
              let visible = (window.screen ?? NSScreen.main)?.visibleFrame else { return }
        window.setFrame(NSRect(x: visible.minX, y: visible.maxY - size.height, width: size.width, height: size.height),
                        display: true)
    }
}
#endif
