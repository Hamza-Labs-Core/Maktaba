import XCTest

/// The Remote's product screenshot (story 60.09): its Queue, in English and in Arabic, dark
/// (the simulator's appearance, set by CI), against the demo library's home
/// (`MockHome(demo:)`: its name, and the running jobs on the demo's films). The stages and
/// counts are MockHome's own made-up queue, with no person's name in it. Attachments named
/// `shot-iphone-remote-<language>` (`shot-ipad-…` on an iPad), kept always: CI exports them
/// from the result bundle (`_apple-apps.yml`, Remote UI tests).
@MainActor
final class RemoteDemoShotsUITests: XCTestCase {
    var app: XCUIApplication!
    var home: MockHome?

    override func setUp() async throws {
        continueAfterFailure = false
        app = XCUIApplication()
    }

    override func tearDown() async throws {
        app?.terminate()
        home?.stop()
    }

    func testQueue() throws {
        for (language, locale) in [("en", "en_US"), ("ar", "ar_JO")] {
            app.terminate()
            home?.stop()
            let home = try MockHome(demo: language)
            home.clock = MockHome.demoClock
            try home.start()
            self.home = home
            signIn(language, locale: locale, home: home)

            let tab = app.buttons[language == "ar" ? "الطابور" : "Queue"].firstMatch
            XCTAssertTrue(tab.waitForExistence(timeout: 15), "the Queue tab")
            tab.tap()
            let title = home.demoVideo(Self.firstVideo(home))?["title"] as? String ?? ""
            XCTAssertFalse(title.isEmpty, "the demo's first video has a title")
            XCTAssertTrue(app.staticTexts[title].firstMatch.waitForExistence(timeout: 15),
                          "a running job on \(title); the home saw \(home.seen)")
            // its rows' counts in
            XCTAssertTrue(wait { home.seen.contains("GET /api/queues") && home.seen.contains("GET /api/queues/running") })
            let a = XCTAttachment(screenshot: XCUIScreen.main.screenshot())
            a.name = "shot-\(UIDevice.current.userInterfaceIdiom == .pad ? "ipad" : "iphone")-remote-\(language)"
            a.lifetime = .keepAlways
            add(a)
        }
    }

    /// The id of the demo's first video (`GET /api/videos`' first item): the first running job's.
    private static func firstVideo(_ home: MockHome) -> String {
        home.demoFirstVideoID ?? ""
    }

    private func element(_ id: String) -> XCUIElement { app.descendants(matching: .any)[id].firstMatch }

    /// A first run: past the permissions walkthrough, the mock home's address, a code approved.
    private func signIn(_ language: String, locale: String, home: MockHome) {
        app.launchArguments = ["-maktaba-permissions-reset", "-maktaba-permissions-fake", "granted", "-maktaba-session-reset",
                               "-AppleLanguages", "(\(language))", "-AppleLocale", locale]
        app.launch()
        let walkthrough = element("permissions.walkthrough")
        if walkthrough.waitForExistence(timeout: 10) {
            app.buttons["permissions.continue"].tap()
            XCTAssertTrue(walkthrough.waitForNonExistence(timeout: 5))
        }
        let field = app.textFields["picker.address"]
        XCTAssertTrue(field.waitForExistence(timeout: 10), "the home picker")
        field.typeExactly(home.url, in: app)
        // submitted from the field's own Return (`onSubmit`, the same `add` as Connect), as
        // PlayerUITestCase.signIn does: a Connect tap as the keyboard still settled after
        // typing was lost (main run 43556127370: no connect began, the picker stayed with the
        // address typed). Connect's own tap is ConnectUITests'.
        field.typeText("\n")
        let byCode = app.buttons[language == "ar" ? "برمز" : "With a code"]
        XCTAssertTrue(byCode.waitForExistence(timeout: 10), "the sign-in screen offers a code")
        byCode.tap()
        home.approve()
        XCTAssertTrue(byCode.waitForNonExistence(timeout: 20), "approved: signed in")
    }

    private func wait(timeout: TimeInterval = 10, _ condition: () -> Bool) -> Bool {
        let end = Date().addingTimeInterval(timeout)
        while Date() < end {
            if condition() { return true }
            RunLoop.current.run(until: Date().addingTimeInterval(0.25))
        }
        return condition()
    }
}
