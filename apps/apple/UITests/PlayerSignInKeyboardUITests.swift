import XCTest

/// The sign-in form from the keyboard (the owner: "sign in need the next field arrow"):
/// Return on the username moves the keyboard to the password, and Return on the password
/// signs in. The mock home refuses the password; what is checked is that it was sent.
final class PlayerSignInKeyboardUITests: PlayerUITestCase {
    func testReturnMovesToThePasswordThenSignsIn() {
        launch()
        let field = app.textFields["picker.address"]
        XCTAssertTrue(element("start.brand").waitForExistence(timeout: 10), "the start screen")
        if !field.waitForExistence(timeout: 2) { scrollUntil(field) }
        field.typeExactly(home.url, in: app)
        // Return on the address connects (its key says Go)
        field.typeText("\n")

        home.stub("POST", "/api/auth/login", status: 401, body: #"{"title":"invalid credentials"}"#)
        let user = app.textFields["signin.username"]
        XCTAssertTrue(user.waitForExistence(timeout: 10), "the sign-in screen")
        user.typeExactly("tester", in: app)
        user.typeText("\n")

        let password = app.secureTextFields["signin.password"]
        XCTAssertTrue(hasFocus(password), "Return on the username moved the keyboard to the password")
        XCTAssertTrue(inSight(password), "the password is in sight, not under the keyboard")
        shot("signin-keyboard-password")

        // the password already has the keyboard (typeExactly taps only a field that has not)
        password.typeExactly("not-the-password", in: app, secure: true)
        password.typeText("\n")
        XCTAssertTrue(element("signin.problem").waitForExistence(timeout: 10), "Return on the password signed in (refused)")
        XCTAssertTrue(home.seen.contains("POST /api/auth/login"), "the sign-in was sent")
    }

    /// On the screen and above the on-screen keyboard (when it shows: a simulator with a
    /// hardware keyboard has none), once the form has scrolled to it.
    private func inSight(_ e: XCUIElement, timeout: TimeInterval = 5) -> Bool {
        let end = Date().addingTimeInterval(timeout)
        repeat {
            let screen = app.windows.firstMatch.frame
            let keyboard = app.keyboards.firstMatch
            let top = keyboard.exists ? keyboard.frame.minY : screen.maxY
            if e.exists, e.frame.minY >= screen.minY, e.frame.maxY <= top { return true }
            RunLoop.current.run(until: Date().addingTimeInterval(0.25))
        } while Date() < end
        return false
    }

    private func hasFocus(_ e: XCUIElement, timeout: TimeInterval = 5) -> Bool {
        let focused = NSPredicate(format: "hasKeyboardFocus == true")
        return XCTWaiter().wait(for: [XCTNSPredicateExpectation(predicate: focused, object: e)], timeout: timeout) == .completed
    }
}
