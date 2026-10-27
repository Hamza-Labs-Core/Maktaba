import XCTest

/// The home picker and signing in by code on Apple TV (Epic 42 story 42.10), with the Siri
/// remote, against `MockHome` (served by the test runner on 127.0.0.1; no real home and no
/// real account). Runs without MAKTABA_HOME. With MAKTABA_SHOTS set, each step's screenshot
/// is also written there.
@MainActor
final class TVConnectUITests: XCTestCase {
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

    private func launch(_ language: String = "en") {
        app.launchArguments = ["-uitest-reset", "-AppleLanguages", "(\(language))", "-AppleLocale", language == "ar" ? "ar_SA" : "en_US"]
        app.launch()
    }

    /// A typed address leads to the code (large, with its QR code); approved elsewhere, the
    /// TV is signed in. Back in the picker the home is a recent home.
    func testTypedAddressThenSignInByCode() throws {
        launch()
        type(home.url, into: app.textFields["picker.address"])
        pressIfStillThere(app.buttons["picker.connect"], with: .right)

        let spelled = MockHome.code.map(String.init).joined(separator: " ")
        let code = app.staticTexts["pairing-code"]
        XCTAssertTrue(code.waitForExistence(timeout: 20), "the code shows; the home saw \(home.seen)")
        XCTAssertEqual(code.label, spelled)
        XCTAssertTrue(home.seen.contains("GET /api/discovery/home"))
        XCTAssertTrue(home.seen.contains("POST /api/pairing/device"))
        shot("tv-code-en")

        home.approve()
        XCTAssertTrue(app.tabBars.buttons["Home"].waitForExistence(timeout: 20), "approved: signed in")
        XCTAssertTrue(home.seen.contains("GET /api/auth/me"))
    }

    /// A `maktaba://connect` link is asked about before anything is fetched; Connect tries
    /// its addresses in order (the first answers nothing) and signs in there.
    func testConnectLinkAsksFirst() throws {
        launch()
        XCTAssertTrue(app.textFields["picker.address"].waitForExistence(timeout: 20))
        app.open(URL(string: "maktaba://connect?id=ui-home&n=Den&u=http://192.0.2.1:9&u=\(home.url)")!)
        let yes = app.buttons["link.connect"]
        XCTAssertTrue(yes.waitForExistence(timeout: 20), "the TV asks first")
        XCTAssertFalse(home.seen.contains("GET /api/discovery/home"), "nothing asked before yes")
        shot("tv-link-asks")
        // Cancel has the focus first; Connect is before it
        XCTAssertTrue(app.buttons["link.cancel"].waitForExistence(timeout: 5))
        moveFocus(to: yes, with: .left)
        remote.press(.select)
        XCTAssertTrue(app.staticTexts["pairing-code"].waitForExistence(timeout: 30), "the link's second address answered")
    }

    /// The picker and the code in Arabic, right to left (screenshots to look at).
    func testArabic() throws {
        launch("ar")
        XCTAssertTrue(app.staticTexts["اختر جهازك الرئيسي"].waitForExistence(timeout: 20))
        shot("tv-picker-ar")
        type(home.url, into: app.textFields["picker.address"])
        pressIfStillThere(app.buttons["picker.connect"], with: .right)
        XCTAssertTrue(app.staticTexts["pairing-code"].waitForExistence(timeout: 20))
        shot("tv-code-ar")
        // the password is the other way
        let password = app.buttons["signin.password"]
        // right to left: it is the first button, on the right
        moveFocus(to: password, with: .right)
        remote.press(.select)
        XCTAssertTrue(app.textFields["signin.username"].waitForExistence(timeout: 10))
        shot("tv-password-ar")
    }

    // MARK: helpers

    private func type(_ text: String, into field: XCUIElement) {
        XCTAssertTrue(field.waitForExistence(timeout: 20))
        moveFocus(to: field, with: .down)
        remote.press(.select)
        app.typeText(text)
        // leave the full-screen keyboard by its own button (submits the field)
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
