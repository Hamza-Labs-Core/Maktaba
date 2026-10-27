import XCTest

/// The permissions walkthrough on iPhone and iPad (Maktaba Player and Maktaba Remote). The
/// simulators have no local network privacy (TN3179), so the Local Network check is
/// scripted with the debug launch arguments `-maktaba-permissions-fake granted|denied`;
/// `-maktaba-permissions-reset` starts as a first run. MAKTABA_SHOTS, when set, is a folder
/// for a screenshot of each step.
@MainActor
final class PermissionsUITests: XCTestCase {
    var app: XCUIApplication!

    override func setUp() async throws {
        continueAfterFailure = false
        app = XCUIApplication()
    }

    private var walkthrough: XCUIElement { app.descendants(matching: .any)["permissions.walkthrough"].firstMatch }
    private var state: XCUIElement { app.descendants(matching: .any)["permissions.localNetwork.state"].firstMatch }

    private func launch(_ fake: String, reset: Bool, extra: [String] = []) {
        app.launchArguments = (reset ? ["-maktaba-permissions-reset"] : []) + ["-maktaba-permissions-fake", fake] + extra
        app.launch()
    }

    private func waitForState(_ value: String, file: StaticString = #filePath, line: UInt = #line) {
        let p = NSPredicate(format: "value == %@", value)
        let e = expectation(for: p, evaluatedWith: state)
        XCTAssertEqual(XCTWaiter.wait(for: [e], timeout: 10), .completed, "state is \(state.value ?? "nil"), want \(value)", file: file, line: line)
    }

    /// First run: the walkthrough explains, Allow access asks, Continue closes it for good.
    func testFirstRunAsksThenStaysClosed() {
        launch("granted", reset: true)
        XCTAssertTrue(walkthrough.waitForExistence(timeout: 10))
        waitForState("notAsked")
        shot("first-run")
        app.buttons["permissions.localNetwork.allow"].tap()
        waitForState("granted")
        shot("allowed")
        app.buttons["permissions.continue"].tap()
        XCTAssertTrue(walkthrough.waitForNonExistence(timeout: 5))

        app.terminate()
        launch("granted", reset: false)
        sleep(3)
        XCTAssertFalse(walkthrough.exists, "seen and allowed: not shown again")
    }

    /// Refused: it offers the Settings app, and shows again on the next start.
    func testRefusalOpensSettingsAndComesBack() {
        launch("denied", reset: true)
        XCTAssertTrue(walkthrough.waitForExistence(timeout: 10))
        app.buttons["permissions.localNetwork.allow"].tap()
        waitForState("denied")
        XCTAssertTrue(app.descendants(matching: .any)["permissions.localNetwork.help"].exists)
        shot("denied")
        app.buttons["permissions.localNetwork.openSettings"].tap()
        let settings = XCUIApplication(bundleIdentifier: "com.apple.Preferences")
        XCTAssertTrue(settings.wait(for: .runningForeground, timeout: 10), "Settings opens")
        shot("settings", of: settings)
        app.activate()
        XCTAssertTrue(walkthrough.waitForExistence(timeout: 10), "still there on return (still refused)")
        app.buttons["permissions.continue"].tap() // "Not now"
        XCTAssertTrue(walkthrough.waitForNonExistence(timeout: 5))

        app.terminate()
        launch("denied", reset: false)
        XCTAssertTrue(walkthrough.waitForExistence(timeout: 10), "a refused required permission shows it again")
        waitForState("denied")
    }

    /// Arabic, right to left, in the web's words.
    func testArabic() {
        launch("denied", reset: true, extra: ["-AppleLanguages", "(ar)", "-AppleLocale", "ar_SA"])
        XCTAssertTrue(walkthrough.waitForExistence(timeout: 10))
        XCTAssertEqual(app.staticTexts["permissions.title"].label, "قبل أن تبدأ")
        XCTAssertTrue(app.buttons["السماح بالوصول"].exists)
        shot("arabic-first-run")
        app.buttons["permissions.localNetwork.allow"].tap()
        waitForState("denied")
        XCTAssertTrue(app.buttons["فتح الإعدادات"].exists)
        shot("arabic-denied")
    }

    private func shot(_ name: String, of target: XCUIApplication? = nil) {
        let png = (target ?? app).screenshot().pngRepresentation
        let a = XCTAttachment(data: png, uniformTypeIdentifier: "public.png")
        a.name = name
        a.lifetime = .keepAlways
        add(a)
        if let dir = ProcessInfo.processInfo.environment["MAKTABA_SHOTS"] {
            // com.hamzalabs.maktaba.<player|remote>.uitests, on an iphone or an ipad
            let parts = Bundle(for: Self.self).bundleIdentifier?.components(separatedBy: ".") ?? []
            let which = parts.count > 3 ? parts[3] : "app"
            let device = UIDevice.current.userInterfaceIdiom == .pad ? "ipad" : "iphone"
            try? png.write(to: URL(fileURLWithPath: dir).appendingPathComponent("\(which)-\(device)-\(name).png"))
        }
    }
}
