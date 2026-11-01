import XCTest

/// Home (Epic 48 story 48.17, FR-10 to FR-13) on iPhone and iPad, against `MockHome`'s
/// fixtures (`Fixtures/home.json`, `recommendations.json`, `channels-now.json`):
/// - the hero, then the rows in FR-10's order; an empty row is absent;
/// - a card opens its video's page; Resume (the context menu) opens the player at the
///   person's position; Not interested removes the card and calls the DELETE route;
/// - a second launch shows the kept rows before the home answers (it answers 3 s late);
/// - an empty home says so, with Switch home and Sign out;
/// - the accessibility audit, and dark screenshots in English and Arabic.
final class PlayerHomeUITests: PlayerUITestCase {
    /// FR-10's order, as the fixtures fill it: Continue Watching, On Deck, Live TV on now,
    /// Favorites, Recently Added, Recently Watched, the pinned Lectures library, For You.
    /// (`From your library` has no items in the fixtures, so it is absent.)
    private let order = ["continue", "ondeck", "live", "favorites", "added", "watched", "pinned-lib-lectures", "rec-for_you"]

    private func row(_ key: String) -> XCUIElement { element("home.row.\(key)") }

    /// Scrolls Home down until `target` is on screen (rows below the fold are not built yet).
    private func scrollTo(_ target: XCUIElement, file: StaticString = #filePath, line: UInt = #line) {
        // the page itself (its scroll view carries the screen's identifier, `screen.home`)
        for _ in 0..<12 where !(target.exists && target.isHittable) {
            app.swipeUp(velocity: .slow)
        }
        XCTAssertTrue(target.exists, "\(target) on Home", file: file, line: line)
    }

    private func openHome(_ language: String = "en") {
        launchSignedIn(language, sections: ["home"])
        XCTAssertTrue(element("home.hero").waitForExistence(timeout: 20), "the hero")
    }

    /// The hero, then each row in FR-10's order, below the one before it; no empty row.
    func testHeroThenRowsInOrder() {
        openHome()
        XCTAssertTrue(app.staticTexts["Seerah"].exists, "the featured series' name")
        XCTAssertTrue(element("hero.play").exists && element("hero.details").exists, "Play and Details")
        XCTAssertTrue(home.seen.contains("GET /api/home"))
        XCTAssertTrue(home.requests.contains { $0.path == "/api/recommendations" && $0.value("surface") == "mobile-home" },
                      "the recommendations of mobile-home")
        XCTAssertTrue(home.seen.contains("GET /api/channels/now"))
        shot("home")

        // scrolling down from the top, each row is met in FR-10's order, below the hero
        let heroY = element("home.hero").frame.minY
        var met: [String] = []
        for _ in 0..<14 {
            let visible = (order + ["rec-from_library"]).filter { row($0).exists }
                .sorted { row($0).frame.minY < row($1).frame.minY }
            for key in visible where !met.contains(key) { met.append(key) }
            if met.count == 1, let first = met.first { XCTAssertGreaterThan(row(first).frame.minY, heroY, "rows below the hero") }
            if met.last == order.last { break }
            app.swipeUp(velocity: .slow)
        }
        XCTAssertEqual(met, order, "the rows in FR-10's order; From your library, empty, is absent")
    }

    /// A row the home sends empty is not shown; the rest keep their order.
    func testEmptyRowIsAbsent() throws {
        var json = try XCTUnwrap(home.fixture(named: "home"))
        json = try Self.replacing(json) { $0["on_deck"] = [] }
        home.stub("GET", "/api/home", body: json)
        home.stub("GET", "/api/channels/now", body: #"{"channels":[]}"#)
        openHome()
        XCTAssertTrue(row("continue").waitForExistence(timeout: 10))
        // the kept rows of an earlier test show first: wait for this answer's
        XCTAssertTrue(row("ondeck").waitForNonExistence(timeout: 10), "On Deck, empty, is absent")
        XCTAssertFalse(row("live").exists, "no channel on now: no Live TV row")
        scrollTo(row("favorites"))
        XCTAssertGreaterThan(row("favorites").frame.minY, row("continue").frame.minY)
    }

    /// A tap opens the video's page; Resume in the context menu opens the player at the
    /// person's position (v2 stopped at 1240 s).
    func testCardOpensVideoAndResumeOpensPlayer() {
        openHome()
        let card = element("home.card.v2")
        scrollTo(card)
        card.tap()
        XCTAssertTrue(element("screen.video").waitForExistence(timeout: 10), "the video's page")
        XCTAssertTrue(app.staticTexts["v2"].exists, "v2's page")
        app.navigationBars.buttons.firstMatch.tap()
        XCTAssertTrue(card.waitForExistence(timeout: 5))

        card.press(forDuration: 1.2)
        let resume = app.buttons["home.menu.resume"].firstMatch
        XCTAssertTrue(resume.waitForExistence(timeout: 5), "Resume in the context menu")
        resume.tap()
        XCTAssertTrue(element("screen.watch").waitForExistence(timeout: 10), "the player")
        XCTAssertTrue(app.staticTexts["v2 @ 1240"].exists, "at the person's position")
    }

    /// Not interested: the card leaves For You at once and the home is told.
    func testNotInterestedRemovesTheCard() {
        openHome()
        let card = row("rec-for_you").descendants(matching: .any)["home.card.v6"].firstMatch
        scrollTo(card)
        card.press(forDuration: 1.2)
        let item = app.buttons["home.menu.notInterested"].firstMatch
        XCTAssertTrue(item.waitForExistence(timeout: 5), "Not interested in the context menu")
        item.tap()
        XCTAssertTrue(card.waitForNonExistence(timeout: 5), "the card is gone")
        XCTAssertTrue(row("rec-for_you").descendants(matching: .any)["home.card.v11"].exists, "the others stay")
        let deadline = Date().addingTimeInterval(5)
        while !home.seen.contains("DELETE /api/recommendations/items/v6"), Date() < deadline {
            RunLoop.current.run(until: Date().addingTimeInterval(0.2))
        }
        XCTAssertTrue(home.seen.contains("DELETE /api/recommendations/items/v6"), "the DELETE route")
    }

    /// FR-12: the second launch shows the rows kept on the device at once; the home's answer
    /// (3 s late, and without Continue Watching this time) then replaces them.
    func testSecondLaunchShowsKeptRowsFirst() throws {
        openHome()
        XCTAssertTrue(row("continue").waitForExistence(timeout: 10))
        app.terminate()

        var json = try XCTUnwrap(home.fixture(named: "home"))
        json = try Self.replacing(json) { $0["continue_watching"] = [] }
        home.stub("GET", "/api/home", body: json, delay: 3)
        launchSignedIn(sections: ["home"])
        XCTAssertTrue(row("continue").waitForExistence(timeout: 2), "the kept rows, before the home answers")
        XCTAssertTrue(element("home.hero").exists)
        XCTAssertTrue(row("continue").waitForNonExistence(timeout: 10), "then the home's answer")
    }

    /// FR-13: no library the person may see: Home says so, with Switch home and Sign out.
    func testEmptyHome() {
        home.stub("GET", "/api/home", body: "{}")
        home.stub("GET", "/api/recommendations", body: #"{"rails":[]}"#)
        home.stub("GET", "/api/channels/now", body: #"{"channels":[]}"#)
        launchSignedIn(sections: ["home"])
        XCTAssertTrue(app.staticTexts["Nothing to watch yet"].waitForExistence(timeout: 20), "the empty home")
        XCTAssertTrue(app.buttons["home.switchHome"].exists)
        XCTAssertTrue(app.buttons["home.signOut"].exists)
        shot("home-empty")
        app.buttons["home.switchHome"].tap()
        XCTAssertTrue(app.textFields["picker.address"].waitForExistence(timeout: 10), "Switch home: the picker")
    }

    /// Arabic: the rows' names, right to left (the hero's title starts on the right).
    func testHomeInArabic() {
        openHome("ar")
        XCTAssertTrue(app.staticTexts["تابع المشاهدة"].waitForExistence(timeout: 10), "Continue Watching in Arabic")
        let title = app.staticTexts["hero.title"].firstMatch
        XCTAssertGreaterThan(title.frame.maxX, app.windows.firstMatch.frame.width * 0.5, "the title starts on the right")
        shot("home")
        scrollTo(row("rec-for_you"))
        XCTAssertTrue(app.staticTexts["مقترح لك"].exists, "For You in Arabic")
        shot("home-for-you")
    }

    /// NFR-6: labels, roles, contrast, Dynamic Type, clipped text and hit regions, in both
    /// languages.
    func testAccessibilityAudit() throws {
        for language in ["en", "ar"] {
            openHome(language)
            let window = app.windows.firstMatch.frame
            try app.performAccessibilityAudit { issue in
                // a progress bar is a reading, not a control: no 44-pt target
                if issue.auditType == .hitRegion, issue.element?.identifier == "progress.strip" { return true }
                // a rail's last card half off the screen's edge: the audit samples pixels
                // beyond it (the cards fully on screen are still checked)
                if let f = issue.element?.frame, !window.contains(f) { return true }
                return false
            }
            app.terminate()
        }
    }

    /// `json` (an object) changed by `change`.
    private static func replacing(_ json: String, _ change: (inout [String: Any]) -> Void) throws -> String {
        var object = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(json.utf8)) as? [String: Any])
        change(&object)
        return String(decoding: try JSONSerialization.data(withJSONObject: object), as: UTF8.self)
    }
}
