import AppKit
import XCTest

/// The Mac Player driven from outside (Epic 48 story 48.25), signed in to `MockHome`:
/// - View: each section on ⌘1–⌘5 (whichever are shown), Search… on ⌘F, Enter Full Screen on ⌃⌘F;
/// - Playback: its items off while nothing plays; Stop (⌘.) closes the player;
/// - the player fills the window, and the whole screen in full screen.
///
/// Screenshots (dark, English and Arabic) are attachments, and files in `MAKTABA_SHOTS` when set.
@MainActor
final class PlayerMacMenuUITests: XCTestCase {
    private var app: XCUIApplication!
    private var home: MockHome!
    private var language = "en"

    override func setUp() async throws {
        continueAfterFailure = false
        home = try MockHome()
        try home.start()
        app = XCUIApplication()
    }

    override func tearDown() async throws {
        if app?.state == .runningForeground || app?.state == .runningBackground {
            // never leave a full-screen space behind
            if app.windows.firstMatch.exists, isFullScreen { app.typeKey("f", modifierFlags: [.control, .command]) }
            app.terminate()
        }
        home?.stop()
    }

    // MARK: the View menu

    /// Every section shown: the View menu chooses each, by a click and by its number, in order.
    func testTheViewMenuChoosesEachSection() {
        launchSignedIn(sections: "all", section: "home")
        XCTAssertTrue(element("screen.home").waitForExistence(timeout: 15), "Home is chosen")
        // no window tabs: AppKit's Show Tab Bar / Show All Tabs would head the View menu as it
        // opens and leave it while open, moving every item under the pointer
        for tabs in ["Show Tab Bar", "Show All Tabs"] {
            XCTAssertFalse(app.menuBars.menuItems[tabs].exists, "View has no \(tabs)")
        }

        menu("View", "Live TV")
        XCTAssertTrue(element("screen.liveTV").waitForExistence(timeout: 5), "View ▸ Live TV")
        menu("View", "Profile")
        XCTAssertTrue(element("screen.profile").waitForExistence(timeout: 5), "View ▸ Profile")
        shot("view-menu")

        let screens = ["screen.home", "screen.libraries", "screen.liveTV", "screen.search", "screen.profile"]
        for (i, id) in screens.enumerated().reversed() {
            app.typeKey("\(i + 1)", modifierFlags: .command)
            XCTAssertTrue(element(id).waitForExistence(timeout: 5), "⌘\(i + 1) opens \(id)")
        }

        // ⌘F: from any section to Search
        app.typeKey("1", modifierFlags: .command)
        XCTAssertTrue(element("screen.home").waitForExistence(timeout: 5))
        app.typeKey("f", modifierFlags: .command)
        XCTAssertTrue(element("screen.search").waitForExistence(timeout: 5), "⌘F goes to Search")
        menu("View", "Home")
        XCTAssertTrue(element("screen.home").waitForExistence(timeout: 5))
        menu("View", "Search…")
        XCTAssertTrue(element("screen.search").waitForExistence(timeout: 5), "View ▸ Search… goes to Search")
    }

    /// Only some sections shown: they take ⌘1, ⌘2 … in their order; the others are not in the menu.
    func testTheNumbersFollowTheShownSections() {
        launchSignedIn(sections: "home,profile", section: "home")
        XCTAssertTrue(element("screen.home").waitForExistence(timeout: 15))
        app.typeKey("2", modifierFlags: .command)
        XCTAssertTrue(element("screen.profile").waitForExistence(timeout: 5), "⌘2 is the second shown section")
        app.menuBars.menuBarItems["View"].click()
        XCTAssertTrue(app.menuBars.menuItems["Home"].exists)
        XCTAssertTrue(app.menuBars.menuItems["Profile"].exists)
        for hidden in ["Libraries", "Live TV"] {
            XCTAssertFalse(app.menuBars.menuItems[hidden].exists, "\(hidden) is not shown")
        }
        app.typeKey(.escape, modifierFlags: [])
        app.typeKey("5", modifierFlags: .command)
        XCTAssertTrue(element("screen.profile").exists, "no fifth section: ⌘5 does nothing")
    }

    /// In Arabic the menus speak Arabic and the numbers still choose the sections.
    func testTheMenusInArabic() {
        launchSignedIn("ar", sections: "all", section: "home")
        XCTAssertTrue(element("screen.home").waitForExistence(timeout: 15))
        app.typeKey("5", modifierFlags: .command)
        XCTAssertTrue(element("screen.profile").waitForExistence(timeout: 5), "⌘5 in Arabic")
        XCTAssertTrue(app.menuBars.menuBarItems["التشغيل"].exists, "the Playback menu in Arabic")
        app.menuBars.menuBarItems["التشغيل"].click()
        XCTAssertTrue(app.menuBars.menuItems["تشغيل/إيقاف مؤقت"].exists)
        XCTAssertTrue(app.menuBars.menuItems["الحلقة التالية"].exists)
        shot("playback-menu")
        app.typeKey(.escape, modifierFlags: [])
    }

    // MARK: the player

    /// Playing: the player covers the whole window, then the whole screen on ⌃⌘F, and back;
    /// Playback ▸ Stop closes it. Its items are on while the player is open.
    func testThePlayerFillsTheWindowAndGoesFullScreen() throws {
        launchSignedIn(sections: "all", section: "home")
        // an On Deck card: a video not started, so its page offers Play
        let card = app.buttons["home.card.v3"].firstMatch
        XCTAssertTrue(card.waitForExistence(timeout: 15), "Home's On Deck row")
        reveal(card)
        card.click()
        XCTAssertTrue(element("screen.video").waitForExistence(timeout: 10), "the video's page")
        // the sidebar beside the page (the player must cover it)
        XCTAssertTrue(sidebar.waitForExistence(timeout: 5) && sidebar.isHittable, "the sidebar beside the page")
        let play = element("video.play")
        XCTAssertTrue(play.waitForExistence(timeout: 10), "the video's Play")
        play.click()
        // the player over the window, by its own name ("Close" also names the title bar's
        // close button, which stays once the player has gone)
        let player = element("player.window")
        XCTAssertTrue(player.waitForExistence(timeout: 10), "the player")

        app.menuBars.menuBarItems["Playback"].click()
        XCTAssertTrue(app.menuBars.menuItems["Stop"].isEnabled, "Stop while a video is open")
        // the watch screen makes its player as it opens, before a picture: Play/Pause is on
        XCTAssertTrue(app.menuBars.menuItems["Play/Pause"].isEnabled, "Play/Pause while the player is open")
        app.typeKey(.escape, modifierFlags: [])

        let window = app.windows.firstMatch
        assertPlayerCovers(window, "the window")
        shot("player-window")

        app.typeKey("f", modifierFlags: [.control, .command])
        XCTAssertTrue(wait { self.isFullScreen }, "⌃⌘F: full screen")
        assertPlayerCovers(window, "the screen")
        // In full screen the menu bar is hidden until the pointer reaches the screen's top: a
        // click on View before then opens nothing (CI: "open menu during menu traversal"). So
        // the pointer goes up first, as a person's would.
        window.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0)).hover()
        _ = wait(1) { false } // the bar slides down
        menu("View", "Exit Full Screen")
        XCTAssertTrue(wait { !self.isFullScreen }, "View ▸ Exit Full Screen")
        XCTAssertTrue(wait { self.app.menuBars.menuBarItems["View"].isHittable }, "the menu bar is back after full screen")

        menu("Playback", "Stop")
        XCTAssertTrue(player.waitForNonExistence(timeout: 5), "Playback ▸ Stop closes the player")
    }

    // MARK: helpers

    /// A first run (permissions passed, no home) in `language` with the dark theme, signed in
    /// to the mock home by a code.
    private func launchSignedIn(_ language: String = "en", sections: String? = nil, section: String? = nil) {
        self.language = language
        // AppKit reads arguments as `-key value` pairs: a flag with no value takes the next
        // one, and a word left over (`granted`) is opened as a document, so SwiftUI opens no
        // window. Xcode's own runs pass this too.
        // No window is restored from the last run either: one saved while full screen can come
        // back full screen, its menu bar hidden.
        var args = ["-NSTreatUnknownArgumentsAsOpen", "NO", "-ApplePersistenceIgnoreState", "YES",
                    "-AppleLanguages", "(\(language))", "-AppleLocale", language == "ar" ? "ar_JO" : "en_US",
                    "-theme", "dark",
                    "-maktaba-permissions-reset", "-maktaba-permissions-fake", "granted", "-maktaba-session-reset"]
        if let sections { args += ["-maktaba-sections", sections] }
        if let section { args += ["-maktaba-section", section] }
        app.launchArguments = args
        app.launch()
        app.activate()
        let walkthrough = element("permissions.walkthrough")
        if walkthrough.waitForExistence(timeout: 5) {
            app.buttons["permissions.continue"].click()
            XCTAssertTrue(walkthrough.waitForNonExistence(timeout: 5))
        }
        let field = app.textFields["picker.address"]
        XCTAssertTrue(field.waitForExistence(timeout: 10), "the home picker")
        field.typeExactly(home.url, in: app)
        app.buttons["picker.connect"].click()
        // the sign-in way is a segmented choice: on the Mac its segments are radio buttons
        let byCode = app.radioButtons[language == "ar" ? "برمز" : "With a code"]
        XCTAssertTrue(byCode.waitForExistence(timeout: 10), "the sign-in screen offers a code")
        byCode.click()
        home.approve()
        XCTAssertTrue(byCode.waitForNonExistence(timeout: 20), "approved: signed in")
    }

    private func element(_ id: String) -> XCUIElement { app.descendants(matching: .any)[id].firstMatch }

    /// Scrolls Home down until `element` is on screen (the On Deck row is below the window's fold).
    private func reveal(_ element: XCUIElement) {
        for _ in 0..<10 where !element.isHittable {
            self.element("screen.home").scroll(byDeltaX: 0, deltaY: -200)
        }
        XCTAssertTrue(element.isHittable, "scrolled to \(element)")
    }

    /// Clicks a menu bar item, then one of its items, as a pointer does.
    ///
    /// `XCUIElement.click()` on a menu item walks the menu itself: it clicks the menu bar item
    /// and waits for AppKit's "menu opened" notification, which the CI runner did not send for
    /// View on five runs running ("timed out while waiting for menu open notification"), and
    /// its own click on the bar item shut the menu this helper had opened. So the item is
    /// clicked at its point instead, once the open menu shows it (hittable); nothing waits on
    /// that notification.
    private func menu(_ top: String, _ item: String) {
        let bar = app.menuBars.menuBarItems[top]
        XCTAssertTrue(wait(10) { bar.isHittable }, "the menu bar shows \(top)")
        let it = app.menuBars.menuItems[item].firstMatch
        XCTAssertTrue(it.waitForExistence(timeout: 3), "\(top) ▸ \(item)")
        bar.click()
        if !wait(3, { it.isHittable }) { bar.click() } // a click that landed as the bar redrew
        XCTAssertTrue(wait(3) { it.isHittable }, "\(top) opens, showing \(item)")
        XCTAssertTrue(it.isEnabled, "\(top) ▸ \(item) is on")
        // Click the item's point from the bar, not from the item: a coordinate on the item finds
        // it again by its identifier, and every SwiftUI menu item's is "menuAction:", so the
        // click could land on another item.
        // The pointer goes there in a straight line from the bar item, and a pointer over another
        // bar item while a menu is open opens that one instead (to Home, View's first item, the
        // line from View crossed Playback and the click landed in the Playback menu). So the
        // point is straight below the bar item, in the item's row: the line stays on View and
        // its own menu.
        let at = it.frame, from = bar.frame
        let x = min(max(from.midX, at.minX + 4), at.maxX - 4)
        bar.coordinate(withNormalizedOffset: .zero)
            .withOffset(CGVector(dx: x - from.minX, dy: at.midY - from.minY)).click()
    }

    /// The window covers its screen (the menu bar hides in full screen).
    private var isFullScreen: Bool {
        guard let screen = NSScreen.main else { return false }
        let w = app.windows.firstMatch.frame
        return abs(w.width - screen.frame.width) < 2 && abs(w.height - screen.frame.height) < 2
    }

    private func wait(_ timeout: TimeInterval = 10, _ condition: @escaping () -> Bool) -> Bool {
        let end = Date.now.addingTimeInterval(timeout)
        while Date.now < end {
            if condition() { return true }
            RunLoop.current.run(until: .now.addingTimeInterval(0.25))
        }
        return condition()
    }

    /// The player covers the whole window: its frame is the window's, and the sidebar, seen
    /// beside the page before (`sidebar`), is under it and out of reach.
    ///
    /// Not by pixels: by the time this runs the mock home's stream is showing its picture (bars
    /// of colour, white at the right edge), so "black at every edge" fails on the video itself.
    private func assertPlayerCovers(_ window: XCUIElement, _ what: String) {
        let player = element("player.window")
        XCTAssertTrue(player.waitForExistence(timeout: 5), "the window's player (\(what))")
        let (p, w) = (player.frame, window.frame)
        for (edge, a, b) in [("left", p.minX, w.minX), ("right", p.maxX, w.maxX), ("top", p.minY, w.minY), ("bottom", p.maxY, w.maxY)] {
            XCTAssertEqual(a, b, accuracy: 2, "the player reaches \(what)'s \(edge) edge: player \(p), window \(w)")
        }
        XCTAssertFalse(sidebar.exists && sidebar.isHittable, "the sidebar is under the player in \(what)")
    }

    /// The window's sidebar: on the Mac an outline (labelled "Sidebar") whose rows hold each
    /// section's name as a static text's value.
    private var sidebar: XCUIElement { app.windows.firstMatch.outlines.firstMatch }

    private func shot(_ name: String) {
        let png = app.windows.firstMatch.screenshot().pngRepresentation
        let full = "player-mac-\(language)-\(name)"
        let a = XCTAttachment(data: png, uniformTypeIdentifier: "public.png")
        a.name = full
        a.lifetime = .keepAlways
        add(a)
        if let dir = ProcessInfo.processInfo.environment["MAKTABA_SHOTS"], !dir.isEmpty {
            try? FileManager.default.createDirectory(atPath: dir, withIntermediateDirectories: true)
            try? png.write(to: URL(fileURLWithPath: dir).appendingPathComponent("\(full).png"))
        }
    }
}
