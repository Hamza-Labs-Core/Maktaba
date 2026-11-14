import XCTest

/// Story 58.01 (T05) on Apple TV: a home this build is too old for shows Update this app,
/// with the App Store on Apple TV, in English and Arabic, with the Siri remote, against
/// `MockHome`; Try again (focused) carries on once the home answers at a level the app serves.
/// With MAKTABA_SHOTS set, each screenshot is also written there.
@MainActor
final class TVCompatUITests: XCTestCase {
    static let tooNew = #"{"id":"ui-home","name":"UI Test Home","version":"9.0.0","urls":[],"sign_in":{"password":true,"code":true},"api_level":99,"api_level_oldest":99,"client_floor":{}}"#
    static let level1 = #"{"id":"ui-home","name":"UI Test Home","version":"0.3.0","urls":[],"sign_in":{"password":true,"code":true},"api_level":1,"api_level_oldest":1,"client_floor":{}}"#

    let remote = XCUIRemote.shared
    var app: XCUIApplication!
    var home: MockHome!

    override func setUp() async throws {
        continueAfterFailure = false
        home = try MockHome()
        try home.start()
        app = XCUIApplication()
    }

    override func tearDown() async throws {
        home?.stop()
    }

    func testUpdateThisAppEnglish() { updateThisApp("en") }
    func testUpdateThisAppArabic() { updateThisApp("ar") }

    private func updateThisApp(_ language: String) {
        app.launchArguments = ["-uitest-reset", "-AppleLanguages", "(\(language))", "-AppleLocale", language == "ar" ? "ar_SA" : "en_US"]
        app.launch()
        home.stub("GET", "/api/discovery/home", body: Self.tooNew)
        type(home.url, into: app.textFields["picker.address"])
        pressIfStillThere(app.buttons["picker.connect"], with: .right)

        let title = element("compat.title")
        XCTAssertTrue(title.waitForExistence(timeout: 20), "Update this app; the home saw \(home.seen)")
        XCTAssertEqual(title.label, language == "ar" ? "حدِّث هذا التطبيق" : "Update this app")
        XCTAssertTrue(element("compat.line.0").label.contains("UI Test Home"), "it names the home")
        XCTAssertTrue(element("compat.line.1").label.contains("Apple TV"), "the App Store on Apple TV")
        XCTAssertFalse(home.seen.contains { $0.hasPrefix("POST /api/pairing") || $0.hasPrefix("POST /api/auth") },
                       "nothing signs in: \(home.seen)")
        let again = app.buttons["compat.tryAgain"]
        XCTAssertTrue(again.waitForExistence(timeout: 5))
        XCTAssertTrue(again.hasFocus, "Try again has the focus")
        XCTAssertTrue(app.buttons["compat.otherHome"].exists, "Choose another home")
        shot("tv-compat-update-app-\(language)")

        home.stub("GET", "/api/discovery/home", body: Self.level1)
        remote.press(.select)
        XCTAssertTrue(app.staticTexts["pairing-code"].waitForExistence(timeout: 20), "the code after Try again")
        XCTAssertFalse(title.exists)
    }

    // MARK: helpers

    private func element(_ id: String) -> XCUIElement { app.descendants(matching: .any)[id].firstMatch }

    private func type(_ text: String, into field: XCUIElement) {
        XCTAssertTrue(field.waitForExistence(timeout: 20))
        field.typeExactly(text, in: app) {
            self.moveFocus(to: field, with: .down)
            self.remote.press(.select)
        }
        let done = app.buttons.matching(NSPredicate(format: "label IN[c] {'next', 'done', 'go', 'search', 'التالي', 'تم', 'انتقال', 'بحث'}")).firstMatch
        if done.waitForExistence(timeout: 5) {
            moveFocus(to: done, with: .down)
            remote.press(.select)
        } else {
            remote.press(.menu)
        }
        sleep(1)
    }

    private func pressIfStillThere(_ button: XCUIElement, with direction: XCUIRemote.Button) {
        guard button.waitForExistence(timeout: 2), button.isEnabled, button.isHittable else { return }
        moveFocus(to: button, with: direction)
        remote.press(.select)
    }

    private func moveFocus(to element: XCUIElement, with direction: XCUIRemote.Button) {
        XCTAssertTrue(element.waitForExistence(timeout: 10), "\(element) is not there")
        for _ in 0..<12 where !element.hasFocus {
            remote.press(direction)
            usleep(400_000)
        }
        XCTAssertTrue(element.hasFocus, "could not focus \(element)")
    }

    private func shot(_ name: String) {
        let s = XCUIScreen.main.screenshot()
        let a = XCTAttachment(screenshot: s)
        a.name = name
        a.lifetime = .keepAlways
        add(a)
        if let dir = ProcessInfo.processInfo.environment["MAKTABA_SHOTS"] {
            try? s.pngRepresentation.write(to: URL(fileURLWithPath: dir).appendingPathComponent("\(name).png"))
        }
    }
}
