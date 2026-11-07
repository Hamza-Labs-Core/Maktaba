import XCTest

/// Crash reports and Report a problem in the Player (Epic 53 stories 53.14 T12, 53.15 T07)
/// against `MockHome`:
/// - after signing in, the crash-report question shows its two buttons, neither chosen; "Not
///   now" leaves the Profile's switch off;
/// - Report a problem opens from Profile with the screenshot box unticked, and Send posts
///   the report without a screenshot and says its number.
///
/// The question shows in a UI test only when it asks for it (`-maktaba-crash-question`), so
/// no other test meets it. Each check waits on something that stays on screen until the test
/// acts (the question's buttons, the form, the sent screen), never on a message that hides
/// itself.
final class PlayerReportProblemUITests: PlayerUITestCase {
    private func launchProfile(_ language: String = "en", extra: [String] = []) {
        launch(language, sections: ["libraries", "profile"], section: "profile", extra: extra)
        signIn()
    }

    func testTheCrashQuestionHasTwoEqualButtonsAndNotNowLeavesItOff() throws {
        launchProfile(extra: ["-maktaba-crash-question"])
        let yes = element("crash.question.yes"), no = element("crash.question.no")
        XCTAssertTrue(no.waitForExistence(timeout: 20), "the question after signing in")
        XCTAssertTrue(yes.exists)
        XCTAssertFalse(yes.isSelected || no.isSelected, "neither is chosen for the person")
        XCTAssertEqual(yes.frame.height, no.frame.height, accuracy: 1, "the same size")
        shot("crash-question")
        no.tap()
        XCTAssertTrue(no.waitForNonExistence(timeout: 10), "answered: the question goes")
        XCTAssertTrue(element("screen.profile").waitForExistence(timeout: 15))
        let toggle = app.switches["crash.setting"].firstMatch
        scrollUntil(toggle)
        XCTAssertTrue(toggle.waitForExistence(timeout: 10), "Send crash reports to the home, in the Profile")
        XCTAssertEqual(toggle.value as? String, "0", "off after Not now")
    }

    func testTheCrashQuestionInArabic() throws {
        launchProfile("ar", extra: ["-maktaba-crash-question"])
        XCTAssertTrue(element("crash.question.no").waitForExistence(timeout: 20))
        XCTAssertTrue(app.buttons["ليس الآن"].exists, "Not now in Arabic")
        shot("crash-question")
        element("crash.question.no").tap()
    }

    func testReportAProblemSendsWithoutAScreenshotUnlessTicked() throws {
        launchProfile()
        home.stub("POST", "/api/reports", body: #"{"id":27}"#)
        XCTAssertTrue(element("screen.profile").waitForExistence(timeout: 15))
        let row = element("profile.reportProblem")
        scrollUntil(row)
        row.tap()
        let send = element("report.send")
        XCTAssertTrue(send.waitForExistence(timeout: 10), "the form")
        let screenshot = app.switches["report.screenshot"].firstMatch
        if screenshot.exists {
            XCTAssertEqual(screenshot.value as? String, "0", "the screenshot box starts unticked")
        }
        XCTAssertFalse(element("report.screenshot.preview").exists, "no screenshot shown, none sent")
        shot("report-a-problem")
        scrollUntil(send)
        send.tap()
        XCTAssertTrue(element("report.sent").waitForExistence(timeout: 15), "Sent to the home (#27)")
        shot("report-sent")
        let posted = home.requests.last { $0.method == "POST" && $0.path == "/api/reports" }
        let body = posted.map { String(decoding: $0.body, as: UTF8.self) } ?? ""
        XCTAssertTrue(body.contains(#"name="report""#), "the app's part")
        XCTAssertFalse(body.contains(#"name="screenshot""#), "no screenshot without the tick")
        XCTAssertTrue(body.contains(#""screenshot_previewed":false"#))
    }

    func testReportAProblemInArabic() throws {
        launchProfile("ar")
        XCTAssertTrue(element("screen.profile").waitForExistence(timeout: 15))
        let row = element("profile.reportProblem")
        scrollUntil(row)
        row.tap()
        XCTAssertTrue(element("report.send").waitForExistence(timeout: 10))
        XCTAssertTrue(app.staticTexts["ما الذي سيُرسل"].exists || app.staticTexts["ما الذي سيُرسل".uppercased()].exists,
                      "What will be sent, in Arabic")
        shot("report-a-problem")
    }
}
