import XCTest

/// The Remote's product screenshots (story 60.09; the store gallery's, story 60.14), in English
/// and in Arabic, dark (the simulator's appearance, set by CI), on iPhone and iPad, against the
/// demo library's home (`MockHome(demo:)`: its name, its account `haddad`, the running jobs on
/// the demo's films):
/// - the Queue (`queue`; also kept under its first name, `shot-<device>-remote-<language>`,
///   which maktaba.tv's home page reads);
/// - Dashboards (`dashboards`), Needs attention (`attention`), Approve a TV or device with a
///   code typed (`approve-code`) and Updates (`updates`).
///
/// What the demo has none of (the charts, the failed jobs, the TV asking, the updates) is this
/// file's own made-up answers, built from the demo's titles: no person's name, no address.
/// Attachments named `shot-<iphone|ipad>-remote-<screen>-<language>`, kept always: CI exports
/// them from the result bundle (`_apple-apps.yml`, Remote UI tests) as
/// `product-shots-<iphone|ipad>-remote`.
@MainActor
final class RemoteDemoShotsUITests: XCTestCase {
    var app: XCUIApplication!
    var home: MockHome?
    private var language = "en"

    private static let languages = [("en", "en_US"), ("ar", "ar_JO")]

    override func setUp() async throws {
        continueAfterFailure = false
        app = XCUIApplication()
    }

    override func tearDown() async throws {
        app?.terminate()
        home?.stop()
    }

    func testQueue() throws {
        for (language, locale) in Self.languages {
            let home = try start(language)
            signIn(language, locale: locale, home: home)

            openTab(language == "ar" ? "الطابور" : "Queue")
            let title = home.demoVideo(home.demoFirstVideoID ?? "")?["title"] as? String ?? ""
            XCTAssertFalse(title.isEmpty, "the demo's first video has a title")
            XCTAssertTrue(app.staticTexts[title].firstMatch.waitForExistence(timeout: 15),
                          "a running job on \(title); the home saw \(home.seen)")
            // its rows' counts in
            XCTAssertTrue(wait { home.seen.contains("GET /api/queues") && home.seen.contains("GET /api/queues/running") })
            let shot = XCUIScreen.main.screenshot()
            attach(shot, "remote")
            attach(shot, "remote-queue")
        }
    }

    /// The Overview's charts: channels on air, the API's requests, memory and disk free.
    func testDashboards() throws {
        for (language, locale) in Self.languages {
            let home = try start(language)
            stubDashboards(home)
            signIn(language, locale: locale, home: home)
            open("home.dashboards", homeTab: homeTab)
            let chart = element("dash.panel.maktaba_tv_channels_on_air")
            XCTAssertTrue(chart.waitForExistence(timeout: 15), "the Overview's first chart; the home saw \(home.seen)")
            XCTAssertTrue(element("dash.panel.http_request_duration_seconds_count").waitForExistence(timeout: 5), "the API's requests")
            waitForQuiet(home)
            attach(XCUIScreen.main.screenshot(), "remote-dashboards")
        }
    }

    /// Needs attention: the failed jobs grouped by cause, on the demo's titles, each saying
    /// what fixes it.
    func testAttention() throws {
        for (language, locale) in Self.languages {
            let home = try start(language)
            stubAttention(home)
            signIn(language, locale: locale, home: home)
            openTab(language == "ar" ? "ما يحتاج إلى انتباه" : "Needs attention")
            let group = app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", title(home, "v-apollo-11-launch"))).firstMatch
            XCTAssertTrue(group.waitForExistence(timeout: 15), "a group's sample, on a demo title; the home saw \(home.seen)")
            waitForQuiet(home)
            attach(XCUIScreen.main.screenshot(), "remote-attention")
        }
    }

    /// Approve a TV or device, a TV's code typed and looked up: what is asking, and Approve.
    func testApproveCode() throws {
        for (language, locale) in Self.languages {
            let home = try start(language)
            let label = language == "ar" ? "تلفاز غرفة الجلوس" : "Living room TV"
            home.stub("GET", "/api/pairing/device/K7M4P2XR", body: """
            {"code":"K7M4-P2XR","client_app":"tv","platform":"tvOS","device":"Apple TV 4K","device_label":"\(label)","expires_at":"{{now+540}}"}
            """)
            signIn(language, locale: locale, home: home)
            let approve = element("approve.tv")
            XCTAssertTrue(approve.waitForExistence(timeout: 15), "Approve a TV or device in the toolbar")
            approve.tap()
            let field = app.textFields["approve.code"]
            XCTAssertTrue(field.waitForExistence(timeout: 10), "the code field")
            field.typeExactly("K7M4P2XR", in: app)
            field.typeText("\n")
            XCTAssertTrue(element("approve.approve").waitForExistence(timeout: 15), "what is asking, and Approve; the home saw \(home.seen)")
            XCTAssertTrue(wait(timeout: 10) { !self.app.keyboards.firstMatch.exists }, "the keyboard gone")
            waitForQuiet(home)
            attach(XCUIScreen.main.screenshot(), "remote-approve-code")
        }
    }

    /// Updates: up to date, automatic in the quiet hours, the next release's night, and last
    /// night's update done on each machine.
    func testUpdates() throws {
        for (language, locale) in Self.languages {
            let home = try start(language)
            stubUpdates(home, language)
            signIn(language, locale: locale, home: home)
            open("home.updates", homeTab: homeTab)
            XCTAssertTrue(element("updates.next").waitForExistence(timeout: 15), "the Updates page; the home saw \(home.seen)")
            XCTAssertTrue(wait(timeout: 15) { home.seen.contains("GET /api/cluster/updates/runs/r12") }, "last night's run")
            waitForQuiet(home)
            attach(XCUIScreen.main.screenshot(), "remote-updates")
        }
    }

    // MARK: made-up answers, on the demo's titles

    private func title(_ home: MockHome, _ id: String) -> String {
        home.demoVideo(id)?["title"] as? String ?? id
    }

    private func stubDashboards(_ home: MockHome) {
        let now = Int(Date().timeIntervalSince1970)
        func series(_ name: String, _ labels: String, _ value: (Int) -> Double) -> String {
            let pts = (0 ..< 24).map { i in "[\(now - (23 - i) * 3600),\(value(i))]" }.joined(separator: ",")
            return #"{"name":"\#(name)","labels":"\#(labels)","kind":"gauge","points":[\#(pts)]}"#
        }
        // an evening's viewing: more requests from late afternoon, the disk filling slowly
        let evening = { (i: Int) -> Double in
            let h = Double((i + 13) % 24)
            return (40 + 160 * max(0, sin((h - 14) / 10 * .pi))).rounded()
        }
        home.stub("GET", "/api/admin/dashboards/overview", body: """
        {"board":"overview","range":"24h","step":3600,"series":[
          \(series("maktaba_tv_channels_on_air", "", { _ in 3 })),
          \(series("http_request_duration_seconds_count", "", evening)),
          \(series("maktaba_component_up", "id=core.api", { _ in 1 })),
          \(series("maktaba_component_up", "id=streaming", { _ in 1 })),
          \(series("maktaba_host_memory_free_bytes", "machine=home", { 6_000_000_000 + 400_000_000 * sin(Double($0) / 3) })),
          \(series("maktaba_host_disk_free_bytes", "machine=home", { 812_000_000_000 - 1_500_000_000 * Double($0) }))]}
        """)
    }

    private func stubAttention(_ home: MockHome) {
        func group(_ stage: String, _ cause: String, _ count: Int, _ action: String, _ video: String?) -> String {
            let sample = video.map { #","sample_message":\#(Self.quoted(title(home, $0)))"# } ?? ""
            return #"{"stage":"\#(stage)","cause":"\#(cause)","label":"","count":\#(count),"action":"\#(action)"\#(sample)}"#
        }
        home.stub("GET", "/api/issues", body: """
        {"groups":[
          \(group("probe", "nas_timeout", 2, "retry", "v-apollo-11-launch")),
          \(group("transcode", "ffmpeg_decode", 1, "retry", "v-ghost-octopus")),
          \(group("transcribe", "disk_full", 3, "open_settings", nil))]}
        """)
    }

    private func stubUpdates(_ home: MockHome, _ language: String) {
        let iso = ISO8601DateFormatter()
        let calendar = Calendar.current
        let now = Date()
        // tonight's quiet window at 03:00, and last night's
        let next = calendar.nextDate(after: now, matching: DateComponents(hour: 3, minute: 0), matchingPolicy: .nextTime)
            ?? now.addingTimeInterval(12 * 3600)
        let last = next.addingTimeInterval(-86400)
        func t(_ d: Date, _ s: TimeInterval = 0) -> String { iso.string(from: d.addingTimeInterval(s)) }
        let (mac, pc) = language == "ar" ? ("ماك ميني", "حاسوب المكتب") : ("Mac mini", "Study PC")
        home.stub("GET", "/api/cluster/updates", body: """
        {"current_version":"0.3.44","watermark":"0.3.44",
         "policy":{"auto_install":true,"channel":"stable","window":"03:00-05:00"},
         "plan":{"target":{"version":"0.3.45","verify_state":"verified"},"newest":{"version":"0.3.45","verify_state":"verified"},
                 "held":false,"stepping_stone":false,"auto_install":true,"due":false,"next_run_at":"\(t(next))"},
         "last_run":{"id":"r12","version":"0.3.44","from_version":"0.3.43","trigger":"auto","state":"done","step":"watch",
                     "started_at":"\(t(last, 300))","finished_at":"\(t(last, 1500))"},
         "catalog":[{"version":"0.3.44","verify_state":"verified"},{"version":"0.3.45","verify_state":"verified"}],
         "last_check_ok":"\(t(now, -1800))","check_stale":false,
         "machines":[
           {"id":"home","name":"\(mac)","role":"home","version":"0.3.44","state":"up_to_date","platform":"darwin/arm64",
            "last_update":{"at":"\(t(last, 300))","ended_at":"\(t(last, 1200))","source":"run","to":"0.3.44","outcome":"installed"}},
           {"id":"n1","name":"\(pc)","role":"node","version":"0.3.44","state":"up_to_date","platform":"linux/amd64",
            "last_update":{"at":"\(t(last, 1200))","ended_at":"\(t(last, 1500))","source":"run","to":"0.3.44","outcome":"installed"}}]}
        """)
        home.stub("GET", "/api/cluster/updates/runs/r12", body: """
        {"id":"r12","version":"0.3.44","from_version":"0.3.43","trigger":"auto","state":"done","step":"watch",
         "started_at":"\(t(last, 300))","finished_at":"\(t(last, 1500))","install_kind":"appbundle","detail":{},
         "steps":[
           {"seq":1,"host":"home","step":"stage","state":"done","started_at":"\(t(last, 300))"},
           {"seq":2,"host":"home","step":"backup","state":"done","started_at":"\(t(last, 500))"},
           {"seq":3,"host":"home","step":"switch","state":"done","started_at":"\(t(last, 700))"},
           {"seq":4,"host":"home","step":"watch","state":"done","started_at":"\(t(last, 900))"}],
         "targets":[
           {"node_id":"home","name":"\(mac)","ord":0,"platform":"darwin/arm64","state":"healthy","from_version":"0.3.43","to_version":"0.3.44","attempts":1},
           {"node_id":"n1","name":"\(pc)","ord":1,"platform":"linux/amd64","state":"healthy","from_version":"0.3.43","to_version":"0.3.44","attempts":1}]}
        """)
    }

    /// `s` as a JSON string, quotes included.
    private static func quoted(_ s: String) -> String {
        String(decoding: (try? JSONSerialization.data(withJSONObject: [s])) ?? Data("[\"\"]".utf8), as: UTF8.self)
            .dropFirst().dropLast().description
    }

    // MARK: helpers

    /// The Home tab's name ("Home", in Arabic the home itself: «الجهاز الرئيسي»).
    private var homeTab: String { language == "ar" ? "الجهاز الرئيسي" : "Home" }

    /// A fresh demo home in `language`, its clock pinned, the app closed.
    private func start(_ language: String) throws -> MockHome {
        app.terminate()
        home?.stop()
        let home = try MockHome(demo: language)
        home.clock = MockHome.demoClock
        try home.start()
        self.home = home
        self.language = language
        return home
    }

    private func element(_ id: String) -> XCUIElement { app.descendants(matching: .any)[id].firstMatch }

    /// A tab: its button in the tab bar (iPhone) or the top tabs (iPad).
    private func openTab(_ title: String) {
        let tab = app.buttons[title].firstMatch
        XCTAssertTrue(tab.waitForExistence(timeout: 15), "the \(title) tab")
        tab.tap()
    }

    /// The Home tab's row `id`, scrolled to and opened (as RemoteDiagnosticsUITests does).
    private func open(_ id: String, homeTab: String) {
        openTab(homeTab)
        let row = element(id)
        for _ in 0 ..< 6 where !(row.exists && row.isHittable) { app.swipeUp(velocity: .slow) }
        XCTAssertTrue(row.waitForExistence(timeout: 10), id)
        // a swipe leaves the list coasting, and a tap on a moving list only stops it
        for attempt in 1 ... 2 {
            waitStill(row)
            row.tap()
            if row.waitForNonExistence(timeout: 5) || !row.isHittable { return }
            XCTAssertLessThan(attempt, 2, "\(id) opened nothing after two taps on a still row")
        }
    }

    /// Waits (at most 5 s) until the element's frame reads the same twice 0.3 s apart.
    private func waitStill(_ e: XCUIElement) {
        var last = e.frame
        let end = Date().addingTimeInterval(5)
        while Date() < end {
            RunLoop.current.run(until: Date().addingTimeInterval(0.3))
            let now = e.frame
            if now == last { return }
            last = now
        }
    }

    /// Until the app has asked the home nothing for 2 s (at most 20 s): every answer drawn.
    private func waitForQuiet(_ home: MockHome) {
        var count = home.requests.count, since = Date()
        let end = Date().addingTimeInterval(20)
        while Date() < end {
            RunLoop.current.run(until: Date().addingTimeInterval(0.2))
            let now = home.requests.count
            if now != count { count = now; since = Date() } else if Date().timeIntervalSince(since) >= 2 { return }
        }
    }

    /// The screen as `shot-<device>-<name>-<language>`, kept always.
    private func attach(_ shot: XCUIScreenshot, _ name: String) {
        let a = XCTAttachment(screenshot: shot)
        a.name = "shot-\(UIDevice.current.userInterfaceIdiom == .pad ? "ipad" : "iphone")-\(name)-\(language)"
        a.lifetime = .keepAlways
        add(a)
    }

    /// A first run: past the permissions walkthrough, the mock home's address, a code approved.
    private func signIn(_ language: String, locale: String, home: MockHome) {
        app.launchArguments = ["-maktaba-permissions-reset", "-maktaba-permissions-fake", "granted", "-maktaba-session-reset",
                               "-AppleLanguages", "(\(language))", "-AppleLocale", locale]
        app.launch()
        let walkthrough = element("permissions.walkthrough")
        if walkthrough.waitForExistence(timeout: 10) {
            app.buttons["permissions.continue"].tap()
            XCTAssertTrue(walkthrough.waitForNonExistence(timeout: 5))
        }
        let field = app.textFields["picker.address"]
        XCTAssertTrue(field.waitForExistence(timeout: 10), "the home picker")
        field.typeExactly(home.url, in: app)
        // submitted from the field's own Return (`onSubmit`, the same `add` as Connect), as
        // PlayerUITestCase.signIn does: a Connect tap as the keyboard still settled after
        // typing was lost (main run 43556127370: no connect began, the picker stayed with the
        // address typed). Connect's own tap is ConnectUITests'.
        field.typeText("\n")
        let byCode = app.buttons[language == "ar" ? "برمز" : "With a code"]
        XCTAssertTrue(byCode.waitForExistence(timeout: 10), "the sign-in screen offers a code")
        byCode.tap()
        home.approve()
        XCTAssertTrue(byCode.waitForNonExistence(timeout: 20), "approved: signed in")
    }

    private func wait(timeout: TimeInterval = 10, _ condition: () -> Bool) -> Bool {
        let end = Date().addingTimeInterval(timeout)
        while Date() < end {
            if condition() { return true }
            RunLoop.current.run(until: Date().addingTimeInterval(0.25))
        }
        return condition()
    }
}
