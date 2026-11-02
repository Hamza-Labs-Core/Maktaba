#if DEBUG && os(iOS)
import UIKit

/// The UI tests change the running app's Dynamic Type size (`assertTextScales` in
/// `UITests/PlayerUITestCase.swift`): the test runner posts a Darwin notification,
/// `com.hamzalabs.maktaba.uitest.text-size.<XS|default|AX3>`, and the app sets that size on
/// every scene (a trait override, as the system's own setting reaches the app's views), then
/// answers with `com.hamzalabs.maktaba.uitest.text-size-applied.<size>` once it has laid the
/// windows out again. One launch is measured at the three sizes, instead of one launch (and
/// sign-in) per size. A Debug build's only: a TestFlight or App Store build has none of it.
enum TextSizeHook {
    static let ask = "com.hamzalabs.maktaba.uitest.text-size."
    static let applied = "com.hamzalabs.maktaba.uitest.text-size-applied."
    static let sizes = ["XS", "default", "AX3"]

    static func start() {
        let center = CFNotificationCenterGetDarwinNotifyCenter()
        for size in sizes {
            CFNotificationCenterAddObserver(center, nil, { _, _, name, _, _ in
                guard let name = name?.rawValue as String?, name.hasPrefix(TextSizeHook.ask) else { return }
                let size = String(name.dropFirst(TextSizeHook.ask.count))
                DispatchQueue.main.async { MainActor.assumeIsolated { TextSizeHook.apply(size) } }
            }, (ask + size) as CFString, nil, .deliverImmediately)
        }
    }

    /// The size a test names: nil for the default (the system's own setting, no override).
    private static func category(_ size: String) -> UIContentSizeCategory? {
        switch size {
        case "XS": .extraSmall
        case "AX3": .accessibilityExtraExtraExtraLarge
        default: nil
        }
    }

    @MainActor private static func apply(_ size: String) {
        guard sizes.contains(size) else { return }
        for case let scene as UIWindowScene in UIApplication.shared.connectedScenes {
            if let category = category(size) {
                scene.traitOverrides.preferredContentSizeCategory = category
            } else {
                scene.traitOverrides.remove(UITraitPreferredContentSizeCategory.self)
            }
            for window in scene.windows { window.layoutIfNeeded() }
        }
        // answered on the next turn of the main loop, after SwiftUI's update for the new size
        DispatchQueue.main.async {
            CFNotificationCenterPostNotification(CFNotificationCenterGetDarwinNotifyCenter(),
                                                 CFNotificationName((applied + size) as CFString), nil, nil, true)
        }
    }
}
#endif
