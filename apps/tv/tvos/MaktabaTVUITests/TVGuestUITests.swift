import XCTest

/// Story 50.27 T05: Watch a broadcast on Apple TV, with the Siri remote, against the mock
/// edge in `MockHome` (served by the test runner on 127.0.0.1; the app is pointed at it with
/// `-maktaba-guest-edge`). No home, no account: the publisher's handle and a short code typed
/// with the remote, then the QR pairing with a fake phone (`MockHome.guestPair`); each plays
/// (the player's segments come through the app's proxy, the token in their path). CI only, on
/// the throwaway Apple TV simulator `_apple-apps.yml` makes. With MAKTABA_SHOTS set, each
/// step's screenshot (dark, test data) is also written there.
@MainActor
final class TVGuestUITests: XCTestCase {
    let remote = XCUIRemote.shared
    var app: XCUIApplication!
    var home: MockHome!

    override func setUp() async throws {
        continueAfterFailure = false
        home = try MockHome()
        try home.start()
        app = XCUIApplication()
    }

    override func tearDown() async throws {
        home?.stop()
    }

    private func launch(_ language: String = "en") {
        // the saved broadcasts in the UI tests' file, not the Keychain an unsigned build may not use
        app.launchArguments = ["-uitest-reset", "-maktaba-secrets-in-file", "-maktaba-session-reset", "-maktaba-guest-edge", home.url,
                               "-AppleLanguages", "(\(language))", "-AppleLocale", language == "ar" ? "ar_SA" : "en_US"]
        app.launch()
    }

    /// From the home picker (no home chosen) into the guest space.
    private func openGuestSpace() {
        let open = app.buttons["picker.watchBroadcast"]
        XCTAssertTrue(open.waitForExistence(timeout: 20), "the picker offers Watch a broadcast")
        moveFocus(to: open, with: .down)
        remote.press(.select)
        XCTAssertTrue(app.textFields["guest.publisher"].waitForExistence(timeout: 20), "the guest space opened")
    }

    /// The handle and the short code, typed with the remote: the channels, then the channel
    /// plays through the proxy, with its CC BY credit; down goes to the next channel, whose
    /// home is offline: the slate plays and the note shows, no error. The broadcast is saved
    /// for next time, and Remove takes it off the TV.
    func testShortCodePlaysAndIsSaved() throws {
        launch()
        openGuestSpace()
        shot("tv-guest-space-en")
        type(MockHome.guestPublisher, into: app.textFields["guest.publisher"])
        type("mk-ui42", into: app.textFields["guest.code"], from: .right)
        pressIfStillThere(app.buttons["guest.watch"], with: .right)

        let first = app.buttons["guest.channel.ch1"]
        XCTAssertTrue(first.waitForExistence(timeout: 20), "the publisher's channels; the edge saw \(home.seen)")
        XCTAssertTrue(home.requests.contains { $0.path == "/api/v1/redeem" && String(decoding: $0.body, as: UTF8.self).contains("MK-UI42") },
                      "the code went as the edge wants it")
        XCTAssertTrue(app.staticTexts["Al-Noor on Maktaba"].waitForExistence(timeout: 5))
        shot("tv-guest-channels-en")
        moveFocus(to: first, with: .down)
        remote.press(.select)

        XCTAssertTrue(app.staticTexts["guest.playing"].waitForExistence(timeout: 20))
        XCTAssertTrue(wait(timeout: 30) { self.home.seen.contains { $0.hasPrefix("GET /vt-1/ch1/low/seg-") } },
                      "the player read the channel's segments; the edge saw \(home.seen)")
        XCTAssertTrue(app.staticTexts["guest.attribution"].exists, "the CC BY programme's credit shows")
        XCTAssertFalse(app.staticTexts["guest.state"].exists, "a live channel says nothing over the picture")
        shot("tv-guest-player-en")

        remote.press(.down)
        let note = app.staticTexts["guest.state"]
        XCTAssertTrue(note.waitForExistence(timeout: 20), "the offline note")
        XCTAssertTrue(note.label.contains("offline"), note.label)
        XCTAssertTrue(wait(timeout: 30) { self.home.seen.contains { $0.hasPrefix("GET /vt-1/ch2/low/seg-") } }, "the slate plays")
        XCTAssertFalse(app.alerts.firstMatch.exists, "no error dialog")
        shot("tv-guest-offline-en")

        // back to the space: the broadcast is saved, and Remove forgets it
        remote.press(.menu)
        XCTAssertTrue(first.waitForExistence(timeout: 10))
        remote.press(.menu)
        let saved = app.buttons["guest.saved.\(MockHome.guestPublisher)"]
        XCTAssertTrue(saved.waitForExistence(timeout: 10), "saved for next time")
        let remove = app.buttons["guest.remove.\(MockHome.guestPublisher)"]
        moveFocus(to: saved, with: .up)
        moveFocus(to: remove, with: .right)
        remote.press(.select)
        XCTAssertTrue(app.staticTexts["guest.noSaved"].waitForExistence(timeout: 10), "removed")
    }

    /// The QR code and the code beside it; a phone sends the pass (the fake phone call), the
    /// TV takes it from its long-poll and the channel plays. In Arabic, right to left.
    func testPhonePairingPlaysInArabic() throws {
        launch("ar")
        openGuestSpace()
        let code = app.staticTexts["guest.pairCode"]
        XCTAssertTrue(code.waitForExistence(timeout: 20), "the TV shows its code; the edge saw \(home.seen)")
        XCTAssertEqual(code.label, MockHome.guestPairCode)
        XCTAssertTrue(app.staticTexts["أو امسح هذا الرمز بهاتفك"].waitForExistence(timeout: 5), "in Arabic")
        shot("tv-guest-space-ar")
        XCTAssertTrue(home.requests.contains { $0.path == "/api/v1/pair/n-ui" && $0.headers["x-maktaba-pair-key"] == "pk-ui" },
                      "the TV polls with its own key, never shown")

        home.guestPair()
        let first = app.buttons["guest.channel.ch1"]
        XCTAssertTrue(first.waitForExistence(timeout: 20), "paired: the channels")
        // the publisher's name in the Arabic sentence (its bidi marks aside)
        let title = app.staticTexts.matching(NSPredicate(format: "label CONTAINS 'Al-Noor' AND label CONTAINS 'مكتبة'")).firstMatch
        XCTAssertTrue(title.waitForExistence(timeout: 10), "the publisher's channels, named in Arabic")
        shot("tv-guest-channels-ar")
        moveFocus(to: first, with: .down)
        remote.press(.select)
        XCTAssertTrue(wait(timeout: 30) { self.home.seen.contains { $0.hasPrefix("GET /vt-1/ch1/low/seg-") } },
                      "the paired TV plays; the edge saw \(home.seen)")
        XCTAssertTrue(home.requests.contains { $0.path == "/api/v1/play" && $0.headers["authorization"] == "Bearer mkbg1.ui-tv.sig" },
                      "with the pass the phone sent")
        shot("tv-guest-player-ar")
    }

    /// Signed in to a home (no limit on the account): Settings opens Watch a broadcast too,
    /// apart from the home and its account.
    func testSettingsOpensTheGuestSpace() throws {
        app.launchArguments = ["-uitest-reset", "-maktaba-secrets-in-file", "-maktaba-session-reset", "-maktaba-guest-edge", home.url,
                               "-AppleLanguages", "(en)", "-AppleLocale", "en_US"]
        app.launch()
        type(home.url, into: app.textFields["picker.address"])
        pressIfStillThere(app.buttons["picker.connect"], with: .right)
        XCTAssertTrue(app.staticTexts["pairing-code"].waitForExistence(timeout: 20), "the code; the home saw \(home.seen)")
        home.approve()
        XCTAssertTrue(app.tabBars.firstMatch.waitForExistence(timeout: 20), "approved: signed in")
        let tab = app.tabBars.buttons["Settings"]
        XCTAssertTrue(tab.waitForExistence(timeout: 20))
        for _ in 0..<6 where !app.tabBars.buttons.allElementsBoundByIndex.contains(where: \.hasFocus) {
            remote.press(.up)
            usleep(400_000)
        }
        moveFocus(to: tab, with: .right)
        remote.press(.select)
        let open = app.buttons["settings.watchBroadcast"]
        XCTAssertTrue(open.waitForExistence(timeout: 20), "Settings offers Watch a broadcast")
        // the form's first row (in a tvOS form the focused element is the row, not the button
        // the identifier is on: the row is known by its words)
        let focused = { self.app.descendants(matching: .any).element(matching: NSPredicate(format: "hasFocus == true")) }
        for _ in 0..<15 where !(focused().exists && focused().label.contains("Watch a broadcast")) {
            remote.press(.down)
            usleep(400_000)
        }
        XCTAssertTrue(focused().label.contains("Watch a broadcast"), "Watch a broadcast has the focus: \(focused().label)")
        remote.press(.select)
        XCTAssertTrue(app.textFields["guest.publisher"].waitForExistence(timeout: 20), "the guest space opened")
        XCTAssertTrue(home.seen.contains("POST /api/v1/pair"), "and asked the edge for a pairing, not the home")
        remote.press(.menu)
        XCTAssertTrue(open.waitForExistence(timeout: 20), "closed: back in Settings, still signed in")
    }

    // MARK: helpers

    /// Types into `field`, reached by pressing `direction` (the code is beside the handle).
    private func type(_ text: String, into field: XCUIElement, from direction: XCUIRemote.Button = .down) {
        XCTAssertTrue(field.waitForExistence(timeout: 20))
        field.typeExactly(text, in: app) {
            self.moveFocus(to: field, with: direction)
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
