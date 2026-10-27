import XCTest

/// Drives Maktaba TV with the Siri remote against a real home. The home and the account
/// come from the environment (xcodebuild passes `TEST_RUNNER_<name>` through):
///   MAKTABA_HOME      e.g. http://192.168.50.177:8088
///   MAKTABA_USER      a viewer account on it
///   MAKTABA_PASSWORD  its password
///   MAKTABA_SHOTS     optional: a folder for screenshots of each step
/// Without them the tests are skipped (CI has no home).
@MainActor
final class MaktabaTVUITests: XCTestCase {
    let remote = XCUIRemote.shared
    var app: XCUIApplication!
    var env: [String: String] { ProcessInfo.processInfo.environment }

    override func setUp() async throws {
        continueAfterFailure = false
        guard env["MAKTABA_HOME"] != nil else { throw XCTSkip("MAKTABA_HOME is not set") }
        app = XCUIApplication()
    }

    /// Choose the home by its address, sign in with the password, browse a library, open a
    /// video and play it, leave and come back to resume.
    func testSignInBrowseAndPlay() throws {
        app.launchArguments = ["-uitest-reset"]
        app.launch()
        try signIn()

        // Home: the rails (or the newest videos)
        XCTAssertTrue(app.tabBars.buttons["Home"].waitForExistence(timeout: 20))
        shot("home")

        // Libraries: the first library, its first video
        remote.press(.up)
        moveFocus(to: app.tabBars.buttons["Libraries"], with: .right)
        remote.press(.down)
        sleep(3)
        shot("libraries")
        remote.press(.select)
        sleep(4)
        shot("library")
        remote.press(.select)
        sleep(4)
        shot("opened")
        let play = app.buttons.matching(NSPredicate(format: "label BEGINSWITH 'Play' OR label BEGINSWITH 'Resume'")).firstMatch
        XCTAssertTrue(play.waitForExistence(timeout: 20))
        shot("video")

        // play it (a transcode may take a while to start)
        remote.press(.select)
        sleep(45)
        shot("playing")
        remote.press(.menu) // closes the player
        sleep(4)
        shot("after-playing")
        // back on the page: it now offers to resume
        XCTAssertTrue(app.buttons.matching(NSPredicate(format: "label BEGINSWITH 'Resume'")).firstMatch.waitForExistence(timeout: 20))
        shot("resume")
        remote.press(.select)
        sleep(30)
        shot("resumed")
        remote.press(.menu)
        sleep(4)
        shot("after-resumed")
    }

    /// Plays the library's second video (MAKTABA_TRANSCODE_INDEX, default 1): on the test
    /// home it is one the home must transcode, started cold.
    func testColdTranscodePlays() throws {
        app.launchArguments = ["-uitest-reset"]
        app.launch()
        try signIn()
        XCTAssertTrue(app.tabBars.buttons["Home"].waitForExistence(timeout: 20))
        remote.press(.up)
        moveFocus(to: app.tabBars.buttons["Libraries"], with: .right)
        remote.press(.down)
        sleep(3)
        remote.press(.select)
        sleep(4)
        for _ in 0..<(Int(env["MAKTABA_TRANSCODE_INDEX"] ?? "1") ?? 1) { remote.press(.right); usleep(500_000) }
        remote.press(.select)
        let play = app.buttons.matching(NSPredicate(format: "label BEGINSWITH 'Play' OR label BEGINSWITH 'Resume'")).firstMatch
        XCTAssertTrue(play.waitForExistence(timeout: 20))
        shot("transcode-video")
        remote.press(.select)
        sleep(20)
        shot("transcode-starting")
        sleep(25)
        shot("transcode-playing")
        XCTAssertFalse(app.staticTexts["Can't play"].exists, "the transcode did not play")
        remote.press(.menu)
    }

    /// The sign-in screen offers a code (and its QR code) on a home that has codes.
    func testShowsASignInCode() throws {
        app.launchArguments = ["-uitest-reset"]
        app.launch()
        try chooseHome()
        let code = app.staticTexts["pairing-code"]
        guard code.waitForExistence(timeout: 20) else {
            throw XCTSkip("this home signs in by password only")
        }
        shot("code")
    }

    /// Signs in by code: the test writes the code it reads off the screen to
    /// `MAKTABA_SHOTS/code.txt`; something outside (a person, a script signed in as
    /// MAKTABA_USER) approves it, and the TV is then signed in as that person.
    func testSignInByCode() throws {
        app.launchArguments = ["-uitest-reset"]
        app.launch()
        try chooseHome()
        let code = app.staticTexts["pairing-code"]
        guard code.waitForExistence(timeout: 20) else {
            throw XCTSkip("this home signs in by password only")
        }
        shot("code")
        if let dir = env["MAKTABA_SHOTS"] {
            try code.label.replacingOccurrences(of: " ", with: "").write(toFile: dir + "/code.txt", atomically: true, encoding: .utf8)
        }
        XCTAssertTrue(app.tabBars.buttons["Home"].waitForExistence(timeout: 120), "not signed in")
        sleep(2)
        shot("signed-in-by-code")
        remote.press(.up)
        moveFocus(to: app.tabBars.buttons["Settings"], with: .right)
        remote.press(.down)
        sleep(2)
        shot("settings")
        if let user = env["MAKTABA_USER"] {
            let shown = app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS %@ OR value CONTAINS %@", user, user)).firstMatch
            XCTAssertTrue(shown.waitForExistence(timeout: 10), "not signed in as \(user)")
        }
    }

    // MARK: steps

    func chooseHome() throws {
        let address = app.textFields["picker.address"]
        XCTAssertTrue(address.waitForExistence(timeout: 20))
        moveFocus(to: address, with: .down)
        remote.press(.select)
        app.typeText(try XCTUnwrap(env["MAKTABA_HOME"]))
        finishTyping() // submits the field: Connect
        pressIfStillThere(app.buttons["picker.connect"], with: .right)
    }

    func signIn() throws {
        try chooseHome()
        let usePassword = app.buttons["signin.password"]
        let username = app.textFields["signin.username"]
        _ = username.waitForExistence(timeout: 10) || usePassword.waitForExistence(timeout: 10)
        shot("sign-in")
        if !username.exists {
            moveFocus(to: usePassword, with: .down)
            remote.press(.select)
        }
        XCTAssertTrue(username.waitForExistence(timeout: 10))
        moveFocus(to: username, with: .up)
        remote.press(.select)
        app.typeText(try XCTUnwrap(env["MAKTABA_USER"]))
        finishTyping() // "next": the keyboard goes on to the password
        app.typeText(try XCTUnwrap(env["MAKTABA_PASSWORD"]))
        finishTyping() // submits the field: Sign in
        pressIfStillThere(app.buttons["signin.submit"], with: .down)
    }

    // MARK: helpers

    /// Presses `button` if the screen still shows it (a submitted field may have done its work).
    func pressIfStillThere(_ button: XCUIElement, with direction: XCUIRemote.Button) {
        guard button.waitForExistence(timeout: 2), button.isEnabled else { return }
        moveFocus(to: button, with: direction)
        remote.press(.select)
    }

    /// Leaves the full-screen keyboard by its own button (next / done), the text kept.
    func finishTyping() {
        let done = app.buttons.matching(NSPredicate(format: "label IN[c] {'next', 'done', 'go', 'search'}")).firstMatch
        XCTAssertTrue(done.waitForExistence(timeout: 5), "no keyboard button")
        moveFocus(to: done, with: .down)
        remote.press(.select)
        sleep(1)
    }

    /// Presses `direction` until `element` has focus (at most 12 times).
    func moveFocus(to element: XCUIElement, with direction: XCUIRemote.Button) {
        XCTAssertTrue(element.waitForExistence(timeout: 10), "\(element) is not there")
        for _ in 0..<12 where !element.hasFocus {
            remote.press(direction)
            usleep(400_000)
        }
        XCTAssertTrue(element.hasFocus, "could not focus \(element)")
    }

    func shot(_ name: String) {
        let s = XCUIScreen.main.screenshot()
        let a = XCTAttachment(screenshot: s)
        a.name = name
        a.lifetime = .keepAlways
        add(a)
        if let dir = env["MAKTABA_SHOTS"] {
            try? s.pngRepresentation.write(to: URL(fileURLWithPath: dir).appendingPathComponent("\(name).png"))
        }
    }
}
