import XCTest

/// The product screenshots of the Player on iPhone and iPad (story 60.09): Home, Sintel's
/// page and the player with Arabic subtitles, in English and in Arabic, dark, on the demo
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
            launchSignedIn(language, section: "home", open: "video/v-sintel")
            XCTAssertTrue(element("screen.video").waitForExistence(timeout: 20), "Sintel's page")
            XCTAssertTrue(element("video.title").waitForExistence(timeout: 10), "its title")
            waitForArt(atLeast: 1)
            demoShot("film")
        }
    }

    /// Sintel playing with the Arabic subtitles on (in both languages: the Arabic line is what
    /// the page shows off), its controls up.
    func testPlayer() throws {
        for language in ["en", "ar"] {
            try demo(language)
            // the stream as long as the film (the demo's `duration_sec`), at 7:00
            if let seconds = home.demoVideo("v-sintel")?["duration_sec"] as? Int { home.streamSeconds = seconds / 2 * 2 }
            launchSignedIn(language, section: "home", open: "watch/v-sintel@420",
                           extra: ["-playback.subtitles_on", "YES", "-playback.subtitle_language", "ar"])
            let surface = element("player.surface")
            XCTAssertTrue(surface.waitForExistence(timeout: 30), "the player")
            XCTAssertTrue(waitUntil(timeout: 30) { self.state["playing"] == "true" && self.state["subtitle"] == "ar" },
                          "playing with the Arabic subtitles: \(state)")
            if !app.buttons["player.playPause"].exists {
                surface.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.15)).tap()
            }
            XCTAssertTrue(app.buttons["player.playPause"].waitForExistence(timeout: 5), "the controls")
            // a frame of the picture and the cue drawn: the position moves past where it opened
            let start = Int(state["position"] ?? "") ?? 0
            XCTAssertTrue(waitUntil(timeout: 15) { (Int(self.state["position"] ?? "") ?? 0) >= start + 2 }, "it plays on: \(state)")
            demoShot("player")
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

    /// The screen as an attachment named `shot-<device>-<screen>-<language>`, kept always.
    private func demoShot(_ screen: String) {
        let a = XCTAttachment(screenshot: XCUIScreen.main.screenshot())
        a.name = "shot-\(isPad ? "ipad" : "iphone")-\(screen)-\(language)"
        a.lifetime = .keepAlways
        add(a)
    }
}
