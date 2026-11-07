import XCTest

/// The Remote's diagnostics (Epic 53 stories 53.11 §4, 53.12 §4, 53.15 §5) against
/// `MockHome`'s made-up answers: Errors (a group's page and Resolve), Logs (a search, and a
/// reference followed), the Overview and Storage dashboards, and the problem-report inbox.
/// MAKTABA_SHOTS, when set, is a folder the screenshots are written to as well (CI's Remote
/// UI tests job keeps them; the maktaba.dev Apps pages take theirs from it).
///
/// Every wait is on what stays on screen until the test acts (a row, a page's title), never
/// on a message that hides itself.
@MainActor
final class RemoteDiagnosticsUITests: XCTestCase {
    var app: XCUIApplication!
    var home: MockHome!

    override func setUp() async throws {
        continueAfterFailure = false
        home = try MockHome()
        try home.start()
        stubDiagnostics()
        app = XCUIApplication()
    }

    override func tearDown() async throws {
        app?.terminate()
        home?.stop()
    }

    private func element(_ id: String) -> XCUIElement { app.descendants(matching: .any)[id].firstMatch }

    private func launch(_ language: String = "en", locale: String = "en_US") {
        app.launchArguments = ["-maktaba-permissions-reset", "-maktaba-permissions-fake", "granted", "-maktaba-session-reset",
                               "-AppleLanguages", "(\(language))", "-AppleLocale", locale]
        app.launch()
        let walkthrough = element("permissions.walkthrough")
        if walkthrough.waitForExistence(timeout: 10) {
            app.buttons["permissions.continue"].tap()
            XCTAssertTrue(walkthrough.waitForNonExistence(timeout: 5))
        }
        let field = app.textFields["picker.address"]
        XCTAssertTrue(field.waitForExistence(timeout: 10))
        field.typeExactly(home.url, in: app)
        app.buttons["picker.connect"].tap()
        let byCode = app.buttons[language == "ar" ? "برمز" : "With a code"]
        XCTAssertTrue(byCode.waitForExistence(timeout: 10))
        byCode.tap()
        home.approve()
        XCTAssertTrue(byCode.waitForNonExistence(timeout: 20), "approved: signed in")
    }

    /// The Home tab's row, scrolled to.
    private func open(_ id: String, homeTab: String) {
        let tab = app.buttons[homeTab].firstMatch
        XCTAssertTrue(tab.waitForExistence(timeout: 10))
        tab.tap()
        let row = element(id)
        for _ in 0 ..< 6 where !(row.exists && row.isHittable) { app.swipeUp(velocity: .slow) }
        XCTAssertTrue(row.waitForExistence(timeout: 10), id)
        row.tap()
    }

    private func back() { app.navigationBars.buttons.firstMatch.tap() }

    func testDiagnostics() throws {
        launch()
        try XCTSkipUnless(app.buttons["Queue"].firstMatch.waitForExistence(timeout: 10), "the Remote's screens (the Player has none)")

        open("home.errors", homeTab: "Home")
        let group = element("errors.group.e1")
        XCTAssertTrue(group.waitForExistence(timeout: 15), "the error groups")
        XCTAssertTrue(home.seen.contains("GET /api/admin/errors"))
        shot("errors")
        group.tap()
        let resolve = element("error.resolve")
        XCTAssertTrue(resolve.waitForExistence(timeout: 10), "the group's page")
        shot("error-group")
        resolve.tap()
        let posted = { self.home.requests.contains { $0.method == "POST" && $0.path == "/api/admin/errors/e1/status" } }
        let end = Date().addingTimeInterval(10)
        while !posted(), Date() < end { RunLoop.current.run(until: Date().addingTimeInterval(0.2)) }
        XCTAssertTrue(posted(), "Resolve went to the home")
        back()
        back()

        open("home.logs", homeTab: "Home")
        let line = app.descendants(matching: .any).containing(NSPredicate(format: "label CONTAINS %@", "segment_missing")).firstMatch
        XCTAssertTrue(line.waitForExistence(timeout: 15), "a line of the search")
        shot("logs")
        back()

        open("home.dashboards", homeTab: "Home")
        let chart = element("dash.panel.maktaba_component_up")
        XCTAssertTrue(chart.waitForExistence(timeout: 15), "the Overview's first chart")
        XCTAssertTrue(chart.label.hasPrefix("Components up: now"), "its summary for VoiceOver: \(chart.label)")
        shot("dashboards")
        back()

        open("home.reports", homeTab: "Home")
        XCTAssertTrue(element("inbox.report.27").waitForExistence(timeout: 15), "the inbox")
        shot("reports")
    }

    func testDiagnosticsInArabic() throws {
        launch("ar", locale: "ar_JO")
        let homeTab = "الجهاز الرئيسي"
        try XCTSkipUnless(app.buttons[homeTab].firstMatch.waitForExistence(timeout: 10), "the Remote's screens (the Player has none)")
        open("home.errors", homeTab: homeTab)
        XCTAssertTrue(element("errors.group.e1").waitForExistence(timeout: 15))
        XCTAssertTrue(app.staticTexts["الجديدة والمرئية"].exists || app.buttons["الجديدة والمرئية"].exists, "the filter in Arabic")
        shot("ar-errors")
        back()
        open("home.dashboards", homeTab: homeTab)
        let chart = element("dash.panel.maktaba_component_up")
        XCTAssertTrue(chart.waitForExistence(timeout: 15))
        XCTAssertTrue(chart.label.hasPrefix("المكوّنات العاملة"), "Components up, in Arabic: \(chart.label)")
        shot("ar-dashboards")
    }

    // MARK: made-up answers

    private func stubDiagnostics() {
        let now = Date()
        func t(_ s: TimeInterval) -> String { ISO8601DateFormatter().string(from: now.addingTimeInterval(s)) }
        home.stub("GET", "/api/admin/errors", body: """
        {"groups":[
         {"fp":"e1","service":"streaming","component":"streaming","code":"segment_missing","title":"A segment of the stream was missing",
          "first_seen":"\(t(-6 * 86400))","last_seen":"\(t(-300))","count":42,"count_24h":7,"users":2,"status":"new",
          "last_version":"0.3.90","days":[0,1,0,2,0,0,3,1,0,0,5,2,4,7]},
         {"fp":"e2","service":"pipeline","component":"transcription","code":"model_timeout","title":"Transcription took too long",
          "first_seen":"\(t(-3 * 86400))","last_seen":"\(t(-7200))","count":5,"count_24h":1,"users":0,"status":"seen",
          "days":[0,0,0,0,0,0,0,0,0,0,1,2,1,1]}]}
        """)
        home.stub("GET", "/api/admin/errors/e1", body: """
        {"group":{"fp":"e1","service":"streaming","component":"streaming","code":"segment_missing","title":"A segment of the stream was missing",
          "first_seen":"\(t(-6 * 86400))","last_seen":"\(t(-300))","count":42,"count_24h":7,"users":2,"status":"new","last_version":"0.3.90"},
         "hours":{"\(t(-3600 * 30))":2,"\(t(-3600 * 20))":3,"\(t(-3600 * 5))":1,"\(t(-3600))":4},
         "samples":{"lines":[{"ts":"\(t(-300))","level":"error","service":"streaming","node":"home","msg":"segment_missing",
           "version":"0.3.90","request_id":"0192f0c4-1111-7000-8000-000000000001"}],"ids":[991]}}
        """)
        home.stub("POST", "/api/admin/errors/e1/status", status: 204)
        home.stub("GET", "/api/admin/logs/search", body: """
        {"lines":[
          {"ts":"\(t(-300))","level":"error","service":"streaming","component":"streaming","msg":"segment_missing","request_id":"0192f0c4-1111-7000-8000-000000000001"},
          {"ts":"\(t(-301))","level":"info","service":"api","component":"core.api","msg":"http_request","request_id":"0192f0c4-1111-7000-8000-000000000001"},
          {"ts":"\(t(-900))","level":"warn","service":"pipeline","component":"transcription","msg":"model_slow"}],
         "ids":[991,990,970]}
        """)
        func series(_ name: String, _ labels: String, _ base: Double, _ step: Double) -> String {
            let pts = (0..<24).map { i in "[\(Int(now.timeIntervalSince1970) - (23 - i) * 3600),\(base + step * Double(i % 6))]" }
            return #"{"name":"\#(name)","labels":"\#(labels)","kind":"gauge","points":[\#(pts.joined(separator: ","))]}"#
        }
        home.stub("GET", "/api/admin/dashboards/overview", body: """
        {"board":"overview","range":"24h","step":3600,"series":[
          \(series("maktaba_component_up", "id=core.api", 1, 0)),
          \(series("maktaba_component_up", "id=streaming", 1, 0)),
          \(series("maktaba_errors_per_hour", "component=streaming", 1, 1)),
          \(series("process_resident_memory_bytes", "", 180_000_000, 4_000_000))]}
        """)
        home.stub("GET", "/api/admin/dashboards/storage", body: """
        {"board":"storage","range":"24h","step":3600,"series":[
          \(series("maktaba_logs_segments_bytes", "", 120_000_000, 2_000_000)),
          \(series("maktaba_logs_index_bytes", "", 60_000_000, 1_000_000))]}
        """)
        home.stub("GET", "/api/reports", body: """
        {"reports":[{"id":27,"at":"\(t(-1800))","name":"Viewer","app":"apple.player","category":"playback","status":"new","shared_with_maktaba":false},
                    {"id":26,"at":"\(t(-86400))","name":"Viewer","app":"android.tv","category":"live_tv","status":"looking","shared_with_maktaba":false}]}
        """)
    }

    private func shot(_ name: String) {
        let png = app.screenshot().pngRepresentation
        let a = XCTAttachment(data: png, uniformTypeIdentifier: "public.png")
        a.name = name
        a.lifetime = .keepAlways
        add(a)
        if let dir = ProcessInfo.processInfo.environment["MAKTABA_SHOTS"] {
            try? FileManager.default.createDirectory(atPath: dir, withIntermediateDirectories: true)
            let device = UIDevice.current.userInterfaceIdiom == .pad ? "ipad" : "iphone"
            try? png.write(to: URL(fileURLWithPath: dir).appendingPathComponent("remote-\(device)-\(name).png"))
        }
    }
}
