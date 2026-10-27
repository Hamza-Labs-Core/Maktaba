import XCTest

/// The home picker and signing in by code (Epic 42 stories 42.2 and 42.6) on iPhone and
/// iPad, against `MockHome` (served by the test runner on 127.0.0.1). Launched as a first
/// run with no homes (`-maktaba-session-reset`) and a scripted Local Network check.
@MainActor
final class ConnectUITests: XCTestCase {
    var app: XCUIApplication!
    var home: MockHome!

    override func setUp() async throws {
        continueAfterFailure = false
        home = try MockHome()
        try home.start()
        app = XCUIApplication()
        app.launchArguments = ["-maktaba-permissions-reset", "-maktaba-permissions-fake", "granted", "-maktaba-session-reset",
                               "-AppleLanguages", "(en)", "-AppleLocale", "en_US"]
        app.launch()
        dismissWalkthrough()
    }

    /// The first run's permissions walkthrough.
    private func dismissWalkthrough() {
        let walkthrough = app.descendants(matching: .any)["permissions.walkthrough"].firstMatch
        if walkthrough.waitForExistence(timeout: 10) {
            app.buttons["permissions.continue"].tap()
            XCTAssertTrue(walkthrough.waitForNonExistence(timeout: 5))
        }
    }

    override func tearDown() async throws {
        home?.stop()
    }

    private func element(_ id: String) -> XCUIElement { app.descendants(matching: .any)[id].firstMatch }

    /// A wrong address says so; the mock home's address leads to signing in; its code,
    /// approved elsewhere, signs the app in.
    func testTypedAddressThenSignInByCode() {
        let field = app.textFields["picker.address"]
        XCTAssertTrue(field.waitForExistence(timeout: 10))
        field.tap()
        field.typeText("ftp://nothing")
        app.buttons["picker.connect"].tap()
        XCTAssertTrue(element("picker.problem").waitForExistence(timeout: 5), "not an address")

        field.tap()
        field.typeText(String(repeating: XCUIKeyboardKey.delete.rawValue, count: 20) + home.url)
        app.buttons["picker.connect"].tap()

        let byCode = app.buttons["With a code"]
        XCTAssertTrue(byCode.waitForExistence(timeout: 10), "the sign-in screen offers a code")
        XCTAssertTrue(home.seen.contains("GET /api/discovery/home"))
        byCode.tap()
        let spelled = MockHome.code.map(String.init).joined(separator: " ")
        let code = app.descendants(matching: .any).matching(NSPredicate(format: "label == %@ OR identifier == 'signin.code'", spelled)).firstMatch
        if !code.waitForExistence(timeout: 10) {
            shot("no-code")
            XCTFail("the code shows; the home saw \(home.seen)")
        }
        shot("code")

        home.approve()
        XCTAssertTrue(code.waitForNonExistence(timeout: 15), "approved: signed in")
        XCTAssertTrue(home.seen.contains("GET /api/auth/me"))
        XCTAssertFalse(app.buttons["With a code"].exists)
    }

    /// A home's connect link (its QR code, or a shared link) opens the app on that home,
    /// once the person says yes (anyone can make a link).
    func testConnectLinkOpensTheHome() {
        XCTAssertTrue(app.textFields["picker.address"].waitForExistence(timeout: 10))
        app.open(URL(string: "maktaba://connect?id=ui-home&n=Home&u=http://192.0.2.1:9&u=\(home.url)")!)
        // XCUITest relaunches the app with its launch arguments: the first-run walkthrough again
        dismissWalkthrough()
        let yes = app.buttons["link.connect"]
        XCTAssertTrue(yes.waitForExistence(timeout: 10), "the app asks first")
        XCTAssertFalse(home.seen.contains("GET /api/discovery/home"), "nothing asked before yes")
        shot("asks")
        yes.tap()
        XCTAssertTrue(app.buttons["With a code"].waitForExistence(timeout: 20), "the link's second address answered")
        XCTAssertTrue(home.seen.contains("GET /api/discovery/home"))
        shot("linked")
        // the home is remembered: back in the picker it is a recent home
        app.buttons["signin.homes"].tap()
        let recent = app.buttons.matching(NSPredicate(format: "label CONTAINS %@", "127.0.0.1:\(home.port)")).firstMatch
        XCTAssertTrue(recent.waitForExistence(timeout: 5))
    }

    /// The Remote's queue on a wide window (story 42.8): the stages and the chosen stage's
    /// page side by side; on a narrow one the page is pushed over the list (the phone).
    func testQueueShowsTheStageBesideTheListWhenWide() throws {
        try XCTSkipUnless(UIDevice.current.userInterfaceIdiom == .pad, "the iPad's layout")
        signInByCode()
        let queue = app.buttons["Queue"].firstMatch
        try XCTSkipUnless(queue.waitForExistence(timeout: 10), "the Remote's queue (the Player has none)")
        queue.tap()
        XCTAssertTrue(app.staticTexts["Choose a stage"].waitForExistence(timeout: 10), "nothing chosen yet")
        let probe = app.staticTexts["Probe"].firstMatch
        XCTAssertTrue(probe.waitForExistence(timeout: 10))
        probe.tap()
        XCTAssertTrue(app.staticTexts["Waiting"].waitForExistence(timeout: 10), "the stage's page")
        let transcribe = app.staticTexts["Transcribe"].firstMatch
        XCTAssertTrue(transcribe.exists, "the list stays beside it")
        XCTAssertLessThan(transcribe.frame.maxX, app.staticTexts["Waiting"].frame.minX, "the list leads")
        XCTAssertTrue(home.seen.contains("GET /api/queues/probe/failures"))
        shot("queue-split")
    }

    /// A typed address and a code approved elsewhere: signed in to the mock home.
    private func signInByCode() {
        let field = app.textFields["picker.address"]
        XCTAssertTrue(field.waitForExistence(timeout: 10))
        field.tap()
        field.typeText(home.url)
        app.buttons["picker.connect"].tap()
        let byCode = app.buttons["With a code"]
        XCTAssertTrue(byCode.waitForExistence(timeout: 10))
        byCode.tap()
        home.approve()
        XCTAssertTrue(byCode.waitForNonExistence(timeout: 20), "approved: signed in")
    }

    private func shot(_ name: String) {
        let a = XCTAttachment(screenshot: app.screenshot())
        a.name = name
        a.lifetime = .keepAlways
        add(a)
    }
}
