import XCTest

/// The base of the Player's UI tests (Epic 48): a fresh `MockHome` per test, the app launched
/// in a language with the dark theme, signed in to the mock home by a code, and screenshots
/// kept as attachments (and written to `MAKTABA_SHOTS`, a folder, when it is set: CI uploads
/// it).
///
/// A screen's test subclasses it:
///
///     final class PlayerHomeUITests: PlayerUITestCase {
///         func testRows() {
///             launchSignedIn(sections: ["home"])
///             XCTAssertTrue(element("screen.home").waitForExistence(timeout: 10))
///             shot("home")
///         }
///     }
///
/// The launch arguments the shell reads in a Debug build (`ShellLaunch`): `sections` shows
/// sections not turned on yet (`PlayerSection.enabled`), `section` chooses one, `open` pushes
/// a route (`video/v1`, `series/s-seerah`, `watch/v1@90` …).
@MainActor
class PlayerUITestCase: XCTestCase {
    var app: XCUIApplication!
    var home: MockHome!
    /// The language of the last launch ("en", "ar").
    private(set) var language = "en"

    var isPad: Bool { UIDevice.current.userInterfaceIdiom == .pad }

    override func setUp() async throws {
        continueAfterFailure = false
        home = try MockHome()
        try home.start()
        app = XCUIApplication()
    }

    override func tearDown() async throws {
        home?.stop()
    }

    /// Opens the app. `fresh`: as a first run (no homes, the permissions walkthrough passed
    /// with its checks faked); otherwise the home and the sign-in of the last launch are kept
    /// (in a signed build only: an unsigned one, as on CI, keeps no token in the Keychain).
    /// `extra`: more launch arguments (the components' gallery, a Dynamic Type size).
    /// `theme`: the theme forced for the run (nil: the stored choice, as Settings changes it).
    /// `controlsStay`: the player's controls stay up once shown instead of hiding 4 s after the
    /// last touch (`WatchView.hideAfter`), so a test that shows them and taps one does not race
    /// the 4 s on a slow runner; the test of the 4 s turns it off.
    func launch(_ language: String = "en", sections: [String]? = nil, section: String? = nil, open: String? = nil,
                fresh: Bool = true, theme: String? = "dark", controlsStay: Bool = true, extra: [String] = []) {
        self.language = language
        let locale = language == "ar" ? "ar_JO" : "en_US"
        var args = ["-AppleLanguages", "(\(language))", "-AppleLocale", locale]
        // the dark theme (the owner's screenshots are dark; FR-2)
        if let theme { args += ["-theme", theme] }
        if fresh {
            args += ["-maktaba-permissions-reset", "-maktaba-permissions-fake", "granted", "-maktaba-session-reset"]
        }
        if let sections { args += ["-maktaba-sections", sections.joined(separator: ",")] }
        if let section { args += ["-maktaba-section", section] }
        if let open { args += ["-maktaba-open", open] }
        if controlsStay { args += ["-player.controls_hide_after", "600"] }
        app.launchArguments = args + extra
        app.launch()
        if fresh {
            let walkthrough = element("permissions.walkthrough")
            if walkthrough.waitForExistence(timeout: 10) {
                app.buttons["permissions.continue"].tap()
                XCTAssertTrue(walkthrough.waitForNonExistence(timeout: 5))
            }
        }
    }

    /// The mock home's address typed in the picker, then a code approved elsewhere.
    /// `timeout`: how long the picker may take to show (longer at a large text size).
    func signIn(timeout: TimeInterval = 10) {
        let field = app.textFields["picker.address"]
        // at a large text size the picker's header and nearby homes push the field (and its
        // Connect) below the screen, where the Form has not made them yet
        XCTAssertTrue(element("start.brand").waitForExistence(timeout: timeout), "the start screen")
        if !field.waitForExistence(timeout: 2) { scrollUntil(field) }
        XCTAssertTrue(field.exists, "the home picker")
        field.typeExactly(home.url, in: app)
        let connect = app.buttons["picker.connect"]
        if !connect.isHittable { scrollUntil(connect) }
        connect.tap()
        let byCode = app.buttons[language == "ar" ? "برمز" : "With a code"]
        XCTAssertTrue(byCode.waitForExistence(timeout: 10), "the sign-in screen offers a code")
        byCode.tap()
        home.approve()
        XCTAssertTrue(byCode.waitForNonExistence(timeout: 20), "approved: signed in")
    }

    /// A first run, signed in to the mock home.
    func launchSignedIn(_ language: String = "en", sections: [String]? = nil, section: String? = nil, open: String? = nil,
                        controlsStay: Bool = true) {
        launch(language, sections: sections, section: section, open: open, controlsStay: controlsStay)
        signIn()
    }

    func element(_ id: String) -> XCUIElement { app.descendants(matching: .any)[id].firstMatch }

    /// A section's button: a tab in the tab bar or the top tabs, or a row of the sidebar.
    func sectionButton(_ label: String) -> XCUIElement {
        let tab = app.tabBars.buttons[label].firstMatch
        return tab.exists ? tab : app.buttons[label].firstMatch
    }

    /// A screenshot attached to the test, named `player-<iphone|ipad>-<language>-<name>`.
    func shot(_ name: String) {
        let full = "player-\(isPad ? "ipad" : "iphone")-\(language)-\(name)"
        let png = app.screenshot().pngRepresentation
        let a = XCTAttachment(data: png, uniformTypeIdentifier: "public.png")
        a.name = full
        a.lifetime = .keepAlways
        add(a)
        if let dir = ProcessInfo.processInfo.environment["MAKTABA_SHOTS"], !dir.isEmpty {
            try? FileManager.default.createDirectory(atPath: dir, withIntermediateDirectories: true)
            try? png.write(to: URL(fileURLWithPath: dir).appendingPathComponent("\(full).png"))
        }
    }

    /// The player opened `video` at `start` seconds: what the last stream session asked the
    /// home for (`POST /api/stream/sessions`: `start_sec`, left out at 0).
    func playerOpened(_ video: String, at start: Int, _ why: String) {
        XCTAssertTrue(element("screen.watch").waitForExistence(timeout: 10), "the player")
        func last() -> [String: Any]? {
            home.requests.last { $0.method == "POST" && $0.path == "/api/stream/sessions" }
                .flatMap { try? JSONSerialization.jsonObject(with: $0.body) as? [String: Any] }
        }
        let end = Date().addingTimeInterval(10)
        while last() == nil, Date() < end { RunLoop.current.run(until: Date().addingTimeInterval(0.2)) }
        XCTAssertEqual(last()?["video_id"] as? String, video, "a stream session")
        XCTAssertEqual(last()?["start_sec"] as? Int ?? 0, start, why)
    }

    /// `performAccessibilityAudit` in two runs, Dynamic Type (it redraws the screen at each
    /// text size, the slow part) apart from the other types, each run again when it could not
    /// finish in time (error -56, "Audit failed to complete in time", or XCTest's own "Timed
    /// out while running accessibility audit", both seen on a loaded CI runner; an iPad's
    /// Profile audit timed out three times running every type at once). Every type asked for
    /// is still audited; any other error, and every issue `handler` does not excuse, fails.
    func accessibilityAudit(for types: XCUIAccessibilityAuditType = .all,
                            _ handler: @escaping (XCUIAccessibilityAuditIssue) throws -> Bool) throws {
        for part in [types.subtracting(.dynamicType), types.intersection(.dynamicType)] where !part.isEmpty {
            for attempt in 1 ... 3 {
                // the page as it is before the audit, for the handler to read: a query of the
                // app from inside the handler, while the audit holds it, stalled an iPad's
                // audit for 150 s on CI and left the app answering no query after it
                shown = settledPage()
                defer { shown = nil }
                do {
                    try app.performAccessibilityAudit(for: part, handler)
                    break
                } catch let error as NSError where Self.auditTimedOut(error) && attempt < 3 {
                    continue
                }
            }
        }
    }

    static func auditTimedOut(_ error: NSError) -> Bool {
        (error.domain == "com.apple.xcode.xctest.accessibilityAudit" && error.code == -56)
            || (error.domain == "com.apple.dt.XCTest.XCTFuture" && error.code == 1000)
    }

    /// An audit issue with the element it is on (identifier, label, frame), for a failure that
    /// says where to look: the audit's own message names only the element's class.
    func described(_ issue: XCUIAccessibilityAuditIssue) -> String {
        guard let e = issue.element else { return "\(issue.compactDescription) [no element: \(issue.detailedDescription)]" }
        return "\(issue.compactDescription) [\(e.identifier)|\(e.label)|\(e.frame)]"
    }

    /// A Dynamic Type issue on a UIKit UILabel the audit cannot map to an element: on an iPad,
    /// the section names of the tab bar at the top of the window (four in Libraries, where four
    /// sections are named; two in Profile, launched with two), which iPadOS keeps at one size
    /// and shows in the large content viewer instead; and the system search field's empty
    /// placeholder. This app's own texts are SwiftUI `Text`, which the audit always maps to an
    /// element (`rail.title`, a card's title), so they are still checked.
    func systemFixedLabel(_ issue: XCUIAccessibilityAuditIssue) -> Bool {
        isPad && issue.auditType == .dynamicType && issue.element == nil && issue.detailedDescription.contains("UILabel")
    }

    /// A contrast issue the audit can't judge: an element half off the screen, or partly under
    /// the floating tab bar, is sampled against what lies past it (a row scrolls on under the
    /// bar; the elements in the clear are still checked).
    ///
    /// The audit may give no element for such an issue: a List row (UIKit's collection view)
    /// part under the tab bar's soft edge, or scrolled up under the status and navigation bars,
    /// is a node it cannot map ("Contrast failed for SwiftUI.AccessibilityNode"). Profile
    /// showed it: one such issue with "Settings on this device" under the tab bar, three with
    /// "Account" and its rows under the top once scrolled to the end. So an issue without an
    /// element is excused only while some text of the page is partly under a bar
    /// (`textUnderABar`); with nothing there it fails. An element in full view maps, unless its
    /// words repeat (below).
    nonisolated fileprivate static let barEdge: CGFloat = 44

    func contrastOutOfView(_ issue: XCUIAccessibilityAuditIssue) -> Bool {
        guard issue.auditType == .contrast else { return false }
        let page = shown ?? (try? Shown(app.snapshot())) ?? Shown()
        guard let e = issue.element else { return page.textUnderABar }
        let f = e.frame
        if !page.window.contains(f) { return true }
        // The issue names its element by label, and a text with the same words elsewhere maps
        // to the first one: on an iPad's Arabic library page the audit failed "٥٥:٠٠" in full
        // view, as drawn 8.9:1, while the copy it measured was a card scrolled off a row's
        // edge (x -117). So a same-worded text out of view is the one the audit could not see.
        let label = e.label
        if page.texts.contains(where: { $0.label == label && !$0.frame.isEmpty && !page.window.contains($0.frame) }) { return true }
        // the bar, and the soft edge iOS draws above it over the scrolling content
        return page.tab.map { $0.insetBy(dx: 0, dy: -Self.barEdge).intersects(f) } ?? false
    }

    /// The page once it has stopped changing: the same texts in the same places on two looks
    /// running, half a second apart (at most 10 s). The audit measures each text at each size
    /// against the page as it was drawn; a page still drawing as it starts (a grid's next page,
    /// a List laying its rows out, an iPad's second column) leaves every text measured stale,
    /// "partially unsupported" all at once (seen on CI on Libraries' and Profile's pages).
    func settledPage(timeout: TimeInterval = 10) -> Shown? {
        let end = Date().addingTimeInterval(timeout)
        var last = try? Shown(app.snapshot())
        while Date() < end {
            RunLoop.current.run(until: Date().addingTimeInterval(0.5))
            let now = try? Shown(app.snapshot())
            if let now, let last, now.sameDrawing(as: last) { return now }
            last = now
        }
        return last
    }

    /// The page an audit runs on, read in one snapshot before it starts.
    private var shown: Shown?

    struct Shown {
        var window = CGRect.null
        var tab: CGRect?
        var nav: CGRect?
        var texts: [(label: String, frame: CGRect)] = []

        init() {}

        func sameDrawing(as other: Shown) -> Bool {
            window == other.window && tab == other.tab && nav == other.nav && texts.count == other.texts.count
                && zip(texts, other.texts).allSatisfy { $0.label == $1.label && $0.frame == $1.frame }
        }

        init(_ app: XCUIElementSnapshot) {
            var stack = [app]
            while let s = stack.popLast() {
                switch s.elementType {
                case .window where window.isNull: window = s.frame
                case .tabBar where tab == nil: tab = s.frame
                case .navigationBar where nav == nil: nav = s.frame
                case .staticText: texts.append((s.label, s.frame))
                default: break
                }
                stack.append(contentsOf: s.children.reversed())
            }
        }

        /// Some text of the page (not a bar's own) lies partly under the tab bar or its soft
        /// edge, or above the navigation bar's foot (under it and the status bar).
        var textUnderABar: Bool {
            var zones: [CGRect] = []
            if let tab { zones.append(tab.insetBy(dx: 0, dy: -PlayerUITestCase.barEdge)) }
            let top = nav?.maxY ?? window.minY
            zones.append(CGRect(x: window.minX, y: window.minY - window.height, width: window.width,
                                height: top - window.minY + window.height))
            let bars = [tab, nav].compactMap { $0 }
            return texts.contains { text in
                !text.frame.isEmpty && !bars.contains { $0.contains(text.frame) } && zones.contains { $0.intersects(text.frame) }
            }
        }
    }

    /// Swipes up until `e` is on screen and can be tapped (a few swipes at most).
    func scrollUntil(_ e: XCUIElement) {
        for _ in 0 ..< 8 where !(e.exists && e.isHittable) { app.swipeUp(velocity: .slow) }
    }
}
