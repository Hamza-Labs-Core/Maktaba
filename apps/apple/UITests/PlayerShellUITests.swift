import XCTest

/// The Player's shell (Epic 48 story 48.13) on iPhone and iPad, against `MockHome`:
/// - only the sections turned on show (today Libraries alone, story 48.18's screens, no tab
///   bar);
/// - all five sections: a bottom tab bar on an iPhone, tabs at the top on an iPad (they
///   become the sidebar there), right to left in Arabic;
/// - every route opens its screen; choosing the chosen section again pops its stack.
final class PlayerShellUITests: PlayerUITestCase {
    private let sections = ["Home", "Libraries", "Live TV", "Search", "Profile"]
    private let arabic = ["الرئيسية", "المكتبات", "البث المباشر", "البحث", "الملف الشخصي"]
    /// A library card of `Fixtures/libraries-summary.json` (the Libraries root, story 48.18).
    private let libraryCard = "library.lib-docs"

    /// A TestFlight build shows `PlayerSection.enabled`: Home (story 48.17) first, then
    /// Libraries (48.18) reading the home's library cards, Live TV (48.21, the mock has
    /// channels), Search (48.20) and Profile (48.22).
    func testOnlyTheEnabledSectionsShow() {
        launchSignedIn()
        XCTAssertTrue(element("screen.home").waitForExistence(timeout: 15), "Home, the first section")
        XCTAssertTrue(element("player.shell").exists, "more than one section: the tab bar")
        for label in ["Search", "Live TV", "Profile"] {
            XCTAssertTrue(sectionButton(label).exists, "\(label) is turned on")
        }
        sectionButton("Libraries").tap()
        XCTAssertTrue(element(libraryCard).waitForExistence(timeout: 15), "the Libraries root")
        XCTAssertTrue(home.seen.contains("GET /api/libraries/summary"))
        shot("enabled-sections")
    }

    /// Every section shown: a tab bar at the bottom of an iPhone, tabs at the top of an iPad;
    /// each opens its screen.
    func testFiveSectionsAdaptToTheDevice() {
        launchSignedIn(sections: ["all"])
        let homeTab = sectionButton("Home")
        XCTAssertTrue(homeTab.waitForExistence(timeout: 15), "the sections")
        XCTAssertTrue(element("screen.home").waitForExistence(timeout: 5), "the first section is chosen")
        let height = app.windows.firstMatch.frame.height
        for label in sections {
            let b = sectionButton(label)
            XCTAssertTrue(b.exists, "\(label) shows")
            if isPad {
                XCTAssertLessThan(b.frame.midY, height * 0.25, "\(label): tabs at the top of an iPad")
            } else {
                XCTAssertGreaterThan(b.frame.midY, height * 0.8, "\(label): the tab bar at the bottom of an iPhone")
            }
        }
        shot("sections")

        for (label, id) in [("Live TV", "screen.liveTV"), ("Search", "screen.search"), ("Profile", "screen.profile")] {
            sectionButton(label).tap()
            XCTAssertTrue(element(id).waitForExistence(timeout: 5), "\(label) opens its screen")
        }
        sectionButton("Libraries").tap()
        XCTAssertTrue(element(libraryCard).waitForExistence(timeout: 10), "Libraries shows its cards")
        shot("sections-libraries")
    }

    /// Choosing the section already chosen takes it back to its first screen (FR-6).
    func testChoosingTheChosenSectionPopsItsStack() {
        launchSignedIn(sections: ["all"], section: "home", open: "video/v1")
        XCTAssertTrue(element("screen.video").waitForExistence(timeout: 15), "the pushed route")
        // the route's id reaches its screen (48.19's page: v1's title)
        XCTAssertTrue(element("video.title").waitForExistence(timeout: 10), "the route's id reaches its screen")
        sectionButton("Home").tap()
        XCTAssertTrue(element("screen.home").waitForExistence(timeout: 5), "back to Home's first screen")
        XCTAssertFalse(element("screen.video").exists)
    }

    /// Each route opens its own screen through the one `playerDestinations`.
    func testEveryRouteOpensItsScreen() {
        launchSignedIn(sections: ["all"], section: "home", open: "series/s-seerah")
        XCTAssertTrue(element("screen.series").waitForExistence(timeout: 15))
        let routes = [("video/v2", "screen.video"), ("library/lib-docs", "screen.library"),
                      ("channel/ch-1", "screen.channel"), ("watch/v3@90", "screen.watch")]
        for (route, id) in routes {
            // a fresh run each time: an unsigned test build keeps no token in the Keychain
            launchSignedIn(sections: ["all"], section: "search", open: route)
            XCTAssertTrue(element(id).waitForExistence(timeout: 15), "\(route) opens \(id)")
        }
        // the player's route carries its time: the stream opens at 90 s (story 48.23)
        let opened = { self.home.requests.contains { r in
            r.method == "POST" && r.path == "/api/stream/sessions"
                && (try? JSONSerialization.jsonObject(with: r.body) as? [String: Any]).map { $0["video_id"] as? String == "v3" && $0["start_sec"] as? Int == 90 } == true
        } }
        let end = Date().addingTimeInterval(15)
        while !opened(), Date() < end { RunLoop.current.run(until: Date().addingTimeInterval(0.25)) }
        XCTAssertTrue(opened(), "the player's route carries its time")
        shot("route-watch")
    }

    /// One section on its own that is not Libraries (no tab bar) still has its stack: a route
    /// opens on it, as it will when a screen story's section is the only one tested.
    func testASingleSectionStillOpensRoutes() {
        launchSignedIn(sections: ["home"], open: "video/v1")
        XCTAssertTrue(element("screen.video").waitForExistence(timeout: 15), "the route on Home's own stack")
        XCTAssertFalse(element("player.shell").exists, "one section: no tab bar")
        app.navigationBars.buttons.firstMatch.tap()
        XCTAssertTrue(element("screen.home").waitForExistence(timeout: 5), "back to Home")
    }

    /// Arabic: the sections' names from the web's ar.json, laid out right to left.
    func testSectionsInArabic() {
        launchSignedIn("ar", sections: ["all"])
        let first = sectionButton(arabic[0])
        XCTAssertTrue(first.waitForExistence(timeout: 15), "الرئيسية")
        for label in arabic { XCTAssertTrue(sectionButton(label).exists, label) }
        // right to left: Home leads from the right, Libraries after it
        XCTAssertGreaterThan(first.frame.midX, sectionButton(arabic[1]).frame.midX, "Home is right of Libraries")
        XCTAssertTrue(app.staticTexts["تابع المشاهدة"].waitForExistence(timeout: 10), "Home's rows in Arabic")
        shot("sections")
    }
}
