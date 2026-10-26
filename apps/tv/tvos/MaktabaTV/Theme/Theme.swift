import SwiftUI
import CoreText
#if canImport(UIKit)
import UIKit
#elseif canImport(AppKit)
import AppKit
#endif

/// Maktaba's "Islamic library" theme: the single place the tvOS app gets
/// its colours, fonts, background art, reading panels and focus ring.
/// Views use these tokens, never ad-hoc colours.
///
/// Two appearances, following the system setting (Settings ▸ General ▸
/// Appearance on Apple TV):
/// - **Dark — "Ink & Brass"** (the TV default): ink background, brass gold.
/// - **Light — "gold on white"**: warm paper background; gold *fills*
///   keep #E3B252 with ink text, but gold used as *text* is the deeper
///   #8A6212 so it keeps contrast on white.
enum Theme {
    // MARK: Palette

    /// App background.
    static let background = Color.adaptive(light: 0xFAF7F0, dark: 0x0B111C)
    /// Navigation / sidebar band.
    static let nav = Color.adaptive(light: 0xFFFFFF, dark: 0x0F1726)
    /// Cards, fields.
    static let surface = Color.adaptive(light: 0xFFFFFF, dark: 0x131D2E)
    /// Raised / active surface.
    static let raised = Color.adaptive(light: 0xF6EEDC, dark: 0x1B2638)
    /// Hairline borders.
    static let border = Color.adaptive(light: 0xEFE3C8, dark: 0x22304A)
    /// Primary text.
    static let text = Color.adaptive(light: 0x1C1810, dark: 0xE8ECF3)
    /// Secondary text.
    static let muted = Color.adaptive(light: 0x5A5040, dark: 0xA9B3C4)
    /// Tertiary text (counts, captions).
    static let faint = Color.adaptive(light: 0x7A6E5A, dark: 0x7F8BA0)
    /// Gold for fills, rings and the ۞ marker (same in both appearances).
    static let gold = Color(hex: 0xE3B252)
    /// Lighter gold for pressed / hover.
    static let goldLight = Color(hex: 0xF2CB7A)
    /// Gold used as *text* — deeper on light backgrounds for contrast.
    static let goldText = Color.adaptive(light: 0x8A6212, dark: 0xE3B252)
    /// Text on a gold fill.
    static let onGold = Color(hex: 0x1A1408)
    /// Live / OK status.
    static let live = Color(hex: 0x4CC38A)

    /// Tint laid over the frosted material on reading panels:
    /// dark rgba(11,17,28,0.78), light rgba(255,253,248,0.86).
    static let panelTint = Color.adaptive(
        light: 0xFFFDF8, lightAlpha: 0.86,
        dark: 0x0B111C, darkAlpha: 0.78
    )

    // MARK: Shape & focus

    static let cornerRadius: CGFloat = 16
    /// Focused-card ring (owner spec: 3px gold outline + scale).
    static let focusRingWidth: CGFloat = 3

    // MARK: Fonts

    /// Heading face: Amiri Bold (classical Naskh).
    static func heading(_ size: CGFloat, relativeTo style: Font.TextStyle = .title2) -> Font {
        Font.custom(FontName.amiriBold, size: size, relativeTo: style)
    }

    /// Body face: IBM Plex Sans Arabic.
    static func body(
        _ size: CGFloat,
        weight: BodyWeight = .regular,
        relativeTo style: Font.TextStyle = .body
    ) -> Font {
        Font.custom(weight.postScriptName, size: size, relativeTo: style)
    }

    enum BodyWeight {
        case regular, medium, semibold

        var postScriptName: String {
            switch self {
            case .regular: return FontName.plexRegular
            case .medium: return FontName.plexMedium
            case .semibold: return FontName.plexSemiBold
            }
        }
    }

    enum FontName {
        static let amiriBold = "Amiri-Bold"
        static let plexRegular = "IBMPlexSansArabic-Regular"
        static let plexMedium = "IBMPlexSansArabic-Medium"
        static let plexSemiBold = "IBMPlexSansArabic-SemiBold"
    }

    /// Font files bundled under `Resources/Fonts` (SIL OFL 1.1; the
    /// licence texts ship next to them).
    private static let fontFiles = [
        "Amiri-Bold",
        "IBMPlexSansArabic-Regular",
        "IBMPlexSansArabic-Medium",
        "IBMPlexSansArabic-SemiBold",
    ]

    /// Registers the bundled fonts with Core Text for this process, and
    /// styles the UIKit navigation titles SwiftUI can't reach. Call once
    /// at launch; repeat calls are harmless.
    @MainActor static func registerFonts() {
        for name in fontFiles {
            guard let url = Bundle.main.url(forResource: name, withExtension: "ttf") else { continue }
            // Already-registered fonts return an error we can ignore.
            CTFontManagerRegisterFontsForURL(url as CFURL, .process, nil)
        }
        #if os(tvOS)
        // tvOS has no large titles; the one title style gets Amiri.
        if let title = UIFont(name: FontName.amiriBold, size: 57) {
            UINavigationBar.appearance().titleTextAttributes = [.font: title]
        }
        #endif
    }

    // MARK: Art

    /// The full-screen library scene for the given appearance
    /// (bookshelves at the sides, a brass lamp at the top, calm middle).
    static func libraryArt(for scheme: ColorScheme) -> Image? {
        let name = scheme == .dark ? "library-dark" : "library-light"
        guard let url = Bundle.main.url(forResource: name, withExtension: "jpg") else { return nil }
        #if canImport(UIKit)
        guard let image = UIImage(contentsOfFile: url.path) else { return nil }
        return Image(uiImage: image)
        #elseif canImport(AppKit)
        guard let image = NSImage(contentsOf: url) else { return nil }
        return Image(nsImage: image)
        #else
        return nil
        #endif
    }

}

// MARK: - Library backdrop

/// The library scene, full-screen, with the readability fades the theme
/// requires: a fade to the background colour at the top (behind the tab
/// bar) and over the lower half (behind the rails). Text never goes
/// directly on the art — it sits on a `.readingPanel()`.
struct LibraryBackdrop: View {
    @Environment(\.colorScheme) private var scheme

    var body: some View {
        ZStack {
            Theme.background
            if let art = Theme.libraryArt(for: scheme) {
                art
                    .resizable()
                    .aspectRatio(contentMode: .fill)
                    .accessibilityHidden(true)
            }
            LinearGradient(
                stops: [
                    .init(color: Theme.background.opacity(0.80), location: 0.00),
                    .init(color: Theme.background.opacity(0.15), location: 0.18),
                    .init(color: Theme.background.opacity(0.10), location: 0.34),
                    .init(color: Theme.background.opacity(0.85), location: 0.52),
                    .init(color: Theme.background.opacity(0.96), location: 0.68),
                    .init(color: Theme.background, location: 1.00),
                ],
                startPoint: .top,
                endPoint: .bottom
            )
        }
        .ignoresSafeArea()
    }
}

// MARK: - Reading panel, section title, focus ring

extension View {
    /// Frosted, tinted reading panel for content over the art:
    /// `.ultraThinMaterial` blur + the panel tint + a hairline border.
    func readingPanel(padding: CGFloat = 32) -> some View {
        let shape = RoundedRectangle(cornerRadius: Theme.cornerRadius, style: .continuous)
        return self
            .padding(padding)
            .background {
                shape.fill(.ultraThinMaterial)
                    .overlay(shape.fill(Theme.panelTint))
            }
            .overlay(shape.strokeBorder(Theme.border, lineWidth: 1))
    }

    /// The theme's focus treatment: a 3pt gold ring, a scale-up and a
    /// soft gold glow so the focused card reads from across the room in
    /// both appearances (the glow carries it on the light background,
    /// where #E3B252 alone is low-contrast against white).
    func goldFocus(_ isFocused: Bool, cornerRadius: CGFloat = Theme.cornerRadius, scale: CGFloat = 1.08) -> some View {
        let shape = RoundedRectangle(cornerRadius: cornerRadius, style: .continuous)
        return self
            .overlay(shape.strokeBorder(Theme.gold, lineWidth: isFocused ? Theme.focusRingWidth : 0))
            .shadow(color: isFocused ? Theme.goldText.opacity(0.55) : .clear, radius: isFocused ? 24 : 0)
            .scaleEffect(isFocused ? scale : 1.0)
            .animation(.easeOut(duration: 0.18), value: isFocused)
    }
}

/// Section heading with the gold ۞ marker before it.
struct SectionTitle: View {
    let text: String
    var size: CGFloat = 44

    init(_ text: String, size: CGFloat = 44) {
        self.text = text
        self.size = size
    }

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 16) {
            Text("۞")
                .font(Theme.heading(size))
                .foregroundStyle(Theme.goldText)
                .accessibilityHidden(true)
            Text(text)
                .font(Theme.heading(size))
                .foregroundStyle(Theme.text)
        }
    }
}

// MARK: - Colour helpers

extension Color {
    /// An sRGB colour from a 0xRRGGBB literal.
    init(hex: UInt32, alpha: Double = 1) {
        self.init(
            .sRGB,
            red: Double((hex >> 16) & 0xFF) / 255,
            green: Double((hex >> 8) & 0xFF) / 255,
            blue: Double(hex & 0xFF) / 255,
            opacity: alpha
        )
    }

    /// A colour that resolves per appearance (light / dark).
    static func adaptive(
        light: UInt32, lightAlpha: Double = 1,
        dark: UInt32, darkAlpha: Double = 1
    ) -> Color {
        #if canImport(UIKit)
        return Color(UIColor { traits in
            traits.userInterfaceStyle == .dark
                ? UIColor(hex: dark, alpha: darkAlpha)
                : UIColor(hex: light, alpha: lightAlpha)
        })
        #elseif canImport(AppKit)
        return Color(NSColor(name: nil) { appearance in
            appearance.bestMatch(from: [.darkAqua, .aqua]) == .darkAqua
                ? NSColor(hex: dark, alpha: darkAlpha)
                : NSColor(hex: light, alpha: lightAlpha)
        })
        #else
        return Color(hex: dark, alpha: darkAlpha)
        #endif
    }
}

#if canImport(UIKit)
private extension UIColor {
    convenience init(hex: UInt32, alpha: Double) {
        self.init(
            red: CGFloat((hex >> 16) & 0xFF) / 255,
            green: CGFloat((hex >> 8) & 0xFF) / 255,
            blue: CGFloat(hex & 0xFF) / 255,
            alpha: CGFloat(alpha)
        )
    }
}
#elseif canImport(AppKit)
private extension NSColor {
    convenience init(hex: UInt32, alpha: Double) {
        self.init(
            srgbRed: CGFloat((hex >> 16) & 0xFF) / 255,
            green: CGFloat((hex >> 8) & 0xFF) / 255,
            blue: CGFloat(hex & 0xFF) / 255,
            alpha: CGFloat(alpha)
        )
    }
}
#endif

// MARK: - Buttons

/// The TV's action button: a quiet raised pill that turns gold with ink text, lifts and
/// glows when focused (the system's bordered button takes the brass tint for both its fill
/// and its label, so its words would vanish).
struct GoldButtonStyle: ButtonStyle {
    func makeBody(configuration: Configuration) -> some View {
        GoldButton(configuration: configuration)
    }

    private struct GoldButton: View {
        let configuration: ButtonStyleConfiguration
        @Environment(\.isFocused) private var focused
        @Environment(\.isEnabled) private var enabled

        var body: some View {
            configuration.label
                .font(Theme.body(30, weight: .semibold))
                .foregroundStyle(focused ? Theme.onGold : Theme.text)
                .padding(.horizontal, 40)
                .padding(.vertical, 18)
                .background(focused ? Theme.gold : Theme.raised, in: .capsule)
                .overlay(Capsule().strokeBorder(Theme.border, lineWidth: focused ? 0 : 1))
                .shadow(color: focused ? Theme.goldText.opacity(0.5) : .clear, radius: 18)
                .scaleEffect(configuration.isPressed ? 0.97 : focused ? 1.06 : 1)
                .opacity(enabled ? 1 : 0.45)
                .animation(.easeOut(duration: 0.15), value: focused)
        }
    }
}

extension ButtonStyle where Self == GoldButtonStyle {
    static var gold: GoldButtonStyle { GoldButtonStyle() }
}
