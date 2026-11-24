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
        // a failure here is recorded, not an interruption: one inside an async tearDown hangs
        // XCTest (`PlayerUITestCase.tearDown`)
        continueAfterFailure = true
        app?.terminate()
        home?.stop()
        XCUIDevice.shared.orientation = .portrait
    }

    /// The Queue: the jobs running now on the demo's titles, and the stages with their counts.
    /// On the iPad (landscape) Transcribe is chosen, so its page fills the pane beside the list.
    /// The stages, steps and states are the home's codes and English labels, as a home sends
    /// them: the app says them in its own words (`StageWords`), in English and in Arabic.
    func testQueue() throws {
        for (language, locale) in Self.languages {
            let home = try start(language)
            home.stub("GET", "/api/queues", body: """
            {"paused_all":false,"issues_count":0,"stages":[
              {"stage":"probe","label":"Length","lane":"a","lane_order":1,"hidden":false,"enabled":true,"paused":false},
              {"stage":"transcribe","label":"Transcribe","lane":"a","lane_order":2,"hidden":false,"enabled":true,"paused":false},
              {"stage":"index","label":"Search index","lane":"a","lane_order":3,"hidden":false,"enabled":true,"paused":false}],
             "queues":[
              {"stage":"probe","counts":{"pending":3,"ready":1,"running":1,"failed_open":0,"done_24h":12},"rate_per_min":0,"no_worker":false},
              {"stage":"transcribe","counts":{"pending":14,"ready":2,"running":1,"failed_open":0,"done_24h":38},
               "rate_per_min":0.4,"eta_sec":5400,"no_worker":false},
              {"stage":"index","counts":{"pending":5,"ready":0,"running":1,"failed_open":0,"done_24h":41},"rate_per_min":0.6,"no_worker":false}]}
            """)
            signIn(language, locale: locale, home: home)

            openTab(language == "ar" ? "الطابور" : "Queue")
            let title = home.demoVideo(home.demoFirstVideoID ?? "")?["title"] as? String ?? ""
            XCTAssertFalse(title.isEmpty, "the demo's first video has a title")
            XCTAssertTrue(app.staticTexts[title].firstMatch.waitForExistence(timeout: 15),
                          "a running job on \(title); the home saw \(home.seen)")
            // its rows' counts in
            XCTAssertTrue(wait { home.seen.contains("GET /api/queues") && home.seen.contains("GET /api/queues/running") })
            if isPad {
                // Transcribe chosen: its page beside the list, its failures read
                // the stage's row by its identifier (its title is a running job's stage too)
                let stage = element("queue.stage.transcribe")
                XCTAssertTrue(stage.waitForExistence(timeout: 10), "the Transcribe stage in the list")
                let opened = { home.seen.contains("GET /api/queues/transcribe/failures") }
                for attempt in 1 ... 2 where !opened() {
                    waitStill(stage)
                    stage.tap()
                    if wait(timeout: 10, opened) { break }
                    XCTAssertLessThan(attempt, 2, "Transcribe's page; the home saw \(home.seen)")
                }                let none = NSPredicate(format: "label == %@", language == "ar" ? "لا إخفاقات" : "No failures")
                XCTAssertTrue(app.descendants(matching: .any).matching(none).firstMatch.waitForExistence(timeout: 10),
                              "its failures read")
            }
            waitForQuiet(home)
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
            // a title with no ": " in it: a sample's right-to-left parts between ": " are each
            // set apart by isolates (`AttentionWords.isolateRTL`), which a label keeps
            let group = app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", title(home, "v-ghost-octopus"))).firstMatch
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
            XCTAssertTrue(element("approve.found").waitForExistence(timeout: 15), "what is asking; the home saw \(home.seen)")
            XCTAssertTrue(wait(timeout: 10) { !self.app.keyboards.firstMatch.exists }, "the keyboard gone")
            // up to Approve, below the fold on an iPhone (the form makes its rows as they show)
            // the section's identifier is its rows' too: Approve is its first button
            let ok = app.buttons.matching(identifier: "approve.found").firstMatch
            for _ in 0 ..< 4 where !(ok.exists && ok.isHittable) { app.swipeUp(velocity: .slow) }
            XCTAssertTrue(ok.waitForExistence(timeout: 5), "Approve")
            waitStill(ok)
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
        // an evening's viewing: the requests rise through the afternoon to a peak at 18:00 and
        // are quiet overnight (by each point's own hour); the disk fills slowly
        let evening = { (i: Int) -> Double in
            let at = Date(timeIntervalSince1970: TimeInterval(now - (23 - i) * 3600))
            let h = Double(Calendar.current.component(.hour, from: at))
            return (30 + 170 * max(0, sin((h - 12) / 12 * .pi))).rounded()
        }
        home.stub("GET", "/api/admin/dashboards/overview", body: """
        {"board":"overview","range":"24h","step":3600,"series":[
          \(series("http_request_duration_seconds_count", "", evening)),
          \(series("maktaba_tv_channels_on_air", "", { _ in 3 })),
          \(series("maktaba_component_up", "id=core.api", { _ in 1 })),
          \(series("maktaba_component_up", "id=streaming", { _ in 1 })),
          \(series("maktaba_host_memory_free_bytes", "machine=home", { 6_000_000_000 + 400_000_000 * sin(Double($0) / 3) })),
          \(series("maktaba_host_disk_free_bytes", "machine=home", { 812_000_000_000 - 1_500_000_000 * Double($0) }))]}
        """)
    }

    /// The samples are titles with no ": " in them: a sample's right-to-left parts between
    /// ": " are each set apart (`AttentionWords.isolateRTL`), and an Arabic title with one
    /// reads with its parts swapped.
    private func stubAttention(_ home: MockHome) {
        func group(_ stage: String, _ cause: String, _ count: Int, _ action: String, _ video: String?) -> String {
            let sample = video.map { #","sample_message":\#(Self.quoted(title(home, $0)))"# } ?? ""
            return #"{"stage":"\#(stage)","cause":"\#(cause)","label":"","count":\#(count),"action":"\#(action)"\#(sample)}"#
        }
        home.stub("GET", "/api/issues", body: """
        {"groups":[
          \(group("probe", "file_missing", 2, "retry", "v-lake-nasser")),
          \(group("extract", "ffmpeg_decode", 1, "retry", "v-ghost-octopus")),
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
         "policy":{"auto_install":true,"window":"03:00-05:00"},
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

    private var isPad: Bool { UIDevice.current.userInterfaceIdiom == .pad }

    /// A fresh demo home in `language`, its clock pinned, the app closed. The iPad is turned to
    /// landscape: the store's iPad size is landscape, and there the Remote's five tabs and its
    /// toolbar's buttons all show (upright, Home went behind the tab bar's Next Page, and
    /// Approve a TV or device into the toolbar's More).
    private func start(_ language: String) throws -> MockHome {
        app.terminate()
        home?.stop()
        if isPad { XCUIDevice.shared.orientation = .landscapeLeft }
        let home = try MockHome(demo: language)
        home.clock = MockHome.demoClock
        try home.start()
        self.home = home
        self.language = language
        return home
    }

    private func element(_ id: String) -> XCUIElement { app.descendants(matching: .any)[id].firstMatch }

    /// A tab: its button in the tab bar (iPhone), or its row in the sidebar (an iPad in
    /// landscape shows the tabs as a sidebar's cells), by the start of its label: a tab with a
    /// badge (Needs attention's count) was not found by its title alone there.
    private func openTab(_ title: String) {
        let named = NSPredicate(format: "label BEGINSWITH %@", title)
        let button = app.buttons.matching(named).firstMatch, cell = app.cells.matching(named).firstMatch
        XCTAssertTrue(wait(timeout: 15) { button.exists || cell.exists }, "the \(title) tab")
        (cell.exists ? cell : button).tap()
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

    /// The screen as `shot-<device>-<name>-<language>`, kept always. On the iPad (landscape)
    /// the screen's picture can come as the panel's, upright with the content's top at its
    /// right edge: it is turned a quarter to the left, as the screen is seen (as
    /// PlayerDemoShots does for the iPhone's player).
    private func attach(_ shot: XCUIScreenshot, _ name: String) {
        let image = shot.image
        let a: XCTAttachment
        if isPad {
            let format = UIGraphicsImageRendererFormat()
            format.scale = image.scale
            let png: Data
            if image.size.width < image.size.height {
                let size = CGSize(width: image.size.height, height: image.size.width)
                png = UIGraphicsImageRenderer(size: size, format: format).pngData { ctx in
                    ctx.cgContext.translateBy(x: 0, y: size.height)
                    ctx.cgContext.rotate(by: -.pi / 2)
                    image.draw(at: .zero)
                }
            } else {
                // already as seen: the image carries its orientation, which the screenshot's own
                // PNG does not keep (CI: the shot came out upright, the content on its side), so
                // it is drawn out as it shows
                png = UIGraphicsImageRenderer(size: image.size, format: format).pngData { _ in image.draw(at: .zero) }
            }
            a = XCTAttachment(data: png, uniformTypeIdentifier: "public.png")
        } else {
            a = XCTAttachment(screenshot: shot)
        }
        a.name = "shot-\(isPad ? "ipad" : "iphone")-\(name)-\(language)"
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
