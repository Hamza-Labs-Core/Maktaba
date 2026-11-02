import XCTest

/// Search (Epic 48 story 48.20: FR-19, FR-20) on iPhone and iPad, against `MockHome`'s
/// `search*.json` fixtures: typing shows suggestions; submit shows Titles and In the
/// transcript; a title opens its page and a passage the player at its time; recent searches
/// run again and are forgotten on the home; an Arabic query goes to the home unchanged.
final class PlayerSearchUITests: PlayerUITestCase {
    /// Search as a TestFlight build shows it: its own tab beside Libraries.
    private func openSearch(_ language: String = "en") {
        launchSignedIn(language, section: "search")
        XCTAssertTrue(element("screen.search").waitForExistence(timeout: 15), "the Search screen")
    }

    private var field: XCUIElement { app.searchFields.firstMatch }

    private func type(_ text: String) {
        XCTAssertTrue(field.waitForExistence(timeout: 10), "the search field")
        field.tap()
        field.typeText(text)
    }

    /// The bodies of every `POST /api/search` so far, as JSON.
    private var searches: [[String: Any]] {
        home.requests.filter { $0.method == "POST" && $0.path == "/api/search" }
            .compactMap { try? JSONSerialization.jsonObject(with: $0.body) as? [String: Any] }
    }

    /// Waits until `check` holds (the app's requests arrive on the mock's own queue).
    private func eventually(_ what: String, timeout: TimeInterval = 10, _ check: () -> Bool) {
        let end = Date().addingTimeInterval(timeout)
        while !check(), Date() < end { RunLoop.current.run(until: Date().addingTimeInterval(0.1)) }
        XCTAssertTrue(check(), what)
    }

    /// NFR-6: labels, roles, contrast, Dynamic Type, clipped text, hit regions.
    private func audit(_ name: String) {
        do {
            try app.performAccessibilityAudit { issue in
                // a row half under the tab bar or past the screen's foot: the audit samples
                // pixels beyond it (the rows fully on screen are still checked)
                if self.contrastOutOfView(issue) { return true }
                // the system's clear button in the search field (iOS 26 puts the field in the
                // tab bar); UIKit's own control, its size not this app's
                if issue.auditType == .hitRegion, issue.detailedDescription.contains("_UITextFieldClearButton") { return true }
                // the system's own search field (UISearchTextField) reports its placeholder as
                // clipped although it shows in full (the shots); this screen's texts are checked
                if issue.auditType == .textClipped, issue.element?.elementType == .searchField { return true }
                // and on an iPad its empty field's placeholder, a UIKit UILabel with no element,
                // as fixed size (this screen's texts are SwiftUI, never a UILabel, and checked)
                if issue.auditType == .dynamicType, issue.element == nil, issue.detailedDescription.contains("UILabel") { return true }
                return false
            }
        } catch {
            XCTFail("\(name) \(language): \(error)")
        }
    }

    func testTypingShowsSuggestions() {
        openSearch()
        type("se")
        // the person's own past searches starting with it (`Fixtures/search-suggest.json`)
        XCTAssertTrue(app.staticTexts["السيرة النبوية"].waitForExistence(timeout: 10), "the suggestions")
        XCTAssertTrue(app.staticTexts["seerah"].exists)
        eventually("asked the home for suggestions") {
            home.requests.contains { $0.path == "/api/search/suggest" && $0.value("q") == "se" }
        }
        shot("search-suggestions")
        // a suggestion runs that search
        app.staticTexts["seerah"].tap()
        XCTAssertTrue(element("search.titles").waitForExistence(timeout: 10), "the suggestion's results")
        eventually("the suggestion's search") { searches.last?["q"] as? String == "seerah" }
        // and only that one: what was typed so far is never searched as well
        XCTAssertEqual(searches.compactMap { $0["q"] as? String }, ["seerah"])
    }

    func testSubmitShowsTitlesAndTranscriptsAndAPassagePlaysAtItsTime() {
        openSearch()
        type("seerah\n")
        XCTAssertTrue(element("search.titles").waitForExistence(timeout: 10), "Titles")
        XCTAssertTrue(app.staticTexts["Titles"].exists)
        XCTAssertTrue(element("search.title.v3").exists, "the first title")
        XCTAssertTrue(app.staticTexts["In the transcript"].waitForExistence(timeout: 5), "In the transcript")
        XCTAssertEqual(searches.count, 1)
        XCTAssertEqual(searches.first?["q"] as? String, "seerah")
        XCTAssertEqual(searches.first?["mode"] as? String, "hybrid", "FR-20: hybrid")
        shot("search-results")
        audit("results")

        // the second passage of v1 starts at 1622 s (`Fixtures/search.json`)
        let passage = element("search.passage.v1.1")
        if !passage.isHittable { app.swipeUp() }
        XCTAssertTrue(passage.waitForExistence(timeout: 5), "a passage")
        XCTAssertTrue(passage.label.hasPrefix("Play from"), "a passage says what it does: \(passage.label)")
        passage.tap()
        playerOpened("v1", at: 1622, "at the passage's time")
    }

    func testATitleOpensItsPage() {
        openSearch()
        type("seerah\n")
        let title = element("search.title.v3")
        XCTAssertTrue(title.waitForExistence(timeout: 10))
        title.tap()
        XCTAssertTrue(element("screen.video").waitForExistence(timeout: 10), "the video's page")
        XCTAssertTrue(app.staticTexts["السيرة النبوية، الدرس ١٢"].waitForExistence(timeout: 10), "v3's title")
    }

    /// Arabic: the query goes to the home as typed, its diacritics too (the home ranks it;
    /// the app normalises nothing), and the screen speaks Arabic.
    func testAnArabicQueryIsSentUnchanged() {
        let query = "الصَّبر"
        openSearch("ar")
        type(query + "\n")
        XCTAssertTrue(app.staticTexts["العناوين"].waitForExistence(timeout: 10), "Titles in Arabic")
        XCTAssertTrue(app.staticTexts["في التفريغ"].exists, "In the transcript in Arabic")
        let sent = searches.last?["q"] as? String
        // scalar by scalar: Swift's == would also take a differently composed string
        XCTAssertEqual(sent.map { Array($0.unicodeScalars) }, Array(query.unicodeScalars), "sent unchanged: \(sent ?? "nil")")
        shot("search-results")
        audit("results")
    }

    func testRecentSearchesRunAgainAndAreForgotten() {
        home.stub("DELETE", "/api/search/recent/sr-0", status: 204)
        openSearch()
        // `Fixtures/search-recent.json`, newest first
        XCTAssertTrue(app.staticTexts["Recent searches"].waitForExistence(timeout: 10), "the recent searches")
        XCTAssertTrue(app.staticTexts["Cordoba"].exists)
        shot("search-recent")
        audit("recent")

        let remove = app.buttons.matching(identifier: "search.recent.remove").element(boundBy: 1)
        XCTAssertEqual(remove.label, "Remove Cordoba")
        remove.tap()
        XCTAssertTrue(app.staticTexts["Cordoba"].waitForNonExistence(timeout: 5), "forgotten here")
        XCTAssertTrue(home.seen.contains("DELETE /api/search/recent/sr-0"), "and on the home")

        app.staticTexts["السيرة"].tap()
        XCTAssertTrue(element("search.titles").waitForExistence(timeout: 10), "the recent search ran again")
        XCTAssertEqual(searches.last?["q"] as? String, "السيرة")
    }

    func testClearAllForgetsEveryRecentSearch() {
        openSearch()
        let clear = element("search.recent.clear")
        XCTAssertTrue(clear.waitForExistence(timeout: 10))
        clear.tap()
        XCTAssertTrue(app.staticTexts["Cordoba"].waitForNonExistence(timeout: 5))
        XCTAssertTrue(home.seen.contains("DELETE /api/search/recent"))
        XCTAssertTrue(app.staticTexts["Search for a title, or for words said in a video."].exists, "the empty hint")
    }

    /// Show more asks for the next page of the search the home kept, and stops at its end.
    func testShowMorePagesTheTitles() throws {
        let first = try XCTUnwrap(home.fixture(named: "search"))
            .replacingOccurrences(of: #""page_size": 20"#, with: #""page_size": 3"#)
            .replacingOccurrences(of: #""videos_total": 3"#, with: #""videos_total": 4"#)
        home.stub("POST", "/api/search", body: first)
        openSearch()
        type("seerah\n")
        let more = element("search.titles.more")
        XCTAssertTrue(more.waitForExistence(timeout: 10), "more titles than the first page")
        more.tap()
        eventually("asked for the next page") {
            home.requests.contains { $0.path == "/api/search/results/sr-1" && $0.value("list") == "titles" && $0.value("offset") == "3" }
        }
        // the page held no new title: the end
        XCTAssertTrue(more.waitForNonExistence(timeout: 5), "no more to ask for")
    }

    func testNothingFound() throws {
        home.stub("POST", "/api/search", body: try XCTUnwrap(home.fixture(named: "search-empty")))
        openSearch()
        type("zzzz\n")
        XCTAssertTrue(element("search.none").waitForExistence(timeout: 10))
        XCTAssertTrue(app.staticTexts["Nothing matches “zzzz”."].exists)
    }
}
