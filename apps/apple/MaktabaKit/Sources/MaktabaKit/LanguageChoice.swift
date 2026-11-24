import Foundation

/// The Mac Player's own language switch (Epic 13 story 13.16, the desktop design's D5).
///
/// AppKit lays a window out once, at launch, in the direction of the app's language: the
/// split view's sidebar and the traffic lights move to the right only in an app that starts in
/// Arabic. So the Mac Player keeps the person's choice in its own defaults and starts again
/// (after asking, in English and Arabic): `keep(in:)` writes what the next launch reads.
///
/// The iPhone and iPad apps keep the system's per-app language page (Settings), which
/// restarts the app itself.
public enum LanguageChoice: String, CaseIterable, Identifiable, Sendable {
    case english = "en"
    case arabic = "ar"

    /// The defaults key the system reads the app's languages from (the per-app language of
    /// System Settings writes the same key).
    public static let languagesKey = "AppleLanguages"
    /// The defaults key that turns AppKit's layout right to left from the first window on
    /// (the traffic lights, the split view, the menus). Kept for Arabic only.
    public static let directionKey = "AppleTextDirection"

    public var id: String { rawValue }

    /// The language's own name, as a language picker shows it.
    public var name: String {
        switch self {
        case .english: "English"
        case .arabic: "العربية"
        }
    }

    /// The locale of the language's own words (a prompt that speaks both).
    public var locale: Locale { Locale(identifier: rawValue) }

    public var isRightToLeft: Bool { self == .arabic }

    /// The choice the app shows now: its main bundle's first localization by default (the
    /// language the system picked for it at launch). Anything not Arabic is English.
    public static func current(_ preferred: [String] = Bundle.main.preferredLocalizations) -> LanguageChoice {
        (preferred.first ?? "en").hasPrefix("ar") ? .arabic : .english
    }

    /// Keeps this language as the app's from its next launch: the app's languages and, for
    /// Arabic, the right-to-left layout; English takes that away again.
    public func keep(in defaults: UserDefaults) {
        defaults.set([rawValue], forKey: Self.languagesKey)
        if isRightToLeft {
            defaults.set(true, forKey: Self.directionKey)
        } else {
            defaults.removeObject(forKey: Self.directionKey)
        }
    }

    /// Choosing `self` while `current` shows: a new language needs a new launch.
    public func needsRelaunch(from current: LanguageChoice) -> Bool { self != current }
}
