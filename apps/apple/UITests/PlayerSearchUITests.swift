import XCTest

/// Search (Epic 48 story 48.20: FR-19, FR-20) on iPhone and iPad, against `MockHome`'s
/// `search*.json` fixtures: typing shows suggestions; submit shows Titles and In the
/// transcript; a title opens its page and a passage the player at its time; recent searches
/// run again and are forgotten on the home; an Arabic query goes to the home unchanged.
final class PlayerSearchUITests: PlayerUITestCase {
    /// Search as a TestFlight build shows it: its own tab beside Libraries.
    private func openSearch(_ language: String = "en", extra: [String] = []) {
        launchSignedIn(language, section: "search", extra: extra)
        XCTAssertTrue(element("screen.search").waitForExistence(timeout: 15), "the Search screen")
    }

    private var field: XCUIElement { app.searchFields.firstMatch }

    /// Types `text` (read back from the field: see `typeExactly`); a closing "\n" submits it.
    private func type(_ text: String) {
        XCTAssertTrue(field.waitForExistence(timeout: 10), "the search field")
        let submit = text.hasSuffix("\n")
        field.typeExactly(submit ? String(text.dropLast()) : text, in: app)
        if submit { field.typeText("\n") }
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

    /// NFR-6: labels, roles, contrast, clipped text, hit regions, then Dynamic Type
    /// (`assertTextScales`: the same screen, in this launch, at the smallest size, the default
    /// and AX3).
    private func audit(_ name: String) {
        // every issue of the screen in one failure, with the element it is on
        var found: [String] = []
        do {
            try accessibilityAudit { issue in
                // a row half under the tab bar or past the screen's foot: the audit samples
                // pixels beyond it (the rows fully on screen are still checked)
                if self.contrastOutOfView(issue) { return true }
                // the system's clear button in the search field (iOS 26 puts the field in the
                // tab bar); UIKit's own control, its size not this app's
                if issue.auditType == .hitRegion, issue.detailedDescription.contains("_UITextFieldClearButton") { return true }
                // the system's own search field (UISearchTextField) reports its placeholder as
                // clipped although it shows in full (the shots); this screen's texts are checked
                if issue.auditType == .textClipped, issue.element?.elementType == .searchField { return true }
                // and, on an iPad, its placeholder's grey on the field's own fill (UIKit's colours
                // for both, "nearly passed"); the app's texts on this screen are still checked
                if issue.auditType == .contrast, issue.element?.elementType == .searchField { return true }
                found.append(self.described(issue))
                return true
            }
        } catch {
            found.append("\(error)")
        }
        XCTAssertTrue(found.isEmpty, "\(name) \(language): " + found.joined(separator: "; "))
        assertTextScales("\(name) \(language)")
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
        tapWhenIdle(passage, "the results after Return")
        playerOpened("v1", at: 1622, "at the passage's time")
    }

    func testATitleOpensItsPage() {
        openSearch()
        type("seerah\n")
        let title = element("search.title.v3")
        XCTAssertTrue(title.waitForExistence(timeout: 10))
        // Return let go of the keyboard before the results came in under the field: idle
        tapWhenIdle(title, "the results after Return")
        XCTAssertTrue(element("screen.video").waitForExistence(timeout: 10), "the video's page")
        XCTAssertTrue(app.staticTexts["السيرة النبوية، الدرس ١٢"].waitForExistence(timeout: 10), "v3's title")
    }

    /// Back in the field to refine, then a result: the field lets go of the keyboard before the
    /// page is pushed (Trail #1425's open gap). Pushed with the keyboard up, its hiding could be
    /// cut off and the app never idle again: the tap after it would wait its full 60 s.
    func testATitleTappedWhileRefiningLetsTheKeyboardGoAndOpensItsPage() {
        // no suggestions over the results while the field is back in use
        home.stub("GET", "/api/search/suggest", body: #"{"suggestions": []}"#)
        openSearch()
        type("seerah\n")
        let title = element("search.title.v3")
        XCTAssertTrue(title.waitForExistence(timeout: 10))
        // back in the field: the same words typed again, the results still under it
        type("seerah")
        XCTAssertTrue(app.keyboards.firstMatch.waitForExistence(timeout: 5), "the keyboard is up again")
        XCTAssertTrue(title.waitForExistence(timeout: 10), "the results under the field")
        tapWhenIdle(title, "the results while refining")
        // the first tap opens the page (a gesture on the row once took the tap on an iPhone)
        XCTAssertTrue(element("screen.video").waitForExistence(timeout: 10), "the video's page")
        XCTAssertTrue(app.staticTexts["السيرة النبوية، الدرس ١٢"].waitForExistence(timeout: 10), "v3's title")
        XCTAssertTrue(app.keyboards.firstMatch.waitForNonExistence(timeout: 5), "the keyboard let go")
        // and the app went idle after the push: the next tap does not wait out the stall
        let back = app.navigationBars.buttons.firstMatch
        XCTAssertTrue(back.waitForExistence(timeout: 5), "the page's back button")
        tapWhenIdle(back, "the video's page after the push")
        XCTAssertTrue(element("screen.search").waitForExistence(timeout: 10), "back on Search")
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

    // MARK: Search help (story 49.11)

    /// A label read without the bidi isolates (U+2068, U+2069) around its interpolated words.
    private func plain(_ s: String) -> String {
        s.replacingOccurrences(of: "\u{2068}", with: "").replacingOccurrences(of: "\u{2069}", with: "")
    }

    /// A bucket of the facets' sheet, scrolled into sight: the sheet's list draws only the rows
    /// on screen (a phone's sheet opens at half height).
    @discardableResult
    private func reveal(_ id: String) -> XCUIElement {
        let bucket = element(id), list = element("search.facets")
        XCTAssertTrue(list.waitForExistence(timeout: 10), "the facets' sheet")
        for _ in 0 ..< 6 where !(bucket.exists && bucket.isHittable) {
            list.swipeUp()
        }
        XCTAssertTrue(bucket.exists && bucket.isHittable, "\(id) in sight")
        return bucket
    }

    /// The `filters` of the last `POST /api/search` (nil: none sent).
    private var lastFilters: [String: Any]? { searches.last?["filters"] as? [String: Any] }

    /// Each suggestion says what it is (`Fixtures/search-suggest.json`, story 49.8's kinds), and
    /// a video's title opens that video rather than searching its words.
    func testSuggestionsShowTheirKindAndATitleOpensItsVideo() {
        openSearch()
        type("se")
        XCTAssertTrue(element("search.suggestion.title").waitForExistence(timeout: 10), "the suggestions")
        XCTAssertTrue(element("search.suggestion.history").exists, "an earlier search")
        XCTAssertTrue(element("search.suggestion.term").exists, "a word")
        XCTAssertTrue(element("search.suggestion.speaker").exists, "a speaker")
        for word in ["Recent", "Word", "Video", "Speaker"] {
            XCTAssertTrue(app.staticTexts[word].exists, "the kind \(word)")
        }
        shot("search-suggestion-kinds")
        element("search.suggestion.title").tap()
        XCTAssertTrue(element("screen.video").waitForExistence(timeout: 10), "the title's video")
        XCTAssertTrue(app.staticTexts["السيرة النبوية، الدرس ١٢"].waitForExistence(timeout: 10), "v3's title")
        XCTAssertTrue(searches.isEmpty, "a title is not searched")
    }

    /// The kinds in Arabic.
    func testSuggestionKindsInArabic() {
        openSearch("ar")
        type("se")
        XCTAssertTrue(element("search.suggestion.title").waitForExistence(timeout: 10), "the suggestions")
        for word in ["بحثت عنه", "كلمة", "مقطع", "متحدث"] {
            XCTAssertTrue(app.staticTexts[word].exists, "the kind \(word)")
        }
        shot("search-suggestion-kinds")
    }

    /// Filters: the sheet lists each facet's buckets with their counts; each press searches
    /// again with that bucket's filter, several combined; the filters in use are listed, each
    /// removable, and Clear all takes them all off. The words and the order stay.
    func testFacetsNarrowCombineAndAreTakenOff() throws {
        openSearch()
        type("seerah\n")
        let filters = element("search.filters")
        XCTAssertTrue(filters.waitForExistence(timeout: 10), "Filters over the results")
        tapWhenIdle(filters, "the results after Return")

        let lectures = element("search.facet.library.lib-lectures")
        XCTAssertTrue(lectures.waitForExistence(timeout: 10), "the facets' sheet")
        XCTAssertEqual(plain(lectures.label), "Lectures, 3 videos")
        XCTAssertTrue(app.staticTexts["Narrow the results"].exists)
        shot("search-filters")

        let arabic = reveal("search.facet.language.ar")
        XCTAssertEqual(plain(arabic.label), "Arabic, 3 videos")
        arabic.tap()
        eventually("searched in Arabic only") { (self.lastFilters?["language"] as? [String]) == ["ar"] }
        eventually("a chosen bucket reads as selected") { arabic.isSelected }
        reveal("search.facet.year.2024").tap()
        eventually("and of 2024, as its bucket's range") {
            (self.lastFilters?["date"] as? [String: String]) == ["gte": "2024-01-01T00:00:00.000Z", "lt": "2025-01-01T00:00:00.000Z"]
        }
        XCTAssertEqual(lastFilters?["language"] as? [String], ["ar"], "the filters combine")
        let long = reveal("search.facet.duration.over_60m")
        XCTAssertEqual(plain(long.label), "Over an hour, 2 videos")
        long.tap()
        eventually("and over an hour") { (self.lastFilters?["duration_sec"] as? [String: Int]) == ["gte": 3600] }
        XCTAssertEqual(searches.last?["q"] as? String, "seerah", "the same words")
        element("search.facets.done").tap()

        let chip = element("search.filter.language.ar")
        XCTAssertTrue(chip.waitForExistence(timeout: 10), "the filters in use")
        XCTAssertEqual(plain(chip.label), "Remove Language: Arabic")
        XCTAssertTrue(element("search.filter.year.2024").exists && element("search.filter.duration.over_60m").exists)
        XCTAssertEqual(plain(filters.label), "Filters (3)", "Filters counts them")
        shot("search-filters-in-use")
        audit("filters")

        tapWhenIdle(chip, "the filters in use")
        eventually("Arabic taken off") { self.lastFilters?["language"] == nil && self.lastFilters?["date"] != nil }
        tapWhenIdle(element("search.filters.clear"), "the filters in use")
        eventually("every filter taken off") { self.searches.last.map { $0["filters"] == nil } ?? false }
        XCTAssertTrue(element("search.filter.year.2024").waitForNonExistence(timeout: 5), "none in use")
    }

    /// New words drop the filters (they were the old results' counts) and keep the order.
    func testNewWordsDropTheFiltersAndKeepTheOrder() {
        openSearch()
        type("seerah\n")
        let sort = element("search.sort")
        XCTAssertTrue(sort.waitForExistence(timeout: 10), "the order over the results")
        XCTAssertEqual(plain(sort.label), "Sort by: Relevance")
        tapWhenIdle(sort, "the results after Return")
        let newest = app.buttons["Newest"]
        XCTAssertTrue(newest.waitForExistence(timeout: 5), "the orders")
        XCTAssertTrue(app.buttons["Most matches"].exists && app.buttons["Relevance"].exists)
        newest.tap()
        eventually("searched newest first") { self.searches.last?["sort"] as? String == "newest" }
        XCTAssertTrue(sort.waitForExistence(timeout: 5))
        eventually("the order shown") { self.plain(sort.label) == "Sort by: Newest" }

        tapWhenIdle(element("search.filters"), "the results in the new order")
        reveal("search.facet.speaker.الشيخ أحمد").tap()
        eventually("by the speaker") { (self.lastFilters?["speaker"] as? [String]) == ["الشيخ أحمد"] }
        XCTAssertEqual(searches.last?["sort"] as? String, "newest", "the order kept")
        element("search.facets.done").tap()
        XCTAssertTrue(element("search.filter.speaker.الشيخ أحمد").waitForExistence(timeout: 10))

        type("cordoba\n")
        eventually("the new words") { self.searches.last?["q"] as? String == "cordoba" }
        XCTAssertNil(searches.last?["filters"], "new words drop the filters")
        XCTAssertEqual(searches.last?["sort"] as? String, "newest", "and keep the order")
    }

    /// Did you mean: a row over the results with the corrected words; a tap searches them, the
    /// order kept, and the field shows them. In Arabic, its words in Arabic's own order.
    func testDidYouMeanRunsTheCorrectedWordsInArabic() throws {
        let corrected = "السيرة النبوية"
        let first = try XCTUnwrap(home.fixture(named: "search"))
            .replacingOccurrences(of: #""q": "السيرة","#, with: #""q": "السيره النبويه", "did_you_mean": "\#(corrected)","#)
        XCTAssertTrue(first.contains("did_you_mean"), "the fixture's query was found")
        home.stub("POST", "/api/search", body: first)
        openSearch("ar")
        type("السيره النبويه\n")
        let dym = element("search.didYouMean")
        XCTAssertTrue(dym.waitForExistence(timeout: 10), "did you mean")
        XCTAssertEqual(plain(dym.label), "هل تقصد السيرة النبوية؟")
        XCTAssertTrue(element("search.titles").exists, "over the results")
        shot("search-did-you-mean")
        audit("did-you-mean")

        tapWhenIdle(dym, "the results after Return")
        eventually("the corrected words searched") { self.searches.last?["q"] as? String == corrected }
        XCTAssertEqual(searches.count, 2)
        eventually("in the field") { (self.field.value as? String) == corrected }
        XCTAssertTrue(dym.waitForNonExistence(timeout: 5), "no did-you-mean for its own words")
    }

    // MARK: Search by meaning off on the home (story 51.11)

    /// An admin: the line under the field says the search is by words only, and it is.
    func testSearchByMeaningOffShowsAnAdminTheWordsOnlyLine() {
        home.capabilities(off: ["semantic_search"], admin: true)
        openSearch()
        let line = element("search.wordsOnly")
        XCTAssertTrue(line.waitForExistence(timeout: 15), "the words-only line; the home saw \(home.seen)")
        XCTAssertEqual(line.label, "Searching by words only. Search by meaning is off.")
        type("seerah\n")
        XCTAssertTrue(element("search.titles").waitForExistence(timeout: 10), "the words' results")
        XCTAssertEqual(searches.last?["mode"] as? String, "fts", "words only")
        shot("search-words-only-admin")
    }

    /// The same line in Arabic.
    func testSearchByMeaningOffShowsAnAdminTheWordsOnlyLineInArabic() {
        home.capabilities(off: ["semantic_search"], admin: true)
        openSearch("ar")
        let line = element("search.wordsOnly")
        XCTAssertTrue(line.waitForExistence(timeout: 15), "the words-only line; the home saw \(home.seen)")
        XCTAssertEqual(line.label, "البحث بالكلمات فقط. البحث بالمعنى متوقف.")
    }

    /// A viewer: words-only results, and no line about it.
    func testSearchByMeaningOffShowsAViewerNoLine() {
        home.capabilities(off: ["semantic_search"], admin: false)
        openSearch()
        eventually("the app asked what is on", timeout: 15) { home.seen.contains("GET /api/capabilities") }
        // the answer is read on the app's side a moment after the mock saw the ask
        RunLoop.current.run(until: Date().addingTimeInterval(1))
        type("seerah\n")
        XCTAssertTrue(element("search.titles").waitForExistence(timeout: 10), "the words' results")
        XCTAssertEqual(searches.last?["mode"] as? String, "fts", "words only")
        XCTAssertFalse(element("search.wordsOnly").exists, "no line for a viewer")
    }
}
