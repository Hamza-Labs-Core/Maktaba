import XCTest

/// The player (story 48.23) against the mock home's real HLS stream: it opens at the resume
/// position and reports progress; Up next counts down and plays the next episode, or, with
/// autoplay off, the video stops at its end; the subtitle picker lists the session's
/// renditions and shows the chosen one; audio tracks switch; quality reopens at the same
/// position with `max_bitrate_kbps`; the controls hide after 4 s; a double-tap seeks, mirrored
/// in Arabic; and a stream with sound plays with sound (FR-39a).
///
/// What plays is read from the video's accessibility value (a Debug build's
/// `Playback.testState`: `position=1240;playing=true;sound=aac;audible=true;…`); what the app
/// asked the home, from `MockHome.requests`.
final class PlayerWatchUITests: PlayerUITestCase {
    // MARK: resume and progress

    func testOpensAtTheResumePositionAndReportsProgress() throws {
        watch("v2")
        // v2 was left at 1240 s (video-v2.json)
        let opened = try XCTUnwrap(sessions().first)
        XCTAssertEqual(opened["video_id"] as? String, "v2")
        XCTAssertEqual(opened["start_sec"] as? Int, 1240)
        waitFor("it plays from 1240 s") { (state["position"].flatMap(Int.init) ?? 0) >= 1240 && state["playing"] == "true" }
        shot("watch-controls")
        // every 10 s while playing
        waitFor("a progress report", timeout: 20) { progress().contains { $0 >= 1240 } }
        // and on pause
        let before = progress().count
        showControls()
        app.buttons["player.playPause"].tap()
        waitFor("paused") { state["playing"] == "false" }
        waitFor("a report on pause") { progress().count > before }
    }

    func testAFinishedVideoStartsAgain() throws {
        watch("v1")
        waitFor("a session") { !sessions().isEmpty }
        let opened = try XCTUnwrap(sessions().first)
        XCTAssertEqual(opened["video_id"] as? String, "v1")
        XCTAssertNil(opened["start_sec"], "v1 was finished: from the start")
        waitFor("it plays") { state["playing"] == "true" }
    }

    // MARK: Up next

    func testUpNextCountsDownAndPlaysTheNextEpisode() {
        // the stream lasts 3120 s: the last 10 s
        watch("v2", at: 3108)
        let card = element("player.upNext")
        XCTAssertTrue(card.waitForExistence(timeout: 20), "Up next shows in the last seconds")
        XCTAssertTrue(element("player.upNext.countdown").label.contains("Up next in"))
        XCTAssertTrue(card.staticTexts["السيرة النبوية، الدرس ١٢"].exists, "the next episode in the season's order is v3")
        shot("watch-upnext")
        waitFor("the next episode opens", timeout: 25) { sessions().contains { $0["video_id"] as? String == "v3" } }
    }

    func testWithAutoplayOffItStopsAtTheEndAndNextStays() {
        launch("en", sections: ["home"], open: "watch/v2@3110", extra: ["-playback.autoplay_next", "NO"])
        signIn()
        waitFor("it plays to its end", timeout: 30) { state["ended"] == "true" }
        XCTAssertFalse(element("player.upNext").exists, "no Up next with autoplay off")
        sleep(3)
        XCTAssertFalse(sessions().contains { $0["video_id"] as? String == "v3" }, "the next episode does not open")
        showControls()
        XCTAssertTrue(app.buttons["player.next"].waitForExistence(timeout: 10), "Next stays")
    }

    // MARK: tracks and quality

    func testThePickerListsTheSessionsRenditionsAndShowsTheChosenOne() {
        watch("v2")
        waitFor("it plays") { state["playing"] == "true" }
        openSheet("player.tracks")
        let off = app.buttons["player.subtitle.off"]
        XCTAssertTrue(off.waitForExistence(timeout: 10))
        XCTAssertTrue(off.isSelected, "subtitles start off (Settings' default)")
        XCTAssertEqual(app.buttons["player.subtitle.en"].label, "English")
        XCTAssertEqual(app.buttons["player.subtitle.ar"].label, "Arabic")
        XCTAssertFalse(app.buttons["player.audio.en"].exists, "the sound is in the video's segments: no audio choice")
        shot("watch-tracks")
        app.buttons["player.subtitle.ar"].tap()
        waitFor("Arabic subtitles") { state["subtitle"] == "ar" }
        openSheet("player.tracks")
        XCTAssertTrue(app.buttons["player.subtitle.ar"].waitForExistence(timeout: 10))
        XCTAssertTrue(app.buttons["player.subtitle.ar"].isSelected, "the chosen one is checked")
        XCTAssertFalse(app.buttons["player.subtitle.off"].isSelected)
    }

    func testAudioTracksSwitchAndTheSoundStaysOn() {
        home.streamMaster = "master-two-audio"
        watch("v2")
        waitFor("it plays with sound") { state["playing"] == "true" && state["sound"] == "aac" }
        openSheet("player.tracks")
        let en = app.buttons["player.audio.en"], ar = app.buttons["player.audio.ar"]
        XCTAssertTrue(en.waitForExistence(timeout: 10))
        XCTAssertTrue(en.isSelected, "an audio track is selected")
        XCTAssertEqual(ar.label, "Arabic")
        ar.tap()
        waitFor("the Arabic audio, with sound") { state["audio"] == "ar" && state["sound"] == "aac" && state["audible"] == "true" }
    }

    func testQualityReopensAtTheSamePositionWithMaxBitrate() throws {
        watch("v2")
        waitFor("it plays") { state["playing"] == "true" }
        openSheet("player.quality")
        XCTAssertTrue(app.buttons["player.quality.auto"].waitForExistence(timeout: 10))
        XCTAssertTrue(app.buttons["player.quality.auto"].isSelected)
        XCTAssertEqual(app.buttons["player.quality.6000"].label, "1080p")
        XCTAssertEqual(app.buttons["player.quality.1200"].label, "480p")
        let at = Int(state["position"] ?? "") ?? 0
        app.buttons["player.quality.3000"].tap()
        waitFor("a session capped at 3000 kb/s") { sessions().contains { $0["max_bitrate_kbps"] as? Int == 3000 } }
        let reopened = try XCTUnwrap(sessions().last)
        XCTAssertEqual(reopened["video_id"] as? String, "v2")
        let start = try XCTUnwrap(reopened["start_sec"] as? Int)
        XCTAssertTrue((at - 1 ... at + 15).contains(start), "reopened at \(start), was at \(at)")
        waitFor("it plays on, capped") { state["quality"] == "3000" && state["playing"] == "true" }
        openSheet("player.quality")
        XCTAssertTrue(app.buttons["player.quality.3000"].waitForExistence(timeout: 10))
        XCTAssertTrue(app.buttons["player.quality.3000"].isSelected)
    }

    // MARK: controls

    func testControlsHideAfterFourSecondsAndATapShowsThem() {
        watch("v2")
        let play = app.buttons["player.playPause"]
        waitFor("it plays") { state["playing"] == "true" }
        XCTAssertTrue(play.waitForNonExistence(timeout: 15), "they hide while it plays")
        // timed from the tap, as the app's 4 s are: the tap and the wait for the controls to
        // show take most of a second on a slow runner
        let shown = Date()
        showControls()
        XCTAssertTrue(play.waitForNonExistence(timeout: 10), "hidden again after 4 s")
        XCTAssertGreaterThan(Date().timeIntervalSince(shown), 3.5, "not before 4 s")
        showControls()
        XCTAssertTrue(play.exists, "a tap shows them")
    }

    func testADoubleTapOnTheRightSeeksForwardAndOnTheLeftBack() {
        doubleTaps("en", rightSeeks: 10)
    }

    func testInArabicTheDoubleTapMirrors() {
        doubleTaps("ar", rightSeeks: -10)
        shot("watch-controls")
        openSheet("player.tracks")
        XCTAssertTrue(app.buttons["player.subtitle.ar"].waitForExistence(timeout: 10))
        XCTAssertEqual(app.buttons["player.subtitle.ar"].label, "العربية", "the renditions named in Arabic")
        shot("watch-tracks")
    }

    // MARK: sound (FR-39a)

    func testAStreamWithSoundPlaysWithSoundSelected() {
        watch("v2")
        waitFor("it plays, AAC sound on an enabled track, audible", timeout: 20) {
            state["playing"] == "true" && state["sound"] == "aac" && state["audible"] == "true"
        }
    }

    // MARK: AirPlay and Picture in Picture (story 48.24)

    /// The AirPlay route button is on the controls (a receiver itself can't run in CI: checked by
    /// hand), and Picture in Picture floats the video from its button and comes back.
    func testAirPlayIsOnTheControlsAndPictureInPictureFloatsTheVideo() {
        watch("v2")
        waitFor("it plays", timeout: 20) { state["playing"] == "true" && state["route"] == "device" }
        showControls()
        let airplay = app.buttons["player.airplay"]
        XCTAssertTrue(airplay.waitForExistence(timeout: 10), "the AirPlay button")
        XCTAssertEqual(airplay.label, "AirPlay")
        waitFor("Picture in Picture can start", timeout: 20) { state["pipPossible"] == "true" }
        showControls()
        app.buttons["player.pip"].tap()
        waitFor("the video floats", timeout: 20) { state["pip"] == "true" }
        shot("watch-pip")
        waitFor("it plays on in the window") { state["playing"] == "true" }
        showControls()
        app.buttons["player.pip"].tap()
        waitFor("back in the app", timeout: 20) { state["pip"] == "false" }
    }

    // MARK: helpers

    private func watch(_ video: String, at: Int? = nil, language: String = "en") {
        launchSignedIn(language, sections: ["home"], open: at.map { "watch/\(video)@\($0)" } ?? "watch/\(video)")
        XCTAssertTrue(element("player.surface").waitForExistence(timeout: 30), "the player opens")
    }

    /// The video's accessibility value as a dictionary.
    private var state: [String: String] {
        let value = element("player.surface").value as? String ?? ""
        var out: [String: String] = [:]
        for pair in value.split(separator: ";") {
            let kv = pair.split(separator: "=", maxSplits: 1).map(String.init)
            if kv.count == 2 { out[kv[0]] = kv[1] }
        }
        return out
    }

    /// The stream sessions the app opened, their bodies in order.
    private func sessions() -> [[String: Any]] {
        home.requests.filter { $0.method == "POST" && $0.path == "/api/stream/sessions" }
            .compactMap { try? JSONSerialization.jsonObject(with: $0.body) as? [String: Any] }
    }

    /// The positions reported, in order.
    private func progress() -> [Int] {
        home.requests.filter { $0.method == "POST" && $0.path.hasSuffix("/progress") }
            .compactMap { try? JSONSerialization.jsonObject(with: $0.body) as? [String: Any] }
            .compactMap { $0["position_sec"] as? Int }
    }

    private func showControls() {
        if !app.buttons["player.playPause"].exists {
            element("player.surface").coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.15)).tap()
        }
        XCTAssertTrue(app.buttons["player.playPause"].waitForExistence(timeout: 5), "the controls")
    }

    private func openSheet(_ button: String) {
        showControls()
        app.buttons[button].tap()
    }

    /// Paused at 600 s, a double-tap on the right moves `rightSeeks`, on the left the other way.
    private func doubleTaps(_ language: String, rightSeeks: Int) {
        watch("v2", at: 600, language: language)
        waitFor("it plays from 600 s") { state["playing"] == "true" }
        showControls()
        app.buttons["player.playPause"].tap()
        waitFor("paused") { state["playing"] == "false" }
        let at = Int(state["position"] ?? "") ?? 0
        let surface = element("player.surface")
        surface.coordinate(withNormalizedOffset: CGVector(dx: 0.9, dy: 0.3)).doubleTap()
        waitFor("the right side seeks \(rightSeeks) s") { Int(state["position"] ?? "") == at + rightSeeks }
        surface.coordinate(withNormalizedOffset: CGVector(dx: 0.1, dy: 0.3)).doubleTap()
        waitFor("the left side seeks back") { Int(state["position"] ?? "") == at }
    }

    private func waitFor(_ what: String, timeout: TimeInterval = 15, _ done: () -> Bool) {
        let end = Date().addingTimeInterval(timeout)
        while !done() {
            if Date() > end {
                XCTFail("timed out: \(what) (state: \(state))")
                return
            }
            RunLoop.current.run(until: Date().addingTimeInterval(0.25))
        }
    }
}
