import SwiftUI
#if canImport(UIKit)
import UIKit
#elseif canImport(AppKit)
import AppKit
#endif

/// The theme a person picks, as on the web (`web/src/lib/theme.ts`): light, dark, or the
/// system's. Kept per device under `ThemeMode.storageKey`.
public enum ThemeMode: String, CaseIterable, Identifiable, Sendable {
    case system, light, dark

    public static let storageKey = "theme"
    public var id: String { rawValue }

    public var label: String {
        switch self {
        case .system: "System"
        case .light: "Light"
        case .dark: "Dark"
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

/// "Ink & Brass", the web's tokens (`web/src/styles/tokens.css`): each colour has its
/// light and its dark value.
public extension Color {
    /// --color-accent: the brand's gold.
    static let brass = Color(red: 0xE3 / 255, green: 0xB2 / 255, blue: 0x52 / 255)
    /// --color-bg: white / ink.
    static let mktBackground = Color(light: 0xFFFFFF, dark: 0x0B111C)
    /// --color-subtle: rows and cards on the background.
    static let mktSurface = Color(light: 0xFBF5E8, dark: 0x131D2E)
    /// --color-border.
    static let mktBorder = Color(light: 0xEFE3C8, dark: 0x22304A)
    /// A chosen row: brass, strong enough to stand out from `mktSurface`.
    static let mktChosen = Color(light: 0xF3DCA2, dark: 0x3A3020)

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
        Picker("Theme", systemImage: mode.systemImage, selection: $mode) {
            ForEach(ThemeMode.allCases) { m in
                Label(m.label, systemImage: m.systemImage).tag(m)
            }
        }
    }
}
