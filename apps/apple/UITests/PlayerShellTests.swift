import XCTest

/// The shell's plain types (compiled into the Player's UI-test targets as well, see
/// project.yml): the sections turned on, the routes' paths and the launch arguments the UI
/// tests use. No app is launched.
final class PlayerShellTests: XCTestCase {
    /// TestFlight uploads every tag: only Libraries (today's screen) is on until the other
    /// screens merge, each turning its own section on.
    func testOnlyLibrariesIsTurnedOn() {
        XCTAssertEqual(PlayerSection.enabled, [.libraries])
        XCTAssertEqual(PlayerSection.visible(arguments: []), [.libraries])
    }

    func testTheFiveSectionsInTheirOrder() {
        XCTAssertEqual(PlayerSection.allCases, [.home, .libraries, .liveTV, .search, .profile])
        XCTAssertEqual(Set(PlayerSection.allCases.map(\.systemImage)).count, 5)
    }

    /// A Debug build (the UI tests) may show sections not turned on yet; order is kept and
    /// unknown names are ignored.
    func testDebugLaunchArgumentsChooseTheSections() {
        XCTAssertEqual(PlayerSection.visible(arguments: ["-maktaba-sections", "all"]), PlayerSection.allCases)
        XCTAssertEqual(PlayerSection.visible(arguments: ["-maktaba-sections", "profile,home,nope"]), [.home, .profile])
        XCTAssertEqual(PlayerSection.visible(arguments: ["-maktaba-sections", "nope"]), [.libraries], "never no section")
        XCTAssertEqual(ShellLaunch.section(arguments: ["-maktaba-section", "live"]), .liveTV)
        XCTAssertNil(ShellLaunch.section(arguments: ["-maktaba-section"]))
        XCTAssertEqual(ShellLaunch.route(arguments: ["-maktaba-open", "watch/v1@90"]), .watch(videoID: "v1", at: 90))
    }

    func testRoutesRoundTripThroughTheirPaths() {
        let routes: [PlayerRoute] = [.video(id: "v1"), .series(id: "s-seerah"), .library(id: "lib-docs"),
                                     .channel(id: "ch-1"), .watch(videoID: "v1"), .watch(videoID: "v1", at: 90),
                                     .watch(videoID: "v1", at: 12.5)]
        for r in routes {
            XCTAssertEqual(PlayerRoute(path: r.path), r, r.path)
        }
        XCTAssertEqual(PlayerRoute.watch(videoID: "v1", at: 3600).path, "watch/v1@3600")
    }

    func testABadPathIsNoRoute() {
        for bad in ["", "video", "video/", "video/a/b", "movie/v1", "watch/@9", "watch/v1@-3", "watch/v1@soon"] {
            XCTAssertNil(PlayerRoute(path: bad), bad)
        }
    }

    func testRoutesSurviveCoding() throws {
        let path: [PlayerRoute] = [.library(id: "lib-docs"), .series(id: "s-seerah"), .watch(videoID: "v4", at: 30)]
        let back = try JSONDecoder().decode([PlayerRoute].self, from: JSONEncoder().encode(path))
        XCTAssertEqual(back, path)
    }
}
