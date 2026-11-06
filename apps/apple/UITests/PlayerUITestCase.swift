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
        try warmUpOnce()
        home = try MockHome()
        try home.start()
        app = XCUIApplication()
    }

    // MARK: the simulator's first launches

    /// Whether this test process has signed in once already (`warmUpOnce`).
    private static var warmedUp = false

    /// The simulator's first launches of the app under XCTest, paid once per test process,
    /// before its first test.
    ///
    /// Each CI shard runs on a simulator made for it. Its first launch under XCTest sets up
    /// the automation session (13 to 36 s on CI, 2 s at every later launch), and for a minute
    /// or two after it the app answers each query slowly: a snapshot of the app took 14 to
    /// 31 s, and the shell drew 70 s after sign-in. The first test of a shard bore all of it:
    /// `PlayerLibrariesUITests.testAccessibilityAudit`, first in its shard, failed on its
    /// page's 20 s wait ("en root") and on the audit's own deadline ("Audit failed to complete
    /// in time"), on iPhone and iPad, while the next test, on the same simulator, signed in
    /// in a third of the time and passed.
    ///
    /// So before the first test the app goes the way a test takes it, on a mock home of its
    /// own (the test's mock sees none of it): launched, signed in, the shell drawn on
    /// Libraries, and read until three snapshots running each come back within 2 s (a page
    /// read at its usual pace), and one full accessibility audit run (a process's first is
    /// slow as well); then closed. What it took is in the test log and the result
    /// bundle. A later test process (after a crash) finds the simulator ready and pays one
    /// sign-in.
    ///
    /// Nothing the warm-up meets fails the test it comes before: on an iPad on CI the
    /// walkthrough took more than its 5 s to go and the shell drew at 200 s (run 41705653975),
    /// and on another the simulator took so long to answer that XCTest's own query timed out
    /// and the shell drew at 478 s (run 41712270578). Each failed the shard's first test
    /// before it began. So its waits are `warmUpPatience` times a test's, what it records is
    /// an expected failure (kept in the log and the result bundle), and it goes on to the end.
    private func warmUpOnce() throws {
        guard !Self.warmedUp else { return }
        Self.warmedUp = true
        home = try MockHome()
        try home.start()
        app = XCUIApplication()
        let stops = continueAfterFailure
        continueAfterFailure = true
        defer {
            continueAfterFailure = stops
            app.terminate()
            home.stop()
        }
        let lenient = XCTExpectedFailure.Options()
        lenient.isStrict = false
        XCTExpectFailure("the simulator's first launch may be slower than any wait", options: lenient) {
            warmUp()
        }
    }

    /// How many times longer than a test the warm-up's launch and sign-in may wait at each step.
    static let warmUpPatience: TimeInterval = 12

    private func warmUp() {
        XCTContext.runActivity(named: "The simulator's first launches of the app") { activity in
            let start = Date()
            // the first launch's slowness is what this pays: its steps wait as long as it takes
            launchSignedIn(section: "libraries", patience: Self.warmUpPatience)
            let shell = element("player.shell").waitForExistence(timeout: 120)
            let drawn = Date().timeIntervalSince(start)
            var quick = 0
            var looks: [String] = []
            let end = Date().addingTimeInterval(180)
            while shell, quick < 3, Date() < end {
                let asked = Date()
                _ = try? app.snapshot()
                let took = Date().timeIntervalSince(asked)
                looks.append(String(format: "%.1f", took))
                quick = took < 2 ? quick + 1 : 0
            }
            let pace = !shell ? "no shell in 120 s" : quick >= 3 ? "read at its usual pace" : "still slow after 180 s"
            // the process's first full accessibility audit is slow too (15 to 16 s on CI, 2 to
            // 2.5 s after; one of a single cheap type warmed nothing): a full one here, its
            // findings ignored, so a first test's audit keeps to its deadline
            let audited = Date()
            if shell { try? app.performAccessibilityAudit(for: XCUIAccessibilityAuditType.all.subtracting(.dynamicType)) { _ in true } }
            let audit = Date().timeIntervalSince(audited)
            let note = String(format: "signed in, shell drawn at %.0f s; %@ at %.0f s; snapshots took %@ s; first audit %.1f s",
                              drawn, pace, Date().timeIntervalSince(start), looks.joined(separator: ", "), audit)
            // in the test log, and kept with the result bundle
            print("PlayerUITestCase warm-up: \(note)")
            let kept = XCTAttachment(string: note)
            kept.lifetime = .keepAlways
            activity.add(kept)
        }
    }

    /// The app is closed when the test ends, before its home goes: XCUITest leaves it running
    /// otherwise, and one left on the watch screen keeps playing with its sound session
    /// (`.playback`, not mixable: `Playback.swift`). The next test's own player
    /// (PlayerWatchTests plays MockHome's stream in this runner) then cannot start its sound
    /// output, and AVPlayer's time, which follows the sound output, stays put: "the time
    /// moves" timed out 14 s in, with the Player still posting progress (CI run 41521240810).
    override func tearDown() async throws {
        Self.close(app)
        home?.stop()
    }

    // MARK: a tap the simulator lost (#639)

    /// A failure after the simulator cancelled one of the test's touches says so first.
    ///
    /// On a loaded runner the simulator's `backboardd` can register XCTest's per-tap touch
    /// device twice; the press and the lift land on different copies and the system cancels
    /// the touch, so the app never sees the tap (about once in a few hundred taps). The test
    /// then fails on what the tap should have done, which reads as an app bug. When a test
    /// fails, the running app is asked how many touches the system cancelled since launch
    /// (`TouchHook`); if any, the failure starts with that. Retrying is not an option: the
    /// failure stays a failure, with its real cause named.
    override nonisolated func record(_ issue: XCTIssue) {
        guard Thread.isMainThread else { return super.record(issue) }
        // on the main thread, so on the main actor, where `app` lives: XCTest records a UI
        // test's issues there (any other thread records the issue as it is)
        nonisolated(unsafe) let test = self
        let cancelled = MainActor.assumeIsolated { Self.cancelledTouches(in: test.app) }
        super.record(Self.withLostTaps(issue, cancelled: cancelled))
    }

    /// How many touches the system cancelled in `app` since it launched; nil when it is not
    /// running or does not answer (launched without the hook).
    static func cancelledTouches(in app: XCUIApplication?) -> Int? {
        guard let app, app.state == .runningForeground || app.state == .runningBackground else { return nil }
        return TouchAnswers.shared.cancelledTouches(within: 3)
    }

    /// `issue`, its description led by `lostTapsNote` when the app said the system cancelled
    /// touches; as it is when it cancelled none or the app did not answer (nil).
    nonisolated static func withLostTaps(_ issue: XCTIssue, cancelled: Int?) -> XCTIssue {
        guard let cancelled, cancelled > 0 else { return issue }
        var issue = issue
        issue.compactDescription = lostTapsNote(cancelled) + issue.compactDescription
        return issue
    }

    /// The words a failure starts with when the system cancelled `cancelled` touches.
    nonisolated static func lostTapsNote(_ cancelled: Int) -> String {
        let count = cancelled > TouchHook.most ? "more than \(TouchHook.most) touches" : cancelled == 1 ? "1 touch" : "\(cancelled) touches"
        return "[the simulator may have dropped a tap: the system cancelled \(count) of this test before the app saw it end; "
            + "see #639 (backboardd's digitizer race) and the app's log before reading the failure as the app's] "
    }

    /// Closes the app if it is running.
    static func close(_ app: XCUIApplication?) {
        guard let app, app.state != .notRunning else { return }
        app.terminate()
    }

    /// Opens the app. `fresh`: as a first run (no homes), past the permissions walkthrough
    /// (`-maktaba-permissions-seen`, its checks faked); otherwise the home and the sign-in of
    /// the last launch are kept.
    /// The tokens are kept in a file, not the Keychain (`-maktaba-secrets-in-file`,
    /// `UITestSecrets`): the app is built unsigned on CI, and an unsigned app may not use the
    /// Keychain, so it would be signed out at each launch.
    /// `extra`: more launch arguments (the components' gallery, a Dynamic Type size).
    /// `theme`: the theme forced for the run (nil: the stored choice, as Settings changes it).
    /// `controlsStay`: the player's controls stay up once shown instead of hiding 4 s after the
    /// last touch (`WatchView.hideAfter`), so a test that shows them and taps one does not race
    /// the 4 s on a slow runner; the test of the 4 s turns it off.
    /// `resetSettings` (with `fresh`): the playback settings and the theme as on a fresh install
    /// (`-maktaba-settings-reset`), so a setting a test changed (Profile's autoplay switch) is
    /// never the next test's; off only for a relaunch that checks a setting was kept.
    func launch(_ language: String = "en", sections: [String]? = nil, section: String? = nil, open: String? = nil,
                fresh: Bool = true, resetSettings: Bool = true, theme: String? = "dark", controlsStay: Bool = true,
                extra: [String] = [], patience: TimeInterval = 1) {
        self.language = language
        let locale = language == "ar" ? "ar_JO" : "en_US"
        var args = ["-AppleLanguages", "(\(language))", "-AppleLocale", locale, "-maktaba-secrets-in-file"]
        // the dark theme (screenshots are dark by default; FR-2)
        if let theme { args += ["-theme", theme] }
        if fresh {
            // past the walkthrough with no tap: a Continue tap was lost in the simulator itself
            // (backboardd attached XCTest's digitizer twice, "unknown digitizer", and skipped
            // the touch; iPad runs of #608). The walkthrough's taps are PermissionsUITests'.
            args += ["-maktaba-permissions-reset", "-maktaba-permissions-seen", "-maktaba-permissions-fake", "granted",
                     "-maktaba-session-reset"]
            if resetSettings { args.append("-maktaba-settings-reset") }
        }
        if let sections { args += ["-maktaba-sections", sections.joined(separator: ",")] }
        if let section { args += ["-maktaba-section", section] }
        if let open { args += ["-maktaba-open", open] }
        if controlsStay { args += ["-player.controls_hide_after", "600"] }
        // the app takes the text sizes `setTextSize` asks for (`TextSizeHook`)
        args.append(TextSizeHook.flag)
        // and counts the touches the system cancels (`TouchHook`, read by `record(_:)`)
        args.append(TouchHook.flag)
        app.launchArguments = args + extra
        app.launch()
    }

    /// Waits (at most `timeout`) until `e` can be tapped and its frame is the same on two looks
    /// a quarter second apart: a sheet or a page that is still moving has settled.
    func waitUntilStill(_ e: XCUIElement, timeout: TimeInterval) {
        let end = Date().addingTimeInterval(timeout)
        var last = CGRect.null
        while Date() < end {
            if e.exists, e.isHittable {
                let now = e.frame
                if now == last { return }
                last = now
            }
            RunLoop.current.run(until: Date().addingTimeInterval(0.25))
        }
    }

    /// The mock home's address typed in the picker, then a code approved elsewhere.
    /// `timeout`: how long the picker may take to show (longer at a large text size).
    /// `patience`: every other wait, as many times longer (the warm-up's first launch).
    func signIn(timeout: TimeInterval = 10, patience: TimeInterval = 1) {
        let field = app.textFields["picker.address"]
        // at a large text size the picker's header and nearby homes push the field (and its
        // Connect) below the screen, where the Form has not made them yet
        XCTAssertTrue(element("start.brand").waitForExistence(timeout: timeout * patience), "the start screen")
        if !field.waitForExistence(timeout: 2 * patience) { scrollUntil(field) }
        XCTAssertTrue(field.exists, "the home picker")
        field.typeExactly(home.url, in: app)
        // submitted from the field's own Return (`onSubmit`, the same `add` as Connect), not a
        // tap on Connect: a tap a second after the last key, as the keyboard still settled
        // after typing a Latin address on the Arabic keyboard, was lost on an iPad on CI (the
        // picker stayed, the address in its field and Connect enabled, no connect begun). A
        // key goes to the field that has the keyboard, whatever the Form is laying out; the
        // Connect button's own tap is still driven by ConnectUITests and the progress tests.
        field.typeText("\n")
        let byCode = app.buttons[language == "ar" ? "برمز" : "With a code"]
        XCTAssertTrue(byCode.waitForExistence(timeout: 10 * patience), "the sign-in screen offers a code")
        byCode.tap()
        home.approve()
        XCTAssertTrue(byCode.waitForNonExistence(timeout: 20 * patience), "approved: signed in")
    }

    /// A first run, signed in to the mock home.
    func launchSignedIn(_ language: String = "en", sections: [String]? = nil, section: String? = nil, open: String? = nil,
                        controlsStay: Bool = true, extra: [String] = [], patience: TimeInterval = 1) {
        launch(language, sections: sections, section: section, open: open, controlsStay: controlsStay, extra: extra,
               patience: patience)
        signIn(patience: patience)
    }

    func element(_ id: String) -> XCUIElement { app.descendants(matching: .any)[id].firstMatch }

    /// A section's button: a tab in the tab bar or the top tabs, or a row of the sidebar.
    func sectionButton(_ label: String) -> XCUIElement {
        let tab = app.tabBars.buttons[label].firstMatch
        return tab.exists ? tab : app.buttons[label].firstMatch
    }

    /// A screenshot attached to the test, named `player-<iphone|ipad>-<language>-<name>`.
    /// `wholeScreen`: the device's screen, not the app's window alone (the on-screen keyboard
    /// is a window of its own, left out of the app's screenshot).
    func shot(_ name: String, wholeScreen: Bool = false) {
        let full = "player-\(isPad ? "ipad" : "iphone")-\(language)-\(name)"
        let png = (wholeScreen ? XCUIScreen.main.screenshot() : app.screenshot()).pngRepresentation
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

    /// `performAccessibilityAudit` on the page once it has stopped drawing, with every type
    /// asked for except Dynamic Type; every issue `handler` does not excuse fails.
    ///
    /// The audit's own Dynamic Type check is left out on purpose: it redraws the screen at
    /// each text size and measures each text against the drawing, and on a loaded CI runner
    /// its measurements go stale. It flagged every text of a page at once as "partially
    /// unsupported" (Search's recent searches, Profile's Sessions, Libraries' pages) where the
    /// screen recording shows each of them redrawn and scaled at every size. Dynamic Type is
    /// checked instead by `assertTextScales`, on each audited page: the page shown at the
    /// smallest size, the default and AX3, each text taller at each step. Clipped text
    /// (`.textClipped`) is still the audit's, and it is the costly part: leaving Dynamic Type
    /// out does not stop the audit stepping through the sizes. The clipping check lays the app
    /// out again at each of the 12 sizes (one UIKit routine runs both checks), all within the
    /// audit's 15 s: two thirds of an audit's time (an iPad's Libraries root: 10 s of 16 s on a
    /// slowed simulator; contrast, hit regions and the rest 0.7 to 2 s each). Its cost is all
    /// the app has laid out at each size, so an audited page is launched with only what it
    /// needs (a section alone, `sections:`, not all five tabs). Each audit's time is printed.
    ///
    /// The handler runs once the audit is over, on every issue it found: nothing queries the
    /// app while the audit holds it. A query from inside the audit's own handler (an issue's
    /// element, its frame or label) stalled an iPad's audit for 150 s on CI and left the app
    /// answering no query after it ("Failed to get matching snapshot" on Libraries' Arabic
    /// grids). Every issue the handler does not excuse fails, all of them in one error
    /// (`AuditIssues`), each with the element it is on.
    func accessibilityAudit(for types: XCUIAccessibilityAuditType = .all,
                            _ handler: @escaping (XCUIAccessibilityAuditIssue) throws -> Bool) throws {
        loadPicturesAtEveryTextSize()
        // the page as it is before the audit, for the handler to read
        shown = settledPage()
        defer { shown = nil }
        var issues: [XCUIAccessibilityAuditIssue] = []
        let start = Date()
        // in the test log on a pass too: an audit near its 15 s shows before it times one out
        defer { print(String(format: "accessibility audit: %.1f s", Date().timeIntervalSince(start))) }
        auditFrom = home.requests.count
        try app.performAccessibilityAudit(for: types.subtracting(.dynamicType)) { issue in
            issues.append(issue)
            return true
        }
        var failed: [String] = []
        for issue in issues where try !handler(issue) { failed.append(described(issue)) }
        if !failed.isEmpty { throw AuditIssues(issues: failed) }
    }

    /// How many requests the home had when the last audit began (after its warm-up): what
    /// the app asked for during the audit is what came after.
    private(set) var auditFrom = 0

    /// Before an audit: the page shown at the smallest text size and at AX3, then the default,
    /// each until the app asks the home for no more pictures. The audit lays the page out at
    /// each text size, and the smallest shows cards the default does not; their posters were
    /// first asked for during the audit, drawn as moving placeholders until they came, and on
    /// a loaded runner the audit never saw the page still (PR #614: "Audit failed to complete
    /// in time" on Libraries' lib-docs, `GET /api/videos/v1/poster` and two more during it, and
    /// on Search's Arabic results). Each picture is in the app's cache once asked for.
    private func loadPicturesAtEveryTextSize() {
        let pictures = {
            self.home.requests.filter { r in
                r.method == "GET" && ["/poster", "/backdrop", "/logo", "/art"].contains { r.path.hasSuffix($0) }
            }.count
        }
        for size in [TextSize.smallest, .ax3, .standard] {
            setTextSize(size)
            var asked = pictures()
            for _ in 0 ..< 5 {
                _ = settledPage()
                let now = pictures()
                if now == asked { break }
                asked = now
            }
        }
    }

    /// The issues of an audit its handler did not excuse.
    struct AuditIssues: Error, CustomStringConvertible {
        let issues: [String]
        var description: String { "accessibility audit: " + issues.joined(separator: "; ") }
    }

    // MARK: Dynamic Type

    /// A launch at the smallest text size or at AX3, for a test that starts there (the size
    /// check changes sizes in the running app instead: `setTextSize`).
    static let smallestText = ["-UIPreferredContentSizeCategoryName", "UICTContentSizeCategoryXS"]
    static let ax3Text = ["-UIPreferredContentSizeCategoryName", "UICTContentSizeCategoryAccessibilityXXXL"]

    /// The Dynamic Type sizes the check measures (`TextSizeHook` in the app).
    enum TextSize: String { case smallest = "XS", standard = "default", ax3 = "AX3" }

    /// Changes the running app's Dynamic Type size (a Debug build's `TextSizeHook`: the
    /// system's setting, overridden on each of its scenes) and waits for the app to say it
    /// has laid its windows out at it. Fails when the app does not answer.
    func setTextSize(_ size: TextSize, file: StaticString = #filePath, line: UInt = #line) {
        let answer = TextSizeAnswers.shared.expect(size.rawValue)
        CFNotificationCenterPostNotification(CFNotificationCenterGetDarwinNotifyCenter(),
                                             CFNotificationName((TextSizeHook.ask + size.rawValue) as CFString),
                                             nil, nil, true)
        let end = Date().addingTimeInterval(15)
        while !TextSizeAnswers.shared.received(answer), Date() < end { RunLoop.current.run(until: Date().addingTimeInterval(0.1)) }
        XCTAssertTrue(TextSizeAnswers.shared.received(answer), "the app took the text size \(size.rawValue)", file: file, line: line)
    }

    /// Dynamic Type on the page open now, at the default size: the same page, in the same
    /// launch, shown at the smallest size, the default and AX3 (`setTextSize`). Each text of
    /// the page, by its identifier or else its words, must be taller at each step (one
    /// allowance, a button's 44-pt minimum at the two smaller sizes: `grows`); one that is not
    /// fails, named with its three heights and where it is. The texts of the system's
    /// navigation and tab bars, every one of them, are UIKit's own (an iPad's tab bar keeps one
    /// size and shows the large content viewer instead; a bar's title grows only at the
    /// accessibility sizes) and are left out. Afterwards the page is at the default size
    /// again, for the test to go on.
    func assertTextScales(_ page: String, file: StaticString = #filePath, line: UInt = #line) {
        let fixed = textsThatDoNotScale()
        XCTAssertTrue(fixed.isEmpty, "\(page): Dynamic Type: " + fixed.joined(separator: "; "), file: file, line: line)
    }

    /// The 44-pt minimum height of a button (`frame(minHeight: 44)`, its hit region), give or
    /// take a point. A text that is a button's label is read at its button's height, so at
    /// the smallest size and at the default (Search's recent rows, the video page's Favorite
    /// and Mark watched) it can be that minimum both times, its words smaller inside it.
    static let buttonMinimum: ClosedRange<CGFloat> = 43 ... 45

    /// Taller at each step. The one allowance: a text inside a button whose heights at the
    /// smallest size and the default both sit at the button's 44-pt minimum (`buttonMinimum`);
    /// then the step to AX3 must still be taller. Any other text must grow at both steps.
    static func grows(_ small: CGFloat, _ normal: CGFloat, _ large: CGFloat, inButton: Bool) -> Bool {
        if inButton, buttonMinimum.contains(small), buttonMinimum.contains(normal) { return normal < large }
        return small < normal && normal < large
    }

    /// The texts of the page that do not grow at each step (see `assertTextScales`).
    ///
    /// Each size is read after the app took it, the default too (last, which leaves the page
    /// there): the page as launched is not read. A view's accessibility frame as first laid
    /// out can differ from the same view's after an update: a poster card on the gallery's
    /// shimmer page read 94 pt (its poster) as launched and 107 pt at XS once updated (its
    /// poster and title), which is no measure of its text.
    func textsThatDoNotScale() -> [String] {
        setTextSize(.smallest)
        let small = settledPage()
        setTextSize(.ax3)
        let large = settledPage()
        setTextSize(.standard)
        let normal = settledPage()
        guard let small, let normal, let large else { return ["the page could not be read at the smallest size, the default or AX3"] }
        let (s, n, l) = (small.heights, normal.heights, large.heights)
        var fixed: [String] = []
        for key in n.keys.sorted() {
            guard let hs = s[key], let hl = l[key], let hn = n[key] else { continue }
            for k in 0 ..< min(hs.count, hn.count, hl.count)
            where !Self.grows(hs[k].height, hn[k].height, hl[k].height, inButton: hn[k].inButton && hs[k].inButton) {
                fixed.append("\"\(key)\" \(Int(hs[k].height)) / \(Int(hn[k].height)) / \(Int(hl[k].height)) pt high (smallest / default / AX3), at \(hn[k].frame) by default\(hn[k].inButton ? " in a button" : "")")
            }
        }
        return fixed
    }

    /// An audit issue with the element it is on (identifier, label, frame), for a failure that
    /// says where to look: the audit's own message names only the element's class.
    func described(_ issue: XCUIAccessibilityAuditIssue) -> String {
        guard let e = issue.element else { return "\(issue.compactDescription) [no element: \(issue.detailedDescription)]" }
        return "\(issue.compactDescription) [\(e.identifier)|\(e.label)|\(e.frame)]"
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

    /// A contrast issue on an element with a shimmer inside its frame (a card whose poster is
    /// still loading): the audit measures the element's whole frame and names it by its label,
    /// and there the light passing over the placeholder is two close surface colours by
    /// design, not text. An iPad run of #608 failed a card's Arabic title, drawn light on the
    /// dark page, for its shimmering poster above it. The shimmer itself has no text.
    func contrastOverShimmer(_ issue: XCUIAccessibilityAuditIssue) -> Bool {
        guard issue.auditType == .contrast, let f = issue.element?.frame, !f.isEmpty else { return false }
        return app.descendants(matching: .any).matching(identifier: "shimmer").allElementsBoundByIndex
            .contains { f.contains($0.frame) }
    }

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
        var texts: [(id: String, label: String, frame: CGRect, inButton: Bool)] = []
        /// Each text a navigation or tab bar holds: the system's own (every bar, not just the
        /// first: an iPad's split view has one navigation bar per column, and the detail's
        /// title "Lectures", 21 / 21 / 26 pt, was measured as the page's on CI).
        var barTexts: Set<Int> = []

        init() {}

        /// Each text's height, by its identifier or else its words, in the order shown; the
        /// texts of the tab and navigation bars left out (the system's own, see
        /// `assertTextScales`).
        var heights: [String: [(height: CGFloat, inButton: Bool, frame: CGRect)]] {
            let bars = [tab, nav].compactMap { $0 }
            var all: [String: [(height: CGFloat, inButton: Bool, frame: CGRect)]] = [:]
            for (i, t) in texts.enumerated() where !t.frame.isEmpty && !barTexts.contains(i) && !bars.contains(where: { $0.intersects(t.frame) }) {
                all[t.id.isEmpty ? t.label : t.id, default: []].append((t.frame.height, t.inButton, t.frame))
            }
            return all
        }

        func sameDrawing(as other: Shown) -> Bool {
            window == other.window && tab == other.tab && nav == other.nav && texts.count == other.texts.count
                && zip(texts, other.texts).allSatisfy { $0.label == $1.label && $0.frame == $1.frame }
        }

        init(_ app: XCUIElementSnapshot) {
            // each element with whether a button holds it
            var stack = [(app, false, false)]
            while let (s, inButton, inBar) = stack.popLast() {
                switch s.elementType {
                case .window where window.isNull: window = s.frame
                case .tabBar where tab == nil: tab = s.frame
                case .navigationBar where nav == nil: nav = s.frame
                case .staticText:
                    if inBar { barTexts.insert(texts.count) }
                    texts.append((s.identifier, s.label, s.frame, inButton))
                default: break
                }
                let below = inButton || s.elementType == .button
                let bar = inBar || s.elementType == .navigationBar || s.elementType == .tabBar
                stack.append(contentsOf: s.children.reversed().map { ($0, below, bar) })
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

    /// Drags the content under `element` by `offset` as a finger does that means to scroll:
    /// down, moving for about half a second, then still for a moment before it lifts.
    ///
    /// Not `swipeUp()` and the like: a swipe is a flick let go while still moving, and a
    /// scroll view follows a flick only when it sees the finger move over a few frames before
    /// the lift. One that is over in under ~20 ms is not followed at all (every time, on a
    /// throwaway simulator); a swipe on a 68-pt guide row is 58 pt in 58 ms. On busy CI
    /// runners the guide's swipes moved nothing, not a point and no animation (2 November
    /// 2026: four runs, iPhone and iPad), while the same tests passed on the same commits on
    /// other runners. A drag that moves for half a second and stops before it lifts is
    /// followed however its touches are paced.
    ///
    /// It holds still for 3 s before it moves. A scroll view takes no pan while the system
    /// may still claim the touch (SpringBoard's own gestures have to fail first), and on a
    /// saturated runner that decision came late: in an iPad run of #608 the simulator's log
    /// has SpringBoard silent from the touch-down until "UIKit magic hysteresis reached with
    /// no gestures still recognizing", 1.34 s later and after the lift, while the app logged
    /// "ignoring pan gesture … because system gestures are possible" for every move, so a
    /// 300-pt drag mid-screen moved nothing. Every other drag of that run was decided within
    /// 0.3 s. The hold lets the decision land before the finger moves; 3 s is the margin over
    /// the 1.34 s seen, so it is not to be shortened. The guide's cells have no long-press
    /// action, so the hold itself does nothing.
    func drag(_ element: XCUIElement, by offset: CGVector, from point: CGVector = CGVector(dx: 0.5, dy: 0.5)) {
        let start = element.coordinate(withNormalizedOffset: point)
        start.press(forDuration: 3, thenDragTo: start.withOffset(offset),
                    withVelocity: XCUIGestureVelocity(rawValue: 400), thenHoldForDuration: 0.3)
    }

    /// Swipes up until `e` is on screen and can be tapped (a few swipes at most).
    func scrollUntil(_ e: XCUIElement) {
        for _ in 0 ..< 8 where !(e.exists && e.isHittable) { app.swipeUp(velocity: .slow) }
    }
}

/// The app's answers to `setTextSize` (Darwin notifications from its `TextSizeHook`): each
/// size's answers counted as they arrive, on the test's main run loop, which the wait turns.
final class TextSizeAnswers: @unchecked Sendable {
    static let shared = TextSizeAnswers()
    private static let prefix = TextSizeHook.applied
    private let lock = NSLock()
    private var counts: [String: Int] = [:]

    private init() {
        for size in TextSizeHook.sizes {
            CFNotificationCenterAddObserver(CFNotificationCenterGetDarwinNotifyCenter(), nil, { _, _, name, _, _ in
                guard let name = name?.rawValue as String?, name.hasPrefix(TextSizeAnswers.prefix) else { return }
                TextSizeAnswers.shared.count(String(name.dropFirst(TextSizeAnswers.prefix.count)))
            }, (Self.prefix + size) as CFString, nil, .deliverImmediately)
        }
    }

    private func count(_ size: String) {
        lock.lock(); defer { lock.unlock() }
        counts[size, default: 0] += 1
    }

    /// The answer to wait for: the next one for `size`, after those already counted.
    func expect(_ size: String) -> (size: String, count: Int) {
        lock.lock(); defer { lock.unlock() }
        return (size, counts[size, default: 0] + 1)
    }

    func received(_ answer: (size: String, count: Int)) -> Bool {
        lock.lock(); defer { lock.unlock() }
        return counts[answer.size, default: 0] >= answer.count
    }
}

/// The app's answers about cancelled touches (Darwin notifications from its `TouchHook`):
/// the last count it gave and how many answers came, on the test's main run loop, which the
/// wait turns.
final class TouchAnswers: @unchecked Sendable {
    static let shared = TouchAnswers()
    private let lock = NSLock()
    private var answers = 0
    private var last: Int?

    private init() {
        for name in TouchHook.answers {
            CFNotificationCenterAddObserver(CFNotificationCenterGetDarwinNotifyCenter(), nil, { _, _, name, _, _ in
                guard let name = name?.rawValue as String?, let count = TouchHook.count(in: name) else { return }
                TouchAnswers.shared.note(count)
            }, name as CFString, nil, .deliverImmediately)
        }
    }

    private func note(_ count: Int) {
        lock.lock(); defer { lock.unlock() }
        answers += 1
        last = count
    }

    private var answered: Int {
        lock.lock(); defer { lock.unlock() }
        return answers
    }

    /// Asks the running app how many touches the system cancelled since it launched; nil
    /// when it does not answer within `seconds` (not running, or launched without the hook).
    func cancelledTouches(within seconds: TimeInterval) -> Int? {
        let before = answered
        CFNotificationCenterPostNotification(CFNotificationCenterGetDarwinNotifyCenter(),
                                             CFNotificationName(TouchHook.ask as CFString), nil, nil, true)
        let end = Date().addingTimeInterval(seconds)
        while answered == before, Date() < end { RunLoop.current.run(until: Date().addingTimeInterval(0.05)) }
        lock.lock(); defer { lock.unlock() }
        return answers > before ? last : nil
    }
}
