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
        // the TV's name is isolated (U+2068 … U+2069) in the right-to-left sentence
        let playingOn = language == "ar" ? "يُعرض على Living Room TV" : "Playing on Living Room TV"
        XCTAssertEqual(unisolated(element("cast.castingTo").label), playingOn)
        XCTAssertEqual(unisolated(element("cast.panel").label), playingOn, "the panel's headline for VoiceOver")
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
        // the list looks for 5 s (a spinner) before it says none was found. On CI iPads the
        // first query, made during that search, took 14 s and once timed out (35 s): no query
        // until the search is over, then a wait that outlasts one slow snapshot
        RunLoop.current.run(until: Date().addingTimeInterval(6))
        let empty = element("cast.picker.empty")
        XCTAssertTrue(empty.waitForExistence(timeout: 40), "the empty state")
        XCTAssertTrue(empty.label.contains("same Wi-Fi network"))
        XCTAssertTrue(empty.label.contains("AirPlay"))
        shot("cast-empty")
    }

    // MARK: the real Google Cast SDK

    /// The real Google Cast SDK, not the fake, as the App Store build ships it: the build's own
    /// receiver from Info.plist, set up, quiet until the local network screen, then searching
    /// with its Bonjour services declared. The runner advertises a Cast device (`CastBeacon`),
    /// and the SDK finds it: no other sign says its search works, since its discovery manager
    /// says it runs and the list says no TV was found also when iOS refuses the search. The
    /// receiver is Google's public Default Media Receiver (CI builds with it,
    /// `_apple-apps.yml`). Casting to a TV needs a device (docs/stores/cast.md).
    func testTheGoogleCastSDKFindsATVOnTheNetwork() throws {
        let tv = try CastBeacon(receivers: [Self.defaultMediaReceiver])
        defer { tv.stop() }
        openWatch("en", cast: "real")
        tapCast()
        let go = app.buttons["cast.interstitial.continue"]
        XCTAssertTrue(go.waitForExistence(timeout: 10), "the local network screen before the first cast")
        let before = try sdk()
        XCTAssertEqual(before.receivers, [Self.defaultMediaReceiver], "set up with the build's receiver")
        // the failing side of the check below, on the same SDK: set up, and not searching
        XCTAssertEqual(before.problems(receiver: Self.defaultMediaReceiver), ["discovery is not running"],
                       "no search before the local network screen's Continue (\(before.text))")
        go.tap()
        // the search begins when the SDK has read the network's state, which it does at
        // background priority (`CastSDKState.searching`): on a runner with no CPU to spare that
        // took over a minute, and no query was sent until then (#1615, #1632: no TV in 60 s). So
        // the wait is for the SDK to begin, and it leaves the CPU alone: a look each 5 s, where
        // `waitForExistence` takes a snapshot of the app each second
        let searching = try waitForTheSearch(upTo: 240)
        XCTAssertEqual(searching.problems(receiver: Self.defaultMediaReceiver), [], searching.text)
        // then it finds the TV within one of its 2 s looks at what it has found
        let row = app.buttons["cast.device.\(tv.id)"]
        let found = row.waitForExistence(timeout: 30)
        let after = try sdk()
        XCTAssertEqual(after.problems(receiver: Self.defaultMediaReceiver), [], after.text)
        XCTAssertTrue(found, "the advertised TV in the list (\(after.text))")
        XCTAssertTrue(after.devices.contains(tv.id), "the SDK found it (\(after.text))")
        XCTAssertTrue(row.label.contains(tv.name), row.label)
        XCTAssertFalse(element("cast.picker.empty").exists)
        XCTAssertTrue(element("player.surface").exists, "the video still plays on the phone")
    }

    func testTheDiscoveryCheckNamesWhatIsWrong() {
        let id = Self.defaultMediaReceiver
        func problems(_ s: CastSDKState) -> [String] { s.problems(receiver: id) }
        XCTAssertEqual(problems(CastSDKState(receivers: [id], running: true, searching: true)), [])
        XCTAssertEqual(problems(CastSDKState(receivers: [id], running: true)),
                       ["the Bonjour search has not begun: the SDK has not read the network's state yet"])
        XCTAssertEqual(problems(CastSDKState()), ["the SDK is not set up", "discovery is not running"])
        XCTAssertEqual(problems(CastSDKState(receivers: ["ABCD1234"], running: true, searching: true)), ["the SDK is set up for ABCD1234, not \(id)"])
        XCTAssertEqual(problems(CastSDKState(receivers: [id])), ["discovery is not running"])
        XCTAssertEqual(problems(CastSDKState(receivers: [id], running: true, searching: true, errors: [CastSDKState.undeclared])),
                       ["a Bonjour service is missing from NSBonjourServices (-72008)"])
        XCTAssertEqual(problems(CastSDKState(receivers: [id], running: true, searching: true, errors: [CastSDKState.denied])),
                       ["local network access is denied (-65570)"])
        XCTAssertEqual(problems(CastSDKState(receivers: [id], running: true, searching: true, errors: [0])), ["a Bonjour search failed (0)"])
        // the line the SDK logs (4.8.6), and its other lines
        let line = "Failed to search for net services with error dictionary {\n    NSNetServicesErrorCode = \"-72008\";\n    NSNetServicesErrorDomain = 10;\n}"
        let function = "-[GCKBonjourServiceBrowser netServiceBrowser:didNotSearch:]"
        XCTAssertEqual(CastSDKState.searchError(function: function, message: line), CastSDKState.undeclared)
        XCTAssertEqual(CastSDKState.searchError(function: function, message: "no code"), 0)
        XCTAssertNil(CastSDKState.searchError(function: "-[GCKDeviceProvider(Protected) notifyDidStartDiscovery]", message: "didStartDiscovery"))
        XCTAssertTrue(CastSDKState.beganSearch(function: "-[GCKCastDeviceMDNSScanner MDNSServiceBrowser:didSendDiscoveryQueryWithSubtypes:unicastResponse:transactionID:packetCount:]"))
        XCTAssertFalse(CastSDKState.beganSearch(function: function), "a refused search is not begun")
        XCTAssertFalse(CastSDKState.beganSearch(function: "-[GCKDeviceProvider(Protected) notifyDidStartDiscovery]"))
        for s in [CastSDKState(), CastSDKState(receivers: [id], running: true, searching: true, errors: [-72008, 0], devices: ["a", "b"])] {
            XCTAssertEqual(CastSDKState(text: s.text), s)
        }
        XCTAssertNil(CastSDKState(text: ""))
    }

    /// Google's Default Media Receiver: public, registered by Google, no console needed. CI's
    /// UI-test build names it (`MAKTABA_CAST_APP_ID`, `_apple-apps.yml`).
    static let defaultMediaReceiver = "CC1AD845"

    /// The SDK's state once its Bonjour search began or failed, else its last one.
    private func waitForTheSearch(upTo timeout: TimeInterval) throws -> CastSDKState {
        let start = Date()
        while true {
            let state = try sdk()
            let took = Int(Date().timeIntervalSince(start))
            if state.searching || !state.errors.isEmpty || took >= Int(timeout) {
                XCTContext.runActivity(named: "The Cast SDK's search: \(took) s after Continue (\(state.text))") { _ in }
                return state
            }
            RunLoop.current.run(until: Date().addingTimeInterval(5))
        }
    }

    private func sdk() throws -> CastSDKState {
        let e = element("cast.sdk")
        XCTAssertTrue(e.waitForExistence(timeout: 20), "the SDK's state: does the build name a receiver (MAKTABA_CAST_APP_ID)?")
        return try XCTUnwrap(CastSDKState(text: e.value as? String ?? ""), "\(String(describing: e.value))")
    }

    func testAHomeWithNoAddressTheTVCanReachHasNoCastButton() {
        openWatch("en", discovery: false)
        showControls()
        XCTAssertTrue(app.buttons["player.tracks"].exists)
        sleep(2)
        XCTAssertFalse(app.buttons["player.cast"].exists, "the phone reaches the home over plain HTTP only: no Cast")
    }

    // MARK: helpers

    /// A label without its bidi isolates: the app wraps a name in U+2068 … U+2069 inside a
    /// right-to-left sentence, so a Latin name keeps its order.
    private func unisolated(_ label: String) -> String {
        label.filter { $0 != "\u{2068}" && $0 != "\u{2069}" }
    }

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

/// A Cast device on the runner's network, as Bonjour shows one: `_googlecast._tcp` with each
/// receiver's subtype and the TXT record a Chromecast sends. Nothing listens behind it. Its id
/// is new each time, since the SDK keeps the devices it found across launches.
private final class CastBeacon: NSObject, NetServiceDelegate {
    let id: String
    let name: String
    private let service: NetService
    private var published = false
    private var failure: [String: NSNumber]?

    init(receivers: [String]) throws {
        id = UUID().uuidString.replacingOccurrences(of: "-", with: "").lowercased()
        name = "Test TV \(id.prefix(4))"
        service = NetService(domain: "local.", type: (["_googlecast._tcp"] + receivers.map { "_\($0)" }).joined(separator: ","),
                             name: "Maktaba-\(id)", port: 8009)
        super.init()
        let txt = ["id": id, "cd": id.uppercased(), "ve": "05", "md": "Chromecast", "ic": "/setup/icon.png",
                   "fn": name, "ca": "4101", "st": "0", "nf": "1"]
        service.setTXTRecord(NetService.data(fromTXTRecord: txt.mapValues { Data($0.utf8) }))
        service.delegate = self
        service.publish()
        let end = Date().addingTimeInterval(20)
        while !published, failure == nil, Date() < end { RunLoop.current.run(until: Date().addingTimeInterval(0.1)) }
        if !published { throw NSError(domain: "CastBeacon", code: failure?["NSNetServicesErrorCode"]?.intValue ?? 0) }
    }

    func stop() { service.stop() }

    func netServiceDidPublish(_ sender: NetService) { published = true }
    func netService(_ sender: NetService, didNotPublish errorDict: [String: NSNumber]) { failure = errorDict }
}
