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

    /// Live TV off on the home (story 51.11): a viewer has no section even with channels; an
    /// admin keeps it (its screen says it is off), whatever the channels say.
    func testLiveTVOffHidesItFromAViewerOnly() {
        let all = PlayerSection.allCases
        let viewer = PlayerSection.shown(all, hasChannels: true, liveTVOff: true, admin: false)
        XCTAssertEqual(viewer, [.home, .libraries, .search, .profile])
        XCTAssertEqual(PlayerSection.shown(all, hasChannels: nil, liveTVOff: true, admin: true), all)
        XCTAssertEqual(PlayerSection.shown(all, hasChannels: false, liveTVOff: true, admin: true), all)
        XCTAssertEqual(PlayerSection.shown(all, hasChannels: true, liveTVOff: false, admin: false), all)
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

    /// #639: the touch hook, like the text-size one, does nothing without its flag.
    func testTheTouchHookIsInertWithoutItsFlag() {
        XCTAssertFalse(TouchHook.start(arguments: []))
        XCTAssertFalse(TouchHook.start(arguments: ["-maktaba-section", "home", TextSizeHook.flag]))
        XCTAssertTrue(TouchHook.start(arguments: ["-AppleLanguages", "(en)", TouchHook.flag]))
    }

    /// Each count the app can answer has a name of its own that reads back as that count
    /// (a Darwin notification carries no data), "more" above the highest; the question is
    /// none of the answers.
    func testTheTouchHooksAnswersNameEachCount() {
        XCTAssertEqual(TouchHook.answers.count, TouchHook.most + 2)
        XCTAssertEqual(Set(TouchHook.answers).count, TouchHook.answers.count, "one name per answer")
        for n in 0 ... TouchHook.most {
            XCTAssertEqual(TouchHook.count(in: TouchHook.answer(n)), n)
        }
        XCTAssertEqual(TouchHook.answer(TouchHook.most + 5), TouchHook.answerPrefix + "more")
        XCTAssertEqual(TouchHook.count(in: TouchHook.answer(TouchHook.most + 5)), TouchHook.most + 1)
        XCTAssertTrue(TouchHook.answers.contains(TouchHook.answer(TouchHook.most + 1)))
        XCTAssertNil(TouchHook.count(in: TouchHook.ask))
        XCTAssertFalse(TouchHook.answers.contains(TouchHook.ask))
    }

    /// The watcher never changes what a tap does: it does not delay or cancel a view's
    /// touches, runs beside every recognizer, neither stops one nor is stopped; a window gets
    /// one, however often it shows.
    @MainActor
    func testTheTouchWatcherOnlyWatches() {
        let watcher = TouchWatcher()
        XCTAssertFalse(watcher.cancelsTouchesInView)
        XCTAssertFalse(watcher.delaysTouchesBegan)
        XCTAssertFalse(watcher.delaysTouchesEnded)
        let other = UITapGestureRecognizer()
        XCTAssertFalse(watcher.canPrevent(other))
        XCTAssertFalse(watcher.canBePrevented(by: other))
        XCTAssertEqual(watcher.delegate?.gestureRecognizer?(watcher, shouldRecognizeSimultaneouslyWith: other), true)

        let window = UIWindow(frame: CGRect(x: 0, y: 0, width: 100, height: 100))
        XCTAssertTrue(TouchHook.watch(window))
        XCTAssertFalse(TouchHook.watch(window), "a window already watched")
        XCTAssertEqual(window.gestureRecognizers?.filter { $0 is TouchWatcher }.count, 1)
    }

    /// A touch the system cancels is counted. (Its `.failed` state isn't checked: outside a
    /// real touch sequence UIKit resets the recognizer to `.possible` at once.)
    @MainActor
    func testACancelledTouchIsCounted() {
        let view = UIView(frame: CGRect(x: 0, y: 0, width: 100, height: 100))
        let watcher = TouchWatcher()
        view.addGestureRecognizer(watcher)
        let before = TouchHook.cancelled
        watcher.touchesCancelled([UITouch()], with: UIEvent())
        XCTAssertEqual(TouchHook.cancelled, before + 1)
        watcher.touchesCancelled([UITouch(), UITouch()], with: UIEvent())
        XCTAssertEqual(TouchHook.cancelled, before + 3, "each cancelled touch counts")
    }

    /// A failure after a lost tap starts with it, the count in words; with none, or no app to
    /// ask, the failure is left as it is.
    @MainActor
    func testAFailureAfterALostTapSaysSo() {
        XCTAssertTrue(PlayerUITestCase.lostTapsNote(1).contains("cancelled 1 touch of this test"))
        XCTAssertTrue(PlayerUITestCase.lostTapsNote(3).contains("cancelled 3 touches"))
        XCTAssertTrue(PlayerUITestCase.lostTapsNote(TouchHook.most + 1).contains("more than \(TouchHook.most) touches"))
        XCTAssertTrue(PlayerUITestCase.lostTapsNote(1).contains("#639"))
        let issue = XCTIssue(type: .assertionFailure, compactDescription: "the row opened")
        XCTAssertEqual(PlayerUITestCase.withLostTaps(issue, cancelled: nil).compactDescription, "the row opened")
        XCTAssertEqual(PlayerUITestCase.withLostTaps(issue, cancelled: 0).compactDescription, "the row opened")
        XCTAssertEqual(PlayerUITestCase.withLostTaps(issue, cancelled: 2).compactDescription,
                       PlayerUITestCase.lostTapsNote(2) + "the row opened")
        XCTAssertNil(PlayerUITestCase.cancelledTouches(in: nil), "no app, no answer")
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
