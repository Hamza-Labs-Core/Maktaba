import XCTest

/// Story 50.27: Watch a broadcast in the Player on iPhone and iPad, against the mock edge in
/// `MockHome` (`-maktaba-guest-edge`). No home is chosen and no one signs in: the home picker's
/// Watch a broadcast, an invite link pasted, the channels, the channel playing through the
/// app's proxy; the offline channel shows its note with no error. Dark, on test data.
final class PlayerGuestUITests: PlayerUITestCase {
    private func openGuestSpace(_ language: String) {
        launch(language, extra: ["-maktaba-guest-edge", home.url, "-maktaba-guest-broadcast"])
        let open = element("picker.watchBroadcast")
        for _ in 0..<8 where !(open.exists && open.isHittable) { app.swipeUp() }
        XCTAssertTrue(open.waitForExistence(timeout: 20), "the picker offers Watch a broadcast")
        open.tap()
        XCTAssertTrue(element("guest.link").waitForExistence(timeout: 20), "the guest space opened")
    }

    func testInviteLinkPlays() throws {
        openGuestSpace("en")
        shot("guest-space")
        let field = app.textFields["guest.link"]
        field.typeExactly("https://watch.maktaba.tv/i/\(MockHome.guestInvite)", in: app)
        element("guest.watchLink").tap()

        let first = element("guest.channel.ch1")
        XCTAssertTrue(first.waitForExistence(timeout: 20), "the publisher's channels; the edge saw \(home.seen)")
        XCTAssertTrue(home.requests.contains { $0.path == "/api/v1/redeem" && String(decoding: $0.body, as: UTF8.self).contains(MockHome.guestInvite) })
        shot("guest-channels")
        // Watch let go of the link field's keyboard before the channels were pushed: idle
        tapWhenIdle(first, "the channels after Watch")
        XCTAssertTrue(element("guest.playing").waitForExistence(timeout: 20), "the player opened")
        XCTAssertTrue(wait(30) { self.home.seen.contains { $0.hasPrefix("GET /vt-1/ch1/low/seg-") } },
                      "the player read the channel's segments; the edge saw \(home.seen)")
        XCTAssertTrue(element("guest.attribution").exists, "the CC BY programme's credit")
        XCTAssertTrue(home.requests.contains { $0.path == "/api/v1/play" && $0.headers["authorization"] == "Bearer mkbg1.ui-pass.sig" })
        shot("guest-player")

        // a swipe up: the next channel, whose home is offline: the slate and the note, no error
        element("guest.player").swipeUp()
        let note = element("guest.state")
        XCTAssertTrue(note.waitForExistence(timeout: 20), "the offline note")
        XCTAssertFalse(app.alerts.firstMatch.exists, "no error dialog")
        shot("guest-offline")
    }

    func testArabicAndAUsedCode() throws {
        openGuestSpace("ar")
        XCTAssertTrue(app.staticTexts["البثوث المحفوظة"].waitForExistence(timeout: 10), "in Arabic")
        let publisher = app.textFields["guest.publisher"]
        for _ in 0..<4 where !publisher.isHittable { app.swipeUp() }
        publisher.typeExactly(MockHome.guestPublisher, in: app)
        let code = app.textFields["guest.code"]
        code.typeExactly("MK-USED", in: app)
        // the form builds its rows as they scroll in (an iPad's sheet is short)
        let watch = element("guest.watch")
        for _ in 0..<4 where !(watch.exists && watch.isHittable) { app.swipeUp() }
        watch.tap()
        let problem = element("guest.problem")
        XCTAssertTrue(problem.waitForExistence(timeout: 20), "the edge's refusal, in words")
        XCTAssertTrue(problem.label.contains("استُخدمت"), problem.label)
        shot("guest-used")
    }

    /// An invite link opened with no home and no limit kept on the device: the guest space
    /// opens on that invite (a guest needs no home).
    func testOpenedInviteLinkOpensTheGuestSpace() throws {
        launch("en", extra: ["-maktaba-guest-edge", home.url, "-maktaba-guest-broadcast"])
        XCTAssertTrue(element("picker.address").waitForExistence(timeout: 20), "the home picker")
        app.open(URL(string: "maktaba://watch/i/\(MockHome.guestInvite)")!)
        XCTAssertTrue(element("guest.channel.ch1").waitForExistence(timeout: 30), "the invite's channels; the edge saw \(home.seen)")
    }

    /// A device where a sign-in was limited when its home last said (Epic 57; the kept flag,
    /// set here as a launch argument): with no home signed in the picker has no Watch a
    /// broadcast, and an invite link is refused in words, never opened.
    func testALimitedDeviceRefusesGuestWatching() throws {
        launch("en", extra: ["-maktaba-guest-edge", home.url, "-maktaba-guest-broadcast", "-parental.limited.device", "YES"])
        XCTAssertTrue(element("picker.address").waitForExistence(timeout: 20), "the home picker")
        for _ in 0..<4 { app.swipeUp() }
        XCTAssertFalse(element("picker.watchBroadcast").exists, "no Watch a broadcast on a limited device")

        app.open(URL(string: "maktaba://watch/i/\(MockHome.guestInvite)")!)
        let alert = app.alerts.firstMatch
        XCTAssertTrue(alert.waitForExistence(timeout: 20), "the link says why it did not open")
        let refusal = alert.staticTexts.containing(NSPredicate(format: "label CONTAINS[c] 'parental limit'")).firstMatch
        XCTAssertTrue(refusal.exists, "the refusal: \(alert.debugDescription)")
        shot("guest-refused")
        XCTAssertFalse(element("guest.link").exists, "the guest space did not open")
        XCTAssertFalse(home.requests.contains { $0.path == "/api/v1/redeem" }, "nothing was asked of the edge")
        alert.buttons.firstMatch.tap()
        XCTAssertTrue(element("picker.address").waitForExistence(timeout: 10), "back at the picker")
    }

    /// While the build has broadcasting off (`GuestBroadcast`; the tests above turn it on with
    /// `-maktaba-guest-broadcast`): the picker has no Watch a broadcast, and an invite link
    /// opens nothing and asks nothing of the edge.
    func testBroadcastingOffShowsNoWatchABroadcast() throws {
        launch("en", extra: ["-maktaba-guest-edge", home.url])
        XCTAssertTrue(element("picker.address").waitForExistence(timeout: 20), "the home picker")
        for _ in 0..<4 { app.swipeUp() }
        XCTAssertFalse(element("picker.watchBroadcast").exists, "no Watch a broadcast while broadcasting is off")

        app.open(URL(string: "maktaba://watch/i/\(MockHome.guestInvite)")!)
        XCTAssertFalse(element("guest.link").waitForExistence(timeout: 5), "the guest space did not open")
        XCTAssertFalse(element("guest.channel.ch1").exists, "no channels")
        XCTAssertFalse(home.requests.contains { $0.path == "/api/v1/redeem" }, "nothing was asked of the edge")
    }

    private func wait(_ timeout: TimeInterval, _ condition: @escaping () -> Bool) -> Bool {
        let end = Date().addingTimeInterval(timeout)
        while Date() < end {
            if condition() { return true }
            RunLoop.current.run(until: Date().addingTimeInterval(0.25))
        }
        return condition()
    }
}
