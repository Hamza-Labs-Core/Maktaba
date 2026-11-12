import XCTest

/// The Mac Player's product screenshots (story 60.09): the library window (Libraries on the
/// demo's Films) and Sintel playing with the Arabic subtitles on, in English and in Arabic,
/// dark, on the demo library (`MockHome(demo:)`, from `tests/fixtures/demo-library/`) with
/// its clock pinned to `library.json`'s noon. Each is the window as an attachment named
/// `shot-mac-<screen>-<language>`, kept always: CI exports them from the result bundle as
/// `product-shots-mac` (`_apple-apps.yml`, Player UI tests (mac)).
@MainActor
final class PlayerMacDemoShotsUITests: XCTestCase {
    private var app: XCUIApplication!
    private var home: MockHome?
    private var language = "en"

    override func setUp() async throws {
        continueAfterFailure = false
        app = XCUIApplication()
    }

    override func tearDown() async throws {
        if app?.state == .runningForeground || app?.state == .runningBackground { app.terminate() }
        home?.stop()
    }

    func testLibrary() throws {
        for language in ["en", "ar"] {
            try launchSignedIn(language, section: "libraries", open: "library/lib-films")
            XCTAssertTrue(element("screen.library").waitForExistence(timeout: 20), "the Films library; the home saw \(home?.seen ?? [])")
            waitForArt(atLeast: 4)
            shot("library")
        }
    }

    func testPlayer() throws {
        for language in ["en", "ar"] {
            try launchSignedIn(language, section: "home", open: "watch/v-sintel@420",
                               extra: ["-playback.subtitles_on", "YES", "-playback.subtitle_language", "ar"])
            XCTAssertTrue(element("player.window").waitForExistence(timeout: 30), "the player; the home saw \(home?.seen ?? [])")
            // what plays, from what the player asked the home (XCUITest on macOS reads no value
            // off the picture): the Arabic subtitles' cues, then a progress report past 7:00 (one
            // each 10 s while it plays; the segments are no measure: the playlist repeats two
            // segments' addresses, which the player fetches once)
            XCTAssertTrue(wait(30) { self.seen.contains { $0.hasPrefix("GET /stream/") && $0.hasSuffix("/subs-ar.vtt") } },
                          "the Arabic subtitles; the home saw \(seen)")
            XCTAssertTrue(wait(30) { self.reported().contains { $0 > 420 } }, "it plays on: reported \(reported())")
            // the controls up: the pointer over the picture
            // (a move, from one point to another: a pointer already there shows nothing)
            let window = app.windows.firstMatch
            window.coordinate(withNormalizedOffset: CGVector(dx: 0.3, dy: 0.4)).hover()
            window.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.5)).hover()
            if !wait(3, { self.app.buttons["player.playPause"].exists }) {
                window.coordinate(withNormalizedOffset: CGVector(dx: 0.6, dy: 0.6)).hover()
            }
            _ = wait(1) { false }
            shot("player")
        }
    }

    // MARK: helpers

    /// A first run on the demo home in `language`, dark, signed in by a code.
    private func launchSignedIn(_ language: String, section: String, open: String, extra: [String] = []) throws {
        if app.state == .runningForeground || app.state == .runningBackground { app.terminate() }
        home?.stop()
        let home = try MockHome(demo: language)
        home.clock = MockHome.demoClock
        if let seconds = home.demoVideo("v-sintel")?["duration_sec"] as? Int { home.streamSeconds = (seconds - 420) / 2 * 2 }
        try home.start()
        self.home = home
        self.language = language
        // as PlayerMacMenuUITests: `-key value` pairs only, no window restored
        app.launchArguments = ["-NSTreatUnknownArgumentsAsOpen", "NO", "-ApplePersistenceIgnoreState", "YES",
                               "-AppleLanguages", "(\(language))", "-AppleLocale", language == "ar" ? "ar_JO" : "en_US",
                               "-theme", "dark",
                               "-maktaba-permissions-reset", "-maktaba-permissions-fake", "granted", "-maktaba-session-reset",
                               "-maktaba-section", section, "-maktaba-open", open] + extra
        app.launch()
        app.activate()
        let walkthrough = element("permissions.walkthrough")
        if walkthrough.waitForExistence(timeout: 5) {
            app.buttons["permissions.continue"].click()
            XCTAssertTrue(walkthrough.waitForNonExistence(timeout: 5))
        }
        let field = app.textFields["picker.address"]
        XCTAssertTrue(field.waitForExistence(timeout: 10), "the home picker")
        field.typeExactly(home.url, in: app)
        app.buttons["picker.connect"].click()
        let byCode = app.radioButtons[language == "ar" ? "برمز" : "With a code"]
        XCTAssertTrue(byCode.waitForExistence(timeout: 10), "the sign-in screen offers a code")
        byCode.click()
        home.approve()
        XCTAssertTrue(byCode.waitForNonExistence(timeout: 20), "approved: signed in")
    }

    private func element(_ id: String) -> XCUIElement { app.descendants(matching: .any)[id].firstMatch }

    private var seen: [String] { home?.seen ?? [] }

    /// The positions the player reported (`POST /api/stream/sessions/{id}/progress`), in order.
    private func reported() -> [Int] {
        (home?.requests ?? []).filter { $0.method == "POST" && $0.path.hasSuffix("/progress") }
            .compactMap { try? JSONSerialization.jsonObject(with: $0.body) as? [String: Any] }
            .compactMap { $0["position_sec"] as? Int }
    }

    /// At least `count` pictures asked for, then 2 s with no request: every picture drawn.
    private func waitForArt(atLeast count: Int) {
        let kinds = ["/poster", "/portrait", "/backdrop", "/art", "/thumbnail"]
        XCTAssertTrue(wait(30) {
            (self.home?.requests ?? []).filter { r in r.method == "GET" && kinds.contains { r.path.hasSuffix($0) } }.count >= count
        }, "\(count) pictures asked for; the home saw \(home?.seen ?? [])")
        var seen = home?.requests.count ?? 0, since = Date.now
        let end = Date.now.addingTimeInterval(30)
        while Date.now < end {
            RunLoop.current.run(until: .now.addingTimeInterval(0.2))
            let now = home?.requests.count ?? 0
            if now != seen { seen = now; since = .now } else if Date.now.timeIntervalSince(since) >= 2 { return }
        }
    }

    private func wait(_ timeout: TimeInterval = 10, _ condition: @escaping () -> Bool) -> Bool {
        let end = Date.now.addingTimeInterval(timeout)
        while Date.now < end {
            if condition() { return true }
            RunLoop.current.run(until: .now.addingTimeInterval(0.25))
        }
        return condition()
    }

    /// The window as an attachment named `shot-mac-<screen>-<language>`, kept always.
    private func shot(_ screen: String) {
        let a = XCTAttachment(screenshot: app.windows.firstMatch.screenshot())
        a.name = "shot-mac-\(screen)-\(language)"
        a.lifetime = .keepAlways
        add(a)
    }
}
