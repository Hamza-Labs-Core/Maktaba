import XCTest

/// The Libraries section (Epic 48 story 48.18: FR-14 to FR-17) on iPhone and iPad, against
/// `MockHome` and `UITests/Fixtures`: Libraries with Pin, a library's grid (cursor paging,
/// facets, sort), Series, and a series (Continue, Mark watched). Requests are asserted on the
/// mock; screenshots dark, English and Arabic (`player-<device>-<language>-libraries-*`).
final class PlayerLibrariesUITests: PlayerUITestCase {
    /// `Fixtures/libraries-summary.json` with nothing pinned: Documentaries, then Lectures.
    private static let nonePinned = #"""
    {"items":[
     {"id":"lib-docs","name":"Documentaries","videos":5,"duration_sec":15360,"poster_video_ids":["v12"],"pinned":false,"pin_position":null},
     {"id":"lib-lectures","name":"Lectures","videos":7,"duration_sec":19385,"poster_video_ids":["v6"],"pinned":false,"pin_position":null}]}
    """#

    // MARK: Libraries (FR-14)

    /// Pin moves a library first and calls PUT with the new pins; the home's answer is kept.
    func testPinMovesALibraryFirstAndCallsPut() {
        home.stub("GET", "/api/libraries/summary", body: Self.nonePinned)
        launchSignedIn(section: "libraries")
        let docs = element("library.lib-docs")
        let lectures = element("library.lib-lectures")
        XCTAssertTrue(docs.waitForExistence(timeout: 20), "the library cards")
        XCTAssertTrue(lectures.exists)
        XCTAssertTrue(before(docs, lectures), "nothing pinned: the home's order")
        shot("libraries-unpinned")

        let pin = element("library.pin.lib-lectures")
        XCTAssertEqual(pin.label, "Pin Lectures")
        pin.tap()
        let put = waitForRequest { $0.method == "PUT" && $0.path == "/api/me/pinned-libraries" }
        XCTAssertNotNil(put, "Pin calls PUT /api/me/pinned-libraries")
        if let put {
            let body = (try? JSONSerialization.jsonObject(with: put.body)) as? [String: Any]
            XCTAssertEqual(body?["library_ids"] as? [String], ["lib-lectures"])
        }
        XCTAssertTrue(waitFor { self.before(lectures, docs) }, "the pinned library moves first")
        XCTAssertEqual(element("library.pin.lib-lectures").label, "Unpin Lectures")
        shot("libraries")
    }

    /// The Series tab: every series the person may see (`GET /api/series`), with progress;
    /// one opens its page.
    func testSeriesTabListsEverySeries() {
        launchSignedIn(section: "libraries")
        XCTAssertTrue(element("libraries.tabs").waitForExistence(timeout: 20))
        element("libraries.tabs").buttons["Series"].tap()
        let seerah = element("series.s-seerah")
        XCTAssertTrue(seerah.waitForExistence(timeout: 10), "the series grid")
        XCTAssertTrue(element("series.s-tafsir").exists)
        XCTAssertTrue(home.seen.contains("GET /api/series"))
        XCTAssertEqual(seerah.value as? String, "33% watched", "the series' progress")
        shot("libraries-series")
        seerah.tap()
        XCTAssertTrue(element("screen.series").waitForExistence(timeout: 10), "a series opens its page")
    }

    // MARK: a library (FR-15)

    /// The hub rows show (an empty one is left out), and the grid pages by cursor to the end.
    func testTheGridPagesByCursorToTheEnd() {
        launchSignedIn(section: "libraries", open: "library/lib-docs")
        XCTAssertTrue(element("screen.library").waitForExistence(timeout: 20), "the library's page")
        XCTAssertTrue(element("library.row.continue").waitForExistence(timeout: 10), "Continue Watching")
        XCTAssertTrue(element("library.row.recentlyAdded").exists, "Recently Added")
        XCTAssertFalse(element("library.row.onDeck").exists, "an empty row is left out")
        shot("library")

        let last = card("The Libraries of Cordoba")
        let window = app.windows.firstMatch.frame
        for _ in 0 ..< 20 where !(last.exists && window.contains(last.frame)) {
            app.swipeUp()
        }
        XCTAssertTrue(last.exists && window.contains(last.frame), "the last page's last video, on screen")
        // a few more swipes at the end ask for nothing more
        app.swipeUp()
        app.swipeUp()
        let pages = docsVideos().map { $0.value("cursor") }
        XCTAssertEqual(pages, [nil, "c2"], "the first page, then the next by cursor, then the end")
    }

    /// Sort and the facets (language, speaker) change the grid's request.
    func testFacetsAndSortChangeTheRequestsQuery() {
        launchSignedIn(section: "libraries", open: "library/lib-docs")
        XCTAssertTrue(element("screen.library").waitForExistence(timeout: 20))
        XCTAssertNotNil(waitForRequest { self.isDocs($0) && $0.value("sort") == "created_at" && $0.value("order") == "desc" },
                        "newest first")

        choose("library.sort", "A–Z")
        XCTAssertNotNil(waitForRequest { self.isDocs($0) && $0.value("sort") == "title" && $0.value("order") == "asc" },
                        "A–Z asks by title")
        XCTAssertTrue(home.seen.contains("GET /api/videos/facets"))

        choose("library.language", "Arabic")
        XCTAssertNotNil(waitForRequest { self.isDocs($0) && $0.value("language") == "ar" && $0.value("sort") == "title" },
                        "the language facet, the sort kept")

        choose("library.speaker", "Lecturer A")
        XCTAssertNotNil(waitForRequest { self.isDocs($0) && $0.value("speaker") == "Lecturer A" && $0.value("language") == "ar" },
                        "the speaker facet, the language kept")
        shot("library-filtered")
    }

    /// A library the person may not see (404) says so.
    func testALibraryGoneIsNotAvailable() {
        home.stub("GET", "/api/libraries/lib-gone/hub", status: 404, body: #"{"title":"not found","status":404}"#)
        launchSignedIn(section: "libraries", open: "library/lib-gone")
        XCTAssertTrue(element("libraries.notAvailable").waitForExistence(timeout: 20), "Not available")
    }

    // MARK: a series (FR-17)

    /// Continue opens the series' next episode (`next_episode`: v2), on today's video page.
    func testASeriesContinueOpensItsNextEpisode() {
        launchSignedIn(section: "libraries", open: "series/s-seerah")
        let go = element("series.continue")
        XCTAssertTrue(go.waitForExistence(timeout: 20), "Continue")
        XCTAssertTrue(element("episode.v1").exists && element("episode.v2").exists && element("episode.v3").exists)
        shot("series")
        go.tap()
        XCTAssertTrue(element("screen.videoDetail").waitForExistence(timeout: 10), "the episode's page")
        XCTAssertNotNil(waitForRequest { $0.method == "GET" && $0.path == "/api/videos/v2" }, "the next episode, v2")
        XCTAssertTrue(app.staticTexts["السيرة النبوية، الدرس ١١"].firstMatch.waitForExistence(timeout: 10))
    }

    /// Mark watched on the season marks each of its episodes; on one episode, that one.
    func testMarkWatchedOnASeasonMarksEachEpisode() {
        launchSignedIn(section: "libraries", open: "series/s-seerah")
        let season = element("series.markSeason")
        XCTAssertTrue(season.waitForExistence(timeout: 20))
        XCTAssertEqual(element("episode.mark.v1").label, "Mark not watched", "v1 is watched already")
        XCTAssertEqual(element("episode.mark.v2").label, "Mark watched")
        season.tap()
        let post = waitForRequest { $0.method == "POST" && $0.path == "/api/me/watched" }
        XCTAssertNotNil(post, "POST /api/me/watched")
        if let post {
            let body = (try? JSONSerialization.jsonObject(with: post.body)) as? [String: Any]
            XCTAssertEqual(Set(body?["video_ids"] as? [String] ?? []), ["v1", "v2", "v3"], "every episode of the season")
            XCTAssertEqual(body?["watched"] as? Bool, true)
        }
        for v in ["v1", "v2", "v3"] {
            XCTAssertTrue(waitFor { self.element("episode.mark.\(v)").label == "Mark not watched" }, "\(v) shows watched")
        }
        shot("series-watched")

        element("episode.mark.v3").tap()
        let one = waitForRequest { r in
            r.method == "POST" && r.path == "/api/me/watched"
                && ((try? JSONSerialization.jsonObject(with: r.body)) as? [String: Any])?["watched"] as? Bool == false
        }
        let body = one.flatMap { (try? JSONSerialization.jsonObject(with: $0.body)) as? [String: Any] }
        XCTAssertEqual(body?["video_ids"] as? [String], ["v3"], "one episode: that one, not watched")
    }

    // MARK: Arabic

    /// The section in Arabic, right to left: the root, a library, a series.
    func testLibrariesInArabic() {
        launchSignedIn("ar", section: "libraries")
        let docs = element("library.lib-docs")
        XCTAssertTrue(docs.waitForExistence(timeout: 20))
        XCTAssertTrue(element("libraries.tabs").buttons["المسلسلات"].exists, "the Series tab in Arabic")
        // the name is isolated (U+2068 … U+2069) in the right-to-left sentence
        XCTAssertEqual(element("library.pin.lib-lectures").label.filter { $0 != "\u{2068}" && $0 != "\u{2069}" },
                       "إلغاء تثبيت Lectures")
        XCTAssertTrue(before(element("library.lib-lectures"), docs), "pinned first, from the right")
        shot("libraries")

        launchSignedIn("ar", section: "libraries", open: "library/lib-docs")
        XCTAssertTrue(element("library.row.continue").waitForExistence(timeout: 20))
        XCTAssertTrue(app.staticTexts["تابع المشاهدة"].exists, "the row's title in Arabic")
        shot("library")

        launchSignedIn("ar", section: "libraries", open: "series/s-seerah")
        XCTAssertTrue(element("series.continue").waitForExistence(timeout: 20))
        XCTAssertTrue(app.staticTexts["الموسم 1"].exists || app.staticTexts["الموسم ١"].exists, "the season in Arabic")
        shot("series")
    }

    // MARK: accessibility (NFR-6)

    /// Apple's accessibility audit (labels, roles, contrast, Dynamic Type, clipped text, hit
    /// regions) on the Libraries root, a library and a series, in English and Arabic.
    func testAccessibilityAudit() {
        let pages: [(open: String?, ready: String)] = [(nil, "library.lib-docs"), ("library/lib-docs", "library.row.continue"),
                                                       ("series/s-seerah", "series.continue")]
        for language in ["en", "ar"] {
            for page in pages {
                let name = "\(language) \(page.open ?? "root")"
                launchSignedIn(language, section: "libraries", open: page.open)
                XCTAssertTrue(element(page.ready).waitForExistence(timeout: 20), name)
                // the posters loaded: no shimmer moving under the contrast check
                waitFor(timeout: 2) { false }
                audit(name)
                app.terminate()
            }
        }
    }

    /// The episode rows at the third accessibility size (AX3): each shows in full, the
    /// picture above the text, nothing cut at the screen's sides. The audit's Dynamic Type
    /// check cannot follow rows that larger text pushes below the screen (see `audit`), so
    /// this checks them at that size directly, scrolling to each.
    func testEpisodesAtAX3() {
        launchAtAX3(open: "series/s-seerah")
        XCTAssertTrue(element("series.continue").waitForExistence(timeout: 30))
        let window = app.windows.firstMatch.frame
        shot("series-ax3")
        for v in ["v3", "v2", "v1"] {
            let row = element("episode.\(v)")
            for _ in 0 ..< 8 where !(row.exists && window.contains(row.frame)) { app.swipeUp() }
            XCTAssertTrue(row.exists && window.contains(row.frame), "\(v) shows in full at AX3")
            XCTAssertTrue(row.label.contains("السيرة النبوية"), "\(v): its title, not cut: \(row.label)")
            XCTAssertTrue(element("episode.mark.\(v)").isHittable, "\(v): its mark can be tapped")
        }
        shot("series-ax3-end")

        // a library's filters (sort, language, speaker) grow too and stay tappable
        launchAtAX3(open: "library/lib-docs")
        let sort = element("library.sort")
        XCTAssertTrue(element("screen.library").waitForExistence(timeout: 30))
        for _ in 0 ..< 12 where !(sort.exists && window.contains(sort.frame)) { app.swipeUp() }
        XCTAssertTrue(sort.exists && window.contains(sort.frame), "the sort menu shows at AX3")
        XCTAssertTrue(sort.isHittable, "the sort menu can be tapped at AX3")
        shot("library-ax3-filters")
    }

    /// A first run at AX3, signed in (the larger start screens can take longer on a loaded runner).
    private func launchAtAX3(open: String) {
        launch("en", section: "libraries", open: open,
               extra: ["-UIPreferredContentSizeCategoryName", "UICTContentSizeCategoryAccessibilityXXXL"])
        signIn(timeout: 30)
    }

    private func audit(_ page: String) {
        // Dynamic Type exemption, for the episode rows and a library's filter row only: the
        // audit grows the text, and what sits low on the page then moves below the screen,
        // which it reports as "partially unsupported" (on iPhone the lowest episode row fails,
        // a row higher up passes, and scrolled up the next one passes; on iPad the filters at
        // the page's foot fail the same way). Both use scaled fonts with no fixed height, and
        // testEpisodesAtAX3 checks them at AX3 directly.
        let lowOnThePage = app.descendants(matching: .any)
            .matching(NSPredicate(format: "identifier BEGINSWITH 'episode.v' OR identifier == 'library.filters'"))
            .allElementsBoundByIndex.map(\.frame)
        // every issue of the page in one failure (the audit itself stops at the first)
        var found: [String] = []
        do {
            try app.performAccessibilityAudit { issue in
                // a progress bar is a reading, not a control: no 44-pt target (as 48.14's audit)
                if issue.auditType == .hitRegion, issue.element?.identifier == "progress.strip" { return true }
                // a card half off the screen's edge (a row scrolls sideways): the audit samples
                // pixels beyond it; the cards fully on screen are still checked (as 48.14's audit)
                if self.contrastOutOfView(issue) { return true }
                if issue.auditType == .dynamicType, let f = issue.element?.frame, lowOnThePage.contains(where: { $0.contains(f) }) { return true }
                found.append("\(issue.compactDescription) [\(issue.element?.identifier ?? "")|\(issue.element?.label ?? "")]")
                return true
            }
        } catch {
            found.append("\(error)")
        }
        XCTAssertTrue(found.isEmpty, "\(page): " + found.joined(separator: "; "))
    }

    // MARK: helpers

    /// `a` comes before `b` in reading order: above it, or on the same row on its leading side
    /// (the right in Arabic).
    private func before(_ a: XCUIElement, _ b: XCUIElement) -> Bool {
        let fa = a.frame, fb = b.frame
        if abs(fa.midY - fb.midY) < 20 { return language == "ar" ? fa.midX > fb.midX : fa.midX < fb.midX }
        return fa.midY < fb.midY
    }

    /// A video card by its title (a card is one element: its title and length).
    private func card(_ title: String) -> XCUIElement {
        app.descendants(matching: .any).matching(NSPredicate(format: "label BEGINSWITH %@", title)).firstMatch
    }

    private func isDocs(_ r: MockHome.Request) -> Bool {
        r.method == "GET" && r.path == "/api/videos" && r.value("library") == "lib-docs"
    }

    /// The grid's requests for Documentaries.
    private func docsVideos() -> [MockHome.Request] { home.requests.filter(isDocs) }

    /// Opens a filter's menu and picks `option`.
    private func choose(_ menu: String, _ option: String) {
        let m = element(menu)
        XCTAssertTrue(m.waitForExistence(timeout: 10), menu)
        m.tap()
        let item = app.buttons[option].firstMatch
        XCTAssertTrue(item.waitForExistence(timeout: 5), "\(menu): \(option)")
        item.tap()
    }

    private func waitForRequest(timeout: TimeInterval = 10, _ match: @escaping (MockHome.Request) -> Bool) -> MockHome.Request? {
        var found: MockHome.Request?
        _ = waitFor(timeout: timeout) {
            found = self.home.requests.last(where: match)
            return found != nil
        }
        return found
    }

    @discardableResult
    private func waitFor(timeout: TimeInterval = 10, _ check: @escaping () -> Bool) -> Bool {
        let end = Date().addingTimeInterval(timeout)
        while Date() < end {
            if check() { return true }
            RunLoop.current.run(until: Date().addingTimeInterval(0.25))
        }
        return check()
    }
}
