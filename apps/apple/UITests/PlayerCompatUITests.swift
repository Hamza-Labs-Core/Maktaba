import XCTest

/// Story 58.01 (T05): a home whose API level this build is too old for shows Update this app
/// instead of signing in, in English and Arabic, against `MockHome`; once the home answers at
/// a level the app serves, Try again carries on to the sign-in. The default MockHome publishes
/// no level (a home from before the contract): every other UI test signs in through it.
@MainActor
final class PlayerCompatUITests: PlayerUITestCase {
    static let tooNew = #"{"id":"ui-home","name":"UI Test Home","version":"9.0.0","urls":[],"sign_in":{"password":true,"code":true},"api_level":99,"api_level_oldest":99,"client_floor":{}}"#
    static let level1 = #"{"id":"ui-home","name":"UI Test Home","version":"0.3.0","urls":[],"sign_in":{"password":true,"code":true},"api_level":1,"api_level_oldest":1,"client_floor":{}}"#

    func testUpdateThisAppEnglish() { updateThisApp("en") }
    func testUpdateThisAppArabic() { updateThisApp("ar") }

    private func updateThisApp(_ language: String) {
        launch(language)
        let field = app.textFields["picker.address"]
        XCTAssertTrue(field.waitForExistence(timeout: 10), "the home picker")
        home.stub("GET", "/api/discovery/home", body: Self.tooNew)
        type(field, home.url)
        app.buttons["picker.connect"].tap()

        let title = element("compat.title")
        XCTAssertTrue(title.waitForExistence(timeout: 20), "Update this app; the home saw \(home.seen)")
        XCTAssertEqual(title.label, language == "ar" ? "حدِّث هذا التطبيق" : "Update this app")
        XCTAssertTrue(element("compat.line.0").label.contains("UI Test Home"), "it names the home")
        XCTAssertTrue(element("compat.line.1").label.contains("App Store"), "it says which store")
        XCTAssertTrue(app.buttons["compat.otherHome"].exists, "Choose another home")
        XCTAssertFalse(home.seen.contains { $0.hasPrefix("POST /api/auth") || $0.hasPrefix("POST /api/pairing") },
                       "nothing signs in: \(home.seen)")
        shot("compat-update-app")

        // the app was updated (here: the home answers at level 1): Try again carries on
        home.stub("GET", "/api/discovery/home", body: Self.level1)
        app.buttons["compat.tryAgain"].tap()
        let byCode = app.buttons[language == "ar" ? "برمز" : "With a code"]
        XCTAssertTrue(byCode.waitForExistence(timeout: 20), "the sign-in screen after Try again")
        XCTAssertFalse(title.exists)
    }

    /// Replaces a text field's contents (read back: see `typeExactly`).
    private func type(_ field: XCUIElement, _ text: String) {
        field.typeExactly(text, in: app)
    }
}
