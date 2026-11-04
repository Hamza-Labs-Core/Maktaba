import XCTest

/// The shell's plain types (compiled into the Player's UI-test targets as well, see
/// project.yml): the sections turned on, the routes' paths and the launch arguments the UI
/// tests use. No app is launched.
final class PlayerShellTests: XCTestCase {
    /// TestFlight uploads every tag: every section is on now that
    /// each screen merged (Home 48.17, Libraries 48.18, Live TV 48.21, Search 48.20, Profile 48.22).
    func testOnlyTheMergedSectionsAreTurnedOn() {
        XCTAssertEqual(PlayerSection.enabled, PlayerSection.allCases)
        XCTAssertEqual(PlayerSection.visible(arguments: []), PlayerSection.allCases)
    }

    /// Live TV only when the person may watch a channel (story 48.21): not while the home has
    /// not said, not with none, and never leaving no section.
    func testLiveTVShowsOnlyWithAChannel() {
        let all = PlayerSection.allCases
        XCTAssertEqual(PlayerSection.shown(all, hasChannels: true), all)
        XCTAssertEqual(PlayerSection.shown(all, hasChannels: false), [.home, .libraries, .search, .profile])
        XCTAssertEqual(PlayerSection.shown([.libraries, .liveTV], hasChannels: nil), [.libraries])
        XCTAssertEqual(PlayerSection.shown([.liveTV], hasChannels: false), [.liveTV], "never no section")
        XCTAssertEqual(PlayerSection.shown([.libraries], hasChannels: true), [.libraries])
    }

    /// The Dynamic Type sizes a UI test asks the running app for (`setTextSize`, answered by
    /// the app's `TextSizeHook`): each one the hook knows, at the size it names; the default
    /// is the system's own setting (no override).
    func testTheTextSizesATestAsksForAreTheHooks() {
        let asked: [PlayerUITestCase.TextSize] = [.smallest, .standard, .ax3]
        XCTAssertEqual(asked.map(\.rawValue), TextSizeHook.sizes)
        XCTAssertEqual(TextSizeHook.category("XS"), .extraSmall)
        XCTAssertEqual(TextSizeHook.category("AX3"), .accessibilityExtraExtraExtraLarge)
        XCTAssertNil(TextSizeHook.category("default"))
        XCTAssertNotEqual(TextSizeHook.ask, TextSizeHook.applied, "the answer is not the question")
    }

    /// Without its launch flag the hook does nothing (a Debug build run by hand, or a UI test
    /// that does not ask for it): it listens only when the app is launched with the flag.
    func testTheTextSizeHookIsInertWithoutItsFlag() {
        XCTAssertFalse(TextSizeHook.start(arguments: []))
        XCTAssertFalse(TextSizeHook.start(arguments: ["-maktaba-section", "home"]))
        XCTAssertTrue(TextSizeHook.start(arguments: ["-AppleLanguages", "(en)", TextSizeHook.flag]))
    }

    /// A size the hook sets on a scene reaches its views while they show: the trait a view
    /// reads, and a label's Dynamic Type font, smaller at XS and larger at AX3; the default
    /// takes the override off again. A size the tests do not use changes nothing.
    @MainActor
    func testTheTextSizeHookReachesTheViews() throws {
        let scene = try XCTUnwrap(UIApplication.shared.connectedScenes.compactMap { $0 as? UIWindowScene }.first,
                                  "the test runner's scene")
        let before = scene.traitCollection.preferredContentSizeCategory
        defer { TextSizeHook.set("default", on: [scene]) }
        let window = UIWindow(windowScene: scene)
        let host = UIViewController()
        let label = UILabel()
        label.font = .preferredFont(forTextStyle: .body)
        label.adjustsFontForContentSizeCategory = true
        label.text = "Maktaba"
        host.view.addSubview(label)
        window.rootViewController = host
        window.isHidden = false
        defer { window.isHidden = true }

        func shown(_ size: String) -> (UIContentSizeCategory, CGFloat) {
            XCTAssertTrue(TextSizeHook.set(size, on: [scene]), size)
            window.layoutIfNeeded()
            return (label.traitCollection.preferredContentSizeCategory, label.font.pointSize)
        }
        let (small, smallPoints) = shown("XS")
        XCTAssertEqual(small, .extraSmall)
        let (large, largePoints) = shown("AX3")
        XCTAssertEqual(large, .accessibilityExtraExtraExtraLarge)
        XCTAssertGreaterThan(largePoints, smallPoints, "the label's text grew")
        let (normal, normalPoints) = shown("default")
        XCTAssertEqual(normal, before, "the default: the system's own size again")
        XCTAssertGreaterThan(normalPoints, smallPoints)
        XCTAssertLessThan(normalPoints, largePoints)

        XCTAssertFalse(TextSizeHook.set("huge", on: [scene]), "an unknown size")
        XCTAssertEqual(label.traitCollection.preferredContentSizeCategory, before, "nothing changed")
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
        XCTAssertEqual(ShellLaunch.liveMode(arguments: ["-maktaba-live", "guide"]), "guide")
        XCTAssertNil(ShellLaunch.liveMode(arguments: ["-maktaba-section", "live"]))
        XCTAssertEqual(ShellLaunch.profilePage(arguments: ["-maktaba-profile", "sessions"]), "sessions")
        XCTAssertNil(ShellLaunch.profilePage(arguments: ["-maktaba-section", "profile"]))
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
