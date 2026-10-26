import XCTest

/// tvOS has no Local Network privilege (Apple's TN3179), so the TV asks for nothing and the
/// permissions walkthrough never shows, even on a first run with a refused (scripted) check.
/// Needs no home, so it runs without MAKTABA_HOME.
@MainActor
final class TVPermissionsUITests: XCTestCase {
    func testTheTVAsksForNothing() {
        continueAfterFailure = false
        let app = XCUIApplication()
        app.launchArguments = ["-uitest-reset", "-maktaba-permissions-reset", "-maktaba-permissions-fake", "denied"]
        app.launch()
        XCTAssertTrue(app.staticTexts["Choose your home"].waitForExistence(timeout: 20))
        sleep(3)
        XCTAssertFalse(app.descendants(matching: .any)["permissions.walkthrough"].exists)
        let shot = XCTAttachment(screenshot: app.screenshot())
        shot.name = "tv-first-run"
        shot.lifetime = .keepAlways
        add(shot)
        if let dir = ProcessInfo.processInfo.environment["MAKTABA_SHOTS"] {
            try? app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: dir).appendingPathComponent("tv-first-run-no-walkthrough.png"))
        }
    }
}
