import Foundation
import Testing
@testable import MaktabaKit

// The browse client (story 48.6) against the answers of a real home: every fixture in
// Tests/Fixtures/Browse was recorded from the branch's API on a scratch Postgres (a viewer
// of one library of two; a series of three episodes, chapters, a transcript, a subtitle, a
// playlist, a search), so a model that reads it reads what the home sends. A stub answers in
// place of the home and keeps what each request asked.

/// The recorded answer `name` (Tests/Fixtures/Browse/<name>.json).
func browseFixture(_ name: String) throws -> String {
    let dir = URL(filePath: #filePath).deletingLastPathComponent().deletingLastPathComponent()
        .appending(path: "Fixtures/Browse")
    return try String(contentsOf: dir.appending(path: "\(name).json"), encoding: .utf8)
}

/// Answers each "METHOD /path" from its queue (the last answer repeats), and keeps every
/// request it saw.
final class BrowseStub: URLProtocol, @unchecked Sendable {
    struct Seen {
        let method: String
        let path: String
        let query: [String: String]
        let json: [String: Any]?
        let bearer: String?
    }

    nonisolated(unsafe) private static var routes: [String: [(Int, String)]] = [:]
    nonisolated(unsafe) private static var seen: [Seen] = []
    private static let lock = NSLock()

    static func reset() {
        lock.withLock {
            routes = [:]
            seen = []
        }
    }

    static func on(_ method: String, _ path: String, _ answers: (Int, String)...) {
        lock.withLock { routes["\(method) \(path)"] = answers }
    }

    static var last: Seen? { lock.withLock { seen.last } }
    static var all: [Seen] { lock.withLock { seen } }

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        var body = request.httpBody
        if body == nil, let stream = request.httpBodyStream {
            stream.open()
            var d = Data()
            var buf = [UInt8](repeating: 0, count: 4096)
            while stream.hasBytesAvailable {
                let n = stream.read(&buf, maxLength: buf.count)
                if n <= 0 { break }
                d.append(buf, count: n)
            }
            stream.close()
            body = d
        }
        let url = request.url!
        let method = request.httpMethod ?? "GET"
        let path = url.path(percentEncoded: false)
        var query: [String: String] = [:]
        for item in URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems ?? [] {
            query[item.name] = item.value ?? ""
        }
        let json = body.flatMap { try? JSONSerialization.jsonObject(with: $0) as? [String: Any] }
        let bearer = request.value(forHTTPHeaderField: "Authorization")
        let answer: (Int, String)? = Self.lock.withLock {
            Self.seen.append(Seen(method: method, path: path, query: query, json: json, bearer: bearer))
            let key = "\(method) \(path)"
            guard var list = Self.routes[key], !list.isEmpty else { return nil }
            let first = list.count > 1 ? list.removeFirst() : list[0]
            Self.routes[key] = list
            return first
        }
        let (code, text) = answer ?? (404, #"{"type":"https://maktaba.dev/problems/not-found","status":404,"title":"no stub"}"#)
        let resp = HTTPURLResponse(url: url, statusCode: code, httpVersion: "HTTP/1.1",
                                   headerFields: ["Content-Type": "application/json"])!
        client?.urlProtocol(self, didReceive: resp, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(text.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }

    override func stopLoading() {}
}

enum BrowseHome {
    static let url = URL(string: "http://browse.test:8088")!
    static let v1 = "00000000-0000-4000-8000-000000000001"
    static let v2 = "00000000-0000-4000-8000-000000000002"
    static let v4 = "00000000-0000-4000-8000-000000000004"
    static let v9 = "00000000-0000-4000-8000-000000000009"
    static let lib = "11111111-1111-4111-8111-111111111111"
    static let otherLib = "22222222-2222-4222-8222-222222222222"
    static let series = "5e000000-0000-4000-8000-000000000001"

    /// A client of the stub home, signed in.
    static func client(app: String = "test.browse") -> (APIClient, TokenStore) {
        let tokens = TokenStore(app: app, home: url)
        tokens.clear()
        tokens.save(AuthTokens(accessToken: "access.one.x", refreshToken: "refresh-1", user: nil))
        let config = URLSessionConfiguration.ephemeral
        config.protocolClasses = [BrowseStub.self]
        return (APIClient(home: url, tokens: tokens, session: URLSession(configuration: config)), tokens)
    }

    /// Answers `path` with the recorded `fixture`.
    static func answer(_ method: String, _ path: String, _ fixture: String, status: Int = 200) throws {
        BrowseStub.on(method, path, (status, try browseFixture(fixture)))
    }

    static func answer(_ path: String, _ fixture: String, status: Int = 200) throws {
        try answer("GET", path, fixture, status: status)
    }
}

/// One stub for every browse suite: they run one at a time (nested, they inherit it).
@Suite(.serialized) struct BrowseClient {
    @Suite struct BrowseHomeScreen {
        init() { BrowseStub.reset() }

        @Test func homeReadsEveryRowInOrder() async throws {
            try BrowseHome.answer("/api/home", "home")
            let rows = try await BrowseHome.client().0.home()
            #expect(BrowseStub.last?.method == "GET" && BrowseStub.last?.path == "/api/home")
            #expect(BrowseStub.last?.bearer == "Bearer access.one.x")
            let hero = try #require(rows.featured)
            #expect(hero.kind == "series" && hero.series?.episodes == 3 && hero.video.id == BrowseHome.v1 && !hero.picked)
            #expect(rows.continueWatching.map(\.id) == [BrowseHome.v2, BrowseHome.v4])
            #expect(rows.continueWatching[0].progress == 754.5 / 3000)
            #expect(rows.continueWatching[0].lastWatchedAt != nil)
            #expect(rows.favorites.map(\.id) == [BrowseHome.v4])
            #expect(rows.onDeck.isEmpty)
            // an untitled video reads as its file
            let untitled = try #require(rows.recentlyAdded.first)
            #expect(untitled.title == nil && untitled.displayTitle == "untitled-lecture.mp4" && untitled.durationSec == nil)
            #expect(rows.recentlyAddedGrouped.contains { $0.kind == "series" && $0.count == 3 && $0.series?.id == BrowseHome.series })
            #expect(rows.pinned.map(\.library.name) == ["Tafsir Lessons"])
            #expect(rows.recentlyWatched.map(\.id) == [BrowseHome.v2, BrowseHome.v4, BrowseHome.v1])
            #expect(rows.recentlyWatched.last?.completed == true && rows.recentlyWatched.last?.progress == 1)
            #expect(!rows.isEmpty)
        }

        @Test func railsAskForThePhonesSurface() async throws {
            try BrowseHome.answer("/api/recommendations", "recommendations")
            let rails = try await BrowseHome.client().0.homeRails()
            #expect(BrowseStub.last?.query == ["surface": "mobile-home", "limit": "20"])
            #expect(rails.map(\.reasonKind) == ["continue_watching"])
            #expect(rails.first?.items.map(\.videoID) == [BrowseHome.v2, BrowseHome.v4])
            #expect(rails.first?.items.first?.progress == 754.5 / 3000)
        }

        @Test func notInterestedReachesItsRoutes() async throws {
            BrowseStub.on("DELETE", "/api/recommendations/items/\(BrowseHome.v4)", (204, ""))
            BrowseStub.on("DELETE", "/api/recommendations/rows/for_you", (204, ""))
            let c = BrowseHome.client().0
            try await c.dismissRecommendation(video: BrowseHome.v4)
            #expect(BrowseStub.last?.method == "DELETE" && BrowseStub.last?.path == "/api/recommendations/items/\(BrowseHome.v4)")
            try await c.dismissRecommendations(reasonKind: "for_you")
            #expect(BrowseStub.last?.path == "/api/recommendations/rows/for_you")
        }

        /// No library visible: every row empty, no hero.
        @Test func anEmptyHome() throws {
            let json = #"{"continue_watching":[],"recently_added":[],"recently_watched":[],"pinned":[],"on_deck":[],"favorites":[],"recently_added_grouped":[],"featured":null}"#
            let rows = try JSONDecoder.home.decode(HomeRows.self, from: Data(json.utf8))
            #expect(rows.isEmpty && rows.featured == nil)
        }
    }

    @Suite struct BrowseLibraries {
        init() { BrowseStub.reset() }

        @Test func cardsAndPins() async throws {
            try BrowseHome.answer("/api/libraries/summary", "libraries-summary")
            try BrowseHome.answer("/api/me/pinned-libraries", "pinned-libraries")
            try BrowseHome.answer("PUT", "/api/me/pinned-libraries", "pinned-libraries-put")
            let c = BrowseHome.client().0
            let cards = try await c.librarySummaries()
            let card = try #require(cards.first)
            #expect(card.name == "Tafsir Lessons" && card.videos == 5 && card.durationSec == 11100)
            #expect(card.pinned && card.pinPosition == 0 && card.posterVideoIDs.count == 4 && card.latestAddedAt != nil)
            #expect(try await c.pinnedLibraries().map(\.id) == [BrowseHome.lib])
            let pins = try await c.setPinnedLibraries([BrowseHome.lib])
            #expect(BrowseStub.last?.method == "PUT")
            #expect(BrowseStub.last?.json?["library_ids"] as? [String] == [BrowseHome.lib])
            #expect(pins.map(\.position) == [0])
        }

        @Test func thePinnedComeFirst() throws {
            let json = #"{"items":[{"id":"a","name":"A"},{"id":"b","name":"B","pinned":true,"pin_position":1},{"id":"c","name":"C","pinned":true,"pin_position":0}]}"#
            let cards = try JSONDecoder.home.decode(Items<LibrarySummary>.self, from: Data(json.utf8)).items
            #expect(LibrarySummary.ordered(cards).map(\.id) == ["c", "b", "a"])
        }

        @Test func aLibrarysHub() async throws {
            try BrowseHome.answer("/api/libraries/\(BrowseHome.lib)/hub", "library-hub")
            let hub = try await BrowseHome.client().0.libraryHub(BrowseHome.lib)
            #expect(hub.library.name == "Tafsir Lessons" && hub.library.videos == 5)
            #expect(hub.continueWatching.count == 2 && hub.recentlyAdded.count == 5 && hub.mostWatched.isEmpty)
            #expect(hub.series.map(\.episodes) == [3] && hub.series.first?.posterVideoID != nil)
            #expect(!hub.years.isEmpty && hub.years.allSatisfy { $0.videos > 0 })
            #expect(hub.recentlyAddedGrouped.contains { $0.kind == "series" })
        }

        /// The grid pages by cursor with its sort and facets in the query.
        @Test func theGridPagesByCursor() async throws {
            try BrowseHome.answer("/api/videos", "videos-page1")
            let c = BrowseHome.client().0
            let q = VideoQuery(library: BrowseHome.lib, sort: .title, language: "ar", speaker: "الشيخ", watched: "unwatched", limit: 2)
            let first = try await c.videos(q)
            #expect(BrowseStub.last?.query == ["library": BrowseHome.lib, "sort": "title", "order": "asc", "limit": "2",
                                              "language": "ar", "speaker": "الشيخ", "watched": "unwatched"])
            #expect(first.items.count == 2)
            let cursor = try #require(first.next)
            try BrowseHome.answer("/api/videos", "videos-page2")
            let second = try await c.videos(q.next(cursor))
            #expect(BrowseStub.last?.query["cursor"] == cursor)
            #expect(second.items.count == 2 && Set(second.items.map(\.id)).isDisjoint(with: first.items.map(\.id)))
            // the newest first by default
            _ = try await c.videos(VideoQuery(library: BrowseHome.lib))
            #expect(BrowseStub.last?.query["sort"] == "created_at" && BrowseStub.last?.query["order"] == "desc")
        }

        @Test func facets() async throws {
            try BrowseHome.answer("/api/videos/facets", "videos-facets")
            let f = try await BrowseHome.client().0.videoFacets(library: BrowseHome.lib)
            #expect(BrowseStub.last?.query == ["library": BrowseHome.lib])
            #expect(f.languages.map(\.value) == ["ar", "en"] && f.languages.first?.videos == 4)
            #expect(f.letters.map(\.value).contains("ت"))
            #expect(f.durations.map(\.value) == ["medium", "long"] && f.speakers.isEmpty && f.tags.isEmpty)
            let tag = try JSONDecoder.home.decode(Facet.self, from: Data(#"{"id":"t1","name":"Fiqh","videos":3}"#.utf8))
            #expect(tag.value == "t1" && tag.name == "Fiqh")
        }

        @Test func aLibrarysSeriesTab() async throws {
            try BrowseHome.answer("/api/libraries/\(BrowseHome.lib)/series", "library-series")
            let page = try await BrowseHome.client().0.librarySeries(BrowseHome.lib, sort: .recent)
            #expect(BrowseStub.last?.query == ["sort": "recent", "limit": "60"])
            let s = try #require(page.items.first)
            #expect(s.episodes == 3 && s.watched == 1 && s.inProgress == 1 && s.progress == 1.0 / 3)
            #expect(page.total == 1 && page.next == nil && page.library?.name == "Tafsir Lessons")
        }
    }

    @Suite struct BrowseSeries {
        init() { BrowseStub.reset() }

        @Test func listAndOne() async throws {
            try BrowseHome.answer("/api/series", "series-list")
            try BrowseHome.answer("/api/series/\(BrowseHome.series)", "series")
            let c = BrowseHome.client().0
            let all = try await c.seriesList()
            #expect(all.map(\.id) == [BrowseHome.series] && all.first?.progress == 1.0 / 3)
            _ = try await c.seriesList(library: BrowseHome.lib)
            #expect(BrowseStub.last?.query == ["library_id": BrowseHome.lib, "page": "0"])
            let one = try await c.series(BrowseHome.series)
            // the one-series answer carries no counts (the home leaves them 0): a page reads its episodes
            #expect(one.name == "Tafsir" && one.year == 2025 && one.numbering == "season" && one.posterVideoID == BrowseHome.v1)
            #expect(one.episodeCount == 0 && one.progress == 0)
        }

        @Test func episodesAndWhatIsNext() async throws {
            try BrowseHome.answer("/api/series/\(BrowseHome.series)/episodes", "series-episodes")
            let e = try await BrowseHome.client().0.episodes(series: BrowseHome.series)
            #expect(BrowseStub.last?.query == ["sort": "episode"])
            #expect(e.seasons.map(\.season) == [1, 2] && e.episodes.count == 3)
            #expect(e.nextEpisode?.videoID == BrowseHome.v2 && e.nextEpisode?.progress == 0.2515)
            #expect(e.episodes.first?.watched == true && e.episodes.first?.progress == 1)
            #expect(e.episode(after: BrowseHome.v2)?.season == 2)
            #expect(e.episode(after: "00000000-0000-4000-8000-000000000003") == nil)
        }
    }

    @Suite struct BrowseVideoPage {
        init() { BrowseStub.reset() }

        @Test func detailAndMarks() async throws {
            try BrowseHome.answer("/api/videos/\(BrowseHome.v1)", "video")
            try BrowseHome.answer("/api/me/videos/\(BrowseHome.v1)", "my-video")
            let c = BrowseHome.client().0
            let v = try await c.videoDetail(BrowseHome.v1)
            #expect(v.displayTitle == "تفسير سورة الفاتحة" && v.durationSec == 3600 && v.description != nil)
            #expect(v.playbackState?.completed == true && v.playbackState?.positionSec == 3590)
            // finished: Play from the start
            #expect(v.resumeAt == nil && !v.playable && v.createdAt != nil)
            let mine = try await c.myVideo(BrowseHome.v1)
            #expect(mine.completed && !mine.favorite && mine.series?.id == BrowseHome.series && mine.playlists.isEmpty)
        }

        @Test func resumeOpensWhereThePersonStopped() throws {
            let json = #"{"id":"v","library_id":"l","filename":"a.mp4","playback_state":{"position_sec":754.5,"completed":false}}"#
            #expect(try JSONDecoder.home.decode(VideoInfo.self, from: Data(json.utf8)).resumeAt == 754.5)
        }

        @Test func chaptersSubtitlesTranscript() async throws {
            try BrowseHome.answer("/api/videos/\(BrowseHome.v1)/chapters", "video-chapters")
            try BrowseHome.answer("/api/videos/\(BrowseHome.v1)/subtitles", "video-subtitles")
            try BrowseHome.answer("/api/videos/\(BrowseHome.v1)/segments", "video-segments")
            let c = BrowseHome.client().0
            let ch = try await c.chapters(video: BrowseHome.v1)
            #expect(ch.map(\.startSec) == [0, 600, 1800] && ch.last?.endSec == nil && ch.first?.title == "المقدمة")
            let subs = try await c.subtitles(video: BrowseHome.v1)
            #expect(subs.first?.language == "ar" && subs.first?.format == "vtt" && subs.first?.url.hasSuffix("ar.vtt") == true)
            let t = try await c.transcript(video: BrowseHome.v1, from: 6.5, to: 60, limit: 50)
            #expect(BrowseStub.last?.query == ["from": "6.5", "to": "60", "limit": "50"])
            #expect(t.segments.count == 3 && !t.partial && t.segments.first?.speaker == "SPEAKER_00" && t.segments.last?.speaker == nil)
            #expect(t.segments.last?.text.contains("prayer") == true)
        }

        /// A video without thumbnails yet answers 404: no scrubber pictures, not an error.
        @Test func noTrickplayYet() async throws {
            try BrowseHome.answer("/api/videos/\(BrowseHome.v1)/trickplay", "video-trickplay", status: 404)
            #expect(try await BrowseHome.client().0.trickplay(video: BrowseHome.v1) == nil)
            let json = #"{"sprite_url":"/api/videos/v/trickplay/sprite","columns":10,"rows":10,"count":95,"interval_sec":10,"tile_width":160,"tile_height":90,"sheet_width":1600,"sheet_height":900}"#
            BrowseStub.on("GET", "/api/videos/\(BrowseHome.v1)/trickplay", (200, json))
            let t = try #require(try await BrowseHome.client().0.trickplay(video: BrowseHome.v1))
            #expect(t.tile(at: 0)! == (0, 0) && t.tile(at: 125)! == (2, 1) && t.tile(at: 99_999)! == (4, 9))
        }

        @Test func aSmallPoster() async throws {
            BrowseStub.on("GET", "/api/videos/\(BrowseHome.v1)/poster", (200, "jpeg"))
            let c = BrowseHome.client().0
            #expect(try await c.poster(video: BrowseHome.v1, small: true) == Data("jpeg".utf8))
            #expect(BrowseStub.last?.query == ["size": "small"])
            _ = try await c.poster(video: BrowseHome.v1, small: false)
            #expect(BrowseStub.last?.query == [:])
        }

        @Test func watchedAndFavorite() async throws {
            try BrowseHome.answer("POST", "/api/me/watched", "mark-watched")
            try BrowseHome.answer("POST", "/api/me/favorites", "mark-favorite")
            let c = BrowseHome.client().0
            #expect(try await c.markWatched([BrowseHome.v2]) == 1)
            #expect(BrowseStub.last?.json?["video_ids"] as? [String] == [BrowseHome.v2])
            #expect(BrowseStub.last?.json?["watched"] as? Bool == true)
            try await c.markFavorite([BrowseHome.v1], false)
            #expect(BrowseStub.last?.path == "/api/me/favorites" && BrowseStub.last?.json?["favorite"] as? Bool == false)
        }
    }

    @Suite struct BrowseSearch {
        init() { BrowseStub.reset() }

        @Test func bothGroupsAndThePassages() async throws {
            try BrowseHome.answer("POST", "/api/search", "search")
            let a = try await BrowseHome.client().0.search("prayer")
            #expect(BrowseStub.last?.json?["mode"] as? String == "hybrid")
            #expect(a.searchID == "GzrxeRrax489Zl2zdRr9PqV6" && a.pageSize == 24 && a.passagesPageSize == 20 && a.degraded)
            #expect(a.titles.map(\.videoID) == [BrowseHome.v4] && a.titlesTotal == 1 && a.titles.first?.libraryName == "Tafsir Lessons")
            let hit = try #require(a.transcripts.first)
            #expect(hit.videoID == BrowseHome.v1 && hit.matches == 1 && a.transcriptsTotal == 1)
            #expect(hit.passages.first?.startSec == 14.2 && hit.passages.first?.endSec == 22)
            #expect(SearchSnippet.plain(hit.passages.first?.snippet ?? "") == "today we speak about prayer and its meaning")
        }

        /// An Arabic query is sent exactly as typed.
        @Test func anArabicQueryIsSentUnchanged() async throws {
            try BrowseHome.answer("POST", "/api/search", "search")
            try BrowseHome.answer("/api/search/suggest", "search-suggest")
            let c = BrowseHome.client().0
            _ = try await c.search("تفسير «الفاتحة»", mode: .fts)
            #expect(BrowseStub.last?.json?["q"] as? String == "تفسير «الفاتحة»")
            #expect(BrowseStub.last?.json?["mode"] as? String == "fts")
            #expect(try await c.searchSuggestions("تفس").isEmpty)
            #expect(BrowseStub.last?.query == ["q": "تفس"])
        }

        @Test func pagesOfASavedSearch() async throws {
            let sid = "GzrxeRrax489Zl2zdRr9PqV6"
            try BrowseHome.answer("/api/search/results/\(sid)", "search-results-titles")
            try BrowseHome.answer("/api/search/passages", "search-passages")
            let c = BrowseHome.client().0
            let titles = try await c.searchResults(sid, list: .titles, offset: 0, limit: 24)
            #expect(BrowseStub.last?.query == ["list": "titles", "offset": "0", "limit": "24"])
            #expect(titles.total == 1 && titles.items.first?.titleSnippet?.contains("<mark>") == true && titles.nextOffset == nil)
            try BrowseHome.answer("/api/search/results/\(sid)", "search-results-transcripts")
            let tr = try await c.searchResults(sid, list: .transcripts, offset: 0, limit: 24)
            #expect(tr.items.first?.passages.count == 1)
            let p = try await c.searchPassages(sid, video: BrowseHome.v1, offset: 0, limit: 20)
            #expect(BrowseStub.last?.query["video_id"] == BrowseHome.v1 && BrowseStub.last?.query["order"] == "score")
            #expect(p.items.first?.segmentID == 3)
        }

        @Test func recentSearches() async throws {
            try BrowseHome.answer("/api/search/recent", "search-recent")
            BrowseStub.on("DELETE", "/api/search/recent", (204, ""))
            BrowseStub.on("DELETE", "/api/search/recent/s1", (204, ""))
            let c = BrowseHome.client().0
            let r = try await c.recentSearches()
            #expect(r.map(\.query) == ["prayer"] && r.first?.live == true && r.first?.lastUsedAt != nil)
            try await c.forgetRecentSearch("s1")
            #expect(BrowseStub.last?.method == "DELETE" && BrowseStub.last?.path == "/api/search/recent/s1")
            try await c.forgetRecentSearch()
            #expect(BrowseStub.last?.path == "/api/search/recent")
        }

        @Test(arguments: [
            ("a <mark>b</mark> c", [("a ", false), ("b", true), (" c", false)]),
            ("<mark>صلاة</mark>", [("صلاة", true)]),
            ("no marks", [("no marks", false)]),
            ("<b>not html</b> <mark>x", [("<b>not html</b> ", false), ("x", true)]),
        ])
        func snippetRuns(snippet: String, want: [(String, Bool)]) {
            let runs = SearchSnippet.runs(snippet)
            #expect(runs.map(\.text) == want.map(\.0) && runs.map(\.marked) == want.map(\.1))
        }
    }

    @Suite struct BrowseProfile {
        init() { BrowseStub.reset() }

        @Test func profileHistoryAndSettings() async throws {
            try BrowseHome.answer("/api/me", "me")
            try BrowseHome.answer("/api/me/history", "me-history")
            try BrowseHome.answer("/api/me/activity/settings", "me-activity-settings")
            BrowseStub.on("PUT", "/api/me/activity/settings", (200, #"{"track_enabled":false}"#))
            BrowseStub.on("DELETE", "/api/me/history/\(BrowseHome.v2)", (204, ""))
            let c = BrowseHome.client().0
            let me = try await c.profile()
            #expect(me.name == "Viewer" && me.username == "stackviewer" && me.email == nil && !me.isAdmin)
            let h = try await c.watchHistory()
            #expect(BrowseStub.last?.query == ["offset": "0", "limit": "50"])
            #expect(h.map(\.videoID) == [BrowseHome.v2] && h.first?.completed == true && h.first?.progress == 1)
            #expect(try await c.historyTracking())
            try await c.setHistoryTracking(false)
            #expect(BrowseStub.last?.json?["track_enabled"] as? Bool == false)
            try await c.forgetHistory(video: BrowseHome.v2)
            #expect(BrowseStub.last?.method == "DELETE")
        }

        @Test func renaming() async throws {
            try BrowseHome.answer("PATCH", "/api/me", "me")
            _ = try await BrowseHome.client().0.updateProfile(displayName: "Viewer")
            #expect(BrowseStub.last?.json?.keys.sorted() == ["display_name"])
        }

        @Test func sessionsCloudAndPlaylists() async throws {
            try BrowseHome.answer("/api/me/sessions", "me-sessions")
            try BrowseHome.answer("/api/me/cloud-link", "me-cloud-link")
            try BrowseHome.answer("/api/me/playlists", "me-playlists")
            let c = BrowseHome.client().0
            #expect(try await c.signInSessions().isEmpty)
            let link = try await c.cloudLink()
            #expect(!link.available && !link.linked)
            let linked = try JSONDecoder.home.decode(CloudLinkState.self, from: Data(#"{"available":true,"server_id":"s","link":{"user_id":"u","cloud_user_id":"c","cloud_label":"me@example.com","scope":"full"}}"#.utf8))
            #expect(linked.available && linked.linked && linked.linkedLabel == "me@example.com")
            let lists = try await c.playlists()
            let pl = try #require(lists.first)
            #expect(pl.name == "رمضان" && pl.videos == 2 && pl.description == nil && pl.durationSec == 5400)
            try BrowseHome.answer("/api/me/playlists/\(pl.id)", "playlist")
            let one = try await c.playlist(pl.id)
            #expect(one.playlist.id == pl.id && one.items.count == 2)
            // a session as the web lists it (the native sign-in has none to record)
            let json = #"{"items":[{"id":"s1","created_at":"2026-10-30T10:00:00Z","last_seen_at":"2026-10-31T10:00:00Z","expires_at":"2026-11-30T10:00:00Z","ip":null,"user_agent":"Safari","current":true}]}"#
            BrowseStub.on("GET", "/api/me/sessions", (200, json))
            BrowseStub.on("DELETE", "/api/me/sessions/s1", (204, ""))
            let s = try await c.signInSessions()
            #expect(s.first?.current == true && s.first?.ip == nil && s.first?.userAgent == "Safari")
            try await c.endSignInSession("s1")
            #expect(BrowseStub.last?.path == "/api/me/sessions/s1")
        }
    }

    /// An older home leaves fields and rows out, a newer one adds them: every screen still reads.
    @Suite struct BrowseLeniency {
        init() { BrowseStub.reset() }

        /// Every object of `json` with fields no home sends yet (an object, a list, a string).
        static func withUnknownFields(_ json: Any) -> Any {
            if let o = json as? [String: Any] {
                var out = o.mapValues(withUnknownFields)
                out["x_future_object"] = ["nested": [1, 2]]
                out["x_future_list"] = [["a": 1], "b"]
                out["x_future_text"] = "?"
                return out
            }
            if let a = json as? [Any] { return a.map(withUnknownFields) }
            return json
        }

        static func decode<T: Decodable>(_ type: T.Type, fixture: String, transform: (Any) -> Any = { $0 }) throws -> T {
            let raw = try JSONSerialization.jsonObject(with: Data(try browseFixture(fixture).utf8), options: .fragmentsAllowed)
            let data = try JSONSerialization.data(withJSONObject: transform(raw))
            return try JSONDecoder.home.decode(T.self, from: data)
        }

        @Test func unknownFieldsEverywhere() throws {
            let f: (Any) -> Any = Self.withUnknownFields
            #expect(try Self.decode(HomeRows.self, fixture: "home", transform: f).continueWatching.count == 2)
            #expect(try Self.decode(LibraryHub.self, fixture: "library-hub", transform: f).recentlyAdded.count == 5)
            #expect(try Self.decode(Items<LibrarySummary>.self, fixture: "libraries-summary", transform: f).items.count == 1)
            #expect(try Self.decode(VideoListPage.self, fixture: "videos-page1", transform: f).items.count == 2)
            #expect(try Self.decode(VideoFacets.self, fixture: "videos-facets", transform: f).languages.count == 2)
            #expect(try Self.decode(LibrarySeriesPage.self, fixture: "library-series", transform: f).items.count == 1)
            #expect(try Self.decode(SeriesList.self, fixture: "series-list", transform: f).items.count == 1)
            #expect(try Self.decode(SeriesEpisodes.self, fixture: "series-episodes", transform: f).episodes.count == 3)
            #expect(try Self.decode(VideoInfo.self, fixture: "video", transform: f).playbackState != nil)
            #expect(try Self.decode(MyVideo.self, fixture: "my-video", transform: f).completed)
            #expect(try Self.decode(Items<Chapter>.self, fixture: "video-chapters", transform: f).items.count == 3)
            #expect(try Self.decode(Items<SubtitleTrack>.self, fixture: "video-subtitles", transform: f).items.count == 1)
            #expect(try Self.decode(Transcript.self, fixture: "video-segments", transform: f).segments.count == 3)
            #expect(try Self.decode(SearchAnswer.self, fixture: "search", transform: f).transcripts.count == 1)
            #expect(try Self.decode(SearchPage<SearchVideo>.self, fixture: "search-results-transcripts", transform: f).items.count == 1)
            #expect(try Self.decode(SearchPage<Passage>.self, fixture: "search-passages", transform: f).items.count == 1)
            #expect(try Self.decode(Items<RecentSearch>.self, fixture: "search-recent", transform: f).items.count == 1)
            #expect(try Self.decode(Profile.self, fixture: "me", transform: f).username == "stackviewer")
            #expect(try Self.decode(Items<HistoryEntry>.self, fixture: "me-history", transform: f).items.count == 1)
            #expect(try Self.decode(Items<PlaylistSummary>.self, fixture: "me-playlists", transform: f).items.count == 1)
            #expect(try Self.decode(Playlist.self, fixture: "playlist", transform: f).items.count == 2)
            #expect(try Self.decode(LenientRecommendations.self, fixture: "recommendations", transform: f).rails.count == 1)
        }

        /// Each of Home's rows may be missing (an older home): the others still show.
        @Test(arguments: ["featured", "continue_watching", "on_deck", "favorites", "recently_added",
                          "recently_added_grouped", "recently_watched", "pinned"])
        func aHomeWithoutOneRow(row: String) throws {
            let rows = try Self.decode(HomeRows.self, fixture: "home") { json in
                var o = json as! [String: Any]
                o[row] = nil
                return o
            }
            #expect(rows.continueWatching.isEmpty == (row == "continue_watching"))
            #expect((rows.featured == nil) == (row == "featured"))
            #expect(rows.pinned.isEmpty == (row == "pinned"))
        }

        @Test(arguments: ["continue_watching", "on_deck", "recently_added", "recently_added_grouped", "recently_watched",
                          "most_watched", "series", "years"])
        func aHubWithoutOneRow(row: String) throws {
            let hub = try Self.decode(LibraryHub.self, fixture: "library-hub") { json in
                var o = json as! [String: Any]
                var rows = o["rows"] as! [String: Any]
                rows[row] = nil
                o["rows"] = rows
                return o
            }
            #expect(hub.recentlyAdded.isEmpty == (row == "recently_added"))
            #expect(hub.series.isEmpty == (row == "series"))
        }

        /// A field of another shape reads as absent; a row element without an id is left out.
        @Test func oddValuesDoNotHideTheRow() throws {
            let json = #"""
            {"continue_watching":[
               {"id":"a","filename":"a.mp4","duration_sec":"long","position_sec":null,"title":7,"series":"x"},
               {"filename":"no-id.mp4"},
               42,
               {"id":"b","filename":"b.mp4","created_at":"not a time"}],
             "pinned":{"not":"a list"},
             "featured":{"kind":"video"}}
            """#
            let rows = try JSONDecoder.home.decode(HomeRows.self, from: Data(json.utf8))
            #expect(rows.continueWatching.map(\.id) == ["a", "b"])
            let a = rows.continueWatching[0]
            #expect(a.durationSec == nil && a.title == nil && a.series == nil && a.progress == 0)
            #expect(rows.continueWatching[1].createdAt == nil)
            #expect(rows.pinned.isEmpty && rows.featured == nil)
        }

        @Test func aHubWithoutRowsAndEpisodesWithoutNext() throws {
            let hub = try JSONDecoder.home.decode(LibraryHub.self, from: Data(#"{"library":{"id":"l","name":"L"}}"#.utf8))
            #expect(hub.recentlyAdded.isEmpty && hub.years.isEmpty && hub.library.videos == nil)
            let e = try JSONDecoder.home.decode(SeriesEpisodes.self, from: Data(#"{"seasons":[{"season":1,"episodes":[{"video_id":"v","title":"T"}]}]}"#.utf8))
            #expect(e.nextEpisode == nil && e.numbering == "season" && e.episodes.first?.progress == 0)
            let v = try JSONDecoder.home.decode(VideoInfo.self, from: Data(#"{"id":"v"}"#.utf8))
            #expect(v.playbackState == nil && v.playable && v.displayTitle == "")
        }
    }

    /// What a screen says when the home refuses.
    @Suite struct BrowseFailures {
        init() { BrowseStub.reset() }

        static func failure(_ call: () async throws -> Void) async -> Error? {
            do {
                try await call()
                return nil
            } catch {
                return error
            }
        }

        /// A library or video the caller may not see answers 404 (and a video's own route 403):
        /// "not available", as for one that does not exist.
        @Test func aHiddenThingIsNotAvailable() async throws {
            let c = BrowseHome.client().0
            try BrowseHome.answer("/api/libraries/\(BrowseHome.otherLib)/hub", "library-hub-404", status: 404)
            #expect(await Self.failure { _ = try await c.libraryHub(BrowseHome.otherLib) } as? BrowseError == .notAvailable)
            try BrowseHome.answer("/api/videos/\(BrowseHome.v9)", "video-hidden-403", status: 403)
            #expect(await Self.failure { _ = try await c.videoDetail(BrowseHome.v9) } as? BrowseError == .notAvailable)
            try BrowseHome.answer("POST", "/api/me/favorites", "mark-404", status: 404)
            #expect(await Self.failure { _ = try await c.markFavorite([BrowseHome.v9]) } as? BrowseError == .notAvailable)
            BrowseStub.on("GET", "/api/series/\(BrowseHome.series)", (404, #"{"type":"https://maktaba.dev/problems/not-found","status":404}"#))
            #expect(await Self.failure { _ = try await c.series(BrowseHome.series) } as? BrowseError == .notAvailable)
            #expect(BrowseError.notAvailable.errorDescription == ErrorStrings.forPreferredLanguages().sentence(.notFound))
        }

        /// Only a route's "not yours" is mapped: a 403 on Home is no access; the front door's
        /// "home unreachable" stays unreachable; a 500 stays a server error.
        @Test func otherRefusalsKeepTheirKind() async throws {
            let c = BrowseHome.client().0
            BrowseStub.on("GET", "/api/home", (403, #"{"type":"https://maktaba.dev/problems/forbidden","status":403}"#))
            #expect((await Self.failure { _ = try await c.home() } as? APIError)?.kind == .noAccess)
            BrowseStub.on("GET", "/api/home", (404, #"{"type":"https://maktaba.dev/problems/home-unreachable","status":404}"#))
            #expect((await Self.failure { _ = try await c.home() } as? APIError)?.kind == .unreachable)
            BrowseStub.on("GET", "/api/home", (500, #"{"type":"https://maktaba.dev/problems/internal","status":500}"#))
            #expect((await Self.failure { _ = try await c.home() } as? APIError)?.kind == .server)
            BrowseStub.on("GET", "/api/home", (200, "<html>"))
            let e = await Self.failure { _ = try await c.home() }
            guard case .decoding? = e as? APIError else {
                Issue.record("not a decoding failure: \(String(describing: e))")
                return
            }
        }

        @Test func anExpiredSearchRunsAgain() async throws {
            BrowseStub.on("GET", "/api/search/results/old", (410, #"{"type":"https://maktaba.dev/problems/search-expired","status":410}"#))
            let e = await Self.failure { _ = try await BrowseHome.client().0.searchResults("old", list: .titles, offset: 24, limit: 24) }
            #expect(e as? BrowseError == .searchExpired)
        }

        /// A 401 the refresh cannot cure (the home turns the refresh token down): the existing
        /// signed-out path, the sign-in forgotten.
        @Test func aSignedOutAnswerSignsOut() async throws {
            let (c, tokens) = BrowseHome.client(app: "test.browse.out")
            try BrowseHome.answer("/api/home", "signed-out", status: 401)
            try BrowseHome.answer("POST", "/api/auth/refresh", "signed-out", status: 401)
            #expect(await Self.failure { _ = try await c.home() } as? APIError == .signedOut)
            #expect(tokens.access == nil && tokens.refresh == nil)
        }

        /// A 401 with a refresh token: one refresh, then the call again with the new token.
        @Test func anExpiredTokenRefreshesOnce() async throws {
            let (c, tokens) = BrowseHome.client(app: "test.browse.refresh")
            BrowseStub.on("GET", "/api/libraries/summary", (401, try browseFixture("signed-out")), (200, try browseFixture("libraries-summary")))
            BrowseStub.on("POST", "/api/auth/refresh", (200, #"{"access_token":"access.two.x","refresh_token":"refresh-2"}"#))
            #expect(try await c.librarySummaries().count == 1)
            #expect(BrowseStub.all.map { "\($0.method) \($0.path)" } == ["GET /api/libraries/summary", "POST /api/auth/refresh", "GET /api/libraries/summary"])
            #expect(BrowseStub.last?.bearer == "Bearer access.two.x" && tokens.access == "access.two.x")
            tokens.clear()
        }
    }
}
