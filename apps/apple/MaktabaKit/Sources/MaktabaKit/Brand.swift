import CoreText
import SwiftUI
#if canImport(UIKit)
import UIKit
#elseif canImport(AppKit)
import AppKit
#endif

// The brand every Apple app shares (Epic 48 story 48.10, FR-1 and FR-4): the fonts, the web's
// library art and the logo (MaktabaKit's Resources/, read from Bundle.module), and the start
// screens' frame built from them: the art behind a scrim, the logo, «Maktaba» in Amiri and the
// app's name, over a frosted panel. Android's `core/ui/StartScreens.kt` and the web's
// `Login.tsx` do the same.

/// Which app shows the brand: its name under «Maktaba».
public enum MaktabaApp: String, CaseIterable, Sendable {
    case player, remote

    /// The app running, from its bundle identifier (`com.hamzalabs.maktaba.player…`,
    /// `…remote…`); nil in a test host or another app.
    public static var current: MaktabaApp? { from(bundleID: Bundle.main.bundleIdentifier) }

    static func from(bundleID: String?) -> MaktabaApp? {
        guard let id = bundleID, id.hasPrefix("com.hamzalabs.maktaba.") else { return nil }
        let parts = id.split(separator: ".")
        return parts.count > 3 ? MaktabaApp(rawValue: String(parts[3])) : nil
    }

    /// The app's name under «Maktaba».
    public func name(_ locale: Locale = AppLanguage.locale) -> String {
        switch self {
        case .player: kit("Player", locale)
        case .remote: kit("Remote", locale)
        }
    }

    /// One line on the home picker: what choosing a home is for.
    public func tagline(_ locale: Locale = AppLanguage.locale) -> String {
        switch self {
        case .player: kit("Choose your home to start watching.", locale)
        case .remote: kit("Choose the home you run.", locale)
        }
    }
}

/// The brand's resources: the fonts (registered once, at start), the library art and the logo.
public enum Brand {
    /// The bundled font files (Resources/Fonts), by PostScript name.
    public static let fontNames = [
        "Amiri-Bold", "IBMPlexSansArabic-Regular", "IBMPlexSansArabic-Medium", "IBMPlexSansArabic-SemiBold",
    ]

    /// Headings: Amiri Bold (the tokens' `heading`).
    public static let headingFont = "Amiri-Bold"
    /// Body text: IBM Plex Sans Arabic (the tokens' `body`), by weight.
    public static let bodyFont = "IBMPlexSansArabic-Regular"
    public static let bodyMediumFont = "IBMPlexSansArabic-Medium"
    public static let bodySemiBoldFont = "IBMPlexSansArabic-SemiBold"

    /// Registers the bundled fonts for this process, once; true when every one is usable.
    /// `SessionGate` calls it at start, and each brand font asks for it, so a screen never
    /// draws before its font is there. An app that already lists the same files (the TV app's
    /// `UIAppFonts`) keeps its own: a font already registered counts as usable.
    @discardableResult
    public static func registerFonts() -> Bool { fontsRegistered }

    private static let fontsRegistered: Bool = {
        var all = true
        for name in fontNames {
            guard let url = Bundle.module.url(forResource: name, withExtension: "ttf") else {
                Log.ui.error("brand font \(name, privacy: .public) is not in the bundle")
                all = false
                continue
            }
            var error: Unmanaged<CFError>?
            if !CTFontManagerRegisterFontsForURL(url as CFURL, .process, &error) {
                let code = error.map { CFErrorGetCode($0.takeRetainedValue()) } ?? 0
                if code != CTFontManagerError.alreadyRegistered.rawValue
                    && code != CTFontManagerError.duplicatedName.rawValue {
                    Log.ui.error("brand font \(name, privacy: .public) did not register (\(code))")
                    all = false
                }
            }
        }
        return all
    }()

    /// The library art's file for a light or dark screen, tall or wide.
    static func artName(dark: Bool, wide: Bool) -> String {
        "library-\(dark ? "dark" : "light")-\(wide ? "wide" : "tall")"
    }

    static let logoName = "maktaba-logo"

    /// The library art (light or dark, tall or wide), decoded once.
    @MainActor public static func art(dark: Bool, wide: Bool) -> Image? {
        image(artName(dark: dark, wide: wide), ext: "jpg")
    }

    /// The logo (the app icon's artwork, 512 px), decoded once.
    @MainActor public static var logo: Image? { image(logoName, ext: "png") }

    @MainActor private static var images: [String: Image] = [:]

    @MainActor private static func image(_ name: String, ext: String) -> Image? {
        if let cached = images[name] { return cached }
        guard let url = Bundle.module.url(forResource: name, withExtension: ext) else { return nil }
        #if canImport(UIKit)
        guard let ui = UIImage(contentsOfFile: url.path) else { return nil }
        let image = Image(uiImage: ui)
        #elseif canImport(AppKit)
        guard let ns = NSImage(contentsOf: url) else { return nil }
        let image = Image(nsImage: ns)
        #endif
        images[name] = image
        return image
    }
}

public extension Font {
    /// Amiri Bold at `size`, growing with Dynamic Type as `style` does.
    static func maktabaHeading(_ size: CGFloat, relativeTo style: Font.TextStyle = .largeTitle) -> Font {
        Brand.registerFonts()
        return .custom(Brand.headingFont, size: size, relativeTo: style)
    }

    /// IBM Plex Sans Arabic at `size` (regular, medium or semibold), growing as `style` does.
    static func maktabaBody(_ size: CGFloat = MaktabaTokens.TypeSize.base, weight: Font.Weight = .regular,
                            relativeTo style: Font.TextStyle = .body) -> Font {
        Brand.registerFonts()
        let name = switch weight {
        case .medium: Brand.bodyMediumFont
        case .semibold, .bold, .heavy, .black: Brand.bodySemiBoldFont
        default: Brand.bodyFont
        }
        return .custom(name, size: size, relativeTo: style)
    }
}

// MARK: - The start screens' frame

/// The web's library art (light or dark, tall or wide) under a scrim, filling the screen
/// (behind the safe areas too): what the picker, by-address and sign-in screens stand on.
public struct StartBackdrop: View {
    @Environment(\.colorScheme) private var scheme

    public init() {}

    public var body: some View {
        GeometryReader { geo in
            let wide = geo.size.width > geo.size.height
            ZStack {
                Color.mktBackground
                if let art = Brand.art(dark: scheme == .dark, wide: wide) {
                    art.resizable()
                        .scaledToFill()
                        .frame(width: geo.size.width, height: geo.size.height)
                        .clipped()
                }
                // the scrim: lighter at the top, where the art shows, darker under the panel
                LinearGradient(colors: [Color.mktScrim, Color.mktBackground.opacity(0.86)],
                               startPoint: .top, endPoint: .bottom)
            }
        }
        .ignoresSafeArea()
        .accessibilityHidden(true)
        .accessibilityIdentifier("start.backdrop")
    }
}

/// The logo, «Maktaba» in Amiri and the app's name.
public struct StartBrand: View {
    let appName: String?

    /// `appName`: the line under «Maktaba» (the running app's, `MaktabaApp.current`, by default).
    public init(appName: String? = MaktabaApp.current?.name()) {
        self.appName = appName
    }

    public var body: some View {
        VStack(spacing: MaktabaTokens.Spacing.s4) {
            if let logo = Brand.logo {
                logo.resizable()
                    .interpolation(.high)
                    // the artwork has its own rounded corners
                    .frame(width: 96, height: 96)
                    .shadow(color: .black.opacity(0.25), radius: 12, y: 4)
                    .accessibilityHidden(true)
                    .accessibilityIdentifier("start.logo")
            }
            VStack(spacing: MaktabaTokens.Spacing.s1) {
                Text(kit("Maktaba"))
                    .font(.maktabaHeading(MaktabaTokens.TypeSize.display))
                    .foregroundStyle(Color.mktBrassText)
                    .accessibilityIdentifier("start.wordmark")
                if let appName {
                    Text(appName)
                        .font(.maktabaBody(MaktabaTokens.TypeSize.lg, weight: .semibold, relativeTo: .title3))
                        .foregroundStyle(Color.mktMuted)
                        .accessibilityIdentifier("start.app")
                }
            }
            .multilineTextAlignment(.center)
        }
        .frame(maxWidth: .infinity)
        .padding(.vertical, MaktabaTokens.Spacing.s4)
        .accessibilityElement(children: .combine)
        .accessibilityAddTraits(.isHeader)
        .accessibilityIdentifier("start.brand")
    }
}

/// The start screens' panel: frosted glass over the art, tinted with the raised surface.
public struct StartPanelFill: View {
    public init() {}

    public var body: some View {
        ZStack {
            Rectangle().fill(.regularMaterial)
            Color.mktRaised.opacity(0.55)
        }
    }
}

public extension View {
    /// A start screen's form (picker, by-address, sign-in) on the brand: its rows on the
    /// frosted panel, the library art behind, at most a readable width on a wide screen.
    /// (Its rows take the panel with `startPanelRows()`.)
    func startScreen() -> some View {
        #if os(tvOS)
        background(StartBackdrop())
        #else
        scrollContentBackground(.hidden)
            // brass as text (AA on the light panel too; the same brass in dark)
            .tint(Color.mktBrassText)
            .frame(maxWidth: 620)
            .frame(maxWidth: .infinity)
            .background(StartBackdrop())
        #endif
    }

    /// A start screen's rows (sections of its form) on the frosted panel.
    func startPanelRows() -> some View {
        listRowBackground(StartPanelFill())
    }

    /// No navigation bar over the brand (iPhone, iPad): the screen's title stays for
    /// VoiceOver and the Mac's window.
    func startTitleHidden() -> some View {
        #if os(iOS)
        toolbar(.hidden, for: .navigationBar)
        #else
        self
        #endif
    }
}

/// The brand as a start screen form's first section: on the art, outside the panel.
struct StartBrandSection: View {
    let appName: String?
    var tagline: String?

    var body: some View {
        Section {
            VStack(spacing: MaktabaTokens.Spacing.s2) {
                StartBrand(appName: appName)
                if let tagline {
                    Text(tagline)
                        .font(.maktabaBody(MaktabaTokens.TypeSize.base))
                        .foregroundStyle(Color.mktText)
                        .multilineTextAlignment(.center)
                        .accessibilityIdentifier("start.tagline")
                }
            }
            .frame(maxWidth: .infinity)
            .listRowBackground(Color.clear)
            .listRowInsets(EdgeInsets())
        }
    }
}
