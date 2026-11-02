import XCTest

/// The sign-in form from the keyboard (the owner: "sign in need the next field arrow"):
/// Return on the username moves the keyboard to the password, the password is in sight above
/// the on-screen keyboard, and Return on the password signs in. The mock home refuses the
/// password; what is checked is that it was sent.
///
/// The on-screen keyboard is the one measured (`app.keyboards`, its frame), and a missing one
/// fails here instead of the field passing as "above" the screen's edge. CI's simulators keep
/// the software keyboard up (`.github/workflows/_apple-apps.yml`: no hardware keyboard, no
/// minimizing it while keys are typed). Its shots are of the whole screen: the keyboard is a
/// window of its own, which the app's screenshot leaves out.
final class PlayerSignInKeyboardUITests: PlayerUITestCase {
    func testReturnMovesToThePasswordThenSignsIn() {
        returnMovesToThePasswordThenSignsIn()
    }

    /// At AX3 the brand and the method picker push the password down to where the keyboard
    /// rises: it is in sight only once the form has scrolled it up (`keepsFocusInView`).
    func testThePasswordScrollsAboveTheKeyboardAtALargeTextSize() {
        returnMovesToThePasswordThenSignsIn(extra: Self.ax3Text, shotName: "signin-keyboard-password-ax3")
    }

    private func returnMovesToThePasswordThenSignsIn(extra: [String] = [], shotName: String = "signin-keyboard-password",
                                                    file: StaticString = #filePath, line: UInt = #line) {
        launch(extra: extra)
        let field = app.textFields["picker.address"]
        XCTAssertTrue(element("start.brand").waitForExistence(timeout: 20), "the start screen", file: file, line: line)
        if !field.waitForExistence(timeout: 2) { scrollUntil(field) }
        field.typeExactly(home.url, in: app)
        // Return on the address connects (its key says Go)
        field.typeText("\n")

        home.stub("POST", "/api/auth/login", status: 401, body: #"{"title":"invalid credentials"}"#)
        let user = app.textFields["signin.username"]
        XCTAssertTrue(user.waitForExistence(timeout: 10), "the sign-in screen", file: file, line: line)
        if !user.isHittable { scrollUntil(user) }
        user.typeExactly("tester", in: app)
        user.typeText("\n")

        let password = app.secureTextFields["signin.password"]
        XCTAssertTrue(hasFocus(password), "Return on the username moved the keyboard to the password", file: file, line: line)
        assertAboveTheKeyboard(password, file: file, line: line)
        shot(shotName, wholeScreen: true)

        // the password already has the keyboard (typeExactly taps only a field that has not)
        password.typeExactly("not-the-password", in: app, secure: true)
        password.typeText("\n")
        XCTAssertTrue(element("signin.problem").waitForExistence(timeout: 10), "Return on the password signed in (refused)",
                      file: file, line: line)
        XCTAssertTrue(home.seen.contains("POST /api/auth/login"), "the sign-in was sent", file: file, line: line)
    }

    /// The on-screen keyboard is up (a full keyboard, not a hardware keyboard's minimized
    /// bar), and `e` lies wholly on the screen above the keyboard's top edge once the form has
    /// scrolled to it. Fails, with both frames, when the keyboard never shows or `e` stays
    /// under it.
    private func assertAboveTheKeyboard(_ e: XCUIElement, timeout: TimeInterval = 5, file: StaticString, line: UInt) {
        let keyboard = app.keyboards.firstMatch
        XCTAssertTrue(keyboard.waitForExistence(timeout: 5),
                      "the on-screen keyboard shows (the simulator has no hardware keyboard)", file: file, line: line)
        let screen = app.windows.firstMatch.frame
        var kb = CGRect.null, field = CGRect.null
        let end = Date().addingTimeInterval(timeout)
        repeat {
            kb = keyboard.exists ? keyboard.frame : .null
            field = e.exists ? e.frame : .null
            let up = !kb.isNull && kb.height >= Self.fullKeyboard && kb.minY < screen.maxY
            if up, !field.isNull, !field.isEmpty, field.minY >= screen.minY, field.maxY <= kb.minY {
                // in the test's log (the CI report): what was measured
                print("\(e.identifier) at \(field), above the on-screen keyboard at \(kb) (screen \(screen))")
                return
            }
            RunLoop.current.run(until: Date().addingTimeInterval(0.25))
        } while Date() < end
        XCTFail("\(e.identifier) at \(field) is not above the on-screen keyboard at \(kb) (screen \(screen))",
                file: file, line: line)
    }

    /// The least height of a full on-screen keyboard (an iPhone's is some 290 pt, an iPad's
    /// more); a hardware keyboard's minimized bar is far less.
    private static let fullKeyboard: CGFloat = 150

    private func hasFocus(_ e: XCUIElement, timeout: TimeInterval = 5) -> Bool {
        let focused = NSPredicate(format: "hasKeyboardFocus == true")
        return XCTWaiter().wait(for: [XCTNSPredicateExpectation(predicate: focused, object: e)], timeout: timeout) == .completed
    }
}
