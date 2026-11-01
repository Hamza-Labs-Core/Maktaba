import Foundation
import Testing
@testable import MaktabaKit

// Story 48.7: the Live client against a stubbed home (answers shaped as the Go handlers write
// them: `handlers/guide/guide.go`, `handlers/tv/tv.go`, `tvremote/sessions.go`), and
// `LiveSource` against a fake API and a fake clock.

enum LiveFixtures {
    static let home = URL(string: "http://192.168.50.177:8088")!

    static let now = #"""
    {"server_time":"2026-10-31T20:15:00Z","channels":[
      {"channel":{"id":"c1","number":101,"name":"Classics","slug":"classics","logo_url":"/api/channels/c1/logo"},
       "current":{"channel_id":"c1","kind":"movie","start":"2026-10-31T20:00:00Z","stop":"2026-10-31T21:00:00Z",
                  "title":"Casablanca","desc":"Of all the gin joints.","is_live":true,"progress":0.25,"on_air":"Casablanca (1942)"},
       "next":{"channel_id":"c1","kind":"movie","start":"2026-10-31T21:00:00Z","stop":"2026-10-31T22:30:00Z","title":"Notorious"},
       "on_air":true},
      {"channel":{"id":"c2","number":102,"name":"قناة الأطفال","slug":"kids"},"current":null,"next":null,"on_air":false},
      {"channel":{"number":"bad"}}]}
    """#

    static let grid = #"""
    {"server_time":"2026-10-31T20:15:00Z","start":"2026-10-31T20:00:00Z","end":"2026-10-31T23:00:00Z","channels":[
      {"channel":{"id":"c1","number":101,"name":"Classics","slug":"classics"},"blocks":[
        {"channel_id":"c1","kind":"movie","start":"2026-10-31T20:00:00Z","stop":"2026-10-31T21:00:00Z","title":"Casablanca","is_live":true,"progress":0.25},
        {"channel_id":"c1","kind":"episode","start":"2026-10-31T21:00:00Z","stop":"2026-10-31T21:30:00Z","title":"Columbo",
         "sub_title":"Murder by the Book","season":1,"episode":2,"series":"Columbo"},
        {"channel_id":"c1","kind":"movie","start":"not a time","stop":"2026-10-31T22:00:00Z","title":"broken"}]},
      {"channel":{"id":"c2","number":102,"name":"Kids","slug":"kids"},"blocks":[]}]}
    """#

    static let channelGuide = #"""
    {"server_time":"2026-10-31T20:15:00Z","channel":{"id":"c1","number":101,"name":"Classics","slug":"classics"},
     "blocks":[{"channel_id":"c1","kind":"movie","start":"2026-10-31T20:00:00Z","stop":"2026-10-31T21:00:00Z","title":"Casablanca"}],
     "horizon_until":"2026-11-07T00:00:00Z"}
    """#

    static func info(viaRelay: Bool, available: Bool = true, reason: String? = nil) -> String {
        let r = reason.map { "\"\($0)\"" } ?? "null"
        return #"""
        {"stream_base_url":"http://192.168.50.177:8089","hdhr_address":"","xmltv_url":"","m3u_url":"","base_from_config":false,
         "xmltv_path":"/api/channels/xmltv","m3u_path":"/api/channels/playlist.m3u","hls_path_template":"/stream/channel/{id}/index.m3u8",
         "remote":{"available":\#(available),"reason":\#(r),"via_relay":\#(viaRelay),"max_streams":2,"token_ttl_minutes":60},
         "cluster":{"hosts_on_air":1}}
        """#
    }

    static let cf = "https://customer-abc.cloudflarestream.com"
}

/// Answers by "METHOD path", in turn (the last answer repeats), and remembers every request.
final class LiveStub: URLProtocol, @unchecked Sendable {
    struct Seen: Sendable {
        let method: String
        let path: String
        let query: [String: String]
        let auth: String?
        let json: [String: String]?
    }

    nonisolated(unsafe) private static var routes: [String: [(Int, String)]] = [:]
    nonisolated(unsafe) private static var seenList: [Seen] = []
    private static let lock = NSLock()

    static func reset() { lock.withLock { routes = [:]; seenList = [] } }
    static func on(_ route: String, _ code: Int, _ body: String) {
        lock.withLock { routes[route, default: []].append((code, body)) }
    }
    static var seen: [Seen] { lock.withLock { seenList } }

    static func client() -> APIClient {
        let c = URLSessionConfiguration.ephemeral
        c.protocolClasses = [LiveStub.self]
        let tokens = TokenStore(service: "test.live", home: LiveFixtures.home,
                                primary: FakeSecrets(["\(LiveFixtures.home.absoluteString)#access": "a-live"]), legacy: nil)
        return APIClient(home: LiveFixtures.home, tokens: tokens, session: URLSession(configuration: c))
    }

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        var data = request.httpBody
        if data == nil, let s = request.httpBodyStream {
            s.open()
            var d = Data()
            var buf = [UInt8](repeating: 0, count: 4096)
            while s.hasBytesAvailable {
                let n = s.read(&buf, maxLength: buf.count)
                if n <= 0 { break }
                d.append(buf, count: n)
            }
            s.close()
            data = d
        }
        let url = request.url!
        let method = request.httpMethod ?? "GET"
        let path = url.path(percentEncoded: true)
        var query: [String: String] = [:]
        for item in URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems ?? [] { query[item.name] = item.value }
        let json = data.flatMap { try? JSONSerialization.jsonObject(with: $0) as? [String: String] }
        let answer: (Int, String) = Self.lock.withLock {
            Self.seenList.append(Seen(method: method, path: path, query: query,
                                 auth: request.value(forHTTPHeaderField: "Authorization"), json: json))
            let key = "\(method) \(path)"
            guard var list = Self.routes[key], !list.isEmpty else { return (599, "no stub for \(key)") }
            let first = list.removeFirst()
            Self.routes[key] = list.isEmpty ? [first] : list
            return first
        }
        let resp = HTTPURLResponse(url: url, statusCode: answer.0, httpVersion: "HTTP/1.1",
                                   headerFields: ["Content-Type": "application/json"])!
        client?.urlProtocol(self, didReceive: resp, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(answer.1.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }

    override func stopLoading() {}
}

private func at(_ s: String) -> Date { ISO8601DateFormatter().date(from: s)! }

@Suite(.serialized) struct LiveClient {
    init() { LiveStub.reset() }

    /// Now: every channel with what is on, its progress and what is next; one the app cannot
    /// read is left out rather than hiding the rest.
    @Test func readsWhatIsOnNow() async throws {
        LiveStub.on("GET /api/channels/now", 200, LiveFixtures.now)
        let now = try await LiveStub.client().channelsNow()
        #expect(now.serverTime == at("2026-10-31T20:15:00Z"))
        #expect(now.channels.map(\.id) == ["c1", "c2"])
        let c1 = try #require(now.channels.first)
        #expect(c1.channel.number == 101 && c1.channel.name == "Classics" && c1.onAir && c1.channel.hasLogo)
        let cur = try #require(c1.current)
        #expect(cur.title == "Casablanca" && cur.isLive && cur.progress == 0.25 && cur.onAir == "Casablanca (1942)")
        #expect(cur.progress(at: at("2026-10-31T20:30:00Z")) == 0.5)
        #expect(cur.progress(at: at("2026-10-31T19:00:00Z")) == 0 && cur.progress(at: at("2026-10-31T23:00:00Z")) == 1)
        #expect(c1.next?.title == "Notorious")
        #expect(now.channels[1].channel.name == "قناة الأطفال" && now.channels[1].current == nil && !now.channels[1].channel.hasLogo)
        let r = try #require(LiveStub.seen.last)
        #expect(r.method == "GET" && r.path == "/api/channels/now" && r.query.isEmpty && r.auth == "Bearer a-live")
    }

    @Test func asksOneCategory() async throws {
        LiveStub.on("GET /api/channels/now", 200, #"{"channels":[]}"#)
        #expect(try await LiveStub.client().channelsNow(category: "news").channels.isEmpty)
        #expect(LiveStub.seen.last?.query == ["category": "news"])
    }

    /// The guide asks for its window in RFC 3339 and reads the grid; a block it cannot read is
    /// dropped, its neighbours stay.
    @Test func readsTheGuideWindow() async throws {
        LiveStub.on("GET /api/channels/guide", 200, LiveFixtures.grid)
        let start = at("2026-10-31T20:00:00Z")
        let g = try await LiveStub.client().guide(from: start.addingTimeInterval(0.6), to: start.addingTimeInterval(3 * 3600))
        #expect(LiveStub.seen.last?.query == ["start": "2026-10-31T20:00:00Z", "end": "2026-10-31T23:00:00Z"])
        #expect(g.start == start && g.end == at("2026-10-31T23:00:00Z"))
        #expect(g.channels.map(\.id) == ["c1", "c2"])
        let row = try #require(g.channels.first)
        #expect(row.blocks.map(\.title) == ["Casablanca", "Columbo"])
        let ep = row.blocks[1]
        #expect(ep.subTitle == "Murder by the Book" && ep.season == 1 && ep.episode == 2 && ep.duration == 1800)
        #expect(row.block(at: at("2026-10-31T21:10:00Z"))?.title == "Columbo")
        #expect(row.block(at: at("2026-10-31T21:30:00Z")) == nil)
        #expect(g.channels[1].blocks.isEmpty)
    }

    @Test func readsOneChannelsGuide() async throws {
        LiveStub.on("GET /api/channels/c1/guide", 200, LiveFixtures.channelGuide)
        let g = try await LiveStub.client().channelGuide("c1", from: at("2026-10-31T20:00:00Z"), to: at("2026-11-01T02:00:00Z"))
        #expect(g.channel.id == "c1" && g.blocks.count == 1 && g.horizonUntil == at("2026-11-07T00:00:00Z"))
        #expect(LiveStub.seen.last?.query == ["start": "2026-10-31T20:00:00Z", "end": "2026-11-01T02:00:00Z"])
        // no window: the home's own (three hours from now)
        _ = try await LiveStub.client().channelGuide("c1")
        #expect(LiveStub.seen.last?.query.isEmpty == true)
    }

    /// A channel the viewer may not read is the home's 404, said as not found (FR-24).
    @Test func aChannelTheViewerMayNotReadIsNotFound() async throws {
        LiveStub.on("GET /api/channels/c9/guide", 404,
                    #"{"type":"https://maktaba.tv/problems/not-found","title":"Not Found","status":404,"detail":"channel c9"}"#)
        await #expect(throws: APIError.self) { _ = try await LiveStub.client().channelGuide("c9") }
        do {
            _ = try await LiveStub.client().channelGuide("c9")
        } catch let e as APIError {
            #expect(e.httpStatus == 404 && e.kind == .notFound)
        }
    }

    /// An id cannot reach another route.
    @Test func aChannelIDStaysOneSegment() async throws {
        let c = LiveStub.client()
        _ = try? await c.channelGuide("../../tv/info")
        let p = try #require(LiveStub.seen.last?.path)
        #expect(p.hasPrefix("/api/channels/") && p.hasSuffix("/guide") && p.split(separator: "/").count == 4, "\(p)")
        let s = c.channelStreamURL("../x")
        #expect(s.path(percentEncoded: true).split(separator: "/").count == 4, "\(s)")
    }

    @Test func theLogo() async throws {
        let c = LiveStub.client()
        let withLogo = LiveChannel(id: "c1", number: 101, name: "Classics", logoPath: "/api/channels/c1/logo")
        #expect(c.channelLogoURL(withLogo)?.absoluteString == "http://192.168.50.177:8088/api/channels/c1/logo")
        #expect(c.channelLogoURL(LiveChannel(id: "c2", number: 102, name: "Kids")) == nil)
        LiveStub.on("GET /api/channels/c1/logo", 200, "PNG")
        #expect(try await c.channelLogo("c1") == Data("PNG".utf8))
        #expect(LiveStub.seen.last?.auth == "Bearer a-live")
    }

    @Test func readsWhereTheTVStreams() async throws {
        LiveStub.on("GET /api/tv/info", 200, LiveFixtures.info(viaRelay: true, available: false, reason: "remote viewing is off"))
        let i = try await LiveStub.client().tvInfo()
        #expect(i.hlsPathTemplate == "/stream/channel/{id}/index.m3u8")
        #expect(i.remote == TVInfo.Remote(available: false, reason: "remote viewing is off", viaRelay: true, maxStreams: 2, tokenTTLMinutes: 60))
        // an older home without the template or the remote object: the LAN path, as the web
        let old = try JSONDecoder.home.decode(TVInfo.self, from: Data(#"{"stream_base_url":"x"}"#.utf8))
        #expect(old == TVInfo())
    }

    @Test func theLANPlaylistIsOnTheWebPort() {
        let c = LiveStub.client()
        #expect(c.channelStreamURL("c1").absoluteString == "http://192.168.50.177:8088/stream/channel/c1/index.m3u8")
        #expect(c.channelStreamURL("c1", template: "/tv/{id}/live.m3u8").absoluteString == "http://192.168.50.177:8088/tv/c1/live.m3u8")
    }

    /// Tune: 202 starting, then 200 live; heartbeat; end. Each reaches its route with its body.
    @Test func tunesBeatsAndEnds() async throws {
        let c = LiveStub.client()
        LiveStub.on("POST /api/tv/remote/tune", 202, #"{"session_id":"s1","state":"starting","retry_after":3}"#)
        LiveStub.on("POST /api/tv/remote/tune", 200,
                    #"{"session_id":"s1","state":"live","hls_url":"\#(LiveFixtures.cf)/tok1/manifest/video.m3u8","expires_at":"2026-10-31T21:15:00Z","refresh_after":"2026-10-31T21:05:00Z"}"#)
        let starting = try await c.tune(channel: "c1", device: "Maktaba on iOS")
        #expect(starting == RemoteTune(sessionID: "s1", state: "starting", retryAfter: 3) && !starting.isLive)
        #expect(LiveStub.seen.last?.json == ["channel_id": "c1", "device": "Maktaba on iOS"])
        let live = try await c.tune(channel: "c1", device: "Maktaba on iOS")
        #expect(live.isLive && live.hlsURL?.absoluteString == "\(LiveFixtures.cf)/tok1/manifest/video.m3u8")
        #expect(live.expiresAt == at("2026-10-31T21:15:00Z") && live.refreshAfter == at("2026-10-31T21:05:00Z"))

        LiveStub.on("POST /api/tv/remote/sessions/s1/heartbeat", 200, #"{"state":"live","expires_at":"2026-10-31T21:20:00Z","token":"tok2"}"#)
        let beat = try await c.heartbeat(session: "s1")
        #expect(beat == RemoteBeat(state: "live", expiresAt: at("2026-10-31T21:20:00Z"), token: "tok2"))

        LiveStub.on("DELETE /api/tv/remote/sessions/s1", 204, "")
        await c.endRemoteSession("s1")
        #expect(LiveStub.seen.map { "\($0.method) \($0.path)" } == [
            "POST /api/tv/remote/tune", "POST /api/tv/remote/tune",
            "POST /api/tv/remote/sessions/s1/heartbeat", "DELETE /api/tv/remote/sessions/s1",
        ])
        #expect(LiveStub.seen.allSatisfy { $0.auth == "Bearer a-live" })
    }

    /// A refused tune keeps the home's code for the screen to word.
    @Test func aRefusedTuneSaysWhy() async throws {
        LiveStub.on("POST /api/tv/remote/tune", 429,
                    #"{"type":"https://maktaba.tv/problems/user-streams-full","title":"Too Many Requests","status":429,"detail":"two streams already","code":"user_streams_full"}"#)
        do {
            _ = try await LiveStub.client().tune(channel: "c1", device: "d")
            Issue.record("a refused tune decoded")
        } catch let e as APIError {
            #expect(e.problem?.code == "user_streams_full")
        }
    }
}

// MARK: - LiveSource

/// A clock the test moves by hand: `sleep` waits until `advance` passes its deadline.
actor FakeClock {
    private var now = Duration.zero
    private var sleepers: [(deadline: Duration, done: CheckedContinuation<Void, Never>)] = []

    nonisolated var sleep: LiveSource.Sleep {
        { [self] d in
            await withCheckedContinuation { c in Task { await self.add(d, c) } }
            try Task.checkCancellation()
        }
    }

    private func add(_ d: Duration, _ c: CheckedContinuation<Void, Never>) {
        sleepers.append((now + d, c))
    }

    var waiting: Int { sleepers.count }
    var elapsed: Duration { now }

    func advance(by d: Duration) {
        now += d
        let due = sleepers.filter { $0.deadline <= now }
        sleepers.removeAll { $0.deadline <= now }
        due.forEach { $0.done.resume() }
    }
}

/// Polls (yielding) until `cond` holds, failing after a while.
func eventually(_ what: String, _ cond: @Sendable () async -> Bool) async {
    for _ in 0..<2000 {
        if await cond() { return }
        try? await Task.sleep(for: .milliseconds(1))
    }
    Issue.record("never: \(what)")
}

actor FakeLiveAPI: LiveAPI {
    var info: TVInfo
    var tunes: [Result<RemoteTune, Error>]
    var beats: [Result<RemoteBeat, Error>]
    private(set) var tuneCount = 0
    private(set) var beatCount = 0
    private(set) var ended: [String] = []
    private(set) var devices: [String] = []

    init(info: TVInfo, tunes: [Result<RemoteTune, Error>] = [], beats: [Result<RemoteBeat, Error>] = []) {
        self.info = info
        self.tunes = tunes
        self.beats = beats
    }

    func tvInfo() async throws -> TVInfo { info }

    func tune(channel: String, device: String) async throws -> RemoteTune {
        tuneCount += 1
        devices.append(device)
        let r = tunes.count > 1 ? tunes.removeFirst() : tunes[0]
        return try r.get()
    }

    func heartbeat(session: String) async throws -> RemoteBeat {
        beatCount += 1
        let r = beats.count > 1 ? beats.removeFirst() : beats[0]
        return try r.get()
    }

    func endRemoteSession(_ id: String) async { ended.append(id) }

    nonisolated func channelStreamURL(_ channel: String, template: String) -> URL {
        URL(string: "http://home.test:8088" + template.replacingOccurrences(of: "{id}", with: channel))!
    }
}

@Suite struct LiveSourcePicksAndSwaps {
    static let relay = TVInfo(remote: .init(available: true, viaRelay: true, maxStreams: 2, tokenTTLMinutes: 60))
    static let cfURL = URL(string: "\(LiveFixtures.cf)/tok1/manifest/video.m3u8")!

    /// At home: the channel's playlist on the web port; no remote session.
    @Test func atHomeItPlaysTheLANPlaylist() async throws {
        let api = FakeLiveAPI(info: TVInfo())
        let src = LiveSource(api: api, channel: "c1", sleep: FakeClock().sleep)
        let (url, path) = try await src.open()
        #expect(path == .lan && url.absoluteString == "http://home.test:8088/stream/channel/c1/index.m3u8")
        #expect(await api.tuneCount == 0)
        await src.close()
        #expect(await api.ended.isEmpty)
    }

    /// Away: it tunes (waiting out a 202 on the clock), plays the signed URL, and at each
    /// heartbeat swaps in the new token without tuning again or changing what the player opened.
    @Test func awayItTunesAndSwapsTheTokenOnAHeartbeat() async throws {
        let clock = FakeClock()
        let api = FakeLiveAPI(info: Self.relay, tunes: [
            .success(RemoteTune(sessionID: "s1", state: "starting", retryAfter: 5)),
            .success(RemoteTune(sessionID: "s1", state: "live", hlsURL: Self.cfURL)),
        ], beats: [
            .success(RemoteBeat(state: "live")),
            .success(RemoteBeat(state: "live", token: "tok2")),
        ])
        let src = LiveSource(api: api, channel: "c1", device: "Maktaba on Apple TV", sleep: clock.sleep)
        let events = src.events
        let opening = Task { try await src.open() }
        await eventually("the 202 waits") { await clock.waiting == 1 }
        await clock.advance(by: .seconds(5))
        let (url, path) = try await opening.value
        #expect(path == .relay && url == Self.cfURL)
        #expect(await api.tuneCount == 2)
        #expect(await api.devices == ["Maktaba on Apple TV", "Maktaba on Apple TV"])
        #expect(await src.sessionID == "s1")
        #expect(await src.token == "tok1")

        // first beat: no new token, nothing changes
        await eventually("the heartbeat waits") { await clock.waiting == 1 }
        await clock.advance(by: .seconds(59))
        #expect(await api.beatCount == 0)
        await clock.advance(by: .seconds(1))
        await eventually("one beat") { await api.beatCount == 1 }
        #expect(await src.token == "tok1")

        // second beat: a new token
        await eventually("the heartbeat waits again") { await clock.waiting == 1 }
        await clock.advance(by: .seconds(60))
        await eventually("two beats") { await api.beatCount == 2 }
        await eventually("the new token") { await src.token == "tok2" }
        #expect(await clock.elapsed == .seconds(125))
        var it = events.makeAsyncIterator()
        #expect(await it.next() == .token(URL(string: "\(LiveFixtures.cf)/tok2/manifest/video.m3u8")!))
        // the same session and the same item: no new tune, the player's URL unchanged
        #expect(await api.tuneCount == 2)
        #expect(await src.openedURL == Self.cfURL)
        #expect(await src.sessionID == "s1")
        // every later request carries the new token; another host is left alone
        let seg = URL(string: "\(LiveFixtures.cf)/tok1/manifest/stream_t1.m3u8?x=1")!
        #expect(await src.rewrite(seg).absoluteString == "\(LiveFixtures.cf)/tok2/manifest/stream_t1.m3u8?x=1")
        let other = URL(string: "http://home.test:8088/stream/channel/c1/index.m3u8")!
        #expect(await src.rewrite(other) == other)

        await src.close()
        #expect(await api.ended == ["s1"])
        #expect(await it.next() == nil)
    }

    /// Away with remote viewing off: the home's reason, and nothing is tuned.
    @Test func awayWithoutRemoteViewingSaysWhy() async throws {
        let api = FakeLiveAPI(info: TVInfo(remote: .init(available: false, reason: "remote viewing is off", viaRelay: true)))
        let src = LiveSource(api: api, channel: "c1", sleep: FakeClock().sleep)
        await #expect(throws: LiveError.remoteUnavailable("remote viewing is off")) { _ = try await src.open() }
        #expect(await api.tuneCount == 0)
    }

    /// A stream that never starts: gives up at the timeout and gives the session back.
    @Test func aStreamThatNeverStartsTimesOut() async throws {
        let clock = FakeClock()
        let api = FakeLiveAPI(info: Self.relay, tunes: [.success(RemoteTune(sessionID: "s1", state: "starting", retryAfter: 30))])
        let src = LiveSource(api: api, channel: "c1", startTimeout: .seconds(60), sleep: clock.sleep)
        let opening = Task { try await src.open() }
        for _ in 0..<2 {
            await eventually("a wait") { await clock.waiting == 1 }
            await clock.advance(by: .seconds(30))
        }
        await #expect(throws: LiveError.startTimeout) { _ = try await opening.value }
        #expect(await api.tuneCount == 3)
        #expect(await api.ended == ["s1"])
    }

    /// A refused tune is the home's error, and a session it opened is given back.
    @Test func aRefusedTuneIsThrown() async throws {
        let refused = APIError.status(402, #"{"code":"budget_exhausted"}"#)
        let api = FakeLiveAPI(info: Self.relay, tunes: [.failure(refused)])
        let src = LiveSource(api: api, channel: "c1", sleep: FakeClock().sleep)
        await #expect(throws: refused) { _ = try await src.open() }
    }

    /// A blip on a heartbeat waits for the next; the home ending the session ends it here.
    @Test func theHomeEndingTheSessionEndsIt() async throws {
        let clock = FakeClock()
        let api = FakeLiveAPI(info: Self.relay, tunes: [.success(RemoteTune(sessionID: "s1", state: "live", hlsURL: Self.cfURL))],
                              beats: [.failure(APIError.status(502, "")),
                                      .failure(APIError.status(410, #"{"code":"session_ended"}"#))])
        let src = LiveSource(api: api, channel: "c1", sleep: clock.sleep)
        let events = src.events
        _ = try await src.open()
        await eventually("the heartbeat waits") { await clock.waiting == 1 }
        await clock.advance(by: .seconds(60))
        await eventually("one beat") { await api.beatCount == 1 }
        let stillOn = await src.ended
        #expect(!stillOn)
        await eventually("the heartbeat waits again") { await clock.waiting == 1 }
        await clock.advance(by: .seconds(60))
        await eventually("ended") { await src.ended }
        var it = events.makeAsyncIterator()
        #expect(await it.next() == .ended)
        #expect(await it.next() == nil)
        // the home ended it already: closing sends no DELETE
        await src.close()
        #expect(await api.ended.isEmpty)
        #expect(await api.beatCount == 2)
    }

    @Test(arguments: [
        ("\(LiveFixtures.cf)/tokA/manifest/video.m3u8", "tokA"),
        ("https://videodelivery.net/tokA/manifest/video.m3u8", nil),
        ("\(LiveFixtures.cf)/", nil),
    ] as [(String, String?)])
    func readsTheToken(url: String, want: String?) {
        #expect(LiveSource.token(of: URL(string: url)!) == want)
    }
}
