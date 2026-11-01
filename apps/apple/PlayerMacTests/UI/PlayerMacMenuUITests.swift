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
    /// The first of `/api/videos` in the fixtures (`UITests/Fixtures/videos.json`).
    private let newestVideo = "Scholars of the Silk Road"

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

        menu("View", "Live TV")
        XCTAssertTrue(element("screen.liveTV").waitForExistence(timeout: 5), "View ▸ Live TV")
        menu("View", "Profile")
        XCTAssertTrue(element("screen.profile").waitForExistence(timeout: 5), "View ▸ Profile")
        shot("view-menu")

        let screens = ["screen.home", nil, "screen.liveTV", "screen.search", "screen.profile"]
        for (i, id) in screens.enumerated().reversed() {
            app.typeKey("\(i + 1)", modifierFlags: .command)
            if let id {
                XCTAssertTrue(element(id).waitForExistence(timeout: 5), "⌘\(i + 1) opens \(id)")
            } else {
                XCTAssertTrue(app.staticTexts[newestVideo].firstMatch.waitForExistence(timeout: 10), "⌘2 opens Libraries")
            }
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
    /// Playback ▸ Stop closes it. Its items are off until the video plays.
    func testThePlayerFillsTheWindowAndGoesFullScreen() throws {
        launchSignedIn()
        let row = app.staticTexts[newestVideo].firstMatch
        XCTAssertTrue(row.waitForExistence(timeout: 15), "the library browser")
        row.click()
        let play = app.buttons["Play"].firstMatch
        XCTAssertTrue(play.waitForExistence(timeout: 10), "the video's page")
        play.click()
        let close = app.buttons["Close"].firstMatch
        XCTAssertTrue(close.waitForExistence(timeout: 10), "the player")

        app.menuBars.menuBarItems["Playback"].click()
        XCTAssertTrue(app.menuBars.menuItems["Stop"].isEnabled, "Stop while a video is open")
        XCTAssertFalse(app.menuBars.menuItems["Play/Pause"].isEnabled, "no picture yet (the mock has no video)")
        app.typeKey(.escape, modifierFlags: [])

        let window = app.windows.firstMatch
        try assertPlayerCovers(window, "the window")
        shot("player-window")

        app.typeKey("f", modifierFlags: [.control, .command])
        XCTAssertTrue(wait { self.isFullScreen }, "⌃⌘F: full screen")
        try assertPlayerCovers(window, "the screen")
        app.menuBars.menuBarItems["View"].click()
        XCTAssertTrue(app.menuBars.menuItems["Exit Full Screen"].waitForExistence(timeout: 3))
        app.menuBars.menuItems["Exit Full Screen"].click()
        XCTAssertTrue(wait { !self.isFullScreen }, "View ▸ Exit Full Screen")

        menu("Playback", "Stop")
        XCTAssertTrue(close.waitForNonExistence(timeout: 5), "Playback ▸ Stop closes the player")
    }

    // MARK: helpers

    /// A first run (permissions passed, no home) in `language` with the dark theme, signed in
    /// to the mock home by a code.
    private func launchSignedIn(_ language: String = "en", sections: String? = nil, section: String? = nil) {
        self.language = language
        var args = ["-AppleLanguages", "(\(language))", "-AppleLocale", language == "ar" ? "ar_JO" : "en_US",
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
        field.click()
        field.typeText(home.url)
        app.buttons["picker.connect"].click()
        let byCode = app.buttons[language == "ar" ? "برمز" : "With a code"]
        XCTAssertTrue(byCode.waitForExistence(timeout: 10), "the sign-in screen offers a code")
        byCode.click()
        home.approve()
        XCTAssertTrue(byCode.waitForNonExistence(timeout: 20), "approved: signed in")
    }

    private func element(_ id: String) -> XCUIElement { app.descendants(matching: .any)[id].firstMatch }

    /// Clicks a menu bar item, then one of its items.
    private func menu(_ top: String, _ item: String) {
        app.menuBars.menuBarItems[top].click()
        let it = app.menuBars.menuItems[item].firstMatch
        XCTAssertTrue(it.waitForExistence(timeout: 3), "\(top) ▸ \(item)")
        XCTAssertTrue(it.isEnabled, "\(top) ▸ \(item) is on")
        it.click()
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

    /// The player's black reaches every corner of the window: nothing of the library around it.
    private func assertPlayerCovers(_ window: XCUIElement, _ what: String) throws {
        let image = window.screenshot().image
        let rep = try XCTUnwrap(image.representations.first as? NSBitmapImageRep
                                ?? image.tiffRepresentation.flatMap(NSBitmapImageRep.init(data:)))
        let (w, h) = (rep.pixelsWide, rep.pixelsHigh)
        // a little in from each edge (the window's rounded corners), then the middle of each side
        let inset = max(w, h) / 40
        let points = [(inset, h / 2), (w - inset, h / 2), (w / 2, h - inset), (inset, h - inset), (w - inset, h - inset),
                      (w / 3, h / 2), (2 * w / 3, h / 2)]
        for (x, y) in points {
            let c = try XCTUnwrap(rep.colorAt(x: x, y: y)?.usingColorSpace(.deviceRGB))
            let level = max(c.redComponent, c.greenComponent, c.blueComponent)
            XCTAssertLessThan(level, 0.08, "the player covers \(what) at (\(x), \(y)): \(c)")
        }
    }

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
