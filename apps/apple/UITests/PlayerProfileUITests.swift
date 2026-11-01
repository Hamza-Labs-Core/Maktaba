import XCTest

/// The Player's Profile and Settings (Epic 48 story 48.22, FR-25 to FR-31) on iPhone and iPad,
/// against `MockHome`:
/// - Theme: Dark is chosen on a fresh install; Light turns the app light at once;
/// - Language opens the system's page for this app (the address it opens is checked), and the
///   Profile reads right to left in Arabic;
/// - History removes one video, Sessions signs one out (the DELETE each sends is checked), and
///   Sign out returns to the start screen;
/// - Favorites, playlists, the playback settings and Help; the accessibility audit.
///
/// Each launch passes `-maktaba-settings-reset` (the theme and the playback settings as on a
/// fresh install) and `-maktaba-record-opens` (a link's address is shown, not opened). The
/// theme is not forced dark here, so the stored choice is the one the app reads.
final class PlayerProfileUITests: PlayerUITestCase {
    private func launchProfile(_ language: String = "en") {
        launch(language, sections: ["libraries", "profile"], section: "profile", theme: nil,
               extra: ["-maktaba-settings-reset", "-maktaba-record-opens"])
        signIn()
        XCTAssertTrue(element("screen.profile").waitForExistence(timeout: 15), "the Profile")
    }

    // MARK: theme (FR-29)

    func testThemeIsDarkOnAFreshInstallAndLightChangesTheAppAtOnce() throws {
        launchProfile()
        let dark = app.buttons["Dark"].firstMatch
        XCTAssertTrue(dark.waitForExistence(timeout: 5), "the theme picker")
        expect(dark, selected: true, "Dark is chosen on a fresh install")
        XCTAssertFalse(app.buttons["Light"].isSelected)
        XCTAssertLessThan(meanLuminance(), 0.3, "the app is dark")
        shot("profile")

        app.buttons["Light"].tap()
        expect(app.buttons["Light"].firstMatch, selected: true, "Light is chosen")
        let lit = expectation(for: NSPredicate { [unowned self] _, _ in meanLuminance() > 0.5 }, evaluatedWith: nil)
        wait(for: [lit], timeout: 5)
        shot("profile-light")

        // back to dark, so the next test (and the brand test) starts dark
        app.buttons["Dark"].tap()
        expect(app.buttons["Dark"].firstMatch, selected: true, "Dark again")
    }

    // MARK: language (FR-30)

    func testLanguageOpensTheSystemsPageForThisApp() {
        launchProfile()
        let language = element("profile.language")
        scrollTo(language)
        XCTAssertTrue(language.label.contains("English"), "the language in use (\(language.label))")
        language.tap()
        let opened = element("profile.opened")
        XCTAssertTrue(opened.waitForExistence(timeout: 5), "a link was opened")
        XCTAssertEqual(opened.label, "app-settings:", "the app's own page in Settings, where its language is chosen")
    }

    func testArabicMirrors() {
        launchProfile("ar")
        let library = app.staticTexts["مكتبتك"].firstMatch
        XCTAssertTrue(library.waitForExistence(timeout: 10), "«Your library» in Arabic")
        let width = app.windows.firstMatch.frame.width
        XCTAssertGreaterThan(library.frame.maxX, width * 0.6, "a group's title starts on the right")
        XCTAssertTrue(app.buttons["داكن"].exists, "the theme's names in Arabic")
        let history = element("profile.history")
        XCTAssertGreaterThan(history.frame.maxX - library.frame.maxX, -40, "rows read from the right as well")
        XCTAssertTrue(app.staticTexts["سجل المشاهدة"].exists)
        shot("profile")

        history.tap()
        XCTAssertTrue(element("history.v2").waitForExistence(timeout: 10), "the history in Arabic")
        let remove = element("history.remove.v2")
        XCTAssertLessThan(remove.frame.midX, width * 0.5, "the remove button is on the left, at the row's end")
        shot("profile-history")
    }

    // MARK: history, sessions, sign out (FR-25, FR-26)

    func testHistoryRemovesOneItem() {
        home.stub("DELETE", "/api/me/history/v5", status: 204)
        launchProfile()
        element("profile.history").tap()
        let row = element("history.v5")
        XCTAssertTrue(row.waitForExistence(timeout: 10), "the history")
        XCTAssertTrue(element("history.v2").exists && element("history.v1").exists)
        shot("profile-history")

        element("history.remove.v5").tap()
        XCTAssertTrue(row.waitForNonExistence(timeout: 5), "the video left the list")
        XCTAssertTrue(waitFor { self.home.seen.contains("DELETE /api/me/history/v5") }, "the home was told")
        XCTAssertTrue(element("history.v2").exists, "the others stay")
        XCTAssertEqual(home.seen.filter { $0.hasPrefix("DELETE") }, ["DELETE /api/me/history/v5"], "only that one")
    }

    func testARefusedRemovalPutsTheRowBack() {
        home.stub("DELETE", "/api/me/history/v5", status: 500, body: #"{"title":"boom","status":500}"#)
        launchProfile()
        element("profile.history").tap()
        XCTAssertTrue(element("history.v5").waitForExistence(timeout: 10))
        element("history.remove.v5").tap()
        XCTAssertTrue(app.alerts.firstMatch.waitForExistence(timeout: 5), "says it did not work")
        app.alerts.firstMatch.buttons.firstMatch.tap()
        XCTAssertTrue(element("history.v5").exists, "the row is back")
    }

    func testAHistoryRowOpensItsVideo() {
        launchProfile()
        element("profile.history").tap()
        let row = element("history.v2")
        XCTAssertTrue(row.waitForExistence(timeout: 10))
        row.tap()
        XCTAssertTrue(element("screen.video").waitForExistence(timeout: 5), "the video's page")
        app.navigationBars.buttons.firstMatch.tap()
        XCTAssertTrue(element("history.v2").waitForExistence(timeout: 5), "Back returns to the history")
    }

    func testSessionsSignsOneOut() {
        home.stub("DELETE", "/api/me/sessions/sess-1", status: 204)
        launchProfile()
        element("profile.sessions").tap()
        XCTAssertTrue(element("session.sess-1").waitForExistence(timeout: 10), "the sessions")
        XCTAssertTrue(app.staticTexts["Maktaba · iPhone"].exists, "a session named by its user agent")
        XCTAssertTrue(app.staticTexts["A browser"].exists, "one with no user agent")
        shot("profile-sessions")

        element("session.signOut.sess-1").tap()
        XCTAssertTrue(element("session.sess-1").waitForNonExistence(timeout: 5), "it left the list")
        XCTAssertTrue(home.seen.contains("DELETE /api/me/sessions/sess-1"))
        XCTAssertTrue(element("session.sess-2").exists, "the other stays")
    }

    func testSignOutReturnsToTheStartScreen() {
        launchProfile()
        let signOut = element("profile.signOut")
        scrollTo(signOut)
        signOut.tap()
        let confirm = app.buttons["profile.signOut.confirm"].firstMatch
        XCTAssertTrue(confirm.waitForExistence(timeout: 5), "asks first")
        confirm.tap()
        XCTAssertTrue(element("start.brand").waitForExistence(timeout: 10), "the start screen")
        XCTAssertFalse(element("screen.profile").exists)
    }

    // MARK: favorites, playlists, playback, help, about

    func testFavoritesAndPlaylists() {
        home.stub("GET", "/api/me/playlists/pl-2", body: Self.playlist)
        home.stub("GET", "/api/me/videos", body: Self.favorites)
        launchProfile()
        element("profile.favorites").tap()
        XCTAssertTrue(element("favorites.grid").waitForExistence(timeout: 10), "the favorites")
        XCTAssertTrue(home.requests.contains { $0.path == "/api/me/videos" && $0.value("favorite") == "true" }, "asks for favorites")
        XCTAssertTrue(element("video.v3").exists && element("video.v7").exists, "each favorite as a card")
        shot("profile-favorites")
        element("profile.back").tap()

        element("profile.playlists").tap()
        XCTAssertTrue(element("playlist.pl-1").waitForExistence(timeout: 10), "the playlists")
        XCTAssertTrue(app.staticTexts["Weekend documentaries"].exists)
        XCTAssertTrue(app.staticTexts.matching(NSPredicate(format: "label BEGINSWITH '1 video'")).firstMatch.exists, "one video, singular")
        shot("profile-playlists")
        element("playlist.pl-2").tap()
        XCTAssertTrue(element("playlist.play").waitForExistence(timeout: 10), "the playlist's page")
        element("playlist.play").tap()
        XCTAssertTrue(element("screen.watch").waitForExistence(timeout: 5), "Play opens the player")
        XCTAssertTrue(app.staticTexts["v10 @ 0"].exists, "at its first video, from the start")
    }

    func testPlaybackSettingsAreKept() {
        launchProfile()
        let playback = element("profile.playback")
        scrollTo(playback)
        playback.tap()
        let autoplay = app.switches["playback.autoplay"].firstMatch
        XCTAssertTrue(autoplay.waitForExistence(timeout: 5))
        XCTAssertEqual(autoplay.value as? String, "1", "next episode: on by default")
        XCTAssertEqual(app.switches["playback.subtitles"].value as? String, "0", "subtitles: off by default")
        XCTAssertEqual(app.switches["playback.resume"].value as? String, "1", "resume without asking: on")
        shot("profile-playback")
        autoplay.switches.firstMatch.tap()
        XCTAssertEqual(autoplay.value as? String, "0")

        // kept on this device: a relaunch without the reset reads it back
        app.terminate()
        launch(sections: ["libraries", "profile"], section: "profile", theme: nil, extra: ["-maktaba-record-opens"])
        signIn()
        XCTAssertTrue(element("screen.profile").waitForExistence(timeout: 15))
        scrollTo(element("profile.playback"))
        element("profile.playback").tap()
        XCTAssertEqual(app.switches["playback.autoplay"].firstMatch.value as? String, "0", "kept")
    }

    func testHelpAndAbout() {
        launchProfile()
        let help = element("profile.help")
        scrollTo(help)
        help.tap()
        XCTAssertTrue(element("profile.opened").waitForExistence(timeout: 5))
        XCTAssertEqual(element("profile.opened").label, "\(home.url)/help/apple-apps", "the home's help page for this app")
        let about = element("profile.about")
        XCTAssertTrue(about.label.contains("0.0.0"), "the home's version (\(about.label))")
        XCTAssertTrue(about.label.contains("Maktaba Player"))
    }

    func testAccessibilityAudit() throws {
        launchProfile()
        try audit()
        element("profile.history").tap()
        XCTAssertTrue(element("history.v2").waitForExistence(timeout: 10))
        try audit()
        element("profile.back").tap()
        element("profile.sessions").tap()
        XCTAssertTrue(element("session.sess-1").waitForExistence(timeout: 10))
        try audit()
    }

    /// The audit, but for a row just above the tab bar: as the audit grows the text it slides
    /// under the bar, which reads as "partially unsupported" (the same row scrolled up passes).
    private func audit() throws {
        let barTop = app.tabBars.firstMatch.exists ? app.tabBars.firstMatch.frame.minY : app.windows.firstMatch.frame.maxY
        try app.performAccessibilityAudit(for: [.dynamicType, .sufficientElementDescription, .hitRegion, .contrast]) { issue in
            if issue.auditType == .dynamicType, (issue.element?.frame.maxY ?? 0) > barTop - 100 { return true }
            return false
        }
    }

    // MARK: helpers

    private static let favorites = #"""
    {"items":[{"id":"v3","title":"Fiqh of fasting","filename":"fasting.mp4","duration_sec":2400},
              {"id":"v7","title":"The Night Journey","filename":"night.mp4","duration_sec":2725,"playback_state":{"position_sec":900}}],"next":null}
    """#

    private static let playlist = #"""
    {"playlist":{"id":"pl-2","name":"Weekend documentaries","description":"","videos":1,"duration_sec":2940,"poster_video_ids":["v10"]},
     "items":[{"id":"v10","title":"The Libraries of Timbuktu","filename":"timbuktu.mp4","duration_sec":2940}]}
    """#

    private func expect(_ e: XCUIElement, selected: Bool, _ message: String) {
        let p = expectation(for: NSPredicate(format: "isSelected == %@", NSNumber(value: selected)), evaluatedWith: e)
        XCTAssertEqual(XCTWaiter.wait(for: [p], timeout: 5), .completed, message)
    }

    private func waitFor(_ condition: @escaping () -> Bool, timeout: TimeInterval = 5) -> Bool {
        let p = expectation(for: NSPredicate { _, _ in condition() }, evaluatedWith: nil)
        return XCTWaiter.wait(for: [p], timeout: timeout) == .completed
    }

    /// Scrolls the Profile's list until `e` is on screen.
    private func scrollTo(_ e: XCUIElement) {
        for _ in 0 ..< 6 where !(e.exists && e.isHittable) {
            app.swipeUp()
        }
        XCTAssertTrue(e.isHittable, "\(e) on screen")
    }

    /// The screen's mean luminance (0 black … 1 white), from a 64 × 64 sample.
    private func meanLuminance() -> Double {
        guard let cg = app.screenshot().image.cgImage else { return -1 }
        let w = 64, h = 64
        var pixels = [UInt8](repeating: 0, count: w * h * 4)
        let ctx = CGContext(data: &pixels, width: w, height: h, bitsPerComponent: 8, bytesPerRow: w * 4,
                            space: CGColorSpaceCreateDeviceRGB(), bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)!
        ctx.draw(cg, in: CGRect(x: 0, y: 0, width: w, height: h))
        var total = 0.0
        for i in stride(from: 0, to: pixels.count, by: 4) {
            total += (0.2126 * Double(pixels[i]) + 0.7152 * Double(pixels[i + 1]) + 0.0722 * Double(pixels[i + 2])) / 255
        }
        return total / Double(w * h)
    }
}
