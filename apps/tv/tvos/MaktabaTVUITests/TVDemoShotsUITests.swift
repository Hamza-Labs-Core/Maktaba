import XCTest

/// The Apple TV's product screenshots (story 60.09): Home, the series Open Shorts with its
/// seasons, Sintel playing, and the live channel guide, in English and in Arabic (the TV is
/// dark by default), with the Siri remote, on the demo library (`MockHome(demo:)`, from
/// `tests/fixtures/demo-library/`). Each is an attachment named
/// `shot-appletv-<screen>-<language>`, kept always: CI exports them from the result bundle as
/// `product-shots-appletv` (`_apple-apps.yml`, TV UI tests). The focus ring stays where the
/// remote left it: it is how the TV looks.
///
/// The clock: `library.json`'s noon for the series and the player; Home's On now row and the
/// guide are drawn against the device's clock, so there the demo's times count from now's
/// quarter hour (the guide's programmes start on its lines).
@MainActor
final class TVDemoShotsUITests: XCTestCase {
    let remote = XCUIRemote.shared
    var app: XCUIApplication!
    var home: MockHome?
    private var language = "en"

    override func setUp() async throws {
        continueAfterFailure = false
        app = XCUIApplication()
    }

    override func tearDown() async throws {
        app?.terminate()
        home?.stop()
    }

    func testHome() throws {
        for language in ["en", "ar"] {
            try signIn(language, clock: MockHome.quarterHour())
            XCTAssertTrue(element("home.row.continue").waitForExistence(timeout: 20), "Home; the home saw \(seen)")
            XCTAssertTrue(element("video.card.v-sintel.art").waitForExistence(timeout: 20), "Sintel's picture")
            waitForQuiet()
            shot("home")
        }
    }

    /// Home's Continue row: Sintel, then Llamigos (an Open Shorts episode); its page's Go to
    /// series opens the series.
    func testSeries() throws {
        for language in ["en", "ar"] {
            try signIn(language)
            let card = element("video.card.v-llamigos.art")
            XCTAssertTrue(card.waitForExistence(timeout: 20), "Llamigos on Home; the home saw \(seen)")
            remote.press(.down)
            usleep(500_000)
            moveFocusInRow(to: "v-llamigos")
            remote.press(.select)
            let toSeries = app.buttons[language == "ar" ? "الانتقال إلى السلسلة" : "Go to series"]
            XCTAssertTrue(toSeries.waitForExistence(timeout: 20), "Llamigos's page goes to its series")
            moveFocus(to: toSeries, with: language == "ar" ? .left : .right)
            remote.press(.select)
            XCTAssertTrue(element("series.page").waitForExistence(timeout: 20), "the series")
            XCTAssertTrue(element("season.1").waitForExistence(timeout: 20), "its seasons")
            XCTAssertTrue(element("season.2").exists, "two of them")
            XCTAssertTrue(wait(timeout: 20) {
                self.app.descendants(matching: .any).matching(NSPredicate(format: "identifier ENDSWITH '.art'")).count >= 2
            }, "its episodes' pictures")
            waitForQuiet()
            shot("series")
        }
    }

    /// Sintel from Home's Continue row: Resume (focused first) plays it through a queue.
    func testPlayer() throws {
        for language in ["en", "ar"] {
            try signIn(language)
            // the TV counts a session's stream from where it opened (Resume: the demo's
            // `position_sec`), so the stream is the rest of the film
            if let home, let length = home.demoVideo("v-sintel")?["duration_sec"] as? Int,
               let at = home.demoDocument("get-me-videos-v-sintel")?["position_sec"] as? Int {
                home.streamSeconds = (length - at) / 2 * 2
            }
            let card = element("video.card.v-sintel.art")
            XCTAssertTrue(card.waitForExistence(timeout: 20), "Sintel on Home; the home saw \(seen)")
            remote.press(.down)
            usleep(500_000)
            moveFocusInRow(to: "v-sintel")
            remote.press(.select)
            let resume = app.buttons.matching(NSPredicate(format: "label BEGINSWITH %@", language == "ar" ? "استئناف" : "Resume")).firstMatch
            XCTAssertTrue(resume.waitForExistence(timeout: 20), "Sintel's page offers Resume")
            XCTAssertTrue(wait(timeout: 10) { resume.hasFocus }, "Resume focused first")
            remote.press(.select)
            XCTAssertTrue(element("queue.player").waitForExistence(timeout: 20), "the player; the home saw \(seen)")
            // the picture in: its segments asked for, and a few seconds played
            XCTAssertTrue(wait(timeout: 30) { self.seen.contains { $0.hasPrefix("GET /stream/") && $0.hasSuffix(".ts") } },
                          "the stream's segments; the home saw \(seen)")
            let asked = seen.count
            XCTAssertTrue(wait(timeout: 20) { self.seen.count > asked + 1 }, "it plays on")
            shot("player")
        }
    }

    func testGuide() throws {
        for language in ["en", "ar"] {
            try signIn(language, clock: MockHome.quarterHour())
            openLiveTab(language == "ar" ? "البث المباشر" : "Live TV")
            openGuide()
            let first = app.buttons["live.guide.ch-films.now"]
            XCTAssertTrue(first.waitForExistence(timeout: 20), "the guide; the home saw \(seen)")
            remote.press(.down)
            XCTAssertTrue(wait(timeout: 5) { first.hasFocus }, "down lands on what is on now")
            waitForQuiet()
            shot("guide")
        }
    }

    // MARK: helpers

    private var seen: [String] { home?.seen ?? [] }

    private func element(_ id: String) -> XCUIElement { app.descendants(matching: .any)[id].firstMatch }

    /// A first launch on the demo home in `language`: its address typed, the code approved.
    private func signIn(_ language: String, clock: Date = MockHome.demoClock) throws {
        app.terminate()
        home?.stop()
        let home = try MockHome(demo: language)
        home.clock = clock
        try home.start()
        self.home = home
        self.language = language
        app.launchArguments = ["-uitest-reset", "-AppleLanguages", "(\(language))",
                               "-AppleLocale", language == "ar" ? "ar_SA" : "en_US"]
        app.launch()
        type(home.url, into: app.textFields["picker.address"])
        pressIfStillThere(app.buttons["picker.connect"], with: .right)
        XCTAssertTrue(app.staticTexts["pairing-code"].waitForExistence(timeout: 20), "the code; the home saw \(seen)")
        home.approve()
        XCTAssertTrue(app.tabBars.firstMatch.waitForExistence(timeout: 20), "approved: signed in")
    }

    /// Along Home's focused row (right in English, left in Arabic) to `video`'s card.
    private func moveFocusInRow(to video: String) {
        let direction: XCUIRemote.Button = language == "ar" ? .left : .right
        let focused = { self.app.descendants(matching: .any).matching(NSPredicate(format: "hasFocus == true")).firstMatch }
        for _ in 0..<8 {
            let f = focused()
            if f.exists, f.identifier.hasPrefix("video.card.\(video)") || f.descendants(matching: .any)
                .matching(NSPredicate(format: "identifier BEGINSWITH %@", "video.card.\(video)")).count > 0 { return }
            remote.press(direction)
            usleep(500_000)
        }
        XCTFail("could not focus \(video)'s card")
    }

    /// Up to the tab bar, along it to Live TV, Select.
    private func openLiveTab(_ title: String) {
        let tab = app.tabBars.buttons[title]
        XCTAssertTrue(tab.waitForExistence(timeout: 20), "the Live TV tab")
        for _ in 0..<4 where !app.tabBars.buttons.allElementsBoundByIndex.contains(where: \.hasFocus) {
            remote.press(.up)
            usleep(400_000)
        }
        moveFocus(to: tab, with: language == "ar" ? .left : .right)
        remote.press(.select)
        sleep(1)
    }

    /// Up to Now / Guide, along to Guide, Select.
    private func openGuide() {
        let now = app.buttons["live.mode.now"], guide = app.buttons["live.mode.guide"]
        XCTAssertTrue(now.waitForExistence(timeout: 20), "Now | Guide")
        for _ in 0..<4 where !(now.hasFocus || guide.hasFocus) {
            remote.press(.up)
            usleep(400_000)
        }
        moveFocus(to: guide, with: language == "ar" ? .left : .right)
        remote.press(.select)
    }

    private func type(_ text: String, into field: XCUIElement) {
        XCTAssertTrue(field.waitForExistence(timeout: 20))
        field.typeExactly(text, in: app) {
            self.moveFocus(to: field, with: .down)
            self.remote.press(.select)
        }
        let done = app.buttons.matching(NSPredicate(format: "label IN[c] {'next', 'done', 'go', 'search', 'التالي', 'تم', 'انتقال', 'بحث'}")).firstMatch
        if done.waitForExistence(timeout: 5) {
            moveFocus(to: done, with: .down)
            remote.press(.select)
        } else {
            remote.press(.menu)
        }
        sleep(1)
    }

    private func pressIfStillThere(_ button: XCUIElement, with direction: XCUIRemote.Button) {
        guard button.waitForExistence(timeout: 2), button.isEnabled, button.isHittable else { return }
        moveFocus(to: button, with: direction)
        remote.press(.select)
    }

    private func moveFocus(to element: XCUIElement, with direction: XCUIRemote.Button) {
        XCTAssertTrue(element.waitForExistence(timeout: 10), "\(element) is not there")
        for _ in 0..<12 where !element.hasFocus {
            remote.press(direction)
            usleep(400_000)
        }
        XCTAssertTrue(element.hasFocus, "could not focus \(element)")
    }

    private func wait(timeout: TimeInterval = 15, _ condition: @escaping () -> Bool) -> Bool {
        let end = Date().addingTimeInterval(timeout)
        while Date() < end {
            if condition() { return true }
            usleep(250_000)
        }
        return condition()
    }

    /// Until the app has asked the home nothing for 2 s (at most 20 s): every picture in.
    private func waitForQuiet() {
        var count = seen.count, since = Date()
        let end = Date().addingTimeInterval(20)
        while Date() < end {
            usleep(200_000)
            if seen.count != count { count = seen.count; since = Date() } else if Date().timeIntervalSince(since) >= 2 { return }
        }
    }

    private func shot(_ screen: String) {
        let a = XCTAttachment(screenshot: XCUIScreen.main.screenshot())
        a.name = "shot-appletv-\(screen)-\(language)"
        a.lifetime = .keepAlways
        add(a)
    }
}
