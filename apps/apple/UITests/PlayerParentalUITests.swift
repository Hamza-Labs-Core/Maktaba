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
        XCTAssertTrue(element("blocked.title").waitForExistence(timeout: 20), "the blocked page")
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
        XCTAssertTrue(element("blocked.title").waitForExistence(timeout: 20), "the blocked page")
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
        XCTAssertTrue(element("blocked.title").waitForExistence(timeout: 20), "the blocked page")
        // the parent allows it on their own phone: the home now gives the video, and the limit moved
        let video = try XCTUnwrap(home.fixture(named: "video-v2"))
        let mine = try XCTUnwrap(home.fixture(named: "me-video-v2"))
        home.stub("GET", "/api/videos/v2", body: video)
        home.stub("GET", "/api/me/videos/v2", body: mine)
        home.stub("GET", "/api/me/parental", body: #"{"restricted":true,"max_tier":13,"unrated":"hide","version":5}"#)
        XCUIDevice.shared.press(.home)
        app.activate()
        XCTAssertTrue(app.buttons["video.favorite"].waitForExistence(timeout: 20), "the video's page, without a restart")
        XCTAssertFalse(element("blocked.title").exists)
    }

    /// The child's own requests (`GET /api/me/parental/requests`): while a request waits the
    /// page offers Check again; a parent's no is said with Ask again; a parent's yes, seen
    /// while the page watched, opens the video without a restart.
    func testAParentsNoIsSaidWithAskAgainAndAParentsYesOpensTheVideo() throws {
        childWithHiddenVideo()
        let request = { (id: String, status: String) in
            #"{"id":"\#(id)","subject_kind":"video","subject_id":"v2","status":"\#(status)","created_at":"{{now-60}}","expires_at":"{{now+604800}}"}"#
        }
        let made = { (id: String) in
            #"{"request":{"id":"\#(id)","child_id":"u1","subject_kind":"video","subject_id":"v2","status":"pending","created_at":"{{now}}","expires_at":"{{now+604800}}"}}"#
        }
        home.stub("GET", "/api/me/parental/requests", body: #"{"requests":[]}"#)
        home.stub("POST", "/api/me/parental/requests", status: 201, body: made("r1"))
        launchSignedIn(sections: ["home"], open: "video/v2")
        XCTAssertTrue(element("blocked.title").waitForExistence(timeout: 20), "the blocked page")
        waitUntil("the child's own requests were read") { self.count("GET", "/api/me/parental/requests") >= 1 }
        XCTAssertEqual(element("blocked.ask").label, "Ask a parent")
        XCTAssertFalse(element("blocked.check").exists, "nothing waits: nothing to check")

        element("blocked.ask").tap()
        XCTAssertTrue(element("blocked.check").waitForExistence(timeout: 10), "a request waits: Check again")
        XCTAssertEqual(element("blocked.check").label, "Check again")
        XCTAssertGreaterThanOrEqual(element("blocked.check").frame.height, 44, "at least 44 pt tall")
        XCTAssertEqual(element("blocked.ask").label, "Waiting for a parent")
        XCTAssertFalse(element("blocked.ask").isEnabled, "asked once: the button waits")

        // the parent says no on their own phone
        home.stub("GET", "/api/me/parental/requests", body: #"{"requests":[\#(request("r1", "denied"))]}"#)
        element("blocked.check").tap()
        waitUntil("the no is said") { self.element("blocked.askLine").label == "A parent said no." }
        XCTAssertEqual(element("blocked.ask").label, "Ask again")
        XCTAssertTrue(element("blocked.ask").isEnabled)
        XCTAssertFalse(element("blocked.check").exists, "nothing waits any more")
        XCTAssertTrue(element("blocked.title").exists, "still the blocked page")
        shot("parental-blocked-denied")

        // asked again; this time the parent says yes: the home gives the video and the limit moved
        home.stub("POST", "/api/me/parental/requests", status: 201, body: made("r2"))
        element("blocked.ask").tap()
        XCTAssertTrue(element("blocked.check").waitForExistence(timeout: 10), "the second request waits")
        XCTAssertEqual(count("POST", "/api/me/parental/requests"), 2)
        home.stub("GET", "/api/videos/v2", body: try XCTUnwrap(home.fixture(named: "video-v2")))
        home.stub("GET", "/api/me/videos/v2", body: try XCTUnwrap(home.fixture(named: "me-video-v2")))
        home.stub("GET", "/api/me/parental", body: #"{"restricted":true,"max_tier":9,"unrated":"hide","version":5}"#)
        home.stub("GET", "/api/me/parental/requests",
                  body: #"{"requests":[\#(request("r2", "approved")),\#(request("r1", "denied"))]}"#)
        element("blocked.check").tap()
        XCTAssertTrue(app.buttons["video.favorite"].waitForExistence(timeout: 20), "the video's page, without a restart")
        XCTAssertFalse(element("blocked.title").exists)
    }

    /// A child watches a video opened from Libraries; the limit's version moves for something
    /// unrelated (a parent added a rule). The Libraries section is not built again around
    /// the video: it stays open: no stream is closed, the player is not dismissed. Held in the
    /// background meanwhile (57.09), that section's player paused too and did not start
    /// again by itself.
    func testAVideoPlayingFromLibrariesStaysOpenWhenTheLimitChanges() {
        home.parental(Self.child)
        launchSignedIn(sections: ["libraries"], open: "video/v1")
        XCTAssertTrue(element("screen.videoDetail").waitForExistence(timeout: 20), "the video's page in Libraries")
        app.buttons["Play"].firstMatch.tap()
        XCTAssertTrue(element("player.surface").waitForExistence(timeout: 30), "the player")
        waitUntil("the stream opened", timeout: 30) { self.count("POST", "/api/stream/sessions") >= 1 }
        waitUntil("it plays", timeout: 30) { (self.element("player.surface").value as? String ?? "").contains("playing=true") }
        let asked = count("GET", "/api/me/parental")
        home.stub("GET", "/api/me/parental", body: #"{"restricted":true,"max_tier":9,"unrated":"hide","version":5}"#)
        XCUIDevice.shared.press(.home)
        RunLoop.current.run(until: Date().addingTimeInterval(2))
        app.activate()
        waitUntil("the limit was read again", timeout: 20) { self.count("GET", "/api/me/parental") > asked }
        // long enough for the section to be built again, were it still keyed on the limit
        RunLoop.current.run(until: Date().addingTimeInterval(4))
        XCTAssertTrue(element("player.surface").exists, "the video is still open")
        XCTAssertEqual(count("DELETE", "/api/stream/sessions/ss-1"), 0, "its stream was not closed")
        XCTAssertEqual(count("POST", "/api/stream/sessions"), 1, "nor opened again")
        let state = element("player.surface").value as? String ?? ""
        XCTAssertTrue(state.contains("playing=false"), "paused in the background under a limit, and not started again: \(state)")
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
        waitUntil("the video's page after the PIN", timeout: 20) { !self.element("blocked.title").exists && self.app.buttons["video.favorite"].exists }
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

    /// The build has neither the Declared Age Range capability nor Family Controls: reading
    /// Apple's own signals must leave the app running, with no system sheet over it, and
    /// send the home only what could be read. Five taps on About show what that was (the
    /// line of the real-device check).
    func testTheDevicesOwnSignalsAreReadWithoutAnyCapability() {
        home.parental(#"{"restricted":false}"#)
        home.stub("POST", "/api/me/age-signal", body: #"{"tier_limit":null,"pending_looser":false,"refresh":true}"#)
        launchSignedIn(sections: ["profile"], section: "profile", extra: ["-maktaba-age-signal", "device"])
        XCTAssertTrue(element("screen.profile").waitForExistence(timeout: 20), "Profile")
        RunLoop.current.run(until: Date().addingTimeInterval(3))
        XCTAssertEqual(app.state, .runningForeground, "the app runs on after reading the device")
        let about = element("profile.about")
        scrollUntil(about)
        XCTAssertTrue(about.isHittable, "nothing of the system's is over the app")
        about.tap(withNumberOfTaps: 5, numberOfTouches: 1)
        let line = element("profile.about.ageSignal")
        XCTAssertTrue(line.waitForExistence(timeout: 5) || about.label.contains("movie"), "the diagnostics line")
        let text = line.exists ? line.label : about.label
        XCTAssertTrue(text.contains("movie") && text.contains("tv") && text.contains("region"), text)
        // no age range without the capability: nothing about an age is sent
        if let sent = body("POST", "/api/me/age-signal") {
            XCTAssertNil(sent["age_range"], "no capability, no age range: \(sent)")
        }
        XCTAssertFalse(element("profile.parental.ageRange").exists, "no Age range row where Apple offers none")
        let a = XCTAttachment(string: text)
        a.name = "age-signal-diagnostics"
        a.lifetime = .keepAlways
        add(a)
    }

    // MARK: 57.07, 57.08

    private static let children = #"[{"id":"c2","username":"Sara","user_id":"c2","max_tier":13,"unrated":"hide","allow_only":false,"version":3,"updated_at":"2026-11-10T10:00:00Z"}]"#
    private static let account = #"{"policy":{"user_id":"c2","max_tier":13,"unrated":"hide","allow_only":false,"version":3,"updated_at":"2026-11-10T10:00:00Z"},"access":"guardian","guardians":[{"id":"u1","username":"tester","created_at":"2026-11-01T10:00:00Z"}],"signals":[{"refresh_family_id":"f1aa22bb-0000-0000-0000-000000000001","lower":9,"upper":12,"declaration":"guardian_declared","region":"US","tier_limit":9,"pending_looser":{"lower":18,"tier_limit":null,"observed_at":"2026-11-11T09:00:00Z"},"observed_at":"2026-11-10T09:00:00Z"}]}"#

    private func guardian() {
        home.parental(#"{"restricted":false}"#, children: Self.children)
        home.stub("GET", "/api/parental/requests",
                  body: #"{"requests":[{"id":"r1","child_id":"c2","subject_kind":"video","subject_id":"v2","status":"pending","created_at":"{{now-600}}","expires_at":"{{now+86400}}","child":{"id":"c2","username":"Sara"},"subject":{"kind":"video","id":"v2","title":"The long night","poster_url":"/api/videos/v2/poster","tier":16}}]}"#)
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
        let approved = body("POST", "/api/parental/requests/r1/approve")
        XCTAssertEqual(approved?["until"] as? String, "day_end", "Today ends when the home's day does: the home works it out")
        XCTAssertNil(approved?["expires_at"], "no time from this device's clock")
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

    /// 57.07: Allowed > Add offers every kind a rule may name; a collection is picked from the
    /// guardian's own libraries and sent as the rule's subject, and a YouTube source too. The
    /// plugins are an admin's to list: the picker says what the home answered.
    func testAGuardianAddsRulesForACollectionAndAYouTubeSource() {
        guardian()
        home.stub("GET", "/api/libraries/lib-docs/youtube/sources",
                  body: #"{"items":[{"id":"yt-1","library_id":"lib-docs","kind":"channel","url":"https://youtube.example/@docs","folder":"Docs channel","mode":"on"}]}"#)
        home.stub("GET", "/api/libraries/lib-lectures/youtube/sources", body: #"{"items":[]}"#)
        home.stub("GET", "/api/plugins", status: 403,
                  body: #"{"type":"https://maktaba.tv/problems/forbidden","title":"forbidden","status":403,"code":"admin_only"}"#)
        home.stub("POST", "/api/users/c2/parental/rules", status: 201,
                  body: #"{"id":"ru7","user_id":"c2","effect":"allow","subject_kind":"collection","subject_id":"col-mine","created_at":"{{now}}"}"#)
        launchSignedIn(sections: ["profile"], section: "profile")
        XCTAssertTrue(element("profile.family").waitForExistence(timeout: 20), "Family")
        element("profile.family").tap()
        XCTAssertTrue(element("family.child.c2").waitForExistence(timeout: 10), "the child")
        element("family.child.c2").tap()
        XCTAssertTrue(element("child.allowed").waitForExistence(timeout: 10), "the child's page")
        element("child.allowed").tap()
        XCTAssertTrue(element("rules.none").waitForExistence(timeout: 10), "no rule yet")

        element("rules.add").tap()
        for kind in ["library", "collection", "series", "channel", "youtube_source", "plugin", "video"] {
            XCTAssertTrue(element("rules.add.\(kind)").waitForExistence(timeout: 5), "Add offers a \(kind)")
        }
        shot("parental-rules-add")
        element("rules.add.collection").tap()
        let picked = element("picker.row.col-mine")
        XCTAssertTrue(picked.waitForExistence(timeout: 15), "the home's collections; the home saw \(home.seen.suffix(6))")
        XCTAssertTrue(picked.label.contains("Picked"), picked.label)
        picked.tap()
        waitUntil("the rule sent") { self.count("POST", "/api/users/c2/parental/rules") == 1 }
        var sent = body("POST", "/api/users/c2/parental/rules")
        XCTAssertEqual(sent?["effect"] as? String, "allow")
        XCTAssertEqual(sent?["subject_kind"] as? String, "collection")
        XCTAssertEqual(sent?["subject_id"] as? String, "col-mine")
        XCTAssertTrue(element("rules.rule.ru7").waitForExistence(timeout: 10), "the rule in the list")

        home.stub("POST", "/api/users/c2/parental/rules", status: 201,
                  body: #"{"id":"ru8","user_id":"c2","effect":"allow","subject_kind":"youtube_source","subject_id":"yt-1","created_at":"{{now}}"}"#)
        element("rules.add").tap()
        element("rules.add.youtube_source").tap()
        let source = element("picker.row.yt-1")
        XCTAssertTrue(source.waitForExistence(timeout: 15), "the libraries' YouTube sources")
        XCTAssertTrue(source.label.contains("Docs channel"), source.label)
        source.tap()
        waitUntil("the second rule sent") { self.count("POST", "/api/users/c2/parental/rules") == 2 }
        sent = body("POST", "/api/users/c2/parental/rules")
        XCTAssertEqual(sent?["subject_kind"] as? String, "youtube_source")
        XCTAssertEqual(sent?["subject_id"] as? String, "yt-1")
        XCTAssertTrue(element("rules.rule.ru8").waitForExistence(timeout: 10), "the rule in the list")

        // a guardian who is not an admin cannot list the plugins: the picker says so
        element("rules.add").tap()
        element("rules.add.plugin").tap()
        XCTAssertTrue(element("picker.failed").waitForExistence(timeout: 15), "what the home answered")
        XCTAssertEqual(count("POST", "/api/users/c2/parental/rules"), 2, "nothing more was sent")
    }

    /// 57.07: an admin makes one of the home's accounts restricted (the accounts the home
    /// lists to admins, `GET /api/admin/library-acl`) with a limit, and names a guardian on a
    /// child's page.
    func testAnAdminMakesAnAccountRestrictedAndAddsAGuardian() {
        guardian()
        home.stub("GET", "/api/admin/library-acl",
                  body: #"{"users":[{"id":"u1","username":"tester"},{"id":"c2","username":"Sara"},{"id":"u3","username":"Omar"},{"id":"u4","username":"Huda"}],"libraries":[],"grants":[]}"#)
        home.stub("GET", "/api/users/c2/parental", body: Self.account.replacingOccurrences(of: #""access":"guardian""#, with: #""access":"admin""#))
        home.stub("PUT", "/api/users/u3/parental",
                  body: #"{"policy":{"user_id":"u3","max_tier":13,"unrated":"hide","allow_only":false,"version":1},"access":"admin","guardians":[],"signals":[]}"#)
        home.stub("POST", "/api/users/c2/parental/guardians", status: 201, body: #"{"guardian_id":"u4"}"#)
        launchSignedIn(sections: ["profile"], section: "profile")
        XCTAssertTrue(element("profile.family").waitForExistence(timeout: 20), "Family")
        element("profile.family").tap()
        let make = element("family.makeRestricted")
        XCTAssertTrue(make.waitForExistence(timeout: 15), "Make an account restricted, for an admin; the home saw \(home.seen.suffix(6))")
        make.tap()
        XCTAssertTrue(element("restrict.account.u3").waitForExistence(timeout: 10), "the home's other accounts")
        XCTAssertTrue(element("restrict.account.u4").exists)
        XCTAssertFalse(element("restrict.account.u1").exists, "not the admin themself")
        XCTAssertFalse(element("restrict.account.c2").exists, "not one restricted already")
        shot("parental-make-restricted")
        element("restrict.account.u3").tap()
        XCTAssertTrue(element("restrict.tier.13").waitForExistence(timeout: 5), "the limits")
        element("restrict.tier.13").tap()
        waitUntil("the limit sent") { self.count("PUT", "/api/users/u3/parental") == 1 }
        let put = body("PUT", "/api/users/u3/parental")
        XCTAssertEqual(put?["max_tier"] as? Int, 13)
        XCTAssertEqual(put?["version"] as? Int, 0, "a new limit")
        waitUntil("the sheet closes") { !self.element("screen.restrict").exists }

        element("family.child.c2").tap()
        let add = element("child.guardian.add")
        scrollUntil(add)
        XCTAssertTrue(add.waitForExistence(timeout: 10), "Add a guardian, for an admin")
        add.tap()
        XCTAssertTrue(element("child.guardian.add.u4").waitForExistence(timeout: 5), "the home's other accounts")
        XCTAssertFalse(element("child.guardian.add.u1").exists, "not who looks after the child already")
        XCTAssertFalse(element("child.guardian.add.c2").exists, "not the child")
        element("child.guardian.add.u4").tap()
        waitUntil("the guardian sent") { self.count("POST", "/api/users/c2/parental/guardians") == 1 }
        XCTAssertEqual(body("POST", "/api/users/c2/parental/guardians")?["guardian_id"] as? String, "u4")
    }

    // MARK: 57.12

    private static func settings(enabled: Bool, restricted: Int = 0, notice: Bool) -> String {
        #"{"enabled":\#(enabled),"restricted_accounts":\#(restricted),"can_disable":\#(restricted == 0),"child_devices":1,"child_device_notice":\#(notice)}"#
    }

    /// T06, in Arabic: parental controls are off and a signed-in device says a child uses it.
    /// An admin sees the notice in Profile and at the top of Family, mirrored; Set up leads to
    /// Rate libraries, where a lectures library is offered All ages and nothing is saved
    /// until the admin saves it; Dismiss tells the home. The switch turns the feature on, and
    /// off is refused in plain words while an account is restricted. Help says where a
    /// guardian with no iPhone goes.
    func testAnAdminSeesTheChildDeviceNoticeAndSetsUpParentalControlsInArabic() {
        home.parental(#"{"restricted":false}"#)
        home.stub("GET", "/api/admin/library-acl", body: #"{"users":[{"id":"u1","username":"tester"},{"id":"u3","username":"Omar"}],"libraries":[],"grants":[]}"#)
        home.stub("GET", "/api/parental/settings", body: Self.settings(enabled: false, notice: true))
        home.stub("GET", "/api/parental/library-suggestions",
                  body: #"{"libraries":[{"library_id":"lib-docs","name":"Documentaries","tier":null,"suggested_tier":null,"classified":20,"lectures":2},{"library_id":"lib-lectures","name":"Lectures","tier":null,"suggested_tier":0,"classified":40,"lectures":39}]}"#)
        home.stub("PUT", "/api/libraries/lib-lectures/age-tier", status: 204)
        home.stub("POST", "/api/parental/settings/notice/dismiss", body: Self.settings(enabled: false, notice: false))
        launchSignedIn("ar", sections: ["profile"], section: "profile")
        let family = element("profile.family")
        scrollUntil(family)
        XCTAssertTrue(family.waitForExistence(timeout: 20), "Family, for an admin")
        let title = element("family.notice.title")
        XCTAssertTrue(title.waitForExistence(timeout: 15), "the notice, above Family; the home saw \(home.seen.suffix(6))")
        XCTAssertEqual(title.label, "ربما يستخدم طفلٌ Maktaba")
        XCTAssertEqual(element("family.notice.body").label, "يشير جهاز مسجَّل الدخول إلى Maktaba إلى أن طفلًا يستخدمه. اضبط الرقابة الأبوية.")
        // the notice came in above Family and moved it down: brought back into reach
        scrollUntil(family)
        shot("parental-notice-profile")
        family.tap()

        XCTAssertTrue(element("screen.family").waitForExistence(timeout: 10), "the Family screen")
        let setUp = element("family.notice.setUp")
        XCTAssertTrue(setUp.waitForExistence(timeout: 15), "the notice's Set up")
        XCTAssertEqual(element("family.notice.title").label, "ربما يستخدم طفلٌ Maktaba")
        XCTAssertTrue(setUp.label.contains("إعداد"), setUp.label)
        XCTAssertEqual(element("family.notice.dismiss").label, "إغلاق")
        // mirrored: the title sits in the right half of the window, its words end at the right
        let window = app.windows.firstMatch.frame
        XCTAssertGreaterThan(element("family.notice.title").frame.midX, window.midX, "right to left: the notice hugs the right edge")
        XCTAssertGreaterThanOrEqual(setUp.frame.height, 44, "Set up is at least 44 pt tall")
        XCTAssertGreaterThanOrEqual(element("family.notice.dismiss").frame.height, 44, "Dismiss is at least 44 pt tall")
        shot("parental-notice")

        // Set up: Rate libraries, with the lectures library offered All ages and nothing saved yet
        setUp.tap()
        XCTAssertTrue(element("screen.rateLibraries").waitForExistence(timeout: 10), "Rate libraries")
        let lectures = element("family.rate.lib-lectures")
        XCTAssertTrue(lectures.waitForExistence(timeout: 15), "the libraries")
        waitUntil("the suggestion shows") { lectures.label.contains("المقترح") }
        XCTAssertTrue(lectures.label.contains("لكل الأعمار"), "All ages is offered: \(lectures.label)")
        XCTAssertFalse(element("family.rate.lib-docs").label.contains("المقترح"), "nothing is offered for the rest")
        XCTAssertEqual(count("PUT", "/api/libraries/lib-lectures/age-tier"), 0, "nothing is saved until the admin says so")
        shot("parental-rate-suggested")
        let save = element("family.rate.saveSuggested")
        XCTAssertTrue(save.waitForExistence(timeout: 5), "Save suggested ratings")
        save.tap()
        waitUntil("the suggested tier saved") { self.count("PUT", "/api/libraries/lib-lectures/age-tier") == 1 }
        XCTAssertEqual(body("PUT", "/api/libraries/lib-lectures/age-tier")?["tier"] as? Int, 0, "All ages")
        XCTAssertEqual(count("PUT", "/api/libraries/lib-docs/age-tier"), 0, "only what was suggested")
        waitUntil("the row is no longer a suggestion") { !lectures.label.contains("المقترح") }
        app.navigationBars.buttons.firstMatch.tap()

        // Dismiss: the home is told and the notice goes
        let dismiss = element("family.notice.dismiss")
        XCTAssertTrue(dismiss.waitForExistence(timeout: 10), "back in Family")
        dismiss.tap()
        waitUntil("the dismissal sent") { self.count("POST", "/api/parental/settings/notice/dismiss") == 1 }
        // (Profile, behind this sheet, keeps its copy until Family closes: Family's own goes now)
        waitUntil("the notice goes from Family") { !dismiss.exists && !self.element("family.notice.setUp").exists }

        // the switch: on is sent; off is refused while an account is restricted, said plainly
        let toggle = app.switches["family.enabled"].firstMatch
        scrollUntil(toggle)
        XCTAssertTrue(toggle.waitForExistence(timeout: 10), "the switch, for an admin")
        XCTAssertEqual(toggle.value as? String, "0", "off in this home")
        XCTAssertTrue(toggle.label.contains("الرقابة الأبوية"), toggle.label)
        home.stub("PUT", "/api/parental/settings", body: Self.settings(enabled: true, notice: false))
        toggle.switches.firstMatch.tap()
        waitUntil("the switch sent") { self.count("PUT", "/api/parental/settings") == 1 }
        XCTAssertEqual(body("PUT", "/api/parental/settings")?["enabled"] as? Bool, true)
        waitUntil("the switch is on") { toggle.value as? String == "1" }
        home.stub("PUT", "/api/parental/settings", status: 409,
                  body: #"{"type":"https://maktaba.tv/problems/parental-restrictions-exist","title":"conflict","status":409,"code":"restrictions_exist"}"#)
        home.stub("GET", "/api/parental/settings", body: Self.settings(enabled: true, restricted: 1, notice: false))
        toggle.switches.firstMatch.tap()
        let refused = app.alerts.firstMatch
        XCTAssertTrue(refused.waitForExistence(timeout: 10), "the home's refusal is said")
        let plain = "ارفع كل التقييدات قبل إيقاف هذا."
        XCTAssertTrue(refused.label == plain || refused.staticTexts[plain].exists, "in plain words: \(refused.debugDescription)")
        XCTAssertEqual(body("PUT", "/api/parental/settings")?["enabled"] as? Bool, false)
        refused.buttons.firstMatch.tap()
        waitUntil("the switch stays on") { toggle.value as? String == "1" }

        // Help: Screen Time, and Family on the home's web page for a guardian with no iPhone
        let web = element("family.help.web")
        scrollUntil(web)
        XCTAssertTrue(web.waitForExistence(timeout: 5), "Help's line about the web page")
        XCTAssertTrue(web.label.contains("/family"), web.label)
        XCTAssertTrue(element("family.help.screenTime").exists)
        shot("parental-family-switch")

        // Family closes: Profile reads the home again, and its copy of the notice is gone too
        element("family.done").tap()
        XCTAssertTrue(family.waitForExistence(timeout: 10), "back in Profile")
        waitUntil("the notice goes from Profile") { !self.element("family.notice.title").exists }
    }

    /// A play-only sign-in through Maktaba Cloud (`cloud_scope: play` in who-am-I): Family
    /// says up front that nothing can be changed here, and its controls are disabled, before
    /// the home has refused anything.
    func testAPlayOnlyCloudSignInSeesFamilyWithItsControlsDisabled() {
        guardian()
        home.stub("GET", "/api/auth/me",
                  body: #"{"user_id":"u1","is_admin":false,"libraries":[],"id":"u1","username":"tester","user":{"id":"u1","username":"tester","is_admin":false,"must_change_password":false},"home_name":"UI Test Home","cloud_scope":"play"}"#)
        launchSignedIn(sections: ["profile"], section: "profile")
        XCTAssertTrue(element("profile.family").waitForExistence(timeout: 20), "Family, for a guardian")
        element("profile.family").tap()
        XCTAssertTrue(element("family.readOnly").waitForExistence(timeout: 10), "why nothing can be changed")
        XCTAssertEqual(element("family.readOnly").label, "Connect at home, or ask an admin for manage access, to change this.")
        XCTAssertTrue(element("family.request.r1").waitForExistence(timeout: 10), "the request still shows")
        XCTAssertFalse(element("family.request.approve.r1").isEnabled, "Approve is disabled")
        XCTAssertFalse(element("family.request.deny.r1").isEnabled, "Deny is disabled")
        shot("parental-family-play-only")

        element("family.child.c2").tap()
        XCTAssertTrue(element("child.allowed").waitForExistence(timeout: 10), "the child's page still opens")
        XCTAssertTrue(element("family.readOnly").exists, "and says the same")
        XCTAssertFalse(element("child.allowOnly").isEnabled, "the limit is disabled")
        element("child.allowed").tap()
        XCTAssertTrue(element("rules.add").waitForExistence(timeout: 10), "the rules still show")
        XCTAssertFalse(element("rules.add").isEnabled, "Add is disabled")
        XCTAssertEqual(count("POST", "/api/parental/requests/r1/approve"), 0, "nothing was sent")
        XCTAssertEqual(count("PUT", "/api/users/c2/parental"), 0)
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
        XCTAssertTrue(element("timeUp.title").waitForExistence(timeout: 40), "the time's-up screen")
        XCTAssertEqual(element("timeUp.title").label, "Time's up for today")
        XCTAssertTrue(element("timeUp.next").label.hasPrefix("You can watch again at "), element("timeUp.next").label)
        XCTAssertTrue(element("timeUp.saved").exists, "where the video stopped")
        XCTAssertEqual(element("timeUp.ask").label, "Ask a parent for more time")
        shot("parental-time-up")
        let opened = count("POST", "/api/stream/sessions")
        RunLoop.current.run(until: Date().addingTimeInterval(12))
        XCTAssertEqual(count("POST", "/api/stream/sessions"), opened, "not tried again")
        XCTAssertTrue(element("timeUp.title").exists)
        element("timeUp.back").tap()
        XCTAssertTrue(element("screen.home").waitForExistence(timeout: 10), "Back leaves the player")
    }

    /// T02 in Arabic, at the start: the home refuses the stream itself.
    func testTimeUpAtTheStartInArabic() {
        home.parental(Self.child, time: #"{"allowed":false,"limited":true,"reason":"outside_slot","remaining_seconds":0,"slot_ends_at":null,"next_allowed_at":"{{now+3600}}","now":"{{now}}"}"#)
        home.stub("POST", "/api/stream/sessions", status: 403,
                  body: #"{"type":"https://maktaba.tv/problems/parental-time","status":403,"code":"parental_time","reason":"outside_slot","next_allowed_at":"{{now+3600}}"}"#)
        launchSignedIn("ar", sections: ["home"], open: "watch/v1")
        XCTAssertTrue(element("timeUp.title").waitForExistence(timeout: 30), "the time's-up screen")
        XCTAssertEqual(element("timeUp.title").label, "لم يحن وقت المشاهدة بعد")
        XCTAssertTrue(element("timeUp.next").label.hasPrefix("يمكنك المشاهدة مجددًا عند"), element("timeUp.next").label)
        XCTAssertEqual(element("timeUp.ask").label, "اطلب من أحد والديك وقتًا إضافيًا")
        XCTAssertEqual(count("POST", "/api/stream/sessions"), 1, "asked once")
        // Ask for more time (57.06): the minutes asked for go as a `time` request
        home.stub("POST", "/api/me/parental/requests", status: 201,
                  body: #"{"request":{"id":"r9","child_id":"u1","subject_kind":"time","subject_id":"30","minutes":30,"status":"pending","created_at":"{{now}}","expires_at":"{{now+3600}}"}}"#)
        XCTAssertTrue(element("timeUp.askMore").waitForExistence(timeout: 10), "Ask for more time")
        element("timeUp.askMore").tap()
        waitUntil("the time request sent") { self.count("POST", "/api/me/parental/requests") == 1 }
        XCTAssertEqual(body("POST", "/api/me/parental/requests")?["subject_kind"] as? String, "time")
        XCTAssertEqual(body("POST", "/api/me/parental/requests")?["subject_id"] as? String, "30")
        XCTAssertTrue(element("timeUp.askLine").waitForExistence(timeout: 10), "the request's line")
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

    /// The same when the channel opens before the home said the account has a limit (the
    /// first moments after sign-in): nothing starts until the home has said, then the slate.
    /// It used to start as for an account with no limit, and never showed the slate.
    func testAChannelOpenedBeforeTheLimitIsKnownStillShowsTheSlate() {
        home.parental(Self.child)
        home.stub("GET", "/api/channels/ch-1/access", body: #"{"channel_id":"ch-1","restricted":true,"allowed":false,"until":null}"#)
        home.delay("GET", "/api/me/parental", by: 5)
        launchSignedIn(sections: ["home", "live"], open: "channel/ch-1")
        XCTAssertTrue(element("live.slate").waitForExistence(timeout: 40), "the slate, once the home said the limit")
        XCTAssertFalse(home.seen.contains("GET /api/tv/info"), "the channel was not started before the home said: \(home.seen)")
        XCTAssertFalse(home.seen.contains("POST /api/tv/remote/tune"), "nothing was tuned")
    }

    /// The same when the home is slow to say it has channels (a loaded iPad runner): the
    /// shell shows without Live TV after its wait, the channel opens on the first section's
    /// stack, and when Live TV's section comes the channel goes there with it. It used to
    /// stay behind on Home's stack, under Live TV's first screen.
    func testAChannelOpenedBeforeLiveTVShowsGoesWithItAndShowsTheSlate() {
        home.parental(Self.child)
        home.stub("GET", "/api/channels/ch-1/access", body: #"{"channel_id":"ch-1","restricted":true,"allowed":false,"until":null}"#)
        // longer than the shell waits for the answer (`LiveSectionGate.patience`, 3 s)
        home.delay("GET", "/api/channels/now", by: 6)
        launchSignedIn(sections: ["home", "live"], open: "channel/ch-1")
        waitUntil("the home said it has channels", timeout: 40) { self.home.seen.contains("GET /api/channels/now") }
        // past the move to Live TV: the slate is there after it, not only before
        RunLoop.current.run(until: Date().addingTimeInterval(8))
        XCTAssertTrue(element("live.slate").waitForExistence(timeout: 20), "the slate, after Live TV's section showed")
        XCTAssertEqual(element("live.slate.text").label,
                       "Not available right now. This channel comes back when the next programme starts.")
        XCTAssertFalse(home.seen.contains("POST /api/tv/remote/tune"), "nothing was tuned")
    }
}
