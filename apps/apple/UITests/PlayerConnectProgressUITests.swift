import XCTest

/// Story 48.37: every wait on the connect, sign-in and restore screens says what is
/// happening (never a bare spinner), in English and Arabic, against `MockHome`. A held
/// route (`hold`, `release`) keeps a step on screen for its screenshot: a latch, not a timer.
@MainActor
final class PlayerConnectProgressUITests: PlayerUITestCase {
    func testPickerStatesEnglish() { pickerStates("en") }
    func testPickerStatesArabic() { pickerStates("ar") }
    func testSignInAndRestoreStatesEnglish() throws { try signInAndRestoreStates("en") }
    func testSignInAndRestoreStatesArabic() throws { try signInAndRestoreStates("ar") }

    /// Finding the home: each address tried, then why it failed (with what was tried and
    /// Retry), the next attempt, and an address where nothing answers.
    private func pickerStates(_ language: String) {
        launch(language)
        let field = app.textFields["picker.address"]
        XCTAssertTrue(field.waitForExistence(timeout: 10), "the home picker")

        // something answers, but not a Maktaba home; held so "Trying … — 1 of 2" stays up
        home.stub("GET", "/api/discovery/home", body: "<html>router</html>")
        home.hold("GET", "/api/discovery/home")
        type(field, "127.0.0.1:\(home.port)")
        app.buttons["picker.connect"].tap()
        XCTAssertTrue(element("connect.progress.trying").waitForExistence(timeout: 10), "each address shows as it is tried")
        shot("connect-trying")
        home.release()
        XCTAssertTrue(element("connect.progress.failed").waitForExistence(timeout: 20), "why it failed")
        XCTAssertTrue(app.buttons["picker.retry"].exists, "Retry")
        shot("connect-failed-not-a-home")

        // Retry: the same steps, as attempt 2
        app.buttons["picker.retry"].tap()
        XCTAssertTrue(element("connect.progress.attempt").waitForExistence(timeout: 20), "the attempt number")
        XCTAssertTrue(element("connect.progress.failed").waitForExistence(timeout: 20))
        shot("connect-retry-attempt-2")

        // nothing listens there: it did not answer
        home.reset()
        type(field, "127.0.0.1:9")
        app.buttons["picker.connect"].tap()
        XCTAssertTrue(element("connect.progress.failed").waitForExistence(timeout: 30))
        XCTAssertFalse(element("connect.progress.attempt").exists, "a new address starts at attempt 1")
        shot("connect-failed-unreachable")
    }

    /// Signing in (a code asked for, waiting for approval, a code that expired), a wrong
    /// password, then the saved sign-in at the next launch: reconnecting, failed with Retry,
    /// and the home busy with the countdown to its next try.
    private func signInAndRestoreStates(_ language: String) throws {
        launch(language)
        let field = app.textFields["picker.address"]
        XCTAssertTrue(field.waitForExistence(timeout: 10), "the home picker")
        type(field, home.url)
        app.buttons["picker.connect"].tap()
        let byCode = app.buttons[language == "ar" ? "برمز" : "With a code"]
        XCTAssertTrue(byCode.waitForExistence(timeout: 10), "the sign-in screen")

        // asking the home for a code
        home.hold("POST", "/api/pairing/device")
        byCode.tap()
        XCTAssertTrue(element("connect.progress.signingIn").waitForExistence(timeout: 10), "asking for a code")
        shot("signin-code-asking")
        home.release()
        XCTAssertTrue(element("connect.progress.awaitingApproval").waitForExistence(timeout: 10), "the code and its countdown")
        shot("signin-code-waiting")

        // nobody approved it in time: a new code, attempt 2
        home.stub("POST", "/api/pairing/device/token", status: 410, body: #"{"title":"code expired"}"#)
        XCTAssertTrue(element("connect.progress.attempt").waitForExistence(timeout: 15), "a new code after it expired")
        home.reset()
        XCTAssertTrue(element("connect.progress.awaitingApproval").waitForExistence(timeout: 10))
        shot("signin-code-new-attempt")

        // a wrong password, then signing in (held, so its words stay up)
        app.buttons[language == "ar" ? "اسم المستخدم وكلمة المرور" : "Username & password"].tap()
        home.stub("POST", "/api/auth/login", status: 401, body: #"{"title":"invalid credentials"}"#)
        let user = app.textFields["signin.username"]
        XCTAssertTrue(user.waitForExistence(timeout: 5))
        user.tap()
        user.typeText("tester")
        let password = app.secureTextFields["signin.password"]
        password.tap()
        password.typeText("not-the-password")
        app.buttons["signin.submit"].tap()
        XCTAssertTrue(element("signin.problem").waitForExistence(timeout: 10), "wrong password")
        shot("signin-wrong-password")
        home.hold("POST", "/api/auth/login")
        app.buttons["signin.submit"].tap()
        XCTAssertTrue(app.buttons.matching(NSPredicate(format: "label CONTAINS %@", "UI Test Home")).firstMatch
            .waitForExistence(timeout: 10), "the button says it is signing in")
        shot("signin-password-busy")
        home.reset()

        // signed in by code, for the saved sign-in at the next launch
        byCode.tap()
        home.approve()
        XCTAssertTrue(byCode.waitForNonExistence(timeout: 20), "approved: signed in")

        // the next launch checks the saved sign-in: held, then the home fails
        app.terminate()
        home.stub("GET", "/api/auth/me", status: 500, body: #"{"title":"boom"}"#)
        home.hold("GET", "/api/auth/me")
        launch(language, fresh: false)
        guard element("connect.progress.reconnecting").waitForExistence(timeout: 15) else {
            // the sign-in states above are kept; the restore's need a token from the last launch
            throw XCTSkip("no saved sign-in to restore (an unsigned build, as on CI, keeps no token in the Keychain)")
        }
        shot("restore-reconnecting")
        home.release()
        XCTAssertTrue(element("connect.progress.failed").waitForExistence(timeout: 15), "why")
        XCTAssertTrue(app.buttons["signin.restore.retry"].exists, "Retry")
        shot("restore-failed")

        // Retry: the home is starting (503, Retry-After), so the countdown to the next try
        home.stub("GET", "/api/auth/me", status: 503, body: #"{"title":"starting"}"#, headers: ["Retry-After": "6"])
        app.buttons["signin.restore.retry"].tap()
        XCTAssertTrue(element("connect.progress.homeBusy").waitForExistence(timeout: 10), "busy, with the countdown")
        shot("restore-busy")
        home.reset()
        XCTAssertTrue(app.buttons["signin.homes"].waitForNonExistence(timeout: 30), "asked again by itself: signed in")
    }

    /// Replaces a text field's contents.
    private func type(_ field: XCUIElement, _ text: String) {
        field.tap()
        let old = (field.value as? String) ?? ""
        field.typeText(String(repeating: XCUIKeyboardKey.delete.rawValue, count: old.count + 2) + text)
    }
}
