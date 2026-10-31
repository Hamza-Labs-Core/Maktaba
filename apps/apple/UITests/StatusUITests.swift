import XCTest

/// The Remote's running work (its updates, processes and running jobs) against `MockHome`'s
/// made-up fixtures: each screen reads and shows them. MAKTABA_SHOTS, when set, is a folder
/// the screenshots are written to as well.
@MainActor
final class StatusUITests: XCTestCase {
    var app: XCUIApplication!
    var home: MockHome!

    override func setUp() async throws {
        continueAfterFailure = false
        home = try MockHome()
        try home.start()
        app = XCUIApplication()
    }

    /// Opens the app in `language` (the walkthrough passed with its permissions faked).
    private func launch(_ language: String = "en", locale: String = "en_US") {
        app.launchArguments = ["-maktaba-permissions-reset", "-maktaba-permissions-fake", "granted", "-maktaba-session-reset",
                               "-AppleLanguages", "(\(language))", "-AppleLocale", locale]
        app.launch()
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

    func testRunningWork() throws {
        launch()
        signInByCode()
        let homeTab = app.buttons["Home"].firstMatch
        try XCTSkipUnless(app.buttons["Queue"].firstMatch.waitForExistence(timeout: 10), "the Remote's screens (the Player has none)")

        // the queue: the jobs running now above the stages
        app.buttons["Queue"].firstMatch.tap()
        XCTAssertTrue(app.staticTexts["Seerah, lecture 12"].waitForExistence(timeout: 20), "a running job")
        XCTAssertTrue(home.seen.contains("GET /api/queues/running"))
        shot("queue-running")

        homeTab.tap()
        let updates = element("home.updates")
        XCTAssertTrue(updates.waitForExistence(timeout: 10))
        updates.tap()
        XCTAssertTrue(element("updates.blocked.0.3.45").waitForExistence(timeout: 10), "the blocked release still to come")
        XCTAssertFalse(element("updates.blocked.0.3.42").exists, "one a newer release passed by is history")
        XCTAssertTrue(home.seen.contains("GET /api/cluster/updates/runs/r10"))
        shot("updates")
        app.swipeUp()
        shot("updates-run")
        app.swipeUp()
        shot("updates-machines")
        app.navigationBars.buttons.firstMatch.tap()

        let processes = element("home.processes")
        XCTAssertTrue(processes.waitForExistence(timeout: 10))
        processes.tap()
        XCTAssertTrue(app.staticTexts["TV slot amber"].waitForExistence(timeout: 10))
        shot("processes")
        app.swipeUp()
        shot("processes-events")
    }

    /// The same screens in Arabic, right to left: the Home tab's running work and the
    /// Updates page say what the web's ar.json says.
    func testRunningWorkInArabic() throws {
        launch("ar", locale: "ar_JO")
        signInByCode(byCode: "برمز")
        let homeTab = app.buttons["الجهاز الرئيسي"].firstMatch
        try XCTSkipUnless(homeTab.waitForExistence(timeout: 10), "the Remote's screens (the Player has none)")
        homeTab.tap()
        let updates = element("home.updates")
        XCTAssertTrue(updates.waitForExistence(timeout: 10))
        XCTAssertTrue(app.staticTexts["العمل الجاري"].waitForExistence(timeout: 10), "the Running work section in Arabic")
        shot("ar-home")
        updates.tap()
        XCTAssertTrue(element("updates.blocked.0.3.45").waitForExistence(timeout: 10))
        // the plan's sentence and the run's state from MaktabaKit's catalog
        XCTAssertTrue(app.staticTexts["التحديثات"].exists || app.navigationBars["التحديثات"].exists, "the Updates title in Arabic")
        shot("ar-updates")
        app.swipeUp()
        shot("ar-updates-run")
    }

    private func signInByCode(byCode label: String = "With a code") {
        let field = app.textFields["picker.address"]
        XCTAssertTrue(field.waitForExistence(timeout: 10))
        field.tap()
        field.typeText(home.url)
        app.buttons["picker.connect"].tap()
        let byCode = app.buttons[label]
        XCTAssertTrue(byCode.waitForExistence(timeout: 10))
        byCode.tap()
        home.approve()
        XCTAssertTrue(byCode.waitForNonExistence(timeout: 20), "approved: signed in")
    }

    private func shot(_ name: String) {
        let png = app.screenshot().pngRepresentation
        let a = XCTAttachment(data: png, uniformTypeIdentifier: "public.png")
        a.name = name
        a.lifetime = .keepAlways
        add(a)
        if let dir = ProcessInfo.processInfo.environment["MAKTABA_SHOTS"] {
            let device = UIDevice.current.userInterfaceIdiom == .pad ? "ipad" : "iphone"
            try? png.write(to: URL(fileURLWithPath: dir).appendingPathComponent("remote-\(device)-\(name).png"))
        }
    }
}
