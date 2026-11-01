import XCTest

/// The Apple brand (Epic 48 story 48.10) on iPhone and iPad, against `MockHome`: the home
/// picker, the by-address step and the sign-in screen stand on the library art, with the
/// logo, «Maktaba» in Amiri and the app's name, over a frosted panel; dark and light, English
/// and Arabic. With no theme chosen the app is dark, whatever the system's setting (FR-2).
final class PlayerBrandTests: PlayerUITestCase {
    /// The brand in each language: the wordmark and the Player's name.
    private let brand = ["en": ("Maktaba", "Player"), "ar": ("مكتبة", "المشغّل")]

    /// The picker, then (by address) the sign-in screen, in both themes and languages.
    func testStartScreensShowTheBrand() {
        for language in ["en", "ar"] {
            for theme in ["dark", "light"] {
                launchFirstRun(language, theme: theme)
                let field = app.textFields["picker.address"]
                XCTAssertTrue(field.waitForExistence(timeout: 10), "the home picker")
                expectBrand(language, on: "picker", theme: theme)
                brandShot("brand-picker-\(theme)")

                // by address: the step that asks the typed address, then the sign-in screen
                field.tap()
                field.typeText(home.url)
                app.buttons["picker.connect"].tap()
                XCTAssertTrue(element("signin.method").waitForExistence(timeout: 10), "the sign-in screen")
                expectBrand(language, on: "sign-in", theme: theme)
                brandShot("brand-signin-\(theme)")
                app.terminate()
            }
        }
    }

    /// Nothing chosen: dark. A chosen Light is kept (the launch argument stands for a stored
    /// choice: it is read from the same defaults key).
    func testNothingChosenIsDarkAndAChoiceIsKept() {
        launchFirstRun("en", theme: nil)
        XCTAssertTrue(app.textFields["picker.address"].waitForExistence(timeout: 10))
        let unchosen = meanLuminance()
        XCTAssertLessThan(unchosen, 0.3, "no theme chosen: the picker is dark (\(unchosen))")
        brandShot("brand-first-launch")
        app.terminate()

        launchFirstRun("en", theme: "light")
        XCTAssertTrue(app.textFields["picker.address"].waitForExistence(timeout: 10))
        let light = meanLuminance()
        XCTAssertGreaterThan(light, 0.5, "Light chosen: the picker is light (\(light))")
    }

    // MARK: helpers

    /// A first run (no homes, the walkthrough passed) in `language`, with `theme` as the
    /// stored choice (nil: none).
    private func launchFirstRun(_ language: String, theme: String?) {
        let locale = language == "ar" ? "ar_JO" : "en_US"
        var args = ["-AppleLanguages", "(\(language))", "-AppleLocale", locale,
                    "-maktaba-permissions-reset", "-maktaba-permissions-fake", "granted", "-maktaba-session-reset"]
        if let theme { args += ["-theme", theme] }
        app.launchArguments = args
        app.launch()
        shotLanguage = language
        let walkthrough = element("permissions.walkthrough")
        if walkthrough.waitForExistence(timeout: 10) {
            app.buttons["permissions.continue"].tap()
            XCTAssertTrue(walkthrough.waitForNonExistence(timeout: 5))
        }
    }

    /// The language of this test's last launch (it launches by itself, to choose the theme).
    private var shotLanguage = "en"

    /// As `shot`, named `player-<iphone|ipad>-<language>-<name>` by this test's language.
    private func brandShot(_ name: String) {
        let full = "player-\(isPad ? "ipad" : "iphone")-\(shotLanguage)-\(name)"
        let png = app.screenshot().pngRepresentation
        let a = XCTAttachment(data: png, uniformTypeIdentifier: "public.png")
        a.name = full
        a.lifetime = .keepAlways
        add(a)
        if let dir = ProcessInfo.processInfo.environment["MAKTABA_SHOTS"], !dir.isEmpty {
            try? FileManager.default.createDirectory(atPath: dir, withIntermediateDirectories: true)
            try? png.write(to: URL(fileURLWithPath: dir).appendingPathComponent("\(full).png"))
        }
    }

    private func expectBrand(_ language: String, on screen: String, theme: String) {
        let (wordmark, appName) = brand[language]!
        let mark = element("start.brand")
        XCTAssertTrue(mark.waitForExistence(timeout: 5), "\(screen) \(language) \(theme): the brand shows")
        XCTAssertTrue(mark.label.contains(wordmark), "\(screen) \(language): «\(wordmark)» (\(mark.label))")
        XCTAssertTrue(mark.label.contains(appName), "\(screen) \(language): the app's name (\(mark.label))")
        // the logo and the wordmark sit above the panel, in the top half of the screen
        let height = app.windows.firstMatch.frame.height
        XCTAssertLessThan(mark.frame.midY, height * 0.5, "\(screen): the brand at the top")
    }

    /// The screen's mean luminance (0 black … 1 white), from a 64 × 64 sample.
    private func meanLuminance() -> Double {
        guard let cg = app.screenshot().image.cgImage else { return -1 }
        let w = 64, h = 64
        var pixels = [UInt8](repeating: 0, count: w * h * 4)
        let ctx = CGContext(data: &pixels, width: w, height: h, bitsPerComponent: 8, bytesPerRow: w * 4,
                            space: CGColorSpaceCreateDeviceRGB(), bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)!
        ctx.draw(cg, in: CGRect(x: 0, y: 0, width: w, height: h))
        var total = 0.0
        for i in stride(from: 0, to: pixels.count, by: 4) {
            total += (0.2126 * Double(pixels[i]) + 0.7152 * Double(pixels[i + 1]) + 0.0722 * Double(pixels[i + 2])) / 255
        }
        return total / Double(w * h)
    }
}
