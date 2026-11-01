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

    /// A TestFlight build shows `PlayerSection.enabled` only: Libraries, reading the home's
    /// library cards; no section that has not merged.
    func testOnlyTheEnabledSectionsShow() {
        launchSignedIn()
        XCTAssertTrue(element(libraryCard).waitForExistence(timeout: 15), "the Libraries root")
        XCTAssertTrue(home.seen.contains("GET /api/libraries/summary"))
        for label in ["Home", "Live TV", "Profile"] {
            XCTAssertFalse(app.tabBars.buttons[label].exists, "\(label) is not turned on")
            XCTAssertFalse(app.buttons[label].exists, "\(label) is not turned on")
        }
        XCTAssertFalse(element("player.shell").exists, "one section: no tab bar")
        shot("libraries-only")
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
        XCTAssertTrue(app.staticTexts["v1"].exists, "the route's id reaches its screen")
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
        XCTAssertTrue(app.staticTexts["v3 @ 90"].exists, "the player's route carries its time")
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
        XCTAssertTrue(app.staticTexts["هذه الشاشة قادمة قريبًا."].exists, "the placeholder in Arabic")
        shot("sections")
    }
}
