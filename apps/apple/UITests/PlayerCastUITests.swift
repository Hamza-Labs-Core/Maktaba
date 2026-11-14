import XCTest

/// Google Cast from the Player (Epic 58 story 58.06, T05) against a fake Cast context
/// (`-maktaba-cast fake`: one TV, "Living Room TV", no Cast SDK and no TV involved; there is
/// no Cast emulator). Google's local network screen shows before the first cast; the TV list
/// shows AirPlay beside Cast; a cast mints a grant on the mock home and loads the receiver
/// with Maktaba's custom data; while the TV plays, the phone posts no progress, and after the
/// app goes to the background and comes back the mini controller still follows the TV. In
/// English and Arabic, dark.
///
/// What the fake TV was loaded with is read from `cast.state`'s accessibility value (a Debug
/// build's `CastController.testState`: `device=Living Room TV;state=playing;home=…;grant=yes`).
final class PlayerCastUITests: PlayerUITestCase {
    /// An HTTPS address the TV could reach, in the home's discovery (never resolved: the
    /// fake TV fetches nothing, and `.invalid` never answers).
    static let relay = "https://ui-home.relay.example.invalid"
    static let grant = #"{"grant":"cast_ui","scope":"cast","expires_at":"2099-01-01T00:00:00Z","video_id":"v2"}"#

    // MARK: cast, lock, resume

    func testCastThenBackgroundThenResumeShowsTheMiniController() throws {
        try castLockResume("en")
        // the local network screen shows once: the next cast opens the list at once
        app.buttons["cast.stop"].tap()
        XCTAssertTrue(element("player.surface").waitForExistence(timeout: 15), "the video is back on the phone")
        tapCast()
        XCTAssertTrue(app.buttons["cast.device.fake-tv"].waitForExistence(timeout: 10), "the TV list")
        XCTAssertFalse(app.buttons["cast.interstitial.continue"].exists, "no local network screen after the first cast")
    }

    func testCastThenBackgroundThenResumeInArabic() throws {
        try castLockResume("ar")
    }

    private func castLockResume(_ language: String) throws {
        openWatch(language)
        tapCast()
        // Google's local network screen before the first cast
        let go = app.buttons["cast.interstitial.continue"]
        XCTAssertTrue(go.waitForExistence(timeout: 10), "the local network screen before the first cast")
        XCTAssertTrue(app.staticTexts[language == "ar" ? "ابحث عن أجهزة التلفاز على شبكتك" : "Find TVs on your network"].exists)
        shot("cast-interstitial")
        go.tap()
        let tv = app.buttons["cast.device.fake-tv"]
        XCTAssertTrue(tv.waitForExistence(timeout: 10), "the TV list")
        XCTAssertTrue(element("cast.picker.airplay").exists, "AirPlay beside Cast")
        shot("cast-picker")
        tv.tap()
        waitFor("a cast grant for v2") { grants().contains { $0["video_id"] as? String == "v2" } }
        XCTAssertTrue(element("cast.panel").waitForExistence(timeout: 15), "the cast's panel")
        XCTAssertEqual(element("cast.castingTo").label,
                       language == "ar" ? "يُعرض على Living Room TV" : "Playing on Living Room TV")
        let loaded = castState
        XCTAssertEqual(loaded["home"], Self.relay, "the home's HTTPS address, never the phone's LAN IP")
        XCTAssertEqual(loaded["grant"], "yes")
        XCTAssertEqual(loaded["lang"], language)
        XCTAssertEqual(loaded["contentURL"], "no", "Maktaba's custom data, not a plain URL")
        XCTAssertEqual(loaded["video"], "v2")
        shot("cast-playing")
        // the receiver reports progress with the grant: the phone posts none while it casts
        sleep(2)
        let posted = progress().count
        let before = Int(castState["position"] ?? "") ?? 0
        XCUIDevice.shared.press(.home)
        sleep(12)
        app.activate()
        XCTAssertTrue(element("cast.mini").waitForExistence(timeout: 15), "the mini controller after the app comes back")
        waitFor("the mini controller follows the TV") { (Int(castState["position"] ?? "") ?? 0) > before }
        XCTAssertEqual(castState["device"], "Living Room TV")
        XCTAssertEqual(progress().count, posted, "no progress from the phone while the TV plays")
        shot("cast-resumed")
    }

    // MARK: an older home, a refusal, no TV, no address

    func testAnOlderHomeCastsThePlainURLAndSaysSo() {
        openWatch("en", grantStatus: 404, grantBody: "404 page not found\n")
        castToTheTV()
        XCTAssertTrue(element("cast.oldHome").waitForExistence(timeout: 15), "cast.oldHome")
        XCTAssertEqual(element("cast.oldHome").label, "This home is older, so the TV shows Google's player.")
        XCTAssertEqual(castState["contentURL"], "yes", "the plain signed URL")
        XCTAssertEqual(castState["grant"], "no", "no custom data")
        shot("cast-oldhome")
    }

    func testARefusedGrantIsShownAndNothingIsCast() {
        openWatch("ar", grantStatus: 403, grantBody: #"{"title":"forbidden","status":403,"code":"cast_via_cloud"}"#)
        castToTheTV()
        let problem = element("cast.problem")
        XCTAssertTrue(problem.waitForExistence(timeout: 15), "the refusal")
        XCTAssertEqual(problem.label, "يحتاج البث إلى تسجيل الدخول على المنزل نفسه، لا عبر Maktaba Cloud.")
        XCTAssertFalse(element("cast.panel").exists, "nothing is cast")
        shot("cast-refused")
    }

    func testNoTVFoundSaysWhyAndSuggestsAirPlay() {
        openWatch("en", cast: "fake-empty")
        tapCast()
        app.buttons["cast.interstitial.continue"].tap()
        let empty = element("cast.picker.empty")
        XCTAssertTrue(empty.waitForExistence(timeout: 15), "the empty state")
        XCTAssertTrue(empty.label.contains("same Wi-Fi network"))
        XCTAssertTrue(empty.label.contains("AirPlay"))
        shot("cast-empty")
    }

    func testAHomeWithNoAddressTheTVCanReachHasNoCastButton() {
        openWatch("en", discovery: false)
        showControls()
        XCTAssertTrue(app.buttons["player.tracks"].exists)
        sleep(2)
        XCTAssertFalse(app.buttons["player.cast"].exists, "the phone reaches the home over plain HTTP only: no Cast")
    }

    // MARK: helpers

    /// The video page of v2 (left at 1240 s), the home's discovery and the grant stubbed,
    /// then Resume: the watch screen reads the discovery as it opens.
    private func openWatch(_ language: String, cast: String = "fake", discovery: Bool = true,
                           grantStatus: Int = 201, grantBody: String = PlayerCastUITests.grant) {
        launchSignedIn(language, sections: ["home"], open: "video/v2", extra: ["-maktaba-cast", cast])
        if discovery {
            home.stub("GET", "/api/discovery/home", body: #"{"id":"ui-home","name":"UI Test Home","version":"0.0.0","urls":["\#(Self.relay)"],"api_level":1,"sign_in":{"password":true,"code":true}}"#)
        }
        home.stub("POST", "/api/cast/grants", status: grantStatus, body: grantBody)
        let resume = app.buttons["video.resume"]
        XCTAssertTrue(resume.waitForExistence(timeout: 20), "the video page")
        resume.tap()
        XCTAssertTrue(element("player.surface").waitForExistence(timeout: 30), "the player opens")
        waitFor("it plays") { surfaceState["playing"] == "true" }
    }

    /// The Cast button, past the local network screen, to the fake TV.
    private func castToTheTV() {
        tapCast()
        let go = app.buttons["cast.interstitial.continue"]
        XCTAssertTrue(go.waitForExistence(timeout: 10))
        go.tap()
        let tv = app.buttons["cast.device.fake-tv"]
        XCTAssertTrue(tv.waitForExistence(timeout: 10))
        tv.tap()
        waitFor("a cast grant asked for") { !grants().isEmpty }
    }

    private func showControls() {
        if !app.buttons["player.playPause"].exists {
            element("player.surface").coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.15)).tap()
        }
        XCTAssertTrue(app.buttons["player.playPause"].waitForExistence(timeout: 5), "the controls")
    }

    /// The controls, then the Cast button (once the watch screen has read the home's addresses).
    private func tapCast() {
        showControls()
        let cast = app.buttons["player.cast"]
        XCTAssertTrue(cast.waitForExistence(timeout: 10), "the Cast button")
        cast.tap()
    }

    private func grants() -> [[String: Any]] {
        home.requests.filter { $0.method == "POST" && $0.path == "/api/cast/grants" }
            .compactMap { try? JSONSerialization.jsonObject(with: $0.body) as? [String: Any] }
    }

    private func progress() -> [Int] {
        home.requests.filter { $0.method == "POST" && $0.path.hasSuffix("/progress") }
            .compactMap { try? JSONSerialization.jsonObject(with: $0.body) as? [String: Any] }
            .compactMap { $0["position_sec"] as? Int }
    }

    private var castState: [String: String] { Self.pairs(element("cast.state").value as? String ?? "") }
    private var surfaceState: [String: String] { Self.pairs(element("player.surface").value as? String ?? "") }

    private static func pairs(_ value: String) -> [String: String] {
        var out: [String: String] = [:]
        for pair in value.split(separator: ";") {
            let kv = pair.split(separator: "=", maxSplits: 1).map(String.init)
            if kv.count == 2 { out[kv[0]] = kv[1] }
        }
        return out
    }

    private func waitFor(_ what: String, timeout: TimeInterval = 20, _ done: () -> Bool) {
        let end = Date().addingTimeInterval(timeout)
        while !done() {
            if Date() > end {
                if done() { return }
                XCTFail("timed out: \(what) (cast: \(castState))")
                return
            }
            usleep(250_000)
        }
    }
}
