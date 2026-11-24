import XCTest

/// The Apple TV's product screenshots (story 60.09): Home, the series Caminandes with its
/// seasons, Curiosity playing, and the live channel guide; and the store gallery's (story
/// 60.14): Search and signing in with a code; in English and in Arabic (the TV is
/// dark by default), with the Siri remote, on the demo library (`MockHome(demo:)`, from
/// `tests/fixtures/demo-library/`). Each is an attachment named
/// `shot-appletv-<screen>-<language>`, kept always: CI exports them from the result bundle as
/// `product-shots-appletv` (`_apple-apps.yml`, TV UI tests). The focus ring stays where the
/// remote left it: it is how the TV looks.
///
/// The clock: `library.json`'s noon for the series and the player; Home's On now row and the
/// guide are drawn against the device's clock, so there the demo's times count from now's
/// quarter hour (the guide's programmes start on its lines).
@MainActor
final class TVDemoShotsUITests: XCTestCase {
    let remote = XCUIRemote.shared
    var app: XCUIApplication!
    var home: MockHome?
    private var language = "en"

    override func setUp() async throws {
        continueAfterFailure = false
        app = XCUIApplication()
    }

    override func tearDown() async throws {
        // a failure here is recorded, not an interruption: one inside an async tearDown hangs
        // XCTest (`PlayerUITestCase.tearDown`)
        continueAfterFailure = true
        app?.terminate()
        home?.stop()
    }

    func testHome() throws {
        for language in ["en", "ar"] {
            try signIn(language, clock: MockHome.quarterHour())
            XCTAssertTrue(element("home.row.continue").waitForExistence(timeout: 20), "Home; the home saw \(seen)")
            XCTAssertTrue(element("video.card.v-curiosity.art").waitForExistence(timeout: 20), "Curiosity's picture")
            waitForQuiet()
            shot("home")
        }
    }

    /// Home's Continue row: Curiosity, then Llamigos (a Caminandes episode); its page's Go to
    /// series opens the series.
    func testSeries() throws {
        for language in ["en", "ar"] {
            try signIn(language)
            let card = element("video.card.v-llamigos.art")
            XCTAssertTrue(card.waitForExistence(timeout: 20), "Llamigos on Home; the home saw \(seen)")
            remote.press(.down)
            usleep(500_000)
            moveFocusInRow(to: "v-llamigos")
            remote.press(.select)
            let toSeries = app.buttons[language == "ar" ? "الانتقال إلى السلسلة" : "Go to series"]
            XCTAssertTrue(toSeries.waitForExistence(timeout: 20), "Llamigos's page goes to its series")
            moveFocus(to: toSeries, with: language == "ar" ? .left : .right)
            remote.press(.select)
            XCTAssertTrue(element("series.page").waitForExistence(timeout: 20), "the series")
            XCTAssertTrue(element("season.1").waitForExistence(timeout: 20), "its seasons")
            XCTAssertTrue(element("season.2").exists, "two of them")
            XCTAssertTrue(wait(timeout: 20) {
                self.app.descendants(matching: .any).matching(NSPredicate(format: "identifier ENDSWITH '.art'")).count >= 2
            }, "its episodes' pictures")
            waitForQuiet()
            shot("series")
        }
    }

    /// Curiosity from Home's Continue row: Resume (focused first) plays it through a queue, then
    /// its Arabic subtitles are chosen in the transport bar, which goes by itself.
    func testPlayer() throws {
        for language in ["en", "ar"] {
            try signIn(language)
            // the TV counts a session's stream from where it opened (Resume: the demo's
            // `position_sec`), so the stream is the rest of the film
            if let home, let length = home.demoVideo("v-curiosity")?["duration_sec"] as? Int,
               let at = home.demoDocument("get-me-videos-v-curiosity")?["position_sec"] as? Int {
                home.streamSeconds = (length - at) / 2 * 2
            }
            let card = element("video.card.v-curiosity.art")
            XCTAssertTrue(card.waitForExistence(timeout: 20), "Curiosity on Home; the home saw \(seen)")
            remote.press(.down)
            usleep(500_000)
            moveFocusInRow(to: "v-curiosity")
            remote.press(.select)
            let resume = app.buttons.matching(NSPredicate(format: "label BEGINSWITH %@", language == "ar" ? "استئناف" : "Resume")).firstMatch
            XCTAssertTrue(resume.waitForExistence(timeout: 20), "Curiosity's page offers Resume")
            XCTAssertTrue(wait(timeout: 10) { resume.hasFocus }, "Resume focused first")
            remote.press(.select)
            XCTAssertTrue(element("queue.player").waitForExistence(timeout: 20), "the player; the home saw \(seen)")
            // the picture in: its segments asked for, and a few seconds played
            XCTAssertTrue(wait(timeout: 30) { self.seen.contains { $0.hasPrefix("GET /stream/") && $0.hasSuffix(".ts") } },
                          "the stream's segments; the home saw \(seen)")
            let asked = seen.count
            XCTAssertTrue(wait(timeout: 20) { self.seen.count > asked + 1 }, "it plays on")
            // the picture drawn, not only asked for: a store gallery run shot a black player
            // once, its segments fetched before the first frame was on screen (run 45359873399)
            XCTAssertTrue(wait(timeout: 30) { self.meanLuminance() > 0.08 },
                          "the picture on screen (mean luminance \(meanLuminance()))")
            arabicSubtitles()
            // the transport bar goes by itself as it plays; the subtitle's letters stay
            let line = CGRect(x: 0.3, y: 0.8, width: 0.4, height: 0.17)
            sleep(8)
            XCTAssertTrue(wait(timeout: 20) { self.brightPixels(in: line) >= 500 }, "the subtitle line")
            shot("player")
        }
    }

    /// Live TV's Now: the channels, what is on each and what comes next (the guide's quarter
    /// hours are too short for their titles).
    func testLive() throws {
        for language in ["en", "ar"] {
            try signIn(language, clock: MockHome.quarterHour())
            openLiveTab(language == "ar" ? "البث المباشر" : "Live TV")
            XCTAssertTrue(app.buttons["live.mode.now"].waitForExistence(timeout: 20), "Now | Guide; the home saw \(seen)")
            waitForQuiet()
            shot("live")
        }
    }

    func testGuide() throws {
        for language in ["en", "ar"] {
            try signIn(language, clock: MockHome.quarterHour())
            openLiveTab(language == "ar" ? "البث المباشر" : "Live TV")
            openGuide()
            let first = app.buttons["live.guide.ch-space.now"]
            XCTAssertTrue(first.waitForExistence(timeout: 20), "the guide; the home saw \(seen)")
            remote.press(.down)
            XCTAssertTrue(wait(timeout: 5) { first.hasFocus }, "down lands on what is on now")
            waitForQuiet()
            shot("guide")
        }
    }

    // MARK: the store gallery's screens (story 60.14 part 2)

    /// Search for the demo's query ("Mars", «المريخ»): typed with the TV's keyboard, Search
    /// for, then down through the results to In the transcript.
    func testSearch() throws {
        for language in ["en", "ar"] {
            try signIn(language)
            // no suggestions row under the keyboard: Down goes from the keys to Search for
            // (TVSearchUITests does the same)
            home?.stub("GET", "/api/search/suggest", body: #"{"suggestions": []}"#)
            let query = language == "ar" ? "المريخ" : "Mars"
            openTab(language == "ar" ? "البحث" : "Search")
            let field = app.searchFields.firstMatch
            XCTAssertTrue(field.waitForExistence(timeout: 20), "the search field")
            field.typeExactly(query, in: app) {
                for _ in 0..<4 where !field.hasFocus {
                    self.remote.press(.down)
                    usleep(400_000)
                }
                if field.hasFocus { self.remote.press(.select) }
                usleep(600_000)
            }
            let run = app.buttons["search.run"]
            XCTAssertTrue(run.waitForExistence(timeout: 10), "Search for “\(query)”")
            for _ in 0..<16 where !run.hasFocus {
                remote.press(.down)
                usleep(400_000)
            }
            XCTAssertTrue(run.hasFocus, "the focus on Search for")
            remote.press(.select)
            XCTAssertTrue(element("search.titles").waitForExistence(timeout: 20), "the results; the home saw \(seen)")
            XCTAssertTrue(element("search.transcripts").waitForExistence(timeout: 10), "In the transcript")
            // down past the titles to what was said: the keyboard's rows scroll away, and the
            // moments the word is spoken show
            XCTAssertTrue(app.buttons["search.titles.v-curiosity"].waitForExistence(timeout: 10), "Curiosity among the titles")
            let said = app.buttons["search.transcripts.v-curiosity"]
            // (its card is made as it scrolls in)
            for _ in 0..<12 where !(said.exists && said.hasFocus) {
                remote.press(.down)
                usleep(500_000)
            }
            XCTAssertTrue(said.exists && said.hasFocus, "the focus on what Curiosity says")
            waitForQuiet()
            shot("search")
        }
    }

    /// Signing in with a code: the code the TV shows and the address it can be approved at.
    /// The code and the address are this test's own (the demo home's is a test's).
    func testCode() throws {
        for language in ["en", "ar"] {
            app.terminate()
            home?.stop()
            let home = try MockHome(demo: language)
            home.clock = MockHome.demoClock
            home.stub("POST", "/api/pairing/device", status: 201, body: """
            {"code":"K7M4-P2XR","device_code":"dc","expires_at":"{{now+600}}","verify_path":"/pair?code=K7M4-P2XR",
             "verify_url":"http://living-room-maktaba.local/pair?code=K7M4-P2XR","interval":1}
            """)
            try home.start()
            self.home = home
            self.language = language
            app.launchArguments = ["-uitest-reset", "-AppleLanguages", "(\(language))",
                                   "-AppleLocale", language == "ar" ? "ar_SA" : "en_US"]
            app.launch()
            type(home.url, into: app.textFields["picker.address"])
            pressIfStillThere(app.buttons["picker.connect"], with: .right)
            let code = app.staticTexts["pairing-code"]
            XCTAssertTrue(code.waitForExistence(timeout: 20), "the code; the home saw \(seen)")
            // read out a character at a time ("K 7 M 4 - P 2 X R")
            XCTAssertTrue(wait(timeout: 10) { code.label.hasPrefix("K 7 M 4") }, "this test's code: \(code.label)")
            waitForQuiet()
            shot("code")
        }
    }

    // MARK: helpers

    /// The Arabic subtitles, from the system player's transport bar: Select (it pauses and the
    /// bar shows), up to the buttons, along to Subtitles, its Language, Arabic (choosing one
    /// turns them on); then Play.
    private func arabicSubtitles() {
        remote.press(.select)
        sleep(1)
        remote.press(.up)
        sleep(1)
        let subtitles = app.cells["AVLegibleSettings"].firstMatch
        XCTAssertTrue(subtitles.waitForExistence(timeout: 10), "Subtitles in the transport bar")
        for _ in 0..<5 where !subtitles.hasFocus {
            remote.press(language == "ar" ? .left : .right)
            usleep(600_000)
        }
        XCTAssertTrue(subtitles.hasFocus, "the focus on Subtitles")
        remote.press(.select)
        let languages = app.cells["AVLanguagesMenu"].firstMatch
        XCTAssertTrue(languages.waitForExistence(timeout: 10), "its menu")
        for _ in 0..<5 where !languages.hasFocus {
            remote.press(.down)
            usleep(600_000)
        }
        XCTAssertTrue(languages.hasFocus, "the focus on Language")
        remote.press(.select)
        sleep(1)
        let arabic = app.cells.matching(NSPredicate(format: "label ENDSWITH 'Arabic' OR label ENDSWITH 'العربية'")).firstMatch
        XCTAssertTrue(arabic.waitForExistence(timeout: 10), "Arabic among the languages")
        for _ in 0..<5 where !arabic.hasFocus {
            remote.press(.down)
            usleep(600_000)
        }
        XCTAssertTrue(arabic.hasFocus, "the focus on Arabic")
        remote.press(.select)
        sleep(1)
        // back in the menu, its On checked: Back closes it
        if languages.exists { remote.press(.menu) }
        sleep(1)
        remote.press(.playPause)
    }

    /// Up to the tab bar, along it to `title`, Select.
    private func openTab(_ title: String) {
        let tab = app.tabBars.buttons[title]
        XCTAssertTrue(tab.waitForExistence(timeout: 20), "the \(title) tab")
        for _ in 0..<6 where !app.tabBars.buttons.allElementsBoundByIndex.contains(where: \.hasFocus) {
            remote.press(.up)
            usleep(400_000)
        }
        moveFocus(to: tab, with: language == "ar" ? .left : .right)
        remote.press(.select)
        sleep(1)
    }

    private var seen: [String] { home?.seen ?? [] }

    private func element(_ id: String) -> XCUIElement { app.descendants(matching: .any)[id].firstMatch }

    /// A first launch on the demo home in `language`: its address typed, the code approved.
    private func signIn(_ language: String, clock: Date = MockHome.demoClock) throws {
        app.terminate()
        home?.stop()
        let home = try MockHome(demo: language)
        home.clock = clock
        try home.start()
        self.home = home
        self.language = language
        app.launchArguments = ["-uitest-reset", "-AppleLanguages", "(\(language))",
                               "-AppleLocale", language == "ar" ? "ar_SA" : "en_US"]
        app.launch()
        type(home.url, into: app.textFields["picker.address"])
        pressIfStillThere(app.buttons["picker.connect"], with: .right)
        XCTAssertTrue(app.staticTexts["pairing-code"].waitForExistence(timeout: 20), "the code; the home saw \(seen)")
        home.approve()
        XCTAssertTrue(app.tabBars.firstMatch.waitForExistence(timeout: 20), "approved: signed in")
    }

    /// Along Home's focused row (right in English, left in Arabic) to `video`'s card.
    private func moveFocusInRow(to video: String) {
        let direction: XCUIRemote.Button = language == "ar" ? .left : .right
        let focused = { self.app.descendants(matching: .any).matching(NSPredicate(format: "hasFocus == true")).firstMatch }
        for _ in 0..<8 {
            let f = focused()
            if f.exists, f.identifier.hasPrefix("video.card.\(video)") || f.descendants(matching: .any)
                .matching(NSPredicate(format: "identifier BEGINSWITH %@", "video.card.\(video)")).count > 0 { return }
            remote.press(direction)
            usleep(500_000)
        }
        XCTFail("could not focus \(video)'s card")
    }

    /// Up to the tab bar, along it to Live TV, Select.
    private func openLiveTab(_ title: String) {
        let tab = app.tabBars.buttons[title]
        XCTAssertTrue(tab.waitForExistence(timeout: 20), "the Live TV tab")
        for _ in 0..<4 where !app.tabBars.buttons.allElementsBoundByIndex.contains(where: \.hasFocus) {
            remote.press(.up)
            usleep(400_000)
        }
        moveFocus(to: tab, with: language == "ar" ? .left : .right)
        remote.press(.select)
        sleep(1)
    }

    /// Up to Now / Guide, along to Guide, Select.
    private func openGuide() {
        let now = app.buttons["live.mode.now"], guide = app.buttons["live.mode.guide"]
        XCTAssertTrue(now.waitForExistence(timeout: 20), "Now | Guide")
        for _ in 0..<4 where !(now.hasFocus || guide.hasFocus) {
            remote.press(.up)
            usleep(400_000)
        }
        moveFocus(to: guide, with: language == "ar" ? .left : .right)
        remote.press(.select)
    }

    private func type(_ text: String, into field: XCUIElement) {
        XCTAssertTrue(field.waitForExistence(timeout: 20))
        field.typeExactly(text, in: app) {
            self.moveFocus(to: field, with: .down)
            self.remote.press(.select)
        }
        let done = app.buttons.matching(NSPredicate(format: "label IN[c] {'next', 'done', 'go', 'search', 'التالي', 'تم', 'انتقال', 'بحث'}")).firstMatch
        if done.waitForExistence(timeout: 5) {
            moveFocus(to: done, with: .down)
            remote.press(.select)
        } else {
            remote.press(.menu)
        }
        sleep(1)
    }

    private func pressIfStillThere(_ button: XCUIElement, with direction: XCUIRemote.Button) {
        guard button.waitForExistence(timeout: 2), button.isEnabled, button.isHittable else { return }
        moveFocus(to: button, with: direction)
        remote.press(.select)
    }

    private func moveFocus(to element: XCUIElement, with direction: XCUIRemote.Button) {
        XCTAssertTrue(element.waitForExistence(timeout: 10), "\(element) is not there")
        for _ in 0..<12 where !element.hasFocus {
            remote.press(direction)
            usleep(400_000)
        }
        XCTAssertTrue(element.hasFocus, "could not focus \(element)")
    }

    private func wait(timeout: TimeInterval = 15, _ condition: @escaping () -> Bool) -> Bool {
        let end = Date().addingTimeInterval(timeout)
        while Date() < end {
            if condition() { return true }
            usleep(250_000)
        }
        return condition()
    }

    /// Until the app has asked the home nothing for 2 s (at most 20 s): every picture in.
    private func waitForQuiet() {
        var count = seen.count, since = Date()
        let end = Date().addingTimeInterval(20)
        while Date() < end {
            usleep(200_000)
            if seen.count != count { count = seen.count; since = Date() } else if Date().timeIntervalSince(since) >= 2 { return }
        }
    }

    /// The screen's mean luminance (0 black … 1 white), from a 64 × 64 sample.
    private func meanLuminance() -> Double {
        guard let cg = XCUIScreen.main.screenshot().image.cgImage else { return -1 }
        let w = 64, h = 64
        var pixels = [UInt8](repeating: 0, count: w * h * 4)
        guard let ctx = CGContext(data: &pixels, width: w, height: h, bitsPerComponent: 8, bytesPerRow: w * 4,
                                  space: CGColorSpaceCreateDeviceRGB(),
                                  bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue) else { return -1 }
        ctx.draw(cg, in: CGRect(x: 0, y: 0, width: w, height: h))
        var total = 0.0
        for i in stride(from: 0, to: pixels.count, by: 4) {
            total += (0.2126 * Double(pixels[i]) + 0.7152 * Double(pixels[i + 1]) + 0.0722 * Double(pixels[i + 2])) / 255
        }
        return total / Double(w * h)
    }

    /// The near-white pixels in `area` (shares of the screen): a subtitle line's letters.
    private func brightPixels(in area: CGRect) -> Int {
        guard let whole = XCUIScreen.main.screenshot().image.cgImage,
              let cg = whole.cropping(to: CGRect(x: area.minX * CGFloat(whole.width), y: area.minY * CGFloat(whole.height),
                                                 width: area.width * CGFloat(whole.width), height: area.height * CGFloat(whole.height)))
        else { return -1 }
        var pixels = [UInt8](repeating: 0, count: cg.width * cg.height * 4)
        guard let ctx = CGContext(data: &pixels, width: cg.width, height: cg.height, bitsPerComponent: 8, bytesPerRow: cg.width * 4,
                                  space: CGColorSpaceCreateDeviceRGB(),
                                  bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue) else { return -1 }
        ctx.draw(cg, in: CGRect(x: 0, y: 0, width: cg.width, height: cg.height))
        var count = 0
        for i in stride(from: 0, to: pixels.count, by: 4) where pixels[i] > 235 && pixels[i + 1] > 235 && pixels[i + 2] > 235 {
            count += 1
        }
        return count
    }

    private func shot(_ screen: String) {
        let a = XCTAttachment(screenshot: XCUIScreen.main.screenshot())
        a.name = "shot-appletv-\(screen)-\(language)"
        a.lifetime = .keepAlways
        add(a)
    }
}
