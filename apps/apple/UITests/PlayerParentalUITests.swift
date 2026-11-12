import XCTest

/// Epic 57 on iPhone and iPad, against `MockHome` (no real home): the restricted Player
/// (57.05), the device's signal (57.04), Ask a parent and the parent PIN (57.06), Family and
/// Activity for a guardian (57.07, 57.08), time (57.15) and live TV's slate (57.10). The mock
/// answers as the Go handlers do (`api/internal/handlers/parental`).
///
/// The mock's account is `tester` (`u1`). `MockHome.parental` makes it a restricted child, a
/// guardian, or leaves it alone; the fixtures' home itself has none of Epic 57's routes.
final class PlayerParentalUITests: PlayerUITestCase {
    private static let child = #"{"restricted":true,"max_tier":9,"unrated":"hide","version":4}"#
    private static let notFound = #"{"type":"https://maktaba.tv/problems/not-found","title":"not found","status":404}"#
    private static let huda = #"{"guardians":[{"id":"g1","username":"Huda"}]}"#

    private func waitUntil(_ what: String, timeout: TimeInterval = 15, _ condition: () -> Bool) {
        let end = Date().addingTimeInterval(timeout)
        while !condition(), Date() < end { RunLoop.current.run(until: Date().addingTimeInterval(0.2)) }
        XCTAssertTrue(condition(), what)
    }

    private func body(_ method: String, _ path: String) -> [String: Any]? {
        guard let r = home.requests.last(where: { $0.method == method && $0.path == path }) else { return nil }
        return (try? JSONSerialization.jsonObject(with: r.body)) as? [String: Any]
    }

    private func count(_ method: String, _ path: String) -> Int {
        home.seen.filter { $0 == "\(method) \(path)" }.count
    }

    /// A restricted child with `v2` hidden (the home answers a hidden video as a missing one).
    private func childWithHiddenVideo(pinHolders: String = #"{"guardians":[]}"#) {
        home.parental(Self.child)
        home.stub("GET", "/api/me/parental/guardians", body: pinHolders)
        home.stub("GET", "/api/videos/v2", status: 404, body: Self.notFound)
        home.stub("GET", "/api/me/videos/v2", status: 404, body: Self.notFound)
    }

    // MARK: 57.05

    /// T03: a link to a blocked video is the blocked page, with the reason and no title or
    /// poster of what was asked for; Ask a parent sends the id the child already had.
    func testALinkToABlockedVideoIsTheBlockedPageAndAsksAParent() {
        childWithHiddenVideo()
        home.stub("POST", "/api/me/parental/requests", status: 201,
                  body: #"{"request":{"id":"r1","child_id":"u1","subject_kind":"video","subject_id":"v2","status":"pending","created_at":"2026-11-11T10:00:00Z","expires_at":"2026-11-18T10:00:00Z"}}"#)
        launchSignedIn(sections: ["home"], open: "video/v2")
        XCTAssertTrue(element("screen.blocked").waitForExistence(timeout: 20), "the blocked page")
        XCTAssertEqual(element("blocked.title").label, "This isn't available")
        XCTAssertEqual(element("blocked.reason").label, "Your account is limited to 9+.")
        XCTAssertEqual(element("blocked.lock").label, "Not available", "VoiceOver reads the lock")
        XCTAssertFalse(element("video.notAvailable").exists, "not the plain 'not available'")
        XCTAssertFalse(app.staticTexts["السيرة النبوية، الدرس ١١"].exists, "no title of what was asked for")
        XCTAssertFalse(element("blocked.pin").exists, "no one has a PIN: no Enter parent PIN")
        for id in ["blocked.ask", "blocked.back"] {
            XCTAssertGreaterThanOrEqual(element(id).frame.height, 44, "\(id) is at least 44 pt tall")
        }
        shot("parental-blocked")

        element("blocked.ask").tap()
        XCTAssertTrue(element("blocked.askLine").waitForExistence(timeout: 10), "the request's line")
        XCTAssertEqual(element("blocked.askLine").label, "Request sent. You'll see it here when a parent answers.")
        let sent = body("POST", "/api/me/parental/requests")
        XCTAssertEqual(sent?["subject_kind"] as? String, "video")
        XCTAssertEqual(sent?["subject_id"] as? String, "v2")
        XCTAssertFalse(element("blocked.ask").isEnabled, "asked once: the button waits")

        element("blocked.back").tap()
        XCTAssertTrue(element("screen.home").waitForExistence(timeout: 10), "Back goes to the screen before")
    }

    /// T04: the same page in Arabic, mirrored, with the tier's plus on its side.
    func testTheBlockedPageInArabic() {
        childWithHiddenVideo()
        launchSignedIn("ar", sections: ["home"], open: "video/v2")
        XCTAssertTrue(element("screen.blocked").waitForExistence(timeout: 20), "the blocked page")
        XCTAssertEqual(element("blocked.title").label, "هذا غير متاح")
        XCTAssertTrue(element("blocked.reason").label.hasPrefix("حسابك مقيَّد بـ"), element("blocked.reason").label)
        XCTAssertTrue(element("blocked.reason").label.contains("+9"), "the tier reads +9: \(element("blocked.reason").label)")
        XCTAssertEqual(element("blocked.ask").label, "اطلب من أحد الوالدين")
        XCTAssertEqual(element("blocked.back").label, "رجوع")
        shot("parental-blocked")
    }

    /// T03: a restricted child's Profile says a parent manages the account and its limit,
    /// and has no Family (nor anything to manage the home).
    func testARestrictedChildsProfileSaysWhoManagesItAndHasNoFamily() {
        home.parental(Self.child)
        launchSignedIn(sections: ["profile"], section: "profile")
        XCTAssertTrue(element("profile.parental.managed").waitForExistence(timeout: 20), "the managed line")
        XCTAssertEqual(element("profile.parental.managed").label, "A parent manages this account.")
        XCTAssertTrue(element("profile.parental.limit").label.contains("9+"), element("profile.parental.limit").label)
        XCTAssertFalse(element("profile.family").exists, "a child has no Family")
        shot("parental-profile-child")
    }

    /// The acceptance's third case: a guardian loosens the limit; when the Player comes back
    /// to the front the limit's version has changed and the blocked page becomes the video.
    func testABlockedPageBecomesTheVideoWhenAParentAllowsIt() throws {
        childWithHiddenVideo()
        launchSignedIn(sections: ["home"], open: "video/v2")
        XCTAssertTrue(element("screen.blocked").waitForExistence(timeout: 20), "the blocked page")
        // the parent allows it on their own phone: the home now gives the video, and the limit moved
        let video = try XCTUnwrap(home.fixture(named: "video-v2"))
        let mine = try XCTUnwrap(home.fixture(named: "me-video-v2"))
        home.stub("GET", "/api/videos/v2", body: video)
        home.stub("GET", "/api/me/videos/v2", body: mine)
        home.stub("GET", "/api/me/parental", body: #"{"restricted":true,"max_tier":13,"unrated":"hide","version":5}"#)
        XCUIDevice.shared.press(.home)
        app.activate()
        XCTAssertTrue(element("video.details").waitForExistence(timeout: 20), "the video's page, without a restart")
        XCTAssertFalse(element("screen.blocked").exists)
    }

    // MARK: 57.06

    /// T09 (and T08's other half): Enter parent PIN, a wrong PIN, then the right one opens
    /// the video. The pad's keys keep their order in Arabic.
    func testAParentsPINOpensTheVideo() throws {
        childWithHiddenVideo(pinHolders: Self.huda)
        home.stub("POST", "/api/me/parental/unlock", status: 403,
                  body: #"{"type":"https://maktaba.tv/problems/parental-pin-wrong","status":403,"code":"pin_wrong"}"#)
        launchSignedIn("ar", sections: ["home"], open: "video/v2")
        XCTAssertTrue(element("blocked.pin").waitForExistence(timeout: 20), "Enter parent PIN")
        element("blocked.pin").tap()
        XCTAssertTrue(element("pin.pad").waitForExistence(timeout: 10), "one person has a PIN: straight to the pad")
        // 1 2 3 left to right, in Arabic too
        XCTAssertLessThan(element("pin.key.1").frame.minX, element("pin.key.2").frame.minX)
        XCTAssertLessThan(element("pin.key.2").frame.minX, element("pin.key.3").frame.minX)
        for d in [1, 2, 3, 4, 5, 6] { element("pin.key.\(d)").tap() }
        XCTAssertTrue(element("pin.line").waitForExistence(timeout: 10), "the wrong PIN's line")
        XCTAssertEqual(element("pin.line").label, "هذا الرمز غير صحيح.")
        let sent = body("POST", "/api/me/parental/unlock")
        XCTAssertEqual(sent?["pin"] as? String, "123456")
        XCTAssertEqual(sent?["guardian_id"] as? String, "g1")
        XCTAssertEqual(sent?["subject_id"] as? String, "v2")
        shot("parental-pin-wrong")

        // the right PIN: the home allows the video until 04:00 and says so
        home.stub("POST", "/api/me/parental/unlock",
                  body: #"{"rule":{"id":"ru1","user_id":"u1","effect":"allow","subject_kind":"video","subject_id":"v2","expires_at":"2026-11-12T04:00:00Z","created_at":"2026-11-11T10:00:00Z"}}"#)
        home.stub("GET", "/api/videos/v2", body: try XCTUnwrap(home.fixture(named: "video-v2")))
        home.stub("GET", "/api/me/videos/v2", body: try XCTUnwrap(home.fixture(named: "me-video-v2")))
        home.stub("GET", "/api/me/parental", body: #"{"restricted":true,"max_tier":9,"unrated":"hide","version":5}"#)
        for d in [6, 5, 4, 3, 2, 1] { element("pin.key.\(d)").tap() }
        waitUntil("the video's page after the PIN", timeout: 20) { !self.element("screen.blocked").exists && self.element("video.details").exists }
    }

    // MARK: 57.04

    /// T08: with the double sharing a guardian-declared 9–12, signing in posts the range,
    /// then refreshes the token; Profile shows the device's limit.
    func testTheDevicesAgeRangeIsSentAtSignInAndShownInProfile() {
        home.parental(#"{"restricted":false,"max_tier":9,"device_limit":9}"#)
        home.stub("POST", "/api/me/age-signal", body: #"{"tier_limit":9,"pending_looser":false,"refresh":true}"#)
        launchSignedIn(sections: ["profile"], section: "profile", extra: ["-maktaba-age-signal", "9-12:guardian_declared:1000:400"])
        waitUntil("the signal posted") { self.count("POST", "/api/me/age-signal") >= 1 }
        let sent = body("POST", "/api/me/age-signal")
        let range = sent?["age_range"] as? [String: Any]
        XCTAssertEqual(range?["lower"] as? Int, 9)
        XCTAssertEqual(range?["upper"] as? Int, 12)
        XCTAssertEqual(range?["declaration"] as? String, "guardian_declared")
        XCTAssertEqual(sent?["tv_ceiling"] as? Int, 400)
        waitUntil("the token refreshed after it") { self.count("POST", "/api/auth/refresh") >= 1 }
        XCTAssertTrue(element("profile.parental.device").waitForExistence(timeout: 15), "the device's limit")
        XCTAssertEqual(element("profile.parental.device").label, "This device is limited to 9+.")
        XCTAssertTrue(element("profile.parental.ageRange").label.contains("9–12"), element("profile.parental.ageRange").label)
        XCTAssertFalse(element("profile.parental.managed").exists, "the account itself is not restricted")
        XCTAssertEqual(count("POST", "/api/me/age-signal"), 1, "sent once: nothing changed since")
        shot("parental-profile-device")
    }

    // MARK: 57.07, 57.08

    private static let children = #"[{"id":"c2","username":"Sara","user_id":"c2","max_tier":13,"unrated":"hide","allow_only":false,"version":3,"updated_at":"2026-11-10T10:00:00Z"}]"#
    private static let account = #"{"policy":{"user_id":"c2","max_tier":13,"unrated":"hide","allow_only":false,"version":3,"updated_at":"2026-11-10T10:00:00Z"},"access":"guardian","guardians":[{"id":"u1","username":"tester","created_at":"2026-11-01T10:00:00Z"}],"signals":[{"refresh_family_id":"f1aa22bb-0000-0000-0000-000000000001","lower":9,"upper":12,"declaration":"guardian_declared","region":"US","tier_limit":9,"pending_looser":{"lower":18,"tier_limit":null,"observed_at":"2026-11-11T09:00:00Z"},"observed_at":"2026-11-10T09:00:00Z"}]}"#

    private func guardian() {
        home.parental(#"{"restricted":false}"#, children: Self.children)
        home.stub("GET", "/api/parental/requests",
                  body: #"{"requests":[{"id":"r1","child_id":"c2","subject_kind":"video","subject_id":"v2","status":"pending","created_at":"{{now-600}}","expires_at":"{{now+86400}}","child":{"id":"c2","username":"Sara"},"subject":{"kind":"video","id":"v2","title":"The long night","tier":16}}]}"#)
        home.stub("GET", "/api/users/c2/parental", body: Self.account)
        home.stub("GET", "/api/users/c2/parental/rules", body: #"{"rules":[]}"#)
        home.stub("GET", "/api/parental/users/c2/time-rules", body: #"{"rules":[],"time_zone":"Asia/Riyadh"}"#)
        home.stub("GET", "/api/parental/users/c2/time-usage",
                  body: #"{"from":"2026-11-05","to":"2026-11-11","time_zone":"Asia/Riyadh","today":"2026-11-11","usage":[{"day":"2026-11-11","library_id":"lib-lectures","seconds":1500}],"grants":[]}"#)
        home.stub("GET", "/api/users/c2/parental/activity",
                  body: #"{"events":[{"id":"e2","kind":"watched","at":"{{now-3600}}","subject":{"kind":"video","id":"v1","title":"Seerah 1","tier":0}},{"id":"e1","kind":"blocked","at":"{{now-7200}}","subject":{"kind":"video","id":"v9","title":"Night","tier":16}}]}"#)
    }

    /// T03 and T05: a guardian's Family lists their child and the request waiting; Approve
    /// (Today) is sent; Devices > Approve calls the clear route for that sign-in.
    func testAGuardiansFamilyAnswersARequestAndApprovesADevice() {
        guardian()
        home.stub("POST", "/api/parental/requests/r1/approve", body: #"{"request":{"id":"r1","status":"approved"}}"#)
        home.stub("POST", "/api/users/c2/parental/signals/f1aa22bb-0000-0000-0000-000000000001/clear",
                  body: #"{"tier_limit":null,"removed":false}"#)
        launchSignedIn(sections: ["profile"], section: "profile")
        XCTAssertTrue(element("profile.family").waitForExistence(timeout: 20), "Family, for a guardian")
        XCTAssertFalse(element("profile.parental.managed").exists)
        element("profile.family").tap()
        XCTAssertTrue(element("screen.family").waitForExistence(timeout: 10), "the Family screen")
        XCTAssertTrue(element("family.child.c2").waitForExistence(timeout: 10), "the child")
        XCTAssertTrue(element("family.child.c2").label.contains("Sara") && element("family.child.c2").label.contains("13+"),
                      element("family.child.c2").label)
        XCTAssertTrue(element("family.request.r1").waitForExistence(timeout: 10), "the request")
        XCTAssertTrue(app.staticTexts["Sara asks for The long night"].exists, "who asks for what")
        shot("parental-family")

        element("family.request.approve.r1").tap()
        XCTAssertTrue(element("family.request.approve.today").waitForExistence(timeout: 5), "Today, This week, Always")
        element("family.request.approve.today").tap()
        waitUntil("the approval sent") { self.count("POST", "/api/parental/requests/r1/approve") == 1 }
        XCTAssertNotNil(body("POST", "/api/parental/requests/r1/approve")?["expires_at"] as? String, "Today ends at 04:00")
        waitUntil("the request leaves the list") { !self.element("family.request.r1").exists }

        element("family.child.c2").tap()
        XCTAssertTrue(element("child.devices").waitForExistence(timeout: 10), "the child's page")
        XCTAssertTrue(element("child.limit").exists && element("child.activity").exists)
        XCTAssertFalse(element("child.lift").exists, "Lift restriction is an admin's")
        element("child.devices").tap()
        let approve = element("devices.approve.f1aa22bb-0000-0000-0000-000000000001")
        XCTAssertTrue(approve.waitForExistence(timeout: 10), "the device's waiting looser limit")
        shot("parental-devices")
        approve.tap()
        waitUntil("the signal cleared") {
            self.count("POST", "/api/users/c2/parental/signals/f1aa22bb-0000-0000-0000-000000000001/clear") == 1
        }
    }

    /// 57.08 T07: a child's Activity in Arabic: by day, with the filters.
    func testAChildsActivityInArabicFiltersByKind() {
        guardian()
        launchSignedIn("ar", sections: ["profile"], section: "profile")
        XCTAssertTrue(element("profile.family").waitForExistence(timeout: 20), "Family")
        element("profile.family").tap()
        XCTAssertTrue(element("family.child.c2").waitForExistence(timeout: 10), "the child")
        element("family.child.c2").tap()
        XCTAssertTrue(element("child.activity").waitForExistence(timeout: 10), "the child's page")
        element("child.activity").tap()
        XCTAssertTrue(element("screen.activity").waitForExistence(timeout: 10), "Activity")
        XCTAssertTrue(element("activity.event.e2").waitForExistence(timeout: 10), "what was watched")
        XCTAssertTrue(element("activity.event.e2").label.hasPrefix("شاهد"), element("activity.event.e2").label)
        XCTAssertTrue(element("activity.event.e1").label.contains("حاول فتح محتوى محظور"), element("activity.event.e1").label)
        XCTAssertTrue(app.staticTexts["اليوم"].exists, "today's heading")
        shot("parental-activity")
        home.stub("GET", "/api/users/c2/parental/activity",
                  body: #"{"events":[{"id":"e1","kind":"blocked","at":"{{now-7200}}","subject":{"kind":"video","id":"v9","title":"Night","tier":16}}]}"#)
        app.buttons["محظور"].firstMatch.tap()
        waitUntil("only what was blocked") { self.element("activity.event.e1").exists && !self.element("activity.event.e2").exists }
        XCTAssertEqual(home.requests.last { $0.path == "/api/users/c2/parental/activity" }?.value("kind"), "blocked")
    }

    // MARK: 57.15

    private func timeOver(_ next: String = "{{now+3600}}") -> String {
        #"{"type":"https://maktaba.tv/problems/parental-time","title":"forbidden","status":403,"detail":"this account's watching time is over for now","code":"parental_time","reason":"limit_reached","next_allowed_at":"\#(next)"}"#
    }

    /// T02: the home answers `parental-time` to a progress post mid-video: the video stops,
    /// the time's-up screen shows when watching opens again, and no stream is asked for again.
    func testTimeUpMidVideoStopsAndIsNotTriedAgain() {
        home.parental(Self.child, time: #"{"allowed":true,"limited":true,"remaining_seconds":600,"slot_ends_at":null,"next_allowed_at":null,"now":"{{now}}"}"#)
        launchSignedIn(sections: ["home"], open: "watch/v1@30")
        XCTAssertTrue(element("screen.watch").waitForExistence(timeout: 20), "the player")
        waitUntil("the stream opened", timeout: 30) { self.count("POST", "/api/stream/sessions") >= 1 }
        XCTAssertTrue(element("time.left").waitForExistence(timeout: 30), "ten minutes left shows over the video")
        // time runs out: the next heartbeat is refused
        home.stub("GET", "/api/me/parental/time", body: #"{"allowed":false,"limited":true,"reason":"limit_reached","remaining_seconds":0,"slot_ends_at":null,"next_allowed_at":"{{now+3600}}","now":"{{now}}"}"#)
        home.stub("POST", "/api/stream/sessions/ss-1/progress", status: 403, body: timeOver())
        XCTAssertTrue(element("screen.timeUp").waitForExistence(timeout: 40), "the time's-up screen")
        XCTAssertEqual(element("timeUp.title").label, "Time's up for today")
        XCTAssertTrue(element("timeUp.next").label.hasPrefix("You can watch again at "), element("timeUp.next").label)
        XCTAssertTrue(element("timeUp.saved").exists, "where the video stopped")
        XCTAssertEqual(element("timeUp.ask").label, "Ask a parent for more time")
        shot("parental-time-up")
        let opened = count("POST", "/api/stream/sessions")
        RunLoop.current.run(until: Date().addingTimeInterval(12))
        XCTAssertEqual(count("POST", "/api/stream/sessions"), opened, "not tried again")
        XCTAssertTrue(element("screen.timeUp").exists)
        element("timeUp.back").tap()
        XCTAssertTrue(element("screen.home").waitForExistence(timeout: 10), "Back leaves the player")
    }

    /// T02 in Arabic, at the start: the home refuses the stream itself.
    func testTimeUpAtTheStartInArabic() {
        home.parental(Self.child, time: #"{"allowed":false,"limited":true,"reason":"outside_slot","remaining_seconds":0,"slot_ends_at":null,"next_allowed_at":"{{now+3600}}","now":"{{now}}"}"#)
        home.stub("POST", "/api/stream/sessions", status: 403,
                  body: #"{"type":"https://maktaba.tv/problems/parental-time","status":403,"code":"parental_time","reason":"outside_slot","next_allowed_at":"{{now+3600}}"}"#)
        launchSignedIn("ar", sections: ["home"], open: "watch/v1")
        XCTAssertTrue(element("screen.timeUp").waitForExistence(timeout: 30), "the time's-up screen")
        XCTAssertEqual(element("timeUp.title").label, "لم يحن وقت المشاهدة بعد")
        XCTAssertTrue(element("timeUp.next").label.hasPrefix("يمكنك المشاهدة مجددًا عند"), element("timeUp.next").label)
        XCTAssertEqual(element("timeUp.ask").label, "اطلب من أحد والديك وقتًا إضافيًا")
        XCTAssertEqual(count("POST", "/api/stream/sessions"), 1, "asked once")
        shot("parental-time-up")
    }

    /// The time left today on Home in its last 15 minutes.
    func testHomeShowsTheTimeLeft() {
        home.parental(Self.child, time: #"{"allowed":true,"limited":true,"remaining_seconds":720,"slot_ends_at":null,"next_allowed_at":null,"now":"{{now}}"}"#)
        launchSignedIn(sections: ["home"])
        XCTAssertTrue(element("time.left").waitForExistence(timeout: 30), "the time left")
        XCTAssertTrue(element("time.left").label.hasSuffix("left today"), element("time.left").label)
        shot("parental-time-left")
    }

    // MARK: 57.10

    /// T07: a channel whose programme now is above the limit shows the slate and does not play.
    func testAChannelAboveTheLimitShowsTheSlate() {
        home.parental(Self.child)
        home.stub("GET", "/api/channels/ch-1/access", body: #"{"channel_id":"ch-1","restricted":true,"allowed":false,"until":null}"#)
        launchSignedIn(sections: ["home", "live"], open: "channel/ch-1")
        XCTAssertTrue(element("live.slate").waitForExistence(timeout: 30), "the slate")
        XCTAssertEqual(element("live.slate.text").label,
                       "Not available right now. This channel comes back when the next programme starts.")
        XCTAssertFalse(home.seen.contains("POST /api/tv/remote/tune"), "nothing was tuned")
        shot("parental-live-slate")
    }
}
