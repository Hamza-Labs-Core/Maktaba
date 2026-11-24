import XCTest

/// The product screenshots of the Player on iPhone and iPad (story 60.09): Home, Curiosity's
/// page and the player with Arabic subtitles; and the store gallery's (story 60.14): Search,
/// the series Caminandes, Live TV and a guardian's Family; in English and in Arabic, dark, on the demo
/// library (`MockHome(demo:)`, from `tests/fixtures/demo-library/`) with its clock pinned
/// (`library.json`'s noon; Home's On now row, drawn against the device's clock, counts from
/// now's quarter hour).
/// Each is an attachment named `shot-<iphone|ipad>-<screen>-<language>`, kept always: CI
/// exports them from the result bundle as `product-shots-<device>` (`_apple-apps.yml`), and
/// `web/packages/website/scripts/shots.mjs --from-run` takes them from there.
///
/// Not in the Player's UI test shards (the file is not a `Player*Tests.swift`): CI runs it on
/// a shard of its own, `shots`. The Remote's shot is `RemoteDemoShotsUITests`.
final class PlayerDemoShots: PlayerUITestCase {
    func testHome() throws {
        for language in ["en", "ar"] {
            // Home's On now row is drawn against the device's clock: the demo's times count from
            // now's quarter hour (the dates it shows are relative: "3 days ago")
            try demo(language, clock: MockHome.quarterHour())
            launchSignedIn(language, section: "home")
            XCTAssertTrue(element("screen.home").waitForExistence(timeout: 20), "Home")
            waitForArt(atLeast: 4)
            demoShot("home")
        }
    }

    func testFilm() throws {
        for language in ["en", "ar"] {
            try demo(language)
            launchSignedIn(language, section: "home", open: "video/v-curiosity")
            XCTAssertTrue(element("screen.video").waitForExistence(timeout: 20), "Curiosity's page")
            XCTAssertTrue(element("video.title").waitForExistence(timeout: 10), "its title")
            waitForArt(atLeast: 1)
            demoShot("film")
        }
    }

    /// Curiosity playing with the Arabic subtitles on (in both languages: the Arabic line is what
    /// the page shows off), the controls gone so the line reads clear of them: on the iPhone in
    /// landscape, on the iPad upright (its sidebar floats over a landscape player).
    func testPlayer() throws {
        defer { XCUIDevice.shared.orientation = .portrait }
        for language in ["en", "ar"] {
            try demo(language)
            // the stream as long as the film (the demo's `duration_sec`): the player seeks to 7:00
            // in it (`Playback.load`); the cue lasts the whole film
            if let seconds = home.demoVideo("v-curiosity")?["duration_sec"] as? Int { home.streamSeconds = seconds / 2 * 2 }
            // signed in upright (in landscape the keyboard covers the sign-in's choices), turned
            // once the player is open
            XCUIDevice.shared.orientation = .portrait
            launchSignedIn(language, section: "home", open: "watch/v-curiosity@420", controlsStay: false,
                           extra: ["-playback.subtitles_on", "YES", "-playback.subtitle_language", "ar"])
            let surface = element("player.surface")
            XCTAssertTrue(surface.waitForExistence(timeout: 30), "the player")
            // on the iPhone (on the iPad its sidebar floats over a landscape player)
            if !isPad {
                XCUIDevice.shared.orientation = .landscapeLeft
                XCTAssertTrue(waitUntil(timeout: 10) { surface.frame.width > surface.frame.height }, "the player in landscape")
            }
            XCTAssertTrue(waitUntil(timeout: 30) { self.state["playing"] == "true" && self.state["subtitle"] == "ar" },
                          "playing with the Arabic subtitles: \(state)")
            // a frame of the picture and the cue drawn: the position moves past where it opened
            // Long enough for the app's own recovery from a stall: on a loaded simulator the
            // stream proxy (its tasks below the interface's priority) can pass a segment's request
            // on after the player's budget, about one target duration (2 s for the test stream);
            // the player then drops its only variant and waits, `playing` still true (#1412's run
            // 43715426951, iPhone shots: "No response for media file in 2s", "Can not proceed
            // after removing variants", the position held at 423). `Playback.watch` looks every
            // 10 s and opens the stream again after `WatchLogic.stuckAfter` (15 s) of waiting:
            // up to about 30 s after the stall.
            let start = Int(state["position"] ?? "") ?? 0
            XCTAssertTrue(waitUntil(timeout: 40) { (Int(self.state["position"] ?? "") ?? 0) >= start + 2 }, "it plays on: \(state)")
            // the controls hide 4 s after the last touch (`WatchView.hideAfter`)
            XCTAssertTrue(app.buttons["player.playPause"].waitForNonExistence(timeout: 15), "the controls gone")
            // the line drawn: the test stream starts its timestamps again every 4 s (two segments
            // in turn), and the player can drop the line there, so the shot waits for its white
            // letters (where the player puts it: on the iPad in English, under the picture); the
            // iPad's top edge left out, where a status bar's clock is white too
            let screen = app.frame
            let area = isPad ? CGRect(x: 0, y: 40, width: screen.width, height: screen.height - 40) : nil
            var shot = XCUIScreen.main.screenshot()
            let end = Date().addingTimeInterval(30)
            while brightPixels(shot, in: area) < 500, Date() < end {
                usleep(500_000)
                shot = XCUIScreen.main.screenshot()
            }
            XCTAssertGreaterThanOrEqual(brightPixels(shot, in: area), 500, "the subtitle line")
            demoShot("player", shot)
        }
    }

    // MARK: the store gallery's screens (story 60.14 part 2)

    /// Search for the demo's query ("Mars", «المريخ»): Curiosity among the titles, and the
    /// lines that say it under In the transcript.
    func testSearch() throws {
        for language in ["en", "ar"] {
            try demo(language)
            launchSignedIn(language, section: "search")
            XCTAssertTrue(element("screen.search").waitForExistence(timeout: 20), "Search")
            let field = app.searchFields.firstMatch
            XCTAssertTrue(field.waitForExistence(timeout: 10), "the search field")
            field.typeExactly(language == "ar" ? "المريخ" : "Mars", in: app)
            field.typeText("\n")
            XCTAssertTrue(element("search.title.v-curiosity").waitForExistence(timeout: 20),
                          "Curiosity among the titles; the home saw \(home.seen)")
            XCTAssertTrue(element("search.transcripts").waitForExistence(timeout: 10), "In the transcript")
            XCTAssertTrue(waitUntil(timeout: 10) { !self.app.keyboards.firstMatch.exists }, "the keyboard gone")
            waitForArt(atLeast: 1)
            demoShot("search")
        }
    }

    /// The series Caminandes: its seasons and its episodes, with their pictures.
    func testSeries() throws {
        for language in ["en", "ar"] {
            try demo(language)
            launchSignedIn(language, section: "libraries", open: "series/s-caminandes")
            XCTAssertTrue(element("screen.series").waitForExistence(timeout: 20), "Caminandes; the home saw \(home.seen)")
            XCTAssertTrue(element("episode.v-llama-drama").waitForExistence(timeout: 20), "its first episode")
            XCTAssertTrue(element("episode.v-gran-dillama").exists, "and the second")
            waitForArt(atLeast: 3)
            demoShot("series")
        }
    }

    /// Live TV's Now: the demo's three channels, what is on each with its progress and what
    /// is next. Drawn against the device's clock, so the demo's times count from now's quarter
    /// hour.
    func testLive() throws {
        for language in ["en", "ar"] {
            try demo(language, clock: MockHome.quarterHour())
            launchSignedIn(language, section: "live")
            XCTAssertTrue(element("screen.liveTV").waitForExistence(timeout: 20), "Live TV")
            for channel in ["ch-space", "ch-kids", "ch-earth"] {
                XCTAssertTrue(element("live.channel.\(channel)").waitForExistence(timeout: 20),
                              "\(channel) on Now; the home saw \(home.seen)")
            }
            waitUntilHomeIsQuiet(2, timeout: 30)
            demoShot("live")
        }
    }

    /// Epic 57, a guardian's side: Family, with two children of the household and what they
    /// asked for (a Caminandes episode, and more time). The account and the requests are this
    /// test's own answers, built from the demo's titles; the children's names are boys'.
    func testParental() throws {
        for language in ["en", "ar"] {
            try demo(language)
            let (yusuf, omar) = language == "ar" ? ("يوسف", "عمر") : ("Yusuf", "Omar")
            let title = home.demoVideo("v-big-buck-bunny")?["title"] as? String ?? ""
            XCTAssertFalse(title.isEmpty, "the demo has Big Buck Bunny")
            func json(_ s: String) -> String {
                String(String(decoding: (try? JSONSerialization.data(withJSONObject: [s])) ?? Data(), as: UTF8.self).dropFirst().dropLast())
            }
            home.parental(#"{"restricted":false}"#, children: """
            [{"id":"c-yusuf","username":\(json(yusuf)),"user_id":"c-yusuf","max_tier":0,"unrated":"hide","allow_only":false,"version":2,"updated_at":"{{now-864000}}"},
             {"id":"c-omar","username":\(json(omar)),"user_id":"c-omar","max_tier":13,"unrated":"hide","allow_only":false,"version":3,"updated_at":"{{now-864000}}"}]
            """)
            home.stub("GET", "/api/parental/requests", body: """
            {"requests":[
             {"id":"r1","child_id":"c-yusuf","subject_kind":"video","subject_id":"v-big-buck-bunny","status":"pending",
              "created_at":"{{now-600}}","expires_at":"{{now+86400}}","child":{"id":"c-yusuf","username":\(json(yusuf))},
              "subject":{"kind":"video","id":"v-big-buck-bunny","title":\(json(title)),"poster_url":"/api/videos/v-big-buck-bunny/poster","tier":7}},
             {"id":"r2","child_id":"c-omar","subject_kind":"time","subject_id":"30","minutes":30,"status":"pending",
              "created_at":"{{now-1500}}","expires_at":"{{now+3600}}","child":{"id":"c-omar","username":\(json(omar))},
              "subject":{"kind":"time","id":"30"}}]}
            """)
            home.stub("GET", "/api/me/parental-pin", body: #"{"set":true}"#)
            home.stub("GET", "/api/parental/settings",
                      body: #"{"enabled":true,"restricted_accounts":2,"can_disable":false,"child_devices":0,"child_device_notice":false}"#)
            launchSignedIn(language, section: "profile")
            let family = element("profile.family")
            XCTAssertTrue(family.waitForExistence(timeout: 20), "Family, for a guardian; the home saw \(home.seen)")
            waitUntilStill(family, timeout: 5)
            open(family, shows: element("screen.family"), "the Family screen")
            XCTAssertTrue(element("family.child.c-yusuf").waitForExistence(timeout: 10), "the first child")
            XCTAssertTrue(element("family.child.c-omar").exists, "the second")
            XCTAssertTrue(element("family.request.r1").waitForExistence(timeout: 10), "what Yusuf asked for")
            XCTAssertTrue(element("family.request.r2").exists, "and Omar's more time")
            XCTAssertTrue(waitUntil(timeout: 20) {
                self.home.requests.contains { $0.method == "GET" && $0.path == "/api/videos/v-big-buck-bunny/poster" }
            }, "the request's poster asked for; the home saw \(home.seen)")
            waitUntilHomeIsQuiet(2, timeout: 30)
            demoShot("parental")
        }
    }

    // MARK: helpers

    /// This test's mock home becomes the demo library in `language`, its clock pinned
    /// (`library.json`'s noon unless the screen draws against the device's clock).
    private func demo(_ language: String, clock: Date = MockHome.demoClock) throws {
        Self.close(app)
        home.stop()
        home = try MockHome(demo: language)
        home.clock = clock
        try home.start()
    }

    /// The player's state (a Debug build's accessibility value: `playing=true;subtitle=ar;…`).
    private var state: [String: String] {
        let value = element("player.surface").value as? String ?? ""
        var out: [String: String] = [:]
        for pair in value.split(separator: ";") {
            let kv = pair.split(separator: "=", maxSplits: 1).map(String.init)
            if kv.count == 2 { out[kv[0]] = kv[1] }
        }
        return out
    }

    /// Waits until the app has fetched at least `count` pictures from the home and then asked
    /// it nothing for 2 s: every card's picture is in, decoded and drawn.
    private func waitForArt(atLeast count: Int) {
        let kinds = ["/poster", "/portrait", "/backdrop", "/art", "/thumbnail"]
        XCTAssertTrue(waitUntil(timeout: 30) {
            self.home.requests.filter { r in r.method == "GET" && kinds.contains { r.path.hasSuffix($0) } }.count >= count
        }, "\(count) pictures asked for; the home saw \(home.seen)")
        waitUntilHomeIsQuiet(2, timeout: 30)
    }

    /// The near-white pixels (a subtitle line's letters) in `shot`, inside `rect` (in the
    /// app's points, upright) or in all of it.
    private func brightPixels(_ shot: XCUIScreenshot, in rect: CGRect? = nil) -> Int {
        guard let cg = shot.image.cgImage else { return 0 }
        var area = CGRect(x: 0, y: 0, width: cg.width, height: cg.height)
        if let rect {
            let k = CGFloat(cg.width) / app.frame.width
            area = CGRect(x: rect.minX * k, y: rect.minY * k, width: rect.width * k, height: rect.height * k).integral.intersection(area)
        }
        guard let crop = cg.cropping(to: area) else { return 0 }
        let (w, h) = (crop.width, crop.height)
        var gray = [UInt8](repeating: 0, count: w * h)
        let drawn = gray.withUnsafeMutableBytes { buffer -> Bool in
            guard let ctx = CGContext(data: buffer.baseAddress, width: w, height: h, bitsPerComponent: 8, bytesPerRow: w,
                                      space: CGColorSpaceCreateDeviceGray(), bitmapInfo: CGImageAlphaInfo.none.rawValue) else { return false }
            ctx.draw(crop, in: CGRect(x: 0, y: 0, width: w, height: h))
            return true
        }
        return drawn ? gray.reduce(0) { $1 > 245 ? $0 + 1 : $0 } : 0
    }

    /// The screen (`shot`, else taken now) as an attachment named
    /// `shot-<device>-<screen>-<language>`, kept always.
    private func demoShot(_ screen: String, _ taken: XCUIScreenshot? = nil) {
        let shot = taken ?? XCUIScreen.main.screenshot()
        guard XCUIDevice.shared.orientation == .landscapeLeft else { return attach(XCTAttachment(screenshot: shot), screen) }
        // in landscape the screen's picture comes as the panel's (upright, the content's top at
        // its right edge): turned a quarter to the left, as the screen is seen
        let image = shot.image
        guard image.size.width < image.size.height else {
            // already as seen (the image carries its orientation): drawn as it is
            let format = UIGraphicsImageRendererFormat()
            format.scale = image.scale
            let png = UIGraphicsImageRenderer(size: image.size, format: format).pngData { _ in image.draw(at: .zero) }
            return attach(XCTAttachment(data: png, uniformTypeIdentifier: "public.png"), screen)
        }
        let size = CGSize(width: image.size.height, height: image.size.width)
        let format = UIGraphicsImageRendererFormat()
        format.scale = image.scale
        let upright = UIGraphicsImageRenderer(size: size, format: format).pngData { ctx in
            ctx.cgContext.translateBy(x: 0, y: size.height)
            ctx.cgContext.rotate(by: -.pi / 2)
            image.draw(at: .zero)
        }
        attach(XCTAttachment(data: upright, uniformTypeIdentifier: "public.png"), screen)
    }

    private func attach(_ a: XCTAttachment, _ screen: String) {
        a.name = "shot-\(isPad ? "ipad" : "iphone")-\(screen)-\(language)"
        a.lifetime = .keepAlways
        add(a)
    }
}
