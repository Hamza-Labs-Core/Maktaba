import XCTest

/// Live TV in the Player (Epic 48 story 48.21, FR-21 to FR-24) on iPhone and iPad, against
/// `MockHome` (its channels, guide, `tv/info` and remote session from `UITests/Fixtures`; the
/// channel playlists stubbed here, their segments two seconds of ink). No real home.
final class PlayerLiveUITests: PlayerUITestCase {
    override func setUp() async throws {
        try await super.setUp()
        // playing asks for a token that lasts (the pairing's is not a JWT): a refresh gives one
        home.stub("POST", "/api/auth/refresh", body: #"{"access_token":"\#(Self.jwt)","refresh_token":"ui-refresh-2"}"#)
        for ch in ["ch-1", "ch-2", "ch-3"] {
            stubPlaylists("/stream/channel/\(ch)")
        }
        stubPlaylists("/stream/remote/rs-1")
    }

    /// A master and a live media playlist (no end) of three segments under `base`.
    private func stubPlaylists(_ base: String) {
        home.stub("GET", "\(base)/index.m3u8",
                  body: "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=200000,RESOLUTION=160x90\nlive.m3u8\n")
        home.stub("GET", "\(base)/live.m3u8",
                  body: "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:2\n#EXT-X-MEDIA-SEQUENCE:1\n"
                      + "#EXTINF:2.0,\nseg1.ts\n#EXTINF:2.0,\nseg2.ts\n#EXTINF:2.0,\nseg3.ts\n")
    }

    // MARK: Now

    /// Now lists every channel the home lets the person watch, with what is on and its
    /// progress and what is next; the section shows (the mock has channels).
    func testNowListsTheChannelsWithProgress() {
        launchSignedIn(section: "live")
        XCTAssertTrue(sectionButton("Live TV").waitForExistence(timeout: 15), "the Live TV section shows: the home has channels")
        XCTAssertTrue(element("screen.liveTV").waitForExistence(timeout: 10))
        let first = element("live.channel.ch-1")
        XCTAssertTrue(first.waitForExistence(timeout: 15), "the Now list; the home saw \(home.seen)")
        XCTAssertTrue(element("live.channel.ch-2").exists)
        XCTAssertTrue(element("live.channel.ch-3").exists)
        XCTAssertTrue(home.seen.contains("GET /api/channels/now"))
        XCTAssertTrue(first.label.contains("Lectures TV"), first.label)
        XCTAssertTrue(first.label.contains("Tafsir al-Kahf, part 2"), "what is on ch-1: \(first.label)")
        XCTAssertTrue(first.label.contains("%"), "its progress is read out: \(first.label)")
        XCTAssertGreaterThanOrEqual(app.descendants(matching: .any).matching(identifier: "live.progress").count, 3, "a progress bar per channel")
        shot("live-now")

        // tapping a channel opens its live player
        first.tap()
        XCTAssertTrue(element("screen.channel").waitForExistence(timeout: 10), "the live player")
        XCTAssertTrue(channel(contains: "Lectures TV"))
    }

    /// No channel for this person: the Live TV section is not offered at all.
    func testNoChannelsHidesTheSection() {
        home.stub("GET", "/api/channels/now", body: #"{"server_time":"2026-11-01T10:00:00Z","channels":[]}"#)
        launchSignedIn()
        XCTAssertTrue(element("screen.home").waitForExistence(timeout: 20), "signed in: Home")
        XCTAssertTrue(sectionButton("Libraries").exists, "the other sections show")
        XCTAssertTrue(home.seen.contains("GET /api/channels/now"), "the home was asked")
        XCTAssertFalse(app.tabBars.buttons["Live TV"].exists, "no Live TV tab")
        XCTAssertFalse(app.buttons["Live TV"].exists, "no Live TV section")
        shot("live-hidden")
    }

    // MARK: Guide

    /// The guide: channels × time with the now-line at the right x; it scrolls sideways
    /// through the time; a programme's sheet opens, and Watch tunes what is on now.
    func testGuideNowLineScrollAndProgrammeSheet() {
        launchSignedIn(section: "live")
        openGuide()
        let onNow = element("live.guide.ch-1.now")
        XCTAssertTrue(onNow.waitForExistence(timeout: 15), "the guide; the home saw \(home.seen)")
        let ask = home.requests.first { $0.path == "/api/channels/guide" }
        XCTAssertNotNil(ask?.value("start"), "the guide asks a window")
        XCTAssertNotNil(ask?.value("end"))

        // the now-line: the fixture's window starts an hour ago, so 60 minutes in
        let line = element("live.guide.nowline")
        let timeline = element("live.guide.timeline")
        XCTAssertTrue(line.waitForExistence(timeout: 5), "the now-line")
        let perMinute: CGFloat = isPad ? 5 : 4
        let x = line.frame.midX - timeline.frame.minX
        XCTAssertEqual(x, 60 * perMinute, accuracy: 10, "the now-line at an hour in (x \(x))")
        XCTAssertTrue(onNow.frame.minX <= line.frame.midX && line.frame.midX <= onNow.frame.maxX,
                      "the now-line crosses what is on now")
        shot("live-guide")

        // sideways through the time
        let before = timeline.frame.minX
        element("live.guide.times").swipeLeft()
        XCTAssertTrue(wait { timeline.frame.minX < before - 50 }, "the time scrolled (\(before) → \(timeline.frame.minX))")
        element("live.guide.times").swipeRight()
        element("live.guide.times").swipeRight()

        // a later programme: its sheet, no Watch (it has not started)
        let later = app.buttons.matching(NSPredicate(format: "identifier BEGINSWITH 'live.guide.ch-1.' AND NOT identifier ENDSWITH '.now'"))
            .allElementsBoundByIndex.first { $0.isHittable }
        XCTAssertNotNil(later, "a later programme on ch-1")
        XCTAssertTrue(openSheet(later), "the programme's sheet")
        XCTAssertFalse(element("live.detail.watch").exists, "nothing to watch yet")
        app.buttons["live.detail.close"].tap()
        XCTAssertTrue(element("live.detail.title").waitForNonExistence(timeout: 10))

        // what is on now: its sheet with its description, and Watch tunes it
        XCTAssertTrue(openSheet(onNow), "what is on now's sheet")
        XCTAssertTrue(app.staticTexts["The story of the people of the cave."].exists || element("live.detail.desc").exists, "its description")
        shot("live-programme")
        app.buttons["live.detail.watch"].tap()
        XCTAssertTrue(element("screen.channel").waitForExistence(timeout: 10), "Watch opens the live player")
        XCTAssertTrue(channel(contains: "Lectures TV"))
    }

    /// Up and down through many channels.
    func testGuideScrollsThroughTheChannels() {
        home.stub("GET", "/api/channels/guide", body: Self.bigGuide(channels: 16))
        launchSignedIn(section: "live")
        openGuide()
        let top = element("live.guide.g-1.now")
        XCTAssertTrue(top.waitForExistence(timeout: 15), "the guide of 16 channels")
        let before = top.frame.minY
        top.swipeUp()
        XCTAssertTrue(wait { top.frame.minY < before - 100 }, "the channels scrolled up (\(before) → \(top.frame.minY))")
        XCTAssertTrue(element("live.guide.g-16.now").waitForExistence(timeout: 5))
    }

    // MARK: the live player

    /// At home: the channel's playlist through the web port (and it plays); next and previous
    /// channel (wrapping round), the mini-guide; pause, then "Back to live".
    func testPlayerPlaysSurfsAndGoesBackToLive() {
        launchSignedIn(section: "live", open: "channel/ch-1")
        XCTAssertTrue(element("screen.channel").waitForExistence(timeout: 20), "the live player")
        XCTAssertTrue(channel(contains: "Lectures TV"), "tuned to channel 1")
        XCTAssertTrue(wait { self.home.seen.contains("GET /api/tv/info") }, "the path from tv/info")
        XCTAssertTrue(wait { self.home.seen.contains("GET /stream/channel/ch-1/index.m3u8") }, "channel 1's playlist; the home saw \(home.seen)")
        XCTAssertTrue(wait(timeout: 20) { self.home.seen.contains { $0.hasPrefix("GET /stream/channel/ch-1/seg") } },
                      "its segments are read: it plays; the home saw \(home.seen)")
        XCTAssertFalse(home.seen.contains("POST /api/tv/remote/tune"), "at home: no remote session")
        // story 48.24: AirPlay and Picture in Picture on the live player
        XCTAssertTrue(app.buttons["live.player.airplay"].waitForExistence(timeout: 10), "the AirPlay button")
        XCTAssertEqual(app.buttons["live.player.airplay"].label, "AirPlay")
        shot("live-player")

        app.buttons["live.player.next"].tap()
        XCTAssertTrue(channel(contains: "قناة السيرة"), "next: channel 2")
        XCTAssertTrue(wait { self.home.seen.contains("GET /stream/channel/ch-2/index.m3u8") }, "channel 2's playlist")
        app.buttons["live.player.previous"].tap()
        XCTAssertTrue(channel(contains: "Lectures TV"), "previous: back to channel 1")
        app.buttons["live.player.previous"].tap()
        XCTAssertTrue(channel(contains: "Documentaries"), "previous from the first wraps to the last")
        XCTAssertTrue(wait { self.home.seen.contains("GET /stream/channel/ch-3/index.m3u8") }, "channel 3's playlist")

        // the mini-guide: this channel marked, its neighbours; tapping one tunes it
        app.buttons["live.player.miniGuide"].tap()
        XCTAssertTrue(element("live.miniguide").waitForExistence(timeout: 5))
        XCTAssertTrue(element("live.miniguide.current").label.contains("Documentaries"))
        shot("live-miniguide")
        element("live.miniguide.ch-1").tap()
        XCTAssertTrue(channel(contains: "Lectures TV"), "the mini-guide tunes")

        // pause: "Back to live" until it is pressed
        let pause = app.buttons["live.player.pause"]
        XCTAssertTrue(pause.waitForExistence(timeout: 20), "playing: the pause button")
        XCTAssertFalse(app.buttons["live.player.backToLive"].exists, "live: nothing to go back to")
        pause.tap()
        let back = app.buttons["live.player.backToLive"]
        XCTAssertTrue(back.waitForExistence(timeout: 5), "after a pause: Back to live")
        shot("live-paused")
        back.tap()
        XCTAssertTrue(back.waitForNonExistence(timeout: 5), "back at the live edge")

        app.buttons["live.player.back"].tap()
        XCTAssertTrue(element("screen.liveTV").waitForExistence(timeout: 10), "back on Live TV")
    }

    /// Away from home (`tv/info` says `via_relay`): it tunes a remote session, plays its URL
    /// through the relay loader, heartbeats (every 2 s here: the beat's own clock is the
    /// fake one in MaktabaKit's LiveRelayTests), and ends the session on leaving.
    func testAwayItTunesHeartbeatsAndEndsOnLeave() {
        home.stub("GET", "/api/tv/info", body: #"{"hls_path_template":"/stream/channel/{id}/index.m3u8","remote":{"available":true,"via_relay":true,"max_streams":2,"token_ttl_minutes":60}}"#)
        launch(section: "live", open: "channel/ch-1", extra: ["-maktaba-live-heartbeat", "2"])
        signIn()
        XCTAssertTrue(element("screen.channel").waitForExistence(timeout: 20), "the live player")
        XCTAssertTrue(wait { self.home.seen.contains("POST /api/tv/remote/tune") }, "a remote session; the home saw \(home.seen)")
        let tune = home.requests.first { $0.path == "/api/tv/remote/tune" }
        XCTAssertTrue(String(decoding: tune?.body ?? Data(), as: UTF8.self).contains(#""channel_id":"ch-1""#), "for channel 1")
        XCTAssertTrue(wait { self.home.seen.contains("GET /stream/remote/rs-1/live.m3u8") },
                      "the session's playlists through the loader; the home saw \(home.seen)")
        XCTAssertFalse(home.seen.contains("GET /stream/channel/ch-1/index.m3u8"), "not the LAN playlist")
        XCTAssertTrue(wait(timeout: 20) { self.home.seen.contains("POST /api/tv/remote/sessions/rs-1/heartbeat") }, "a heartbeat")
        XCTAssertTrue(wait(timeout: 20) { self.home.seen.filter { $0 == "GET /stream/remote/rs-1/live.m3u8" }.count >= 2 },
                      "the live playlist reloads through the loader")
        shot("live-away")
        XCTAssertFalse(home.seen.contains("DELETE /api/tv/remote/sessions/rs-1"))
        app.buttons["live.player.back"].tap()
        XCTAssertTrue(wait { self.home.seen.contains("DELETE /api/tv/remote/sessions/rs-1") }, "leaving ends the session; the home saw \(home.seen.suffix(12))")
        let beats = home.seen.filter { $0 == "POST /api/tv/remote/sessions/rs-1/heartbeat" }.count
        sleep(5)
        XCTAssertEqual(home.seen.filter { $0 == "POST /api/tv/remote/sessions/rs-1/heartbeat" }.count, beats, "no beat after leaving")
    }

    // MARK: Arabic

    /// Arabic: Now, the guide (time runs right to left) and the player, mirrored.
    func testLiveTVInArabic() {
        launchSignedIn("ar", section: "live")
        XCTAssertTrue(sectionButton("البث المباشر").waitForExistence(timeout: 15))
        let first = element("live.channel.ch-1")
        XCTAssertTrue(first.waitForExistence(timeout: 15))
        XCTAssertTrue(element("live.channel.ch-2").label.contains("السيرة النبوية"), element("live.channel.ch-2").label)
        XCTAssertTrue(app.buttons["الآن"].exists || app.segmentedControls.buttons["الآن"].exists, "Now in Arabic")
        shot("live-now")
        openGuide(label: "الدليل")
        let onNow = element("live.guide.ch-1.now")
        XCTAssertTrue(onNow.waitForExistence(timeout: 15))
        let line = element("live.guide.nowline")
        XCTAssertTrue(line.waitForExistence(timeout: 5))
        XCTAssertLessThan(line.frame.midX, element("live.guide.timeline").frame.maxX, "time runs right to left")
        shot("live-guide")
        XCTAssertTrue(openSheet(onNow), "what is on now's sheet")
        XCTAssertTrue(app.buttons["live.detail.watch"].waitForExistence(timeout: 5))
        app.buttons["live.detail.watch"].tap()
        XCTAssertTrue(element("screen.channel").waitForExistence(timeout: 10))
        XCTAssertTrue(channel(contains: "Lectures TV"))
        shot("live-player")
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

    /// A guide of `channels` channels (`g-1` …), each with one programme on now.
    static func bigGuide(channels: Int) -> String {
        let f = ISO8601DateFormatter()
        let now = Date()
        func t(_ s: TimeInterval) -> String { f.string(from: now.addingTimeInterval(s)) }
        let rows = (1...channels).map { n in
            #"{"channel":{"id":"g-\#(n)","number":\#(100 + n),"name":"Channel \#(n)","slug":"g\#(n)"},"blocks":[{"channel_id":"g-\#(n)","kind":"program","start":"\#(t(-1200))","stop":"\#(t(2400))","title":"Programme \#(n)","is_live":true}]}"#
        }
        return #"{"server_time":"\#(t(0))","start":"\#(t(-1800))","end":"\#(t(9000))","channels":[\#(rows.joined(separator: ","))]}"#
    }

    /// Taps a programme and waits for its sheet. A tap while the guide still glides after a
    /// swipe only stops the scrolling (as a finger's does), so it waits for the guide to settle
    /// and taps once more if no sheet came.
    private func openSheet(_ block: XCUIElement?) -> Bool {
        guard let block else { return false }
        let title = element("live.detail.title")
        for _ in 0..<2 {
            sleep(1)
            block.tap()
            if title.waitForExistence(timeout: 5) { return true }
        }
        return title.exists
    }

    private func openGuide(label: String = "Guide") {
        let seg = app.segmentedControls.buttons[label].firstMatch
        let b = seg.waitForExistence(timeout: 15) ? seg : app.buttons[label].firstMatch
        XCTAssertTrue(b.waitForExistence(timeout: 5), "the Guide segment")
        b.tap()
    }

    /// The player's banner names a channel (number · name), within a few seconds.
    private func channel(contains name: String) -> Bool {
        let label = element("live.player.channel")
        return wait(timeout: 10) { label.exists && label.label.contains(name) }
    }

    private func wait(timeout: TimeInterval = 15, _ condition: @escaping () -> Bool) -> Bool {
        let end = Date().addingTimeInterval(timeout)
        while Date() < end {
            if condition() { return true }
            usleep(250_000)
        }
        return condition()
    }
}
