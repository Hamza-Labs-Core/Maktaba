import XCTest

/// Search on Apple TV (story 49.11), with the Siri remote, against `MockHome` (served by the
/// test runner on 127.0.0.1; no real home): the suggestions with their kinds as you type,
/// **Search for “…”** under the keyboard, the facets' sheet with its counts (several filters
/// combined, then taken off), the order, and the did-you-mean in Arabic. The answers are
/// `UITests/Fixtures/search*.json`, the Player's (story 49.8's fields).
@MainActor
final class TVSearchUITests: XCTestCase {
    let remote = XCUIRemote.shared
    var app: XCUIApplication!
    var home: MockHome!
    private var language = "en"

    override func setUp() async throws {
        continueAfterFailure = false
        home = try MockHome()
        try home.start()
        app = XCUIApplication()
    }

    override func tearDown() async throws {
        home?.stop()
    }

    /// A suggestion under the keyboard, by the kind its label ends with ("seerah  ·  Word"):
    /// tvOS shows them in its own row of cells, which keep the label and not the identifier.
    private func suggestion(_ kind: String) -> XCUIElement {
        app.buttons.matching(NSPredicate(format: "label ENDSWITH %@", "·  \(kind)")).firstMatch
    }

    /// Typing shows the suggestions, each with its kind; a video's title opens its page and
    /// searches nothing.
    func testSuggestionsShowTheirKindsAndATitleOpensItsPage() throws {
        signIn("en")
        openSearch()
        typeQuery("se")
        let title = suggestion("Video")
        XCTAssertTrue(title.waitForExistence(timeout: 15), "the suggestions; the home saw \(home.seen.suffix(8))")
        XCTAssertTrue(wait(timeout: 10) { self.home.requests.contains { $0.path == "/api/search/suggest" && $0.value("q") == "se" } },
                      "asked the home for suggestions")
        XCTAssertTrue(suggestion("Recent").exists, "an earlier search")
        XCTAssertTrue(suggestion("Word").exists, "a word")
        XCTAssertEqual(plain(title.label), "السيرة النبوية، الدرس ١٢  ·  Video")
        XCTAssertTrue(searches.isEmpty, "typing searches nothing")
        shot("tv-search-suggestions-en")

        // down to the suggestions' row, then along it to the title
        for _ in 0..<4 where !app.cells.allElementsBoundByIndex.contains(where: \.hasFocus) && !title.hasFocus {
            remote.press(.down)
            usleep(400_000)
        }
        // the cell or its button takes the focus
        let cell = app.cells.matching(NSPredicate(format: "label CONTAINS %@", "·  Video")).firstMatch
        for _ in 0..<8 where !(cell.hasFocus || title.hasFocus) {
            remote.press(.right)
            usleep(400_000)
        }
        XCTAssertTrue(cell.hasFocus || title.hasFocus, "the focus on the title's suggestion")
        remote.press(.select)
        let play = app.buttons.matching(NSPredicate(format: "label BEGINSWITH %@ OR label BEGINSWITH %@", "Play", "Resume")).firstMatch
        XCTAssertTrue(play.waitForExistence(timeout: 20), "the title's page; the home saw \(home.seen.suffix(8))")
        XCTAssertTrue(home.seen.contains("GET /api/videos/v3"), "v3's page")
        XCTAssertTrue(searches.isEmpty, "a title is not searched")
    }

    /// Search for “seerah”, then Filters: the sheet's buckets with their counts, two combined;
    /// the filters in use, one taken off; then the order.
    func testFiltersCombineAndTheOrderChangesWithTheRemote() throws {
        home.stub("GET", "/api/search/suggest", body: #"{"suggestions": []}"#)
        signIn("en")
        openSearch()
        typeQuery("seerah")
        runTyped("seerah")

        let filters = app.buttons["search.filters"], sort = element("search.sort")
        XCTAssertTrue(reach(filters, from: sort), "the focus on Filters")
        remote.press(.select)
        let arabic = app.buttons["search.facet.language.ar"]
        XCTAssertTrue(arabic.waitForExistence(timeout: 10), "the facets' sheet")
        XCTAssertEqual(plain(arabic.label), "Arabic, 3 videos")
        XCTAssertEqual(plain(app.buttons["search.facet.library.lib-lectures"].label), "Lectures, 3 videos")
        moveFocus(to: arabic, with: .down)
        remote.press(.select)
        XCTAssertTrue(wait { (self.lastFilters?["language"] as? [String]) == ["ar"] }, "searched in Arabic only")
        // down to the length row (its first bucket, under 20 to 60 min), then along it
        let long = app.buttons["search.facet.duration.over_60m"]
        moveFocus(to: app.buttons["search.facet.duration.20m_60m"], with: .down)
        moveFocus(to: long, with: language == "ar" ? .left : .right)
        remote.press(.select)
        XCTAssertTrue(wait { (self.lastFilters?["duration_sec"] as? [String: Int]) == ["gte": 3600] }, "and over an hour")
        XCTAssertEqual(lastFilters?["language"] as? [String], ["ar"], "the filters combine")
        shot("tv-search-filters-en")
        // Menu closes the sheet, as on any tvOS sheet
        remote.press(.menu)
        XCTAssertTrue(long.waitForNonExistence(timeout: 10), "the sheet closed")

        let chip = app.buttons["search.filter.language.ar"]
        XCTAssertTrue(chip.waitForExistence(timeout: 10), "the filters in use")
        XCTAssertEqual(plain(chip.label), "Remove Language: Arabic")
        XCTAssertTrue(app.buttons["search.filter.duration.over_60m"].exists)
        shot("tv-search-filters-in-use-en")
        XCTAssertTrue(reach(chip, from: filters, then: .down), "the focus on the Arabic filter")
        remote.press(.select)
        XCTAssertTrue(wait { self.lastFilters?["language"] == nil && self.lastFilters?["duration_sec"] != nil }, "Arabic taken off")

        XCTAssertTrue(reach(sort, from: filters), "the focus on the order")
        remote.press(.select)
        let newest = app.buttons["search.sort.newest"]
        XCTAssertTrue(newest.waitForExistence(timeout: 10), "the orders")
        moveFocus(to: newest, with: .down)
        remote.press(.select)
        XCTAssertTrue(wait { self.searches.last?["sort"] as? String == "newest" }, "newest first")
        XCTAssertNotNil(lastFilters?["duration_sec"], "the filter kept")
        XCTAssertEqual(searches.last?["q"] as? String, "seerah", "the same words")
    }

    /// Did you mean, in Arabic: the row over the results says the corrected words, and Select
    /// searches them.
    func testDidYouMeanInArabic() throws {
        let corrected = "السيرة النبوية"
        let answer = try XCTUnwrap(home.fixture(named: "search"))
            .replacingOccurrences(of: #""q": "السيرة","#, with: #""q": "seerah", "did_you_mean": "\#(corrected)","#)
        XCTAssertTrue(answer.contains("did_you_mean"), "the fixture's query was found")
        home.stub("POST", "/api/search", body: answer)
        home.stub("GET", "/api/search/suggest", body: #"{"suggestions": []}"#)
        signIn("ar")
        openSearch("البحث")
        typeQuery("seerah")
        runTyped("seerah")

        let dym = app.buttons["search.didYouMean"]
        XCTAssertTrue(dym.waitForExistence(timeout: 15), "did you mean")
        XCTAssertEqual(plain(dym.label), "هل تقصد السيرة النبوية؟")
        XCTAssertTrue(element("search.titles").exists, "over the results")
        shot("tv-search-did-you-mean-ar")
        XCTAssertTrue(reach(dym, from: element("search.sort"), then: .up), "the focus on did you mean")
        remote.press(.select)
        XCTAssertTrue(wait { self.searches.last?["q"] as? String == corrected }, "the corrected words searched")
        XCTAssertTrue(dym.waitForNonExistence(timeout: 10), "no did-you-mean for its own words")
    }

    // MARK: steps

    /// The bodies of every `POST /api/search` so far, as JSON.
    private var searches: [[String: Any]] {
        home.requests.filter { $0.method == "POST" && $0.path == "/api/search" }
            .compactMap { try? JSONSerialization.jsonObject(with: $0.body) as? [String: Any] }
    }

    private var lastFilters: [String: Any]? { searches.last?["filters"] as? [String: Any] }

    /// A label without the bidi isolates (U+2068, U+2069) around its interpolated words.
    private func plain(_ s: String) -> String {
        s.replacingOccurrences(of: "\u{2068}", with: "").replacingOccurrences(of: "\u{2069}", with: "")
    }

    /// The Search tab (its title in the app's language).
    private func openSearch(_ title: String = "Search") {
        let tab = app.tabBars.buttons[title]
        XCTAssertTrue(tab.waitForExistence(timeout: 20), "the \(title) tab")
        for _ in 0..<6 where !app.tabBars.buttons.allElementsBoundByIndex.contains(where: \.hasFocus) {
            remote.press(.up)
            usleep(400_000)
        }
        moveFocus(to: tab, with: language == "ar" ? .left : .right)
        remote.press(.select)
        sleep(1)
    }

    /// Types `text` in the search field: down from the tab bar into the keyboard.
    private func typeQuery(_ text: String) {
        let field = app.searchFields.firstMatch
        XCTAssertTrue(field.waitForExistence(timeout: 20), "the search field")
        field.typeExactly(text, in: app) {
            // as the other TV tests open a field: down to it, Select; the search tab's field
            // may instead give the keyboard as soon as the focus is in it
            for _ in 0..<4 where !field.hasFocus {
                self.remote.press(.down)
                usleep(400_000)
            }
            if field.hasFocus { self.remote.press(.select) }
            usleep(600_000)
        }
    }

    /// Down from the keyboard to Search for “text”, Select: the search runs and its results show.
    private func runTyped(_ text: String) {
        let run = app.buttons["search.run"]
        XCTAssertTrue(run.waitForExistence(timeout: 10), "Search for “\(text)”")
        XCTAssertEqual(plain(run.label).contains(text), true, "it names the words: \(run.label)")
        moveFocus(to: run, with: .down, tries: 16)
        remote.press(.select)
        XCTAssertTrue(element("search.titles").waitForExistence(timeout: 15), "the results; the home saw \(home.seen.suffix(8))")
        XCTAssertEqual(searches.last?["q"] as? String, text)
        XCTAssertTrue(run.waitForNonExistence(timeout: 5), "the words are searched: no Search for")
    }

    /// Gets the focus to `target` once the focus is on the row of `near` (or `target`): up
    /// from the results to that row, then along it (or `then`, from `near` to `target`).
    private func reach(_ target: XCUIElement, from near: XCUIElement, then: XCUIRemote.Button? = nil) -> Bool {
        XCTAssertTrue(target.waitForExistence(timeout: 10), "\(target) is not there")
        // the row is found by what has the focus (a Menu is not a button to XCUITest): up from
        // the results, then down from the keyboard if the focus went back up there
        let row = [target.identifier, near.identifier]
        func onRow() -> Bool { row.contains(focused) }
        for _ in 0..<6 where !onRow() {
            remote.press(.up)
            usleep(400_000)
            // up into the keyboard: the help row is below it
            if app.keyboards.firstMatch.exists, app.keys.allElementsBoundByIndex.contains(where: \.hasFocus) { break }
        }
        for _ in 0..<8 where !onRow() {
            remote.press(.down)
            usleep(400_000)
        }
        let along = then ?? (language == "ar" ? .left : .right)
        let back: XCUIRemote.Button = along == .right ? .left : along == .left ? .right : along == .down ? .up : .down
        for move in [along, along, along, back, back, back, back] where focused != target.identifier {
            remote.press(move)
            usleep(400_000)
        }
        return focused == target.identifier
    }

    /// The identifier of what has the focus ("" when none has one).
    private var focused: String {
        app.descendants(matching: .any).matching(NSPredicate(format: "hasFocus == true")).allElementsBoundByIndex
            .map(\.identifier).first { !$0.isEmpty } ?? ""
    }

    private func element(_ id: String) -> XCUIElement { app.descendants(matching: .any)[id].firstMatch }

    /// First launch: the mock home's address typed, then the code approved elsewhere.
    private func signIn(_ language: String) {
        self.language = language
        app.launchArguments = ["-uitest-reset", "-AppleLanguages", "(\(language))",
                               "-AppleLocale", language == "ar" ? "ar_SA" : "en_US"]
        app.launch()
        let address = app.textFields["picker.address"]
        XCTAssertTrue(address.waitForExistence(timeout: 20))
        address.typeExactly(home.url, in: app) {
            self.moveFocus(to: address, with: .down)
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
        let connect = app.buttons["picker.connect"]
        if connect.waitForExistence(timeout: 2), connect.isEnabled, connect.isHittable {
            moveFocus(to: connect, with: .right)
            remote.press(.select)
        }
        XCTAssertTrue(app.staticTexts["pairing-code"].waitForExistence(timeout: 20), "the code; the home saw \(home.seen)")
        home.approve()
        XCTAssertTrue(app.tabBars.firstMatch.waitForExistence(timeout: 20), "approved: signed in")
    }

    private func moveFocus(to element: XCUIElement, with direction: XCUIRemote.Button, tries: Int = 12) {
        XCTAssertTrue(element.waitForExistence(timeout: 10), "\(element) is not there")
        for _ in 0..<tries where !element.hasFocus {
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

    private func shot(_ name: String) {
        let s = XCUIScreen.main.screenshot()
        let a = XCTAttachment(screenshot: s)
        a.name = name
        a.lifetime = .keepAlways
        add(a)
        if let dir = ProcessInfo.processInfo.environment["MAKTABA_SHOTS"], !dir.isEmpty {
            try? FileManager.default.createDirectory(atPath: dir, withIntermediateDirectories: true)
            try? s.pngRepresentation.write(to: URL(fileURLWithPath: dir).appendingPathComponent("\(name).png"))
        }
    }
}
