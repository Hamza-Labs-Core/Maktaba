import XCTest

/// `MockHome` answers every browse and live route the Player's screens read (Epic 48's
/// route table) with its fixtures: valid JSON, times filled in around now, stubs and delays
/// winning, and the requests recorded with their query and body.
final class PlayerMockHomeTests: XCTestCase {
    private var home: MockHome!

    override func setUpWithError() throws {
        home = try MockHome()
        try home.start()
    }

    override func tearDown() {
        home?.stop()
    }

    private func call(_ method: String, _ path: String, body: String? = nil) async throws -> (Int, Data) {
        var r = URLRequest(url: URL(string: home.url + path)!)
        r.httpMethod = method
        if let body {
            r.httpBody = Data(body.utf8)
            r.setValue("application/json", forHTTPHeaderField: "Content-Type")
        }
        let (data, response) = try await URLSession.shared.data(for: r)
        return ((response as? HTTPURLResponse)?.statusCode ?? 0, data)
    }

    /// Each route of the spec's table (research.md, "Every route below was found in the Go
    /// handlers"), as a screen calls it.
    func testEveryBrowseAndLiveRouteAnswers() async throws {
        let routes: [(String, String, Int)] = [
            ("GET", "/api/home", 200),
            ("GET", "/api/recommendations?surface=mobile-home", 200),
            ("DELETE", "/api/recommendations/items/v3", 204),
            ("DELETE", "/api/recommendations/rows/because_watched", 204),
            ("GET", "/api/libraries", 200),
            ("GET", "/api/libraries/summary", 200),
            ("GET", "/api/me/pinned-libraries", 200),
            ("PUT", "/api/me/pinned-libraries", 200),
            ("GET", "/api/libraries/lib-lectures/hub", 200),
            ("GET", "/api/libraries/lib-docs/hub", 200),
            ("GET", "/api/libraries/lib-lectures/series", 200),
            ("GET", "/api/videos?library=lib-lectures", 200),
            ("GET", "/api/videos/facets", 200),
            ("GET", "/api/videos/v1", 200),
            ("GET", "/api/me/videos/v1", 200),
            ("GET", "/api/videos/v1/chapters", 200),
            ("GET", "/api/videos/v1/subtitles", 200),
            ("GET", "/api/videos/v1/segments", 200),
            ("GET", "/api/series", 200),
            ("GET", "/api/series/s-seerah", 200),
            ("GET", "/api/series/s-seerah/episodes", 200),
            ("POST", "/api/me/watched", 200),
            ("POST", "/api/me/favorites", 200),
            ("POST", "/api/search", 200),
            ("GET", "/api/search/suggest?q=sa", 200),
            ("GET", "/api/search/recent", 200),
            ("DELETE", "/api/search/recent", 204),
            ("GET", "/api/channels", 200),
            ("GET", "/api/channels/ch-1", 200),
            ("GET", "/api/channels/now", 200),
            ("GET", "/api/channels/guide", 200),
            ("GET", "/api/channels/ch-2/guide", 200),
            ("GET", "/api/tv/info", 200),
            ("POST", "/api/tv/remote/tune", 200),
            ("GET", "/api/me", 200),
            ("GET", "/api/me/sessions", 200),
            ("GET", "/api/me/history", 200),
            ("GET", "/api/me/playlists", 200),
            ("POST", "/api/stream/sessions", 201),
        ]
        for (method, path, want) in routes {
            let (status, data) = try await call(method, path, body: method == "GET" || method == "DELETE" ? nil : "{}")
            XCTAssertEqual(status, want, "\(method) \(path)")
            if status != 204 {
                XCTAssertNoThrow(try JSONSerialization.jsonObject(with: data), "\(method) \(path) answers JSON")
                XCTAssertFalse(String(decoding: data, as: UTF8.self).contains("{{now"), "\(method) \(path): times filled in")
            }
        }
        let unknown = try await call("GET", "/api/nothing/here")
        XCTAssertEqual(unknown.0, 404)
    }

    /// What is on now started before now and ends after it.
    func testLiveTimesStraddleNow() async throws {
        let (_, data) = try await call("GET", "/api/channels/now")
        let doc = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
        let channels = try XCTUnwrap(doc["channels"] as? [[String: Any]])
        XCTAssertFalse(channels.isEmpty)
        let f = ISO8601DateFormatter()
        for c in channels {
            let current = try XCTUnwrap(c["current"] as? [String: Any])
            let start = try XCTUnwrap(f.date(from: try XCTUnwrap(current["start"] as? String)))
            let stop = try XCTUnwrap(f.date(from: try XCTUnwrap(current["stop"] as? String)))
            XCTAssertLessThan(start, Date())
            XCTAssertGreaterThan(stop, Date())
        }
    }

    /// The videos page by cursor to the end.
    func testVideosPageByCursor() async throws {
        let first = try JSONSerialization.jsonObject(with: try await call("GET", "/api/videos").1) as? [String: Any]
        let next = try XCTUnwrap(first?["next"] as? String)
        let last = try JSONSerialization.jsonObject(with: try await call("GET", "/api/videos?cursor=\(next)").1) as? [String: Any]
        XCTAssertTrue(last?["next"] is NSNull, "the last page")
        XCTAssertEqual(home.requests.last?.value("cursor"), next)
    }

    func testStubsWinAndRequestsAreRecorded() async throws {
        home.stub("GET", "/api/home", status: 503, body: #"{"title":"down"}"#)
        let stubbed = try await call("GET", "/api/home")
        XCTAssertEqual(stubbed.0, 503)
        home.reset()
        let fixture = try await call("GET", "/api/home")
        XCTAssertEqual(fixture.0, 200)

        _ = try await call("PUT", "/api/me/pinned-libraries", body: #"{"library_ids":["lib-docs"]}"#)
        let put = try XCTUnwrap(home.requests.last)
        XCTAssertEqual(put.method, "PUT")
        XCTAssertEqual(String(decoding: put.body, as: UTF8.self), #"{"library_ids":["lib-docs"]}"#)
        XCTAssertTrue(home.seen.contains("PUT /api/me/pinned-libraries"))

        let (status, png) = try await call("GET", "/api/videos/v1/poster")
        XCTAssertEqual(status, 200)
        XCTAssertEqual(png.prefix(4), Data([0x89, 0x50, 0x4E, 0x47]), "a PNG")
    }

    func testADelayedAnswerWaits() async throws {
        home.delay("GET", "/api/home", by: 1.5)
        let started = Date()
        let (status, _) = try await call("GET", "/api/home")
        XCTAssertEqual(status, 200)
        XCTAssertGreaterThanOrEqual(Date().timeIntervalSince(started), 1.4)
    }

    func testTimesAreFilledRelativeToNow() {
        let now = Date(timeIntervalSince1970: 1_800_000_000)
        XCTAssertEqual(MockHome.fillTimes(#"{"a":"{{now}}","b":"{{now-60}}","c":"{{now+3600}}"}"#, now: now),
                       #"{"a":"2027-01-15T08:00:00Z","b":"2027-01-15T07:59:00Z","c":"2027-01-15T09:00:00Z"}"#)
    }
}
