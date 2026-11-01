import XCTest

/// Live TV on Apple TV (Epic 48 story 48.34), with the Siri remote, against `MockHome`
/// (served by the test runner on 127.0.0.1; its channels, guide and `tv/info` come from
/// `apps/apple/UITests/Fixtures`). No real home, no real account. With MAKTABA_SHOTS set,
/// each step's screenshot is also written there.
///
/// The channel playlists are stubbed (a master and its media playlist, no segment), so a
/// tune reaches the home's web port as the real one would; the picture itself stays black.
@MainActor
final class TVLiveUITests: XCTestCase {
    let remote = XCUIRemote.shared
    var app: XCUIApplication!
    var home: MockHome!
    private var language = "en"

    override func setUp() async throws {
        continueAfterFailure = false
        home = try MockHome()
        try home.start()
        // playing asks for a token that lasts (the pairing's is not a JWT): a refresh gives one
        home.stub("POST", "/api/auth/refresh", body: #"{"access_token":"\#(Self.jwt)","refresh_token":"ui-refresh-2"}"#)
        for ch in ["ch-1", "ch-2", "ch-3"] {
            home.stub("GET", "/stream/channel/\(ch)/index.m3u8",
                      body: "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=800000,RESOLUTION=1280x720\nlive.m3u8\n")
            home.stub("GET", "/stream/channel/\(ch)/live.m3u8",
                      body: "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:6\n#EXT-X-MEDIA-SEQUENCE:1\n#EXTINF:6.0,\nseg1.ts\n")
        }
        app = XCUIApplication()
    }

    override func tearDown() async throws {
        home?.stop()
    }

    /// Now, the guide, a programme's page, tuning, ▲ / ▼ through the channels, the
    /// mini-guide, Menu back, in English.
    func testLiveTV() throws {
        signIn("en")
        openLiveTab("Live TV")

        // Now: every channel with what is on
        let first = app.buttons["live.channel.ch-1"]
        XCTAssertTrue(first.waitForExistence(timeout: 20), "the Now grid; the home saw \(home.seen)")
        XCTAssertTrue(app.buttons["live.channel.ch-2"].exists)
        XCTAssertTrue(app.buttons["live.channel.ch-3"].exists)
        XCTAssertTrue(home.seen.contains("GET /api/channels/now"))
        XCTAssertTrue(first.label.contains("Tafsir al-Kahf, part 2"), "what is on ch-1: \(first.label)")
        moveFocus(to: first, with: .down)
        shot("tv-live-now-en")

        // the guide: a focusable grid; what is on now carries the channel's id
        openGuide()
        let onNow = app.buttons["live.guide.ch-1.now"]
        XCTAssertTrue(onNow.waitForExistence(timeout: 10), "the guide")
        let guideAsk = home.requests.first { $0.path == "/api/channels/guide" }
        XCTAssertNotNil(guideAsk?.value("start"), "the guide asks a window")
        XCTAssertNotNil(guideAsk?.value("end"))
        remote.press(.down)
        XCTAssertTrue(wait(timeout: 5) { onNow.hasFocus }, "down into the guide lands on what is on now")
        shot("tv-live-guide-en")

        // a later programme shows its page (no Watch: it has not started)
        remote.press(.right)
        usleep(500_000)
        XCTAssertFalse(onNow.hasFocus, "focus moved along the row")
        remote.press(.select)
        let detail = app.staticTexts["live.detail.title"]
        XCTAssertTrue(detail.waitForExistence(timeout: 10), "a later programme's page")
        XCTAssertFalse(app.buttons["live.detail.watch"].exists, "nothing to watch yet")
        shot("tv-live-programme-en")
        remote.press(.menu)
        XCTAssertTrue(detail.waitForNonExistence(timeout: 10))

        // Select on what is on now tunes
        moveFocus(to: onNow, with: .left)
        remote.press(.select)
        let player = element("live.player")
        XCTAssertTrue(player.waitForExistence(timeout: 10), "the live player")
        XCTAssertTrue(channelLabel(contains: "Lectures TV"), "tuned to channel 1")
        XCTAssertTrue(wait { self.home.seen.contains("GET /api/tv/info") }, "the path from tv/info")
        XCTAssertTrue(wait { self.home.seen.contains("GET /stream/channel/ch-1/index.m3u8") },
                      "channel 1's playlist through the web port; the home saw \(home.seen)")
        shot("tv-live-player-en")

        // ▲: the next channel; ▼ ▼: back past the first to the last
        remote.press(.up)
        XCTAssertTrue(channelLabel(contains: "قناة السيرة"), "▲ tunes channel 2")
        XCTAssertTrue(wait { self.home.seen.contains("GET /stream/channel/ch-2/index.m3u8") }, "channel 2's playlist")
        remote.press(.down)
        XCTAssertTrue(channelLabel(contains: "Lectures TV"), "▼ goes back to channel 1")
        remote.press(.down)
        XCTAssertTrue(channelLabel(contains: "Documentaries"), "▼ from the first wraps to the last")
        XCTAssertTrue(wait { self.home.seen.contains("GET /stream/channel/ch-3/index.m3u8") }, "channel 3's playlist")

        // Select: the mini-guide, this channel marked; Menu closes it, then the player
        remote.press(.select)
        let mini = element("live.miniguide")
        XCTAssertTrue(mini.waitForExistence(timeout: 10), "the mini-guide")
        XCTAssertTrue(element("live.miniguide.current").label.contains("Documentaries"))
        XCTAssertTrue(element("live.miniguide.ch-1").exists, "its neighbours")
        shot("tv-live-miniguide-en")
        remote.press(.menu)
        XCTAssertTrue(mini.waitForNonExistence(timeout: 10), "Menu closes the mini-guide")
        XCTAssertTrue(player.exists, "and the channel keeps playing")
        remote.press(.menu)
        XCTAssertTrue(player.waitForNonExistence(timeout: 10), "Menu again leaves the player")
        XCTAssertTrue(onNow.waitForExistence(timeout: 10), "back on the guide")
    }

    /// The same in Arabic: the tab, Now and the guide right to left, the player and the
    /// mini-guide (screenshots to look at).
    func testLiveTVArabic() throws {
        signIn("ar")
        openLiveTab("البث المباشر")

        let first = app.buttons["live.channel.ch-1"]
        XCTAssertTrue(first.waitForExistence(timeout: 20), "the Now grid")
        XCTAssertTrue(app.buttons["live.channel.ch-2"].label.contains("السيرة النبوية، الدرس ١١"), "what is on ch-2")
        moveFocus(to: first, with: .down)
        shot("tv-live-now-ar")

        // right to left: Guide is to the left of Now
        openGuide()
        let onNow = app.buttons["live.guide.ch-1.now"]
        XCTAssertTrue(onNow.waitForExistence(timeout: 10), "the guide")
        XCTAssertTrue(app.buttons["live.guide.ch-2.now"].exists)
        remote.press(.down)
        XCTAssertTrue(wait(timeout: 5) { onNow.hasFocus }, "down lands on now")
        shot("tv-live-guide-ar")

        remote.press(.select)
        XCTAssertTrue(element("live.player").waitForExistence(timeout: 10), "the live player")
        XCTAssertTrue(channelLabel(contains: "Lectures TV"))
        remote.press(.up)
        XCTAssertTrue(channelLabel(contains: "قناة السيرة"), "▲ tunes channel 2")
        shot("tv-live-player-ar")
        remote.press(.select)
        XCTAssertTrue(element("live.miniguide").waitForExistence(timeout: 10))
        shot("tv-live-miniguide-ar")
        remote.press(.menu)
        remote.press(.menu)
        XCTAssertTrue(element("live.player").waitForNonExistence(timeout: 10))
    }

    // MARK: helpers

    /// A JWT (unsigned) whose `exp` is in 2100: MaktabaKit reads only its expiry.
    static var jwt: String {
        func b64(_ s: String) -> String {
            Data(s.utf8).base64EncodedString()
                .replacingOccurrences(of: "+", with: "-").replacingOccurrences(of: "/", with: "_")
                .replacingOccurrences(of: "=", with: "")
        }
        return b64(#"{"alg":"none","typ":"JWT"}"#) + "." + b64(#"{"sub":"u1","exp":4102444800}"#) + ".sig"
    }

    private func element(_ id: String) -> XCUIElement { app.descendants(matching: .any)[id].firstMatch }

    /// The player's banner names a channel (number · name), within a few seconds.
    private func channelLabel(contains name: String) -> Bool {
        let label = element("live.player.channel")
        return wait(timeout: 10) { label.exists && label.label.contains(name) }
    }

    /// First launch: the mock home's address typed, then the code approved elsewhere.
    private func signIn(_ language: String) {
        self.language = language
        app.launchArguments = ["-uitest-reset", "-AppleLanguages", "(\(language))",
                               "-AppleLocale", language == "ar" ? "ar_SA" : "en_US"]
        app.launch()
        type(home.url, into: app.textFields["picker.address"])
        pressIfStillThere(app.buttons["picker.connect"], with: .right)
        XCTAssertTrue(app.staticTexts["pairing-code"].waitForExistence(timeout: 20), "the code; the home saw \(home.seen)")
        home.approve()
        XCTAssertTrue(app.tabBars.firstMatch.waitForExistence(timeout: 20), "approved: signed in")
    }

    /// Up to the tab bar, along it to Live TV (after Libraries: right in English, left in
    /// Arabic), then down into the tab.
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

    /// Up to Now / Guide, along to Guide (right in English, left in Arabic), Select.
    private func openGuide() {
        let now = app.buttons["live.mode.now"], guide = app.buttons["live.mode.guide"]
        for _ in 0..<4 where !(now.hasFocus || guide.hasFocus) {
            remote.press(.up)
            usleep(400_000)
        }
        moveFocus(to: guide, with: language == "ar" ? .left : .right)
        remote.press(.select)
    }

    private func type(_ text: String, into field: XCUIElement) {
        XCTAssertTrue(field.waitForExistence(timeout: 20))
        moveFocus(to: field, with: .down)
        remote.press(.select)
        app.typeText(text)
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

    private func shot(_ name: String) {
        let s = XCUIScreen.main.screenshot()
        let a = XCTAttachment(screenshot: s)
        a.name = name
        a.lifetime = .keepAlways
        add(a)
        if let dir = ProcessInfo.processInfo.environment["MAKTABA_SHOTS"], !dir.isEmpty {
            try? FileManager.default.createDirectory(atPath: dir, withIntermediateDirectories: true)
            try? s.pngRepresentation.write(to: URL(fileURLWithPath: dir).appendingPathComponent("\(name).png"))
        }
    }
}
