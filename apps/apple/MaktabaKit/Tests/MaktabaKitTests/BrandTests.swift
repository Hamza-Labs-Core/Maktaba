import CoreText
import Foundation
import ImageIO
import SwiftUI
import Testing
@testable import MaktabaKit

/// Story 48.10: the Apple brand. Dark by default, the fonts and art from Bundle.module, the
/// start screens' frame, and the launch screen's ink.
@Suite struct AppleBrand {
    // MARK: ThemeMode: dark with nothing stored (FR-2)

    private func freshDefaults() -> (UserDefaults, String) {
        let name = "maktaba.brand.tests.\(UUID().uuidString)"
        return (UserDefaults(suiteName: name)!, name)
    }

    @Test func nothingStoredIsDark() {
        let (defaults, name) = freshDefaults()
        defer { defaults.removePersistentDomain(forName: name) }
        #expect(ThemeMode.stored(in: defaults) == .dark)
        #expect(ThemeMode(stored: nil) == .dark)
        #expect(ThemeMode(stored: "sepia") == .dark)
        #expect(ThemeMode.defaultMode == .dark)
    }

    @Test func aStoredChoiceIsKept() {
        let (defaults, name) = freshDefaults()
        defer { defaults.removePersistentDomain(forName: name) }
        ThemeMode.registerDefault(in: defaults)
        defaults.set("system", forKey: ThemeMode.storageKey)
        #expect(ThemeMode.stored(in: defaults) == .system)
        defaults.set("light", forKey: ThemeMode.storageKey)
        #expect(ThemeMode.stored(in: defaults) == .light)
        defaults.set("dark", forKey: ThemeMode.storageKey)
        #expect(ThemeMode.stored(in: defaults) == .dark)
    }

    /// The apps read the theme through `@AppStorage(ThemeMode.storageKey) var theme =
    /// ThemeMode.system`: with the registered default, nothing stored reads dark there too.
    @Test func appStorageReadsDarkWhenNothingIsStored() {
        let (defaults, name) = freshDefaults()
        defer { defaults.removePersistentDomain(forName: name) }
        ThemeMode.registerDefault(in: defaults)
        let unset = AppStorage(wrappedValue: ThemeMode.system, ThemeMode.storageKey, store: defaults)
        #expect(unset.wrappedValue == .dark)
        defaults.set("light", forKey: ThemeMode.storageKey)
        let light = AppStorage(wrappedValue: ThemeMode.system, ThemeMode.storageKey, store: defaults)
        #expect(light.wrappedValue == .light)
        defaults.set("system", forKey: ThemeMode.storageKey)
        let system = AppStorage(wrappedValue: ThemeMode.dark, ThemeMode.storageKey, store: defaults)
        #expect(system.wrappedValue == .system)
    }

    /// Reading the key registers dark in the standard defaults (what `@AppStorage` reads),
    /// without storing anything.
    @Test func theKeyRegistersDarkInTheStandardDefaults() {
        #expect(ThemeMode.storageKey == "theme")
        let registered = UserDefaults.standard.volatileDomain(forName: UserDefaults.registrationDomain)
        #expect(registered["theme"] as? String == "dark")
    }

    // MARK: fonts and art from Bundle.module (FR-4)

    @Test func fontsRegisterAndResolve() {
        #expect(Brand.registerFonts())
        for name in Brand.fontNames {
            let font = CTFontCreateWithName(name as CFString, 20, nil)
            // CoreText falls back to another face for a name it does not know
            #expect(CTFontCopyPostScriptName(font) as String == name, "\(name) resolves")
        }
        let families = CTFontManagerCopyAvailableFontFamilyNames() as? [String] ?? []
        #expect(families.contains(MaktabaTokens.Fonts.heading), "Amiri")
        #expect(families.contains(MaktabaTokens.Fonts.body), "IBM Plex Sans Arabic")
        // registering again is harmless (an app may list the same files)
        #expect(Brand.registerFonts())
    }

    @Test func artAndLogoAreInTheBundle() throws {
        for dark in [false, true] {
            for wide in [false, true] {
                let url = try #require(Bundle.module.url(forResource: Brand.artName(dark: dark, wide: wide), withExtension: "jpg"))
                let size = try #require(imageSize(url))
                #expect(wide ? size.width > size.height : size.height > size.width, "\(url.lastPathComponent)")
            }
        }
        let logo = try #require(Bundle.module.url(forResource: Brand.logoName, withExtension: "png"))
        #expect(imageSize(logo) == CGSize(width: 512, height: 512))
        for name in ["OFL-Amiri", "OFL-IBMPlexSansArabic"] {
            #expect(Bundle.module.url(forResource: name, withExtension: "txt") != nil, "\(name): the fonts' licence ships with them")
        }
    }

    // MARK: the brand's words

    @Test func appFromBundleID() {
        #expect(MaktabaApp.from(bundleID: "com.hamzalabs.maktaba.player") == .player)
        #expect(MaktabaApp.from(bundleID: "com.hamzalabs.maktaba.player.tablet") == .player)
        #expect(MaktabaApp.from(bundleID: "com.hamzalabs.maktaba.player.mac") == .player)
        #expect(MaktabaApp.from(bundleID: "com.hamzalabs.maktaba.remote.tablet") == .remote)
        #expect(MaktabaApp.from(bundleID: "com.hamzalabs.maktaba.tv") == nil)
        #expect(MaktabaApp.from(bundleID: "com.apple.dt.xctest.tool") == nil)
        #expect(MaktabaApp.from(bundleID: nil) == nil)
    }

    @Test func wordsInEnglishAndArabic() {
        let en = Locale(identifier: "en"), ar = Locale(identifier: "ar")
        #expect(kit("Maktaba", en) == "Maktaba")
        #expect(kit("Maktaba", ar) == "مكتبة")
        #expect(MaktabaApp.player.name(en) == "Player")
        #expect(MaktabaApp.player.name(ar) == "المشغّل")
        #expect(MaktabaApp.remote.name(ar) == "الإدارة عن بُعد")
        #expect(MaktabaApp.player.tagline(ar).contains("جهازك الرئيسي"))
        #expect(MaktabaApp.remote.tagline(ar).contains("الجهاز الرئيسي"))
    }

    // MARK: the start frame renders on the brand, light and dark

    /// The backdrop and the brand, as an iPhone and an iPad show them: the dark frame is dark
    /// (ink and the dark art, no white), the light one is light; the PNGs go to
    /// `MAKTABA_SHOTS` when it is set.
    @MainActor @Test(arguments: [(390.0, 844.0, "iphone"), (1024.0, 768.0, "ipad")])
    func startFrameRendersOnTheBrand(width: Double, height: Double, device: String) throws {
        for scheme in [ColorScheme.dark, .light] {
            for (lang, dir) in [("en", LayoutDirection.leftToRight), ("ar", .rightToLeft)] {
                let locale = Locale(identifier: lang)
                let view = ZStack(alignment: .top) {
                    StartBackdrop()
                    StartBrand(appName: MaktabaApp.player.name(locale)).padding(.top, 60)
                }
                .frame(width: width, height: height)
                .environment(\.colorScheme, scheme)
                .environment(\.layoutDirection, dir)
                let renderer = ImageRenderer(content: view)
                renderer.scale = 1
                let image = try #require(renderer.cgImage)
                let lum = meanLuminance(image)
                if scheme == .dark {
                    #expect(lum < 0.3, "dark \(device) \(lang): mean luminance \(lum)")
                } else {
                    #expect(lum > 0.5, "light \(device) \(lang): mean luminance \(lum)")
                }
                save(image, "brand-start-\(device)-\(lang)-\(scheme == .dark ? "dark" : "light")")
            }
        }
    }

    // MARK: the launch screen: ink with the logo (FR-3)

    /// apps/apple, from this file.
    private var appleDir: URL {
        URL(fileURLWithPath: #filePath).deletingLastPathComponent() // MaktabaKitTests
            .deletingLastPathComponent().deletingLastPathComponent() // Tests, MaktabaKit
            .deletingLastPathComponent()
    }

    @Test func launchScreenIsInkWithTheLogo() throws {
        for plist in ["Player/Info-iPhone.plist", "Player/Info-iPad.plist", "Remote/Info-iPhone.plist", "Remote/Info-iPad.plist"] {
            let data = try Data(contentsOf: appleDir.appendingPathComponent(plist))
            let info = try #require(try PropertyListSerialization.propertyList(from: data, format: nil) as? [String: Any])
            let launch = try #require(info["UILaunchScreen"] as? [String: Any], "\(plist) declares its launch screen")
            #expect(launch["UIColorName"] as? String == "LaunchBackground", "\(plist)")
            #expect(launch["UIImageName"] as? String == "LaunchLogo", "\(plist)")
        }
        let assets = appleDir.appendingPathComponent("Shared/Assets.xcassets")
        let colorJSON = try Data(contentsOf: assets.appendingPathComponent("LaunchBackground.colorset/Contents.json"))
        let colorset = try #require(try JSONSerialization.jsonObject(with: colorJSON) as? [String: Any])
        let colors = try #require(colorset["colors"] as? [[String: Any]])
        #expect(colors.count == 2, "any and dark appearances")
        let ink = MaktabaTokens.Colors.bg.dark
        for entry in colors {
            let c = try #require((entry["color"] as? [String: Any])?["components"] as? [String: String])
            let rgb = try ["red", "green", "blue"].map { try #require(component(c[$0])) }
            // ink in light mode too: the system draws the launch screen before the app's theme
            #expect(rgb == [Int(ink >> 24 & 0xFF), Int(ink >> 16 & 0xFF), Int(ink >> 8 & 0xFF)])
        }
        let logoJSON = try Data(contentsOf: assets.appendingPathComponent("LaunchLogo.imageset/Contents.json"))
        let imageset = try #require(try JSONSerialization.jsonObject(with: logoJSON) as? [String: Any])
        let files = (imageset["images"] as? [[String: Any]] ?? []).compactMap { $0["filename"] as? String }
        #expect(!files.isEmpty)
        for f in files {
            #expect(imageSize(assets.appendingPathComponent("LaunchLogo.imageset/\(f)")) != nil, "\(f)")
        }
    }

    // MARK: helpers

    private func component(_ s: String?) -> Int? {
        guard let s else { return nil }
        if s.hasPrefix("0x") { return Int(s.dropFirst(2), radix: 16) }
        return Double(s).map { Int(($0 * 255).rounded()) }
    }

    private func imageSize(_ url: URL) -> CGSize? {
        guard let src = CGImageSourceCreateWithURL(url as CFURL, nil),
              let image = CGImageSourceCreateImageAtIndex(src, 0, nil) else { return nil }
        return CGSize(width: image.width, height: image.height)
    }

    private func meanLuminance(_ image: CGImage) -> Double {
        let w = 64, h = 64
        var pixels = [UInt8](repeating: 0, count: w * h * 4)
        let ctx = CGContext(data: &pixels, width: w, height: h, bitsPerComponent: 8, bytesPerRow: w * 4,
                            space: CGColorSpaceCreateDeviceRGB(), bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)!
        ctx.draw(image, in: CGRect(x: 0, y: 0, width: w, height: h))
        var total = 0.0
        for i in stride(from: 0, to: pixels.count, by: 4) {
            total += (0.2126 * Double(pixels[i]) + 0.7152 * Double(pixels[i + 1]) + 0.0722 * Double(pixels[i + 2])) / 255
        }
        return total / Double(w * h)
    }

    private func save(_ image: CGImage, _ name: String) {
        guard let dir = ProcessInfo.processInfo.environment["MAKTABA_SHOTS"], !dir.isEmpty else { return }
        let url = URL(fileURLWithPath: dir).appendingPathComponent("\(name).png")
        try? FileManager.default.createDirectory(atPath: dir, withIntermediateDirectories: true)
        guard let dest = CGImageDestinationCreateWithURL(url as CFURL, "public.png" as CFString, 1, nil) else { return }
        CGImageDestinationAddImage(dest, image, nil)
        CGImageDestinationFinalize(dest)
    }
}
