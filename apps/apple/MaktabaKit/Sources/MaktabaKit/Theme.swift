import SwiftUI
#if canImport(UIKit)
import UIKit
#elseif canImport(AppKit)
import AppKit
#endif

/// The theme a person picks, as on the web (`web/src/lib/theme.ts`): light, dark, or the
/// system's. Kept per device under `ThemeMode.storageKey`. With nothing stored the app is
/// dark (Ink & Brass, Epic 48 FR-2), whatever the system's setting; a stored choice is kept.
public enum ThemeMode: String, CaseIterable, Identifiable, Sendable {
    case system, light, dark

    /// What an app shows before the person picks: dark.
    public static let defaultMode = ThemeMode.dark

    /// The defaults key. Reading it first registers `defaultMode` as the key's registered
    /// default, so every `@AppStorage(ThemeMode.storageKey) var theme = …` in the apps reads
    /// dark while nothing is stored (its own fallback value is then never used).
    public static let storageKey: String = {
        registerDefault(in: .standard)
        return key
    }()

    static let key = "theme"

    /// Registers dark as the theme of `defaults` while none is stored there.
    public static func registerDefault(in defaults: UserDefaults) {
        defaults.register(defaults: [key: defaultMode.rawValue])
    }

    /// The mode a stored value names: dark for nothing stored, or for an unknown value.
    public init(stored: String?) {
        self = stored.flatMap(ThemeMode.init(rawValue:)) ?? ThemeMode.defaultMode
    }

    /// The mode kept in `defaults` (dark while nothing is).
    public static func stored(in defaults: UserDefaults) -> ThemeMode {
        ThemeMode(stored: defaults.string(forKey: key))
    }

    public var id: String { rawValue }

    public var label: String {
        switch self {
        case .system: kit("System")
        case .light: kit("Light")
        case .dark: kit("Dark")
        }
    }

    public var systemImage: String {
        switch self {
        case .system: "circle.lefthalf.filled"
        case .light: "sun.max"
        case .dark: "moon"
        }
    }

    /// nil follows the system.
    public var colorScheme: ColorScheme? {
        switch self {
        case .system: nil
        case .light: .light
        case .dark: .dark
        }
    }
}

/// "Ink & Brass": the generated design tokens (`Tokens.swift`, from
/// `web/design-system/tokens/`, Story 48.2). Each colour has its light and its dark value.
public extension Color {
    /// The brand's gold, a fill (`accent`).
    static let brass = MaktabaTokens.Colors.accent.color
    /// Brass as text: AA on the background in light and dark (`accentText`).
    static let mktBrassText = MaktabaTokens.Colors.accentText.color
    /// The page: white / ink (`bg`).
    static let mktBackground = MaktabaTokens.Colors.bg.color
    /// Body text (`fg`).
    static let mktText = MaktabaTokens.Colors.fg.color
    /// Secondary text (`muted`).
    static let mktMuted = MaktabaTokens.Colors.muted.color
    /// Rows and cards on the background (`subtle`).
    static let mktSurface = MaktabaTokens.Colors.subtle.color
    /// A sheet, a menu, the start screens' panel (`raised`).
    static let mktRaised = MaktabaTokens.Colors.raised.color
    /// The layer over the library art (`scrim`).
    static let mktScrim = MaktabaTokens.Colors.scrim.color
    /// Hairlines (`border`).
    static let mktBorder = MaktabaTokens.Colors.border.color
    /// A chosen row: brass, strong enough to stand out from `mktSurface` (`chosen`).
    static let mktChosen = MaktabaTokens.Colors.chosen.color

    init(light: UInt32, dark: UInt32) {
        #if canImport(UIKit)
        self.init(uiColor: UIColor { $0.userInterfaceStyle == .dark ? UIColor(rgb: dark) : UIColor(rgb: light) })
        #elseif canImport(AppKit)
        self.init(nsColor: NSColor(name: nil) { appearance in
            appearance.bestMatch(from: [.darkAqua, .aqua]) == .darkAqua ? NSColor(rgb: dark) : NSColor(rgb: light)
        })
        #endif
    }
}

#if canImport(UIKit)
private extension UIColor {
    convenience init(rgb: UInt32) {
        self.init(red: CGFloat(rgb >> 16 & 0xFF) / 255, green: CGFloat(rgb >> 8 & 0xFF) / 255,
                  blue: CGFloat(rgb & 0xFF) / 255, alpha: 1)
    }
}
#elseif canImport(AppKit)
private extension NSColor {
    convenience init(rgb: UInt32) {
        self.init(srgbRed: CGFloat(rgb >> 16 & 0xFF) / 255, green: CGFloat(rgb >> 8 & 0xFF) / 255,
                  blue: CGFloat(rgb & 0xFF) / 255, alpha: 1)
    }
}
#endif

public extension View {
    /// The app's theme: the picked mode (dark while `forceDark`, as while a video plays)
    /// and the brass tint.
    func maktabaTheme(_ mode: ThemeMode, forceDark: Bool = false) -> some View {
        preferredColorScheme(forceDark ? .dark : mode.colorScheme)
            .tint(.brass)
    }

    /// A list or scroll view on the theme's background instead of the system grey.
    func maktabaSurface() -> some View {
        #if os(tvOS)
        background(Color.mktBackground)
        #else
        scrollContentBackground(.hidden)
            .background(Color.mktBackground)
        #endif
    }
}

/// The theme picker for an account menu.
public struct ThemePicker: View {
    @Binding var mode: ThemeMode

    public init(mode: Binding<ThemeMode>) {
        _mode = mode
    }

    public var body: some View {
        Picker(kit("Theme"), systemImage: mode.systemImage, selection: $mode) {
            ForEach(ThemeMode.allCases) { m in
                Label(m.label, systemImage: m.systemImage).tag(m)
            }
        }
    }
}
