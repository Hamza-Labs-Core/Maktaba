import XCTest

/// Live TV on Apple TV (Epic 48 story 48.34), with the Siri remote, against `MockHome`
/// (served by the test runner on 127.0.0.1; its channels, guide and `tv/info` come from
/// `apps/apple/UITests/Fixtures`). No real home, no real account. With MAKTABA_SHOTS set,
/// each step's screenshot is also written there.
///
/// The channel playlists are stubbed (a master and its media playlist), so a tune reaches the
/// home's web port as the real one would. The media playlist is ten minutes of the mock
/// home's two-second segment (`Fixtures/live-segment.ts`, served for any `/stream/…/*.ts`), so
/// the picture really plays and never runs dry in a test unless one holds a segment (a stall).
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
            home.stub("GET", "/stream/channel/\(ch)/live.m3u8", body: Self.playlist)
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
        // Menu puts a banner away before it leaves: wait for it to go on its own first
        XCTAssertTrue(element("live.player.banner").waitForNonExistence(timeout: 15), "the banner goes")
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
        XCTAssertTrue(element("live.miniguide").waitForNonExistence(timeout: 10))
        XCTAssertTrue(element("live.player.banner").waitForNonExistence(timeout: 15), "the banner goes")
        remote.press(.menu)
        XCTAssertTrue(element("live.player").waitForNonExistence(timeout: 10))
    }

    // MARK: Live TV off on the home (story 51.11)

    /// T09: Live TV off and a viewer: no Live TV tab, the other tabs as always.
    func testLiveTVOffHidesTheTabFromAViewer() throws {
        home.capabilities(off: ["live_tv"], admin: false)
        signIn("en")
        XCTAssertTrue(app.tabBars.buttons["Home"].waitForExistence(timeout: 20), "the tabs")
        XCTAssertTrue(wait(timeout: 15) { self.home.seen.contains("GET /api/capabilities") }, "the TV asked what is on")
        XCTAssertTrue(wait(timeout: 10) { !self.app.tabBars.buttons["Live TV"].exists }, "no Live TV tab")
        XCTAssertTrue(app.tabBars.buttons["Libraries"].exists)
        XCTAssertTrue(app.tabBars.buttons["Search"].exists)
        shot("tv-live-off-viewer")
    }

    /// Live TV off and an admin: the tab stays and says Live TV is off, and where to turn it on.
    func testLiveTVOffExplainsItToAnAdmin() throws {
        home.capabilities(off: ["live_tv"], admin: true)
        signIn("en")
        openLiveTab("Live TV")
        let note = element("feature.off.live_tv")
        XCTAssertTrue(note.waitForExistence(timeout: 20), "the admin note; the home saw \(home.seen)")
        XCTAssertEqual(note.label, "Live TV is off on this home.")
        XCTAssertTrue(element("feature.turnOn").label.contains("/settings/components"), "where to turn it on")
        XCTAssertFalse(app.buttons["live.channel.ch-1"].exists, "no channels")
        shot("tv-live-off-admin")
    }

    /// The bug: a channel slower to start than the banner's six seconds kept the
    /// banner up for good. Here the playlist is held until the banner's time has passed (a
    /// latch: a 9 s delay raced the checks on a loaded runner): the banner stays while it
    /// starts, then goes 6 s after the picture plays; Play/Pause brings it back, Menu puts it away. The
    /// picture fills the screen edge to edge, and nothing is drawn over the top-right corner,
    /// where the home burns the channel's logo into the picture.
    func testBannerHidesAfterASlowStart() throws {
        home.hold("GET", "/stream/channel/ch-1/index.m3u8")
        signIn("en")
        openLiveTab("Live TV")
        let first = app.buttons["live.channel.ch-1"]
        moveFocus(to: first, with: .down)
        remote.press(.select)

        let player = element("live.player")
        XCTAssertTrue(player.waitForExistence(timeout: 10), "the live player")
        let banner = element("live.player.banner")
        XCTAssertTrue(banner.waitForExistence(timeout: 5), "the banner on the tune")
        sleep(7)
        XCTAssertTrue(banner.exists, "it stays while the channel starts")
        XCTAssertTrue(element("live.player.starting").exists, "still starting after the banner's time")

        // the whole picture, nothing cropped; the watermark's corner kept clear (checked while
        // the start is held, so the banner's 6 s never race these queries on a loaded runner)
        let screen = app.windows.firstMatch.frame
        let video = element("live.player.video")
        XCTAssertTrue(video.waitForExistence(timeout: 5), "the picture")
        XCTAssertEqual(video.frame, screen, "the picture fills the screen")
        let corner = CGRect(x: screen.maxX - screen.width * 0.16, y: screen.minY,
                            width: screen.width * 0.16, height: screen.height * 0.2)
        XCTAssertTrue(banner.exists)
        XCTAssertFalse(banner.frame.intersects(corner), "the banner sits clear of the top-right corner: \(banner.frame)")

        home.release()
        XCTAssertTrue(element("live.player.starting").waitForNonExistence(timeout: 30), "the channel plays")
        shot("tv-live-slow-start-playing")

        XCTAssertTrue(banner.waitForNonExistence(timeout: 15), "the banner goes after a slow start")
        XCTAssertTrue(player.exists, "and the channel keeps playing")
        shot("tv-live-slow-start-clear")

        remote.press(.playPause)
        XCTAssertTrue(banner.waitForExistence(timeout: 5), "Play/Pause shows the banner again")
        remote.press(.menu)
        XCTAssertTrue(banner.waitForNonExistence(timeout: 5), "Menu puts it away")
        XCTAssertTrue(player.exists, "without leaving the channel")

        remote.press(.select)
        let mini = element("live.miniguide")
        XCTAssertTrue(mini.waitForExistence(timeout: 10), "the mini-guide")
        XCTAssertFalse(mini.frame.intersects(corner), "clear of the top-right corner too")
        XCTAssertTrue(mini.waitForNonExistence(timeout: 15), "the mini-guide goes on its own")
        remote.press(.menu)
        XCTAssertTrue(player.waitForNonExistence(timeout: 10), "Menu leaves the player")
    }

    /// The bug: the TV's home had Continue watching and nothing else. Every row of
    /// the Player's Home is there, with its posters; a row that fails stays as a retry tile.
    func testHomeHasEveryRow() throws {
        home.stub("GET", "/api/channels/now", status: 500, body: #"{"title":"down","status":500}"#)
        signIn("en")
        let rows = ["continue", "ondeck", "live", "favorites", "added", "watched", "pinned-lib-lectures", "rec-for_you"]
        XCTAssertTrue(element("home.row.continue").waitForExistence(timeout: 20), "Home; the home saw \(home.seen)")
        for row in rows {
            XCTAssertTrue(element("home.row.\(row)").exists, "the \(row) row")
        }
        XCTAssertTrue(home.seen.contains("GET /api/home"), "Home's rows from /api/home")
        XCTAssertTrue(app.staticTexts["Recently added"].exists && app.staticTexts["Up next"].exists, "the row titles")
        XCTAssertTrue(element("video.card.v2.art").waitForExistence(timeout: 20), "a card's poster")
        shot("tv-home-en")

        // On now could not be read: a retry tile in its place; Select reads again
        let retry = element("home.row.live").descendants(matching: .any)["home.row.retry"].firstMatch
        XCTAssertTrue(retry.exists, "a failed row stays as a retry tile")
        home.reset()
        moveFocus(to: retry, with: .down)
        remote.press(.select)
        XCTAssertTrue(element("home.live.ch-1").waitForExistence(timeout: 20), "On now, read again")
    }

    /// Home in Arabic: the rows' titles are the app's own, translated.
    func testHomeHasEveryRowArabic() throws {
        signIn("ar")
        XCTAssertTrue(element("home.row.added").waitForExistence(timeout: 20), "Home")
        XCTAssertTrue(app.staticTexts["أضيف حديثًا"].exists, "Recently added in Arabic")
        XCTAssertTrue(app.staticTexts["يُعرض الآن"].exists, "On now in Arabic")
        shot("tv-home-ar")
    }

    /// The bug: the libraries had no pictures. Each tile shows its newest video's.
    func testLibraryTilesHaveArt() throws {
        signIn("en")
        let tab = app.tabBars.buttons["Libraries"]
        XCTAssertTrue(tab.waitForExistence(timeout: 20))
        for _ in 0..<4 where !app.tabBars.buttons.allElementsBoundByIndex.contains(where: \.hasFocus) {
            remote.press(.up)
            usleep(400_000)
        }
        moveFocus(to: tab, with: .right)
        remote.press(.select)
        XCTAssertTrue(wait { self.home.seen.contains("GET /api/libraries/summary") }, "the libraries with their art")
        for lib in ["lib-docs", "lib-lectures"] {
            XCTAssertTrue(element("library.card.\(lib).art").waitForExistence(timeout: 20), "\(lib)'s picture")
        }
        XCTAssertTrue(home.seen.contains("GET /api/videos/v12/poster"), "the newest video's poster, with the token")
        shot("tv-libraries-en")
    }

    /// The bug: a buffering stall did not change the playback phase, so the banner
    /// did not come back. Segment 6 is held (a latch): the picture plays twelve seconds, the
    /// banner goes, then the picture waits for data. The buffering state and the banner show;
    /// when the segment comes, buffering goes at once and the banner after its time. (AVPlayer
    /// on CI gave up on the held segment after three tries and waited for good; the player
    /// then opens the channel again after `LivePlayback.stuck`, which this covers too.)
    func testABufferingStallShowsTheBanner() throws {
        home.hold("GET", "/stream/channel/ch-1/seg6.ts")
        signIn("en")
        openLiveTab("Live TV")
        let first = app.buttons["live.channel.ch-1"]
        moveFocus(to: first, with: .down)
        remote.press(.select)

        let banner = element("live.player.banner")
        XCTAssertTrue(banner.waitForExistence(timeout: 10), "the banner on the tune")
        XCTAssertTrue(element("live.player.starting").waitForNonExistence(timeout: 30), "the channel plays")
        XCTAssertTrue(banner.waitForNonExistence(timeout: 15), "the banner goes while the picture plays")

        let buffering = element("live.player.buffering")
        XCTAssertTrue(buffering.waitForExistence(timeout: 40),
                      "the picture waits for the held segment: buffering; the home saw \(home.seen.suffix(12))")
        XCTAssertTrue(banner.exists, "the banner comes back with it")
        XCTAssertTrue(home.seen.contains("GET /stream/channel/ch-1/seg6.ts"), "the player asked for the held segment")
        shot("tv-live-buffering-en")

        home.release()
        XCTAssertTrue(buffering.waitForNonExistence(timeout: 30),
                      "buffering goes when the picture moves again; segments asked: \(home.seen.filter { $0.hasSuffix(".ts") }.suffix(20))")
        XCTAssertTrue(element("live.player").exists, "on the same channel")
        XCTAssertTrue(banner.waitForNonExistence(timeout: 15), "and the banner after its time")
    }

    /// The bug: the banner could cover a channel logo the home burns into a bottom
    /// corner. With ch-1's logo bottom right (`watermark_position`), the banner and the
    /// mini-guide sit at the top, clear of that corner.
    func testTheBannerKeepsABottomCornerLogoClear() throws {
        let fixture = try XCTUnwrap(home.fixture(named: "channels-now"))
        let logo = #""logo_url": "/api/channels/ch-1/logo""#
        let moved = fixture.replacingOccurrences(of: logo, with: logo + #", "watermark_position": "bottom-right""#)
        XCTAssertNotEqual(moved, fixture, "ch-1's logo corner set")
        home.stub("GET", "/api/channels/now", body: moved)
        // the channel kept starting (a latch), so the banner stays for the checks: a playing
        // channel's banner goes after 6 s, which a loaded runner's checks outlasted
        home.hold("GET", "/stream/channel/ch-1/index.m3u8")
        signIn("en")
        openLiveTab("Live TV")
        let first = app.buttons["live.channel.ch-1"]
        moveFocus(to: first, with: .down)
        remote.press(.select)

        let banner = element("live.player.banner")
        XCTAssertTrue(banner.waitForExistence(timeout: 10), "the banner on the tune")
        XCTAssertTrue(channelLabel(contains: "Lectures TV"))
        let screen = app.windows.firstMatch.frame
        let corner = CGRect(x: screen.maxX - screen.width * 0.16, y: screen.maxY - screen.height * 0.2,
                            width: screen.width * 0.16, height: screen.height * 0.2)
        XCTAssertFalse(banner.frame.intersects(corner), "the banner sits clear of the bottom-right corner: \(banner.frame)")
        XCTAssertLessThan(banner.frame.midY, screen.midY, "at the top of the picture")
        shot("tv-live-banner-top-en")

        remote.press(.select)
        let mini = element("live.miniguide")
        XCTAssertTrue(mini.waitForExistence(timeout: 10), "the mini-guide")
        XCTAssertFalse(mini.frame.intersects(corner), "the mini-guide too: \(mini.frame)")
        XCTAssertLessThan(mini.frame.minY, screen.midY, "from the top")
        shot("tv-live-miniguide-top-en")
    }

    // MARK: helpers

    /// Ten minutes of two-second segments, each after a discontinuity (the one segment's
    /// timestamps start again), ended: the picture plays from the start and never runs dry.
    static let playlist: String = {
        var out = "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:2\n#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-PLAYLIST-TYPE:VOD\n"
        for i in 0..<300 {
            if i > 0 { out += "#EXT-X-DISCONTINUITY\n" }
            out += "#EXTINF:2.000,\nseg\(i).ts\n"
        }
        return out + "#EXT-X-ENDLIST\n"
    }()

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
