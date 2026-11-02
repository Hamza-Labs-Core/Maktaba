#if DEBUG && os(iOS)
import UIKit

/// The UI tests change the running app's Dynamic Type size (`assertTextScales` in
/// `UITests/PlayerUITestCase.swift`): launched with `-maktaba-text-size-hook`, the app listens
/// for a Darwin notification from the test runner,
/// `com.hamzalabs.maktaba.uitest.text-size.<XS|default|AX3>`, sets that size on every scene
/// (a trait override, as the system's own setting reaches the app's views), and answers with
/// `com.hamzalabs.maktaba.uitest.text-size-applied.<size>` once it has laid the windows out
/// again. One launch is measured at the three sizes, instead of one launch (and sign-in) per
/// size. A Debug build's only, and only with the flag: a TestFlight or App Store build has
/// none of it. The Player's UI-test targets compile this file too (its tests, and the same
/// names on both sides).
enum TextSizeHook {
    static let flag = "-maktaba-text-size-hook"
    static let ask = "com.hamzalabs.maktaba.uitest.text-size."
    static let applied = "com.hamzalabs.maktaba.uitest.text-size-applied."
    static let sizes = ["XS", "default", "AX3"]

    /// Listens for the tests' sizes when launched with `flag`; without it, does nothing.
    /// Whether it listens.
    @discardableResult
    static func start(arguments: [String] = CommandLine.arguments) -> Bool {
        guard arguments.contains(flag) else { return false }
        let center = CFNotificationCenterGetDarwinNotifyCenter()
        for size in sizes {
            CFNotificationCenterAddObserver(center, nil, { _, _, name, _, _ in
                guard let name = name?.rawValue as String?, name.hasPrefix(TextSizeHook.ask) else { return }
                let size = String(name.dropFirst(TextSizeHook.ask.count))
                DispatchQueue.main.async { MainActor.assumeIsolated { TextSizeHook.apply(size) } }
            }, (ask + size) as CFString, nil, .deliverImmediately)
        }
        return true
    }

    /// The size a test names: nil for the default (the system's own setting, no override).
    static func category(_ size: String) -> UIContentSizeCategory? {
        switch size {
        case "XS": .extraSmall
        case "AX3": .accessibilityExtraExtraExtraLarge
        default: nil
        }
    }

    /// Sets `size` on each scene (every window and view of it takes the scene's traits) and
    /// lays its windows out. False for a size the tests do not use (nothing changes).
    @MainActor @discardableResult
    static func set(_ size: String, on scenes: [UIWindowScene]) -> Bool {
        guard sizes.contains(size) else { return false }
        for scene in scenes {
            if let category = category(size) {
                scene.traitOverrides.preferredContentSizeCategory = category
            } else {
                scene.traitOverrides.remove(UITraitPreferredContentSizeCategory.self)
            }
            for window in scene.windows { window.layoutIfNeeded() }
        }
        return true
    }

    @MainActor private static func apply(_ size: String) {
        let scenes = UIApplication.shared.connectedScenes.compactMap { $0 as? UIWindowScene }
        guard set(size, on: scenes) else { return }
        // answered on the next turn of the main loop, after SwiftUI's update for the new size
        DispatchQueue.main.async {
            CFNotificationCenterPostNotification(CFNotificationCenterGetDarwinNotifyCenter(),
                                                 CFNotificationName((applied + size) as CFString), nil, nil, true)
        }
    }
}
#endif
