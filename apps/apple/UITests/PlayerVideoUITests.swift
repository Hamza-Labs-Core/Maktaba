import XCTest

/// A video's page (Epic 48 story 48.19; FR-18, FR-9) on iPhone and iPad, against `MockHome`:
/// Resume from `playback_state` and Play from start, a chapter opening the player at its time,
/// Favorite and Mark watched sent to the home and shown again after a reload, a 404's "not
/// available", the poster at the top at its own shape (the small copy on a phone, the full one
/// on an iPad; a placeholder of the same size on a 404), and `maktaba://video/{id}`.
///
/// Where the player (`PlayerRoute.watch`, 48.23) opens is read from the stream session it asks
/// the home for (`POST /api/stream/sessions`: `start_sec`, left out at 0).
///
/// `v2` in the fixtures: "السيرة النبوية، الدرس ١١", 2980 s, stopped at 1240 s (20:40), episode
/// 11 of Seerah, not a favorite; its chapters start at 0, 420, 1500 and 2600 s. The mock's
/// posters are a 32 × 48 PNG: portrait, so "at its own shape" is 2:3, not 16:9.
final class PlayerVideoUITests: PlayerUITestCase {
    private let title = "السيرة النبوية، الدرس ١١"

    /// The page of `id` on Home's stack (Home stands in for any section with routes).
    private func openPage(_ id: String = "v2", _ language: String = "en") {
        launchSignedIn(language, sections: ["home"], open: "video/\(id)")
        XCTAssertTrue(element("screen.video").waitForExistence(timeout: 15), "the video's page")
    }

    private func back() {
        app.navigationBars.buttons.firstMatch.tap()
    }

    /// The player opened `video` at `start` seconds (the last stream session asked for).
    private func playerOpened(_ video: String, at start: Int, _ why: String) {
        XCTAssertTrue(element("screen.watch").waitForExistence(timeout: 10), "the player")
        var last: [String: Any]?
        waitUntil("a stream session") {
            last = home.requests.last { $0.method == "POST" && $0.path == "/api/stream/sessions" }
                .flatMap { try? JSONSerialization.jsonObject(with: $0.body) as? [String: Any] }
            return last != nil
        }
        XCTAssertEqual(last?["video_id"] as? String, video)
        XCTAssertEqual(last?["start_sec"] as? Int ?? 0, start, why)
    }

    /// Close on the player's controls (they show on a tap) back to the page.
    private func closePlayer() {
        let close = app.buttons["player.close"]
        if !close.waitForExistence(timeout: 3) {
            element("screen.watch").coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.15)).tap()
        }
        XCTAssertTrue(close.waitForExistence(timeout: 5), "the player's Close")
        close.tap()
        XCTAssertTrue(element("screen.video").waitForExistence(timeout: 10), "back on the page")
    }

    private func count(_ method: String, _ path: String) -> Int {
        home.seen.filter { $0 == "\(method) \(path)" }.count
    }

    /// The last `POST path`'s JSON body.
    private func lastBody(_ path: String) -> [String: Any]? {
        guard let r = home.requests.last(where: { $0.method == "POST" && $0.path == path }) else { return nil }
        return (try? JSONSerialization.jsonObject(with: r.body)) as? [String: Any]
    }

    private func waitUntil(_ what: String, timeout: TimeInterval = 10, _ condition: () -> Bool) {
        let end = Date().addingTimeInterval(timeout)
        while !condition(), Date() < end { RunLoop.current.run(until: Date().addingTimeInterval(0.2)) }
        XCTAssertTrue(condition(), what)
    }

    private func scrollTo(_ e: XCUIElement) {
        var tries = 0
        while !(e.exists && e.isHittable), tries < 8 {
            app.swipeUp(velocity: .slow)
            tries += 1
        }
    }

    // MARK: Resume, Play from start, Play

    func testResumeShowsThePositionFromPlaybackStateAndPlayFromStartOpensAtZero() {
        openPage()
        let resume = app.buttons["video.resume"]
        XCTAssertTrue(resume.waitForExistence(timeout: 10), "a started video offers Resume")
        XCTAssertEqual(resume.label, "Resume from 20:40", "the position from playback_state")
        XCTAssertTrue(element("progress.strip").exists, "how far it was watched")
        resume.tap()
        playerOpened("v2", at: 1240, "the player opens at the saved position")

        closePlayer()
        let fromStart = app.buttons["video.playFromStart"]
        XCTAssertTrue(fromStart.waitForExistence(timeout: 5))
        fromStart.tap()
        playerOpened("v2", at: 0, "Play from start opens at 0")
    }

    func testAFinishedVideoOffersPlay() {
        openPage("v1")
        XCTAssertTrue(app.buttons["video.play"].waitForExistence(timeout: 10), "v1 was finished: Play")
        XCTAssertFalse(app.buttons["video.resume"].exists)
        XCTAssertTrue(app.buttons["video.watched"].isSelected, "and it is marked watched")
    }

    // MARK: the page's parts

    func testThePageShowsTheSeriesTitleFactsAndNextEpisodes() {
        openPage()
        XCTAssertTrue(element("video.title").waitForExistence(timeout: 10))
        XCTAssertEqual(element("video.title").label, title)
        XCTAssertTrue(element("video.series").label.contains("Seerah · Season 1, Episode 11"), element("video.series").label)
        XCTAssertTrue(element("video.facts").label.hasPrefix("49:40 · "), element("video.facts").label)
        XCTAssertTrue(app.staticTexts["الدرس الحادي عشر من سلسلة السيرة النبوية."].exists, "the description")
        for path in ["/api/videos/v2", "/api/me/videos/v2", "/api/videos/v2/chapters", "/api/videos/v2/segments",
                     "/api/series/s-seerah/episodes"] {
            XCTAssertTrue(home.seen.contains("GET \(path)"), "asked \(path)")
        }
        let next = element("video.next")
        scrollTo(next)
        XCTAssertTrue(next.exists, "the next episodes")
        // only what follows episode 11: episode 12, not 10
        XCTAssertTrue(next.buttons["السيرة النبوية، الدرس ١٢, Season 1, Episode 12"].exists, "episode 12 follows")
        XCTAssertFalse(next.buttons["السيرة النبوية، الدرس ١٠, Season 1, Episode 10"].exists, "episode 10 does not")
    }

    func testAChapterOpensThePlayerAtItsTime() {
        openPage()
        let chapter = app.buttons["video.chapter.1"]
        XCTAssertTrue(chapter.waitForExistence(timeout: 10), "the chapters")
        scrollTo(chapter)
        XCTAssertTrue(chapter.label.contains("Hijrah to Madinah"), chapter.label)
        chapter.tap()
        playerOpened("v2", at: 420, "the chapter's time")
    }

    // MARK: Favorite and Mark watched

    /// Each toggle is sent to the home; after a reload (away to the player and back reads the
    /// home again) the page shows what the home now says. The mock home keeps no state, so the
    /// test gives it the answer a real home gives after each mark.
    func testFavoriteAndMarkWatchedToggleAndPersistOnReload() {
        openPage()
        let favorite = app.buttons["video.favorite"]
        let watched = app.buttons["video.watched"]
        XCTAssertTrue(favorite.waitForExistence(timeout: 10))
        XCTAssertFalse(favorite.isSelected)
        XCTAssertFalse(watched.isSelected, watched.debugDescription)

        favorite.tap()
        waitUntil("favorite sent") { lastBody("/api/me/favorites") != nil }
        let fav = lastBody("/api/me/favorites")
        XCTAssertEqual(fav?["video_ids"] as? [String], ["v2"])
        XCTAssertEqual(fav?["favorite"] as? Bool, true)
        XCTAssertTrue(favorite.isSelected)

        watched.tap()
        waitUntil("watched sent") { lastBody("/api/me/watched") != nil }
        let mark = lastBody("/api/me/watched")
        XCTAssertEqual(mark?["video_ids"] as? [String], ["v2"])
        XCTAssertEqual(mark?["watched"] as? Bool, true)
        XCTAssertTrue(watched.isSelected)
        // watched: the home starts it over, so Play, not Resume
        XCTAssertTrue(app.buttons["video.play"].waitForExistence(timeout: 5))
        XCTAssertFalse(app.buttons["video.resume"].exists)

        app.buttons["video.play"].tap()
        playerOpened("v2", at: 0, "Play after Mark watched starts over")
        // what the home answers from now on (once the player has its video)
        home.stub("GET", "/api/me/videos/v2", body: #"{"video_id":"v2","position_sec":0,"completed":true,"favorite":true,"playlists":[],"series":{"id":"s-seerah","name":"Seerah"}}"#)
        home.stub("GET", "/api/videos/v2", body: #"{"id":"v2","filename":"seerah-11.mp4","title":"\#(title)","duration_sec":2980,"playback_state":{"position_sec":0,"completed":true}}"#)
        let reads = count("GET", "/api/me/videos/v2")
        closePlayer()
        waitUntil("the page read the home again") { count("GET", "/api/me/videos/v2") > reads }
        XCTAssertTrue(favorite.waitForExistence(timeout: 5))
        waitUntil("still a favorite") { favorite.isSelected }
        XCTAssertTrue(watched.isSelected, "still watched")
        XCTAssertTrue(app.buttons["video.play"].exists)

        // and off again
        favorite.tap()
        waitUntil("unfavorite sent") { (lastBody("/api/me/favorites")?["favorite"] as? Bool) == false }
        XCTAssertFalse(favorite.isSelected)
    }

    func testAMarkTheHomeRefusesGoesBack() {
        home.stub("POST", "/api/me/favorites", status: 500, body: #"{"title":"boom","status":500}"#)
        openPage()
        let favorite = app.buttons["video.favorite"]
        XCTAssertTrue(favorite.waitForExistence(timeout: 10))
        favorite.tap()
        XCTAssertTrue(app.alerts["Couldn't save that. Try again."].waitForExistence(timeout: 10))
        XCTAssertFalse(favorite.isSelected, "the toggle went back")
    }

    // MARK: not available

    func testA404SaysNotAvailable() {
        openPage("hidden")
        XCTAssertTrue(element("video.notAvailable").waitForExistence(timeout: 10))
        XCTAssertTrue(app.staticTexts["This video isn't available"].exists)
        XCTAssertTrue(home.seen.contains("GET /api/videos/hidden"))
        shot("video-not-available")
    }

    func testNotAvailableInArabic() {
        openPage("hidden", "ar")
        XCTAssertTrue(element("video.notAvailable").waitForExistence(timeout: 10))
        XCTAssertTrue(app.staticTexts["هذا الفيديو غير متاح"].exists)
        shot("video-not-available")
    }

    // MARK: the poster

    /// At the top, above the title; at its own shape (the mock's poster is portrait, 2:3, and
    /// is not cropped to 16:9); the small copy on a phone, the full one on an iPad.
    func testThePosterIsAtTheTopAtItsOwnShapeAndSize() {
        openPage()
        let poster = element("video.poster")
        XCTAssertTrue(poster.waitForExistence(timeout: 10), "the poster shows")
        XCTAssertEqual(poster.label, title)
        let title = element("video.title")
        XCTAssertLessThanOrEqual(poster.frame.maxY, title.frame.minY, "the poster is above the title")
        XCTAssertEqual(poster.frame.width / poster.frame.height, 32.0 / 48.0, accuracy: 0.03, "its own shape: \(poster.frame)")
        XCTAssertEqual(poster.frame.midX, app.windows.firstMatch.frame.midX, accuracy: 30, "centred")
        let asked = home.requests.filter { $0.path == "/api/videos/v2/poster" }
        XCTAssertFalse(asked.isEmpty, "the poster was asked")
        if isPad {
            XCTAssertTrue(asked.allSatisfy { $0.value("size") == nil }, "the full poster on an iPad")
        } else {
            XCTAssertTrue(asked.allSatisfy { $0.value("size") == "small" }, "the small copy on a phone")
        }
        shot("video")
        audit()
    }

    func testThePosterInArabic() {
        openPage("v2", "ar")
        XCTAssertTrue(element("video.poster").waitForExistence(timeout: 10))
        let resume = app.buttons["video.resume"]
        XCTAssertTrue(resume.exists)
        XCTAssertTrue(resume.label.hasPrefix("استئناف من "), resume.label)
        // right to left: the buttons start from the right
        XCTAssertGreaterThan(resume.frame.maxX, app.windows.firstMatch.frame.width * 0.6, "Resume leads from the right")
        XCTAssertTrue(app.buttons["المفضلة"].exists, "Favorite in Arabic")
        shot("video")
    }

    /// No poster (404): a placeholder of the same size (16:9) with the title on the brand
    /// colour, at the top.
    func testWithNoPosterAPlaceholderOfTheSameSizeShowsTheTitle() {
        home.stub("GET", "/api/videos/v2/poster", status: 404, body: #"{"title":"not found","status":404}"#)
        for language in ["en", "ar"] {
            openPage("v2", language)
            let missing = element("video.poster.missing")
            XCTAssertTrue(missing.waitForExistence(timeout: 10), "the placeholder (\(language))")
            XCTAssertEqual(missing.label, title)
            XCTAssertEqual(missing.frame.width / missing.frame.height, 16.0 / 9.0, accuracy: 0.03, "16:9: \(missing.frame)")
            XCTAssertLessThanOrEqual(missing.frame.maxY, element("video.title").frame.minY)
            XCTAssertFalse(element("video.poster").exists)
            shot("video-no-poster")
            app.terminate()
        }
    }

    // MARK: deep links

    /// `maktaba://video/{id}` opens the page on the section in view.
    func testADeepLinkOpensTheVideoPage() throws {
        launchSignedIn(sections: ["home"])
        XCTAssertTrue(element("screen.home").waitForExistence(timeout: 15))
        try openLink("maktaba://video/v2")
        XCTAssertTrue(element("screen.video").waitForExistence(timeout: 10), "the link opens the page")
        XCTAssertTrue(element("video.title").waitForExistence(timeout: 10))
        XCTAssertEqual(element("video.title").label, title)
        back()
        XCTAssertTrue(element("screen.home").waitForExistence(timeout: 5), "back to where the link found the app")
    }

    /// Over Libraries, whose browser keeps its own stack: the page opens in a sheet with Done.
    func testADeepLinkOverLibrariesOpensInASheet() throws {
        launchSignedIn(sections: ["libraries"])
        XCTAssertTrue(element("library.lib-docs").waitForExistence(timeout: 15), "Libraries")
        try openLink("maktaba://video/v2")
        XCTAssertTrue(element("video.title").waitForExistence(timeout: 10), "the linked page")
        app.buttons["link.done"].tap()
        XCTAssertTrue(element("video.title").waitForNonExistence(timeout: 5), "Done closes it")
    }

    /// A video the person may not see (the home answers 404) says "not available".
    func testADeepLinkToAVideoTheUserMayNotSeeSaysNotAvailable() throws {
        launchSignedIn(sections: ["home"])
        XCTAssertTrue(element("screen.home").waitForExistence(timeout: 15))
        try openLink("maktaba://video/hidden")
        XCTAssertTrue(element("video.notAvailable").waitForExistence(timeout: 10))
        XCTAssertTrue(app.staticTexts["This video isn't available"].exists)
    }

    // MARK: helpers

    /// Opens `link` as another app would: the system hands it to the running app (which keeps
    /// its sign-in; `XCUIApplication.open` relaunches it, and an unsigned test build keeps no
    /// token), confirming the system's "Open in …?" prompt when it asks.
    private func openLink(_ link: String) throws {
        XCUIDevice.shared.system.open(try XCTUnwrap(URL(string: link)))
        let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        let confirm = springboard.buttons["Open"]
        if confirm.waitForExistence(timeout: 3) { confirm.tap() }
    }

    /// NFR-6 on the page as it shows: labels, contrast, hit regions, clipped text.
    private func audit() {
        let window = app.windows.firstMatch.frame
        do {
            try app.performAccessibilityAudit { issue in
                // a progress bar is a reading, not a control: no 44-pt target
                if issue.auditType == .hitRegion, issue.element?.identifier == "progress.strip" { return true }
                // an element partly off screen: the audit samples pixels beyond it
                if issue.auditType == .contrast, let f = issue.element?.frame, !window.contains(f) { return true }
                print("audit issue: \(issue.compactDescription) on \(issue.element?.debugDescription ?? "?")")
                return false
            }
        } catch {
            XCTFail("audit: \(error)")
        }
    }
}
