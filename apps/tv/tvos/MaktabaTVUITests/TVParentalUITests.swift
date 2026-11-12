import XCTest

/// Epic 57 on Apple TV (stories 57.11 and 57.15), with the Siri remote, against `MockHome`
/// (served by the test runner on 127.0.0.1; no real home, no real account): the blocked page
/// with the focus on Ask a parent, the parent PIN on a pad of focusable keys, the time left,
/// the time's-up screen that does not try again on its own, asking for more time, live TV's
/// masked programme and its slate, Settings' managed line, and the device's Screen Time
/// limits. The mock answers as the Go handlers do (`api/internal/handlers/parental`).
///
/// The mock's account is `tester` (`u1`); `MockHome.parental` makes it a restricted child.
/// With MAKTABA_SHOTS set, each step's screenshot is also written there.
@MainActor
final class TVParentalUITests: XCTestCase {
    let remote = XCUIRemote.shared
    var app: XCUIApplication!
    var home: MockHome!
    private var language = "en"

    private static let child = #"{"restricted":true,"max_tier":9,"unrated":"hide","version":4}"#
    private static let notFound = #"{"type":"https://maktaba.tv/problems/not-found","title":"not found","status":404}"#
    private static let huda = #"{"guardians":[{"id":"g1","username":"Huda"}]}"#
    private static let videos = (1...12).map { "v\($0)" }

    override func setUp() async throws {
        continueAfterFailure = false
        home = try MockHome()
        try home.start()
        // playing asks for a token that lasts (the pairing's is not a JWT): a refresh gives one
        home.stub("POST", "/api/auth/refresh", body: #"{"access_token":"\#(TVLiveUITests.jwt)","refresh_token":"ui-refresh-2"}"#)
        app = XCUIApplication()
    }

    override func tearDown() async throws {
        home?.stop()
    }

    // MARK: 57.11

    /// T02: a restricted child opens something the home does not give (a hidden video
    /// answers as a missing one): the blocked page, with the reason, no title of what was
    /// asked for, and the focus on Ask a parent. Select asks; Menu goes back.
    func testABlockedVideoIsTheBlockedPageWithTheFocusOnAskAParent() throws {
        childWithEveryVideoHidden()
        home.stub("POST", "/api/me/parental/requests", status: 201,
                  body: #"{"request":{"id":"r1","child_id":"u1","subject_kind":"video","subject_id":"v2","status":"pending","created_at":"{{now}}","expires_at":"{{now+604800}}"}}"#)
        signIn("en")
        let opened = openFirstCard()

        XCTAssertTrue(element("blocked.title").waitForExistence(timeout: 20), "the blocked page; the home saw \(home.seen.suffix(8))")
        XCTAssertEqual(element("blocked.title").label, "This isn't available")
        XCTAssertEqual(element("blocked.reason").label, "Your account is limited to 9+.")
        XCTAssertEqual(element("blocked.lock").label, "Not available", "VoiceOver reads the lock")
        XCTAssertEqual(element("blocked.onPhone").label, "A parent can answer from Maktaba on their iPhone or iPad.")
        XCTAssertFalse(app.buttons["Play"].exists, "nothing of the video's page")
        XCTAssertFalse(element("blocked.pin").exists, "no one has a PIN: no Enter parent PIN")
        let ask = app.buttons["blocked.ask"]
        XCTAssertTrue(wait(timeout: 10) { ask.hasFocus }, "the focus lands on Ask a parent")
        shot("tv-parental-blocked-en")

        remote.press(.select)
        XCTAssertTrue(element("blocked.askLine").waitForExistence(timeout: 10), "the request's line")
        XCTAssertEqual(element("blocked.askLine").label, "Request sent. You'll see it here when a parent answers.")
        let sent = body("POST", "/api/me/parental/requests")
        XCTAssertEqual(sent?["subject_kind"] as? String, "video")
        XCTAssertEqual(sent?["subject_id"] as? String, opened, "the id the child already had")
        XCTAssertTrue(wait(timeout: 5) { !ask.isEnabled }, "asked once: the button waits")
        XCTAssertTrue(wait(timeout: 5) { self.app.buttons["blocked.back"].hasFocus }, "and Back takes the focus")

        remote.press(.menu)
        XCTAssertTrue(element("blocked.title").waitForNonExistence(timeout: 10), "Menu returns")
        XCTAssertTrue(element("home.row.continue").waitForExistence(timeout: 10), "to Home")
    }

    /// The child's own requests (`GET /api/me/parental/requests`) with the remote: while a
    /// request waits, Check again has the focus; a parent's no is said and the focus goes to
    /// Ask again; a parent's yes, seen while the page watched, opens the video's page.
    func testAParentsNoIsSaidWithAskAgainAndAParentsYesOpensTheVideo() throws {
        childWithEveryVideoHidden()
        home.stub("GET", "/api/me/parental/requests", body: #"{"requests":[]}"#)
        let made = { (id: String) in
            #"{"request":{"id":"\#(id)","child_id":"u1","subject_kind":"video","subject_id":"v2","status":"pending","created_at":"{{now}}","expires_at":"{{now+604800}}"}}"#
        }
        home.stub("POST", "/api/me/parental/requests", status: 201, body: made("r1"))
        signIn("en")
        let opened = openFirstCard()
        let request = { (id: String, status: String) in
            #"{"id":"\#(id)","subject_kind":"video","subject_id":"\#(opened)","status":"\#(status)","created_at":"{{now-60}}","expires_at":"{{now+604800}}"}"#
        }
        XCTAssertTrue(element("blocked.title").waitForExistence(timeout: 20), "the blocked page; the home saw \(home.seen.suffix(8))")
        let ask = app.buttons["blocked.ask"], check = app.buttons["blocked.check"]
        XCTAssertTrue(wait(timeout: 10) { ask.hasFocus }, "the focus lands on Ask a parent")
        XCTAssertTrue(wait(timeout: 10) { self.count("GET", "/api/me/parental/requests") >= 1 }, "the child's own requests were read")
        XCTAssertFalse(check.exists, "nothing waits: nothing to check")

        remote.press(.select)
        XCTAssertTrue(check.waitForExistence(timeout: 10), "a request waits: Check again")
        XCTAssertEqual(check.label, "Check again")
        XCTAssertEqual(ask.label, "Waiting for a parent")
        XCTAssertTrue(wait(timeout: 5) { !ask.isEnabled }, "asked once: the button waits")
        XCTAssertTrue(wait(timeout: 5) { check.hasFocus }, "and Check again takes the focus")

        // the parent says no on their own phone
        home.stub("GET", "/api/me/parental/requests", body: #"{"requests":[\#(request("r1", "denied"))]}"#)
        remote.press(.select)
        XCTAssertTrue(wait(timeout: 10) { self.element("blocked.askLine").label == "A parent said no." }, "the no is said")
        XCTAssertEqual(ask.label, "Ask again")
        XCTAssertTrue(wait(timeout: 5) { ask.isEnabled && ask.hasFocus }, "Ask again has the focus")
        XCTAssertFalse(check.exists, "nothing waits any more")
        shot("tv-parental-blocked-denied-en")

        // asked again; this time the parent says yes: the home gives the video and the limit moved
        home.stub("POST", "/api/me/parental/requests", status: 201, body: made("r2"))
        remote.press(.select)
        XCTAssertTrue(wait(timeout: 10) { check.exists && check.hasFocus }, "the second request waits")
        XCTAssertEqual(count("POST", "/api/me/parental/requests"), 2)
        home.stub("GET", "/api/videos/\(opened)", body: try XCTUnwrap(home.fixture(named: "video-\(opened)")))
        home.stub("GET", "/api/me/videos/\(opened)", body: try XCTUnwrap(home.fixture(named: "me-video-\(opened)")))
        home.stub("GET", "/api/me/parental", body: #"{"restricted":true,"max_tier":9,"unrated":"hide","version":5}"#)
        home.stub("GET", "/api/me/parental/requests",
                  body: #"{"requests":[\#(request("r2", "approved")),\#(request("r1", "denied"))]}"#)
        remote.press(.select)
        XCTAssertTrue(wait(timeout: 20) { !self.element("blocked.title").exists },
                      "the video's page after the yes; the home saw \(home.seen.suffix(8))")
        XCTAssertTrue(app.buttons.matching(NSPredicate(format: "label BEGINSWITH %@ OR label BEGINSWITH %@", "Play", "Resume")).firstMatch
            .waitForExistence(timeout: 20), "its Play or Resume")
    }

    /// T03: the same in Arabic, then Live TV: a programme above the limit reads "Not
    /// available" with nothing of its title, the grid runs right to left, and a channel whose
    /// programme now is above the limit shows the slate and does not play.
    func testTheBlockedPageAndTheMaskedProgrammeInArabic() throws {
        childWithEveryVideoHidden()
        let now = try XCTUnwrap(home.fixture(named: "channels-now"))
        let title = #""title": "Tafsir al-Kahf, part 2","#
        let range = try XCTUnwrap(now.range(of: title), "ch-1's programme")
        home.stub("GET", "/api/channels/now", body: now.replacingCharacters(in: range, with: #""title": "", "restricted": true,"#))
        home.stub("GET", "/api/channels/ch-1/access", body: #"{"channel_id":"ch-1","restricted":true,"allowed":false,"until":null}"#)
        signIn("ar")
        openFirstCard()

        XCTAssertTrue(element("blocked.title").waitForExistence(timeout: 20), "the blocked page")
        XCTAssertEqual(element("blocked.title").label, "هذا غير متاح")
        XCTAssertTrue(element("blocked.reason").label.hasPrefix("حسابك مقيَّد بـ"), element("blocked.reason").label)
        XCTAssertTrue(element("blocked.reason").label.contains("+9"), "the tier reads +9: \(element("blocked.reason").label)")
        XCTAssertEqual(app.buttons["blocked.ask"].label, "اطلب من أحد الوالدين")
        XCTAssertEqual(app.buttons["blocked.back"].label, "رجوع")
        XCTAssertEqual(element("blocked.onPhone").label, "يمكن لأحد الوالدين الرد من Maktaba على iPhone أو iPad.")
        XCTAssertTrue(wait(timeout: 10) { self.app.buttons["blocked.ask"].hasFocus }, "the focus lands on Ask a parent")
        shot("tv-parental-blocked-ar")
        remote.press(.menu)
        XCTAssertTrue(element("blocked.title").waitForNonExistence(timeout: 10), "Menu returns")

        openTab("البث المباشر")
        let first = app.buttons["live.channel.ch-1"], second = app.buttons["live.channel.ch-2"]
        XCTAssertTrue(first.waitForExistence(timeout: 20), "the Now grid; the home saw \(home.seen.suffix(8))")
        XCTAssertTrue(first.label.contains("غير متاح"), "the masked programme: \(first.label)")
        XCTAssertFalse(first.label.contains("Tafsir al-Kahf, part 2"), "nothing of its title")
        XCTAssertGreaterThan(first.frame.minX, second.frame.minX, "right to left: channel 1 is right of channel 2")
        moveFocus(to: first, with: .down)
        shot("tv-parental-live-masked-ar")

        remote.press(.select)
        let slate = element("live.slate")
        XCTAssertTrue(slate.waitForExistence(timeout: 20), "the slate; the home saw \(home.seen.suffix(8))")
        XCTAssertTrue(slate.label.contains("غير متاح الآن. تعود هذه القناة عند بدء البرنامج التالي."), slate.label)
        XCTAssertTrue(home.seen.contains("GET /api/channels/ch-1/access"), "the home was asked first")
        sleep(3)
        XCTAssertFalse(home.seen.contains("GET /stream/channel/ch-1/index.m3u8"), "nothing was tuned")
        XCTAssertFalse(home.seen.contains("POST /api/tv/remote/tune"), "nor a remote session")
        shot("tv-parental-live-slate-ar")
    }

    // MARK: 57.06 on the TV

    /// Enter parent PIN with the remote, in Arabic: the pad's keys keep their phone order
    /// left to right and the focus moves over them as drawn; a wrong PIN says so, the right
    /// one opens the video.
    func testAParentsPINOnTheRemoteOpensTheVideo() throws {
        childWithEveryVideoHidden(pinHolders: Self.huda)
        home.stub("POST", "/api/me/parental/unlock", status: 403,
                  body: #"{"type":"https://maktaba.tv/problems/parental-pin-wrong","status":403,"code":"pin_wrong"}"#)
        signIn("ar")
        let opened = openFirstCard()
        let pin = app.buttons["blocked.pin"]
        XCTAssertTrue(pin.waitForExistence(timeout: 20), "Enter parent PIN")
        XCTAssertTrue(wait(timeout: 10) { self.app.buttons["blocked.ask"].hasFocus }, "Ask a parent first")
        moveFocus(to: pin, with: .down)
        remote.press(.select)

        XCTAssertTrue(element("pin.pad").waitForExistence(timeout: 10), "one person has a PIN: straight to the pad")
        let one = app.buttons["pin.key.1"], two = app.buttons["pin.key.2"], three = app.buttons["pin.key.3"]
        XCTAssertTrue(wait(timeout: 10) { one.hasFocus }, "the focus starts on 1")
        // 1 2 3 left to right, in Arabic too; Right on the remote goes from 1 to 2
        XCTAssertLessThan(one.frame.minX, two.frame.minX)
        XCTAssertLessThan(two.frame.minX, three.frame.minX)
        XCTAssertLessThan(one.frame.minY, app.buttons["pin.key.4"].frame.minY, "1 2 3 on the top row")
        remote.press(.right)
        XCTAssertTrue(wait(timeout: 5) { two.hasFocus }, "Right moves from 1 to 2")
        shot("tv-parental-pin-ar")
        for _ in 0..<6 {
            remote.press(.select)
            usleep(300_000)
        }
        XCTAssertTrue(element("pin.line").waitForExistence(timeout: 10), "the wrong PIN's line")
        XCTAssertEqual(element("pin.line").label, "هذا الرمز غير صحيح.")
        let sent = body("POST", "/api/me/parental/unlock")
        XCTAssertEqual(sent?["pin"] as? String, "222222", "Western digits, as pressed")
        XCTAssertEqual(sent?["guardian_id"] as? String, "g1")
        XCTAssertEqual(sent?["subject_kind"] as? String, "video")
        XCTAssertEqual(sent?["subject_id"] as? String, opened)
        shot("tv-parental-pin-wrong-ar")

        // the right PIN: the home allows the video until 04:00, and the limit moved
        home.stub("POST", "/api/me/parental/unlock",
                  body: #"{"rule":{"id":"ru1","user_id":"u1","effect":"allow","subject_kind":"video","subject_id":"\#(opened)","expires_at":"{{now+3600}}","created_at":"{{now}}"}}"#)
        home.stub("GET", "/api/videos/\(opened)", body: try XCTUnwrap(home.fixture(named: "video-\(opened)")))
        home.stub("GET", "/api/me/videos/\(opened)", body: try XCTUnwrap(home.fixture(named: "me-video-\(opened)")))
        home.stub("GET", "/api/me/parental", body: #"{"restricted":true,"max_tier":9,"unrated":"hide","version":5}"#)
        XCTAssertTrue(wait(timeout: 10) { self.app.buttons.allElementsBoundByIndex.contains { $0.identifier.hasPrefix("pin.key.") && $0.hasFocus } },
                      "the keys take the remote again")
        let focused = app.buttons.allElementsBoundByIndex.first { $0.identifier.hasPrefix("pin.key.") && $0.hasFocus }
        let digit = String(try XCTUnwrap(focused).identifier.dropFirst("pin.key.".count))
        for _ in 0..<6 {
            remote.press(.select)
            usleep(300_000)
        }
        XCTAssertTrue(wait(timeout: 10) { self.count("POST", "/api/me/parental/unlock") == 2 }, "the second PIN sent")
        XCTAssertEqual(body("POST", "/api/me/parental/unlock")?["pin"] as? String, String(repeating: digit, count: 6))
        XCTAssertTrue(wait(timeout: 20) { !self.element("blocked.title").exists && !self.element("pin.pad").exists },
                      "the video's page after the PIN; the home saw \(home.seen.suffix(8))")
        XCTAssertTrue(app.buttons.matching(NSPredicate(format: "label BEGINSWITH %@ OR label BEGINSWITH %@", "تشغيل", "استئناف")).firstMatch
            .waitForExistence(timeout: 20), "its Play or Resume")
    }

    // MARK: 57.15

    /// The time left today on Home and over the video; then the home refuses a heartbeat
    /// (`parental-time`): the video stops, the time's-up screen says when watching opens
    /// again and where the video stopped, and no stream is asked for again until the home
    /// says there is time: then the video goes on by itself.
    func testTimeUpMidVideoStopsIsNotTriedAgainAndGoesOnWhenTimeIsGiven() throws {
        home.parental(Self.child, time: #"{"allowed":true,"limited":true,"remaining_seconds":720,"slot_ends_at":null,"next_allowed_at":null,"now":"{{now}}"}"#)
        signIn("en")
        XCTAssertTrue(element("time.left").waitForExistence(timeout: 30), "the time left on Home; the home saw \(home.seen.suffix(8))")
        XCTAssertTrue(element("time.left").label.hasSuffix("left today"), element("time.left").label)
        shot("tv-parental-time-left-en")

        openFirstCard()
        play()
        XCTAssertTrue(element("queue.player").waitForExistence(timeout: 40), "the video plays; the home saw \(home.seen.suffix(8))")
        XCTAssertTrue(element("time.left").waitForExistence(timeout: 30), "the time left over the video")
        XCTAssertTrue(wait(timeout: 30) { self.count("POST", "/api/stream/sessions/ss-1/progress") >= 1 },
                      "the heartbeat, well inside a minute")

        // time runs out: the next heartbeat is refused
        home.stub("GET", "/api/me/parental/time", body: #"{"allowed":false,"limited":true,"reason":"limit_reached","remaining_seconds":0,"slot_ends_at":null,"next_allowed_at":"{{now+3600}}","now":"{{now}}"}"#)
        home.stub("POST", "/api/stream/sessions/ss-1/progress", status: 403, body: timeOver("limit_reached"))
        XCTAssertTrue(element("timeUp.title").waitForExistence(timeout: 40), "the time's-up screen")
        XCTAssertEqual(element("timeUp.title").label, "Time's up for today")
        XCTAssertTrue(element("timeUp.next").label.hasPrefix("You can watch again at "), element("timeUp.next").label)
        XCTAssertTrue(element("timeUp.saved").exists, "where the video stopped")
        XCTAssertEqual(element("timeUp.ask").label, "Ask a parent for more time")
        XCTAssertFalse(element("queue.player").exists, "the picture is gone")
        shot("tv-parental-time-up-en")
        let opened = count("POST", "/api/stream/sessions")
        sleep(14)
        XCTAssertEqual(count("POST", "/api/stream/sessions"), opened, "not tried again")
        XCTAssertTrue(element("timeUp.title").exists)

        // a parent gives time from their phone: the home says so at the next ask
        home.stub("GET", "/api/me/parental/time", body: #"{"allowed":true,"limited":true,"remaining_seconds":1800,"slot_ends_at":null,"next_allowed_at":null,"now":"{{now}}"}"#)
        home.stub("POST", "/api/stream/sessions/ss-1/progress", status: 204)
        XCTAssertTrue(element("queue.player").waitForExistence(timeout: 60), "the video goes on without a restart")
        XCTAssertEqual(count("POST", "/api/stream/sessions"), opened + 1, "opened once more, when the home allowed it")
    }

    /// In Arabic, at the start: the home refuses the stream itself. Ask for more time sends
    /// the minutes chosen as a `time` request; a parent's PIN gives them.
    func testTimeUpAtTheStartAsksForMoreTimeInArabic() throws {
        home.parental(Self.child, time: #"{"allowed":false,"limited":true,"reason":"outside_slot","remaining_seconds":0,"slot_ends_at":null,"next_allowed_at":"{{now+3600}}","now":"{{now}}"}"#)
        home.stub("GET", "/api/me/parental/guardians", body: Self.huda)
        home.stub("POST", "/api/stream/sessions", status: 403, body: timeOver("outside_slot"))
        home.stub("POST", "/api/me/parental/requests", status: 201,
                  body: #"{"request":{"id":"r9","child_id":"u1","subject_kind":"time","subject_id":"15","minutes":15,"status":"pending","created_at":"{{now}}","expires_at":"{{now+3600}}"}}"#)
        signIn("ar")
        XCTAssertTrue(element("time.up.banner").waitForExistence(timeout: 30), "Home says the time is over")
        XCTAssertTrue(element("time.up.banner").label.contains("لم يحن وقت المشاهدة بعد"), element("time.up.banner").label)
        openFirstCard()
        play()

        XCTAssertTrue(element("timeUp.title").waitForExistence(timeout: 30), "the time's-up screen; the home saw \(home.seen.suffix(8))")
        XCTAssertEqual(element("timeUp.title").label, "لم يحن وقت المشاهدة بعد")
        XCTAssertTrue(element("timeUp.next").label.hasPrefix("يمكنك المشاهدة مجددًا عند"), element("timeUp.next").label)
        XCTAssertEqual(element("timeUp.ask").label, "اطلب من أحد والديك وقتًا إضافيًا")
        XCTAssertEqual(count("POST", "/api/stream/sessions"), 1, "asked once")
        let askMore = app.buttons["timeUp.askMore"]
        XCTAssertTrue(askMore.waitForExistence(timeout: 10), "Ask for more time")
        XCTAssertTrue(wait(timeout: 10) { askMore.hasFocus }, "with the focus")
        // 15, 30 or 60 minutes: mirrored, 15 is the rightmost
        let fifteen = app.buttons["timeUp.minutes.15"], sixty = app.buttons["timeUp.minutes.60"]
        XCTAssertGreaterThan(fifteen.frame.minX, sixty.frame.minX, "right to left")
        remote.press(.up)
        usleep(500_000)
        moveFocus(to: fifteen, with: .right)
        remote.press(.select)
        shot("tv-parental-time-up-ar")
        moveFocus(to: askMore, with: .down)
        remote.press(.select)
        XCTAssertTrue(wait(timeout: 10) { self.count("POST", "/api/me/parental/requests") == 1 }, "the time request sent")
        XCTAssertEqual(body("POST", "/api/me/parental/requests")?["subject_kind"] as? String, "time")
        XCTAssertEqual(body("POST", "/api/me/parental/requests")?["subject_id"] as? String, "15")
        XCTAssertTrue(element("timeUp.askLine").waitForExistence(timeout: 10), "the request's line")

        // a parent who is there types their PIN: the minutes are given, and the video opens
        home.stub("POST", "/api/me/parental/unlock", body: #"{"grant":{"id":"gr1","minutes":15}}"#)
        let stream = try XCTUnwrap(home.fixture(named: "stream-session"))
        home.stub("POST", "/api/stream/sessions", body: stream)
        home.stub("GET", "/api/me/parental/time", body: #"{"allowed":true,"limited":true,"remaining_seconds":900,"slot_ends_at":null,"next_allowed_at":null,"now":"{{now}}"}"#)
        let pin = app.buttons["timeUp.pin"]
        moveFocus(to: pin, with: .up)
        remote.press(.select)
        XCTAssertTrue(element("pin.pad").waitForExistence(timeout: 10), "the pad")
        XCTAssertTrue(wait(timeout: 10) { self.app.buttons["pin.key.1"].hasFocus }, "the focus starts on 1")
        for _ in 0..<6 {
            remote.press(.select)
            usleep(300_000)
        }
        XCTAssertTrue(wait(timeout: 10) { self.count("POST", "/api/me/parental/unlock") == 1 }, "the PIN sent")
        let unlock = body("POST", "/api/me/parental/unlock")
        XCTAssertEqual(unlock?["subject_kind"] as? String, "time")
        XCTAssertEqual(unlock?["subject_id"] as? String, "15", "the minutes chosen, as text")
        XCTAssertEqual(unlock?["pin"] as? String, "111111")
        XCTAssertTrue(element("queue.player").waitForExistence(timeout: 40), "the video plays; the home saw \(home.seen.suffix(8))")
    }

    // MARK: 57.04 on the TV, Settings

    /// The Apple TV's Screen Time TV limit (the double: TV-PG, 400) is posted with no age
    /// range, the token refreshed, and Settings says a parent manages the account, its limit
    /// and the device's.
    func testTheTVsScreenTimeLimitIsSentAndSettingsSaysWhoManagesTheAccount() throws {
        home.parental(#"{"restricted":true,"max_tier":13,"unrated":"hide","version":2,"device_limit":9}"#)
        home.stub("POST", "/api/me/age-signal", body: #"{"tier_limit":9,"pending_looser":false,"refresh":true}"#)
        signIn("en", extra: ["-maktaba-age-signal", "none::1000:400"])
        XCTAssertTrue(wait(timeout: 20) { self.count("POST", "/api/me/age-signal") >= 1 }, "the signal posted; the home saw \(home.seen.suffix(8))")
        let sent = body("POST", "/api/me/age-signal")
        XCTAssertEqual(sent?["tv_ceiling"] as? Int, 400)
        XCTAssertEqual(sent?["movie_ceiling"] as? Int, 1000)
        XCTAssertNil(sent?["age_range"], "tvOS has no age range")
        XCTAssertTrue(wait(timeout: 10) { self.count("POST", "/api/auth/refresh") >= 1 }, "the token refreshed after it")

        openTab("Settings")
        XCTAssertTrue(element("settings.parental.managed").waitForExistence(timeout: 20), "the managed line")
        XCTAssertEqual(element("settings.parental.managed").label, "A parent manages this account.")
        XCTAssertTrue(element("settings.parental.limit").label.contains("13+"), element("settings.parental.limit").label)
        XCTAssertEqual(element("settings.parental.device").label, "This device is limited to 9+.")
        XCTAssertEqual(count("POST", "/api/me/age-signal"), 1, "sent once: nothing changed since")
        shot("tv-parental-settings-en")
    }

    /// The build has no Family Controls entitlement: reading the system's own limits must
    /// leave the app running, and send the home only what could be read, never an age range.
    func testTheSystemsOwnLimitsAreReadWithoutAnyEntitlement() throws {
        home.parental(#"{"restricted":false}"#)
        home.stub("POST", "/api/me/age-signal", body: #"{"tier_limit":null,"pending_looser":false,"refresh":false}"#)
        signIn("en", extra: ["-maktaba-age-signal", "device"])
        XCTAssertTrue(element("home.row.continue").waitForExistence(timeout: 20), "Home")
        sleep(3)
        XCTAssertEqual(app.state, .runningForeground, "the app runs on after reading the device")
        if let sent = body("POST", "/api/me/age-signal") {
            XCTAssertNil(sent["age_range"], "no age range from a TV: \(sent)")
            let a = XCTAttachment(string: String(describing: sent))
            a.name = "age-signal-sent"
            a.lifetime = .keepAlways
            add(a)
        }
        openTab("Settings")
        XCTAssertTrue(app.staticTexts["Signed in as"].waitForExistence(timeout: 20) || app.cells.firstMatch.waitForExistence(timeout: 5), "Settings")
        XCTAssertFalse(element("settings.parental.managed").exists, "an account that is not restricted has no managed line")
    }

    // MARK: helpers

    /// A restricted child for whom the home hides every video (each answers 404, as a
    /// missing one does), so whichever card is opened is blocked.
    private func childWithEveryVideoHidden(pinHolders: String = #"{"guardians":[]}"#) {
        home.parental(Self.child)
        home.stub("GET", "/api/me/parental/guardians", body: pinHolders)
        for v in Self.videos {
            home.stub("GET", "/api/videos/\(v)", status: 404, body: Self.notFound)
            home.stub("GET", "/api/me/videos/\(v)", status: 404, body: Self.notFound)
        }
    }

    private func timeOver(_ reason: String) -> String {
        #"{"type":"https://maktaba.tv/problems/parental-time","title":"forbidden","status":403,"detail":"this account's watching time is over for now","code":"parental_time","reason":"\#(reason)","next_allowed_at":"{{now+3600}}"}"#
    }

    /// Down from the tab bar onto Home's first card, Select; returns the video it names.
    @discardableResult
    private func openFirstCard() -> String {
        XCTAssertTrue(element("home.row.continue").waitForExistence(timeout: 30), "Home; the home saw \(home.seen.suffix(8))")
        var card: XCUIElement?
        for _ in 0..<6 {
            card = app.buttons.allElementsBoundByIndex.first { $0.hasFocus && $0.identifier.hasPrefix("video.card.") }
            if card != nil { break }
            remote.press(.down)
            usleep(600_000)
        }
        let id = card?.identifier ?? ""
        XCTAssertTrue(id.hasPrefix("video.card."), "a card has the focus")
        // the button carries its card's identifiers joined ("video.card.v2-video.card.v2-…"),
        // each with ".art" once its picture showed: the first names the video
        let first = id.dropFirst("video.card.".count).split(separator: "-").first.map(String.init) ?? ""
        let video = first.replacingOccurrences(of: ".art", with: "")
        remote.press(.select)
        return video
    }

    /// The video's page: Select on Resume or Play (the first has the focus).
    private func play() {
        let button = app.buttons.matching(NSPredicate(format: "label BEGINSWITH %@ OR label BEGINSWITH %@ OR label BEGINSWITH %@ OR label BEGINSWITH %@",
                                                      "Resume", "Play", "استئناف", "تشغيل")).firstMatch
        XCTAssertTrue(button.waitForExistence(timeout: 20), "the video's page; the home saw \(home.seen.suffix(8))")
        XCTAssertTrue(wait(timeout: 10) { self.app.buttons.allElementsBoundByIndex.contains { $0.hasFocus } }, "a button has the focus")
        remote.press(.select)
    }

    private func body(_ method: String, _ path: String) -> [String: Any]? {
        guard let r = home.requests.last(where: { $0.method == method && $0.path == path }) else { return nil }
        return (try? JSONSerialization.jsonObject(with: r.body)) as? [String: Any]
    }

    private func count(_ method: String, _ path: String) -> Int {
        home.seen.filter { $0 == "\(method) \(path)" }.count
    }

    private func element(_ id: String) -> XCUIElement { app.descendants(matching: .any)[id].firstMatch }

    /// First launch: the mock home's address typed, then the code approved elsewhere.
    private func signIn(_ language: String, extra: [String] = []) {
        self.language = language
        app.launchArguments = ["-uitest-reset", "-AppleLanguages", "(\(language))",
                               "-AppleLocale", language == "ar" ? "ar_SA" : "en_US"] + extra
        app.launch()
        type(home.url, into: app.textFields["picker.address"])
        pressIfStillThere(app.buttons["picker.connect"], with: .right)
        XCTAssertTrue(app.staticTexts["pairing-code"].waitForExistence(timeout: 20), "the code; the home saw \(home.seen)")
        home.approve()
        XCTAssertTrue(app.tabBars.firstMatch.waitForExistence(timeout: 20), "approved: signed in")
    }

    /// Up to the tab bar, along it to `title` (right in English, left in Arabic), Select.
    private func openTab(_ title: String) {
        let tab = app.tabBars.buttons[title]
        XCTAssertTrue(tab.waitForExistence(timeout: 20), "the \(title) tab")
        for _ in 0..<6 where !app.tabBars.buttons.allElementsBoundByIndex.contains(where: \.hasFocus) {
            remote.press(.up)
            usleep(400_000)
        }
        moveFocus(to: tab, with: language == "ar" ? .left : .right)
        remote.press(.select)
        sleep(1)
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
