import Foundation

/// The Mac Player's own language switch (Epic 13 story 13.16, the desktop design's D5).
///
/// AppKit lays a window out once, at launch, in its layout direction: the split view's sidebar
/// and the traffic lights move to the right only in an app started right to left. The app's
/// language alone does not do it (an app started in Arabic still lays out left to right), so
/// at every launch, before any window, the Player sets AppKit's direction from the language it
/// starts in (`applyLayout`): right to left for Arabic, and nothing kept otherwise. That holds
/// whichever way the language was chosen: here, or the system's per-app language, which
/// writes only `AppleLanguages`.
///
/// The person's choice is the app's own `AppleLanguages` (`keep(in:)`), or none to follow the
/// system's languages (`.system`, as the iPhone and iPad offer). A new choice starts the Player
/// again, after asking.
public enum LanguageChoice: String, CaseIterable, Identifiable, Sendable {
    case english = "en"
    case arabic = "ar"
    case system

    /// The defaults key the system reads the app's languages from (the per-app language of
    /// System Settings writes the same key).
    public static let languagesKey = "AppleLanguages"
    /// The defaults key that turns AppKit's layout right to left from the first window on
    /// (the traffic lights, the split view, the menus). Set at launch for Arabic only.
    public static let directionKey = "AppleTextDirection"

    public var id: String { rawValue }

    /// The choice's name as a picker shows it: each language in itself, System in the app's.
    public var name: String {
        switch self {
        case .english: "English"
        case .arabic: "العربية"
        case .system: kit("System")
        }
    }

    /// The language a choice names (nil: the system's).
    public var language: String? { self == .system ? nil : rawValue }

    /// The choice kept in the app's own defaults domain (`domain`, its persistent values):
    /// English or Arabic by its first language, `.system` with none.
    public static func kept(in domain: [String: Any]?) -> LanguageChoice {
        guard let first = (domain?[languagesKey] as? [String])?.first else { return .system }
        return first.hasPrefix("ar") ? .arabic : .english
    }

    /// Whether the app lays out right to left: its main bundle's first localization by default
    /// (the language the system picked for it at launch) is Arabic.
    public static func isRightToLeft(_ preferred: [String] = Bundle.main.preferredLocalizations) -> Bool {
        (preferred.first ?? "en").hasPrefix("ar")
    }

    /// At launch, before any window: AppKit's layout right to left when the app starts in
    /// Arabic, and the key removed otherwise (so English, chosen anywhere, never starts
    /// mirrored).
    public static func applyLayout(_ preferred: [String] = Bundle.main.preferredLocalizations, to defaults: UserDefaults) {
        if isRightToLeft(preferred) {
            defaults.set(true, forKey: directionKey)
        } else {
            defaults.removeObject(forKey: directionKey)
        }
    }

    /// Keeps this choice for the next launch: the app's languages, or none to follow the
    /// system's. The layout follows at that launch (`applyLayout`).
    public func keep(in defaults: UserDefaults) {
        if let language {
            defaults.set([language], forKey: Self.languagesKey)
        } else {
            defaults.removeObject(forKey: Self.languagesKey)
        }
    }

    /// Choosing `self` while `kept` is kept: a new choice needs a new launch.
    public func needsRelaunch(from kept: LanguageChoice) -> Bool { self != kept }
}

/// The app's own values of the two keys as they were (each possibly not set), to put back
/// exactly when the Player could not start again after a new choice.
public struct LanguageDefaults: Equatable, Sendable {
    public let languages: [String]?
    public let direction: Bool?

    /// The values in the app's own domain (`domain`: its persistent values).
    public init(domain: [String: Any]?) {
        languages = domain?[LanguageChoice.languagesKey] as? [String]
        direction = (domain?[LanguageChoice.directionKey] as? NSNumber)?.boolValue
    }

    /// Writes them back, removing a key that was not set.
    public func restore(in defaults: UserDefaults) {
        if let languages { defaults.set(languages, forKey: LanguageChoice.languagesKey) }
        else { defaults.removeObject(forKey: LanguageChoice.languagesKey) }
        if let direction { defaults.set(direction, forKey: LanguageChoice.directionKey) }
        else { defaults.removeObject(forKey: LanguageChoice.directionKey) }
    }
}
