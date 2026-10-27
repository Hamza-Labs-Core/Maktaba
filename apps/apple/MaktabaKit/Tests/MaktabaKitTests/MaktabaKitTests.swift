import Foundation
import Testing
@testable import MaktabaKit

@Suite struct HomeAddress {
    @Test(arguments: [
        ("192.168.50.177:8088", "http://192.168.50.177:8088"),
        ("mac.local:8088/library", "http://mac.local:8088"),
        ("home.example.com", "https://home.example.com"),
        ("https://home.example.com/x?y=1", "https://home.example.com"),
        ("  http://10.0.0.5:8088  ", "http://10.0.0.5:8088"),
    ])
    func normalizes(input: String, want: String) {
        #expect(Home.normalize(input)?.absoluteString == want)
    }

    @Test(arguments: ["", "   ", "ftp://x", "http://"])
    func refuses(input: String) {
        #expect(Home.normalize(input) == nil)
    }
}

@Suite struct Decoding {
    @Test func streamSessionResolvesAgainstTheHome() throws {
        let json = #"{"session_id":"s1","mode":"transcode","manifest_url":"/stream/s1/manifest.m3u8?sig=abc","expires_at":"2026-10-25T00:00:00Z"}"#
        let s = try JSONDecoder().decode(StreamSession.self, from: Data(json.utf8))
        let url = s.playbackURL(home: URL(string: "http://192.168.50.177:8088")!)
        #expect(url?.absoluteString == "http://192.168.50.177:8088/stream/s1/manifest.m3u8?sig=abc")
    }

    @Test func directURLWins() throws {
        let json = #"{"session_id":"s1","mode":"direct","manifest_url":"/stream/s1/manifest.m3u8","direct_url":"/stream/direct/s1?sig=x"}"#
        let s = try JSONDecoder().decode(StreamSession.self, from: Data(json.utf8))
        #expect(s.playbackURL(home: URL(string: "http://h:8088")!)?.path() == "/stream/direct/s1")
    }

    @Test func streamURLsStayUnderTheHomesBasePath() throws {
        let home = URL(string: "https://example.com/maktaba")!
        let root = try JSONDecoder().decode(StreamSession.self, from: Data(#"{"session_id":"s","mode":"transcode","manifest_url":"/stream/s/master.m3u8?sig=1"}"#.utf8))
        #expect(root.playbackURL(home: home)?.absoluteString == "https://example.com/maktaba/stream/s/master.m3u8?sig=1")
        #expect(root.playbackURL(home: URL(string: "https://example.com/maktaba/")!)?.absoluteString
            == "https://example.com/maktaba/stream/s/master.m3u8?sig=1")
        let under = try JSONDecoder().decode(StreamSession.self, from: Data(#"{"session_id":"s","mode":"direct","direct_url":"/maktaba/stream/s/a.mp4"}"#.utf8))
        #expect(under.playbackURL(home: home)?.absoluteString == "https://example.com/maktaba/stream/s/a.mp4", "a path already under it is kept")
        #expect(root.playbackURL(home: URL(string: "http://h:8088")!)?.absoluteString == "http://h:8088/stream/s/master.m3u8?sig=1")
    }

    @Test func meDecodesAsUser() throws {
        let json = #"{"user_id":"u1","is_admin":true,"libraries":[],"id":"u1","username":"mahmoud","user":{"id":"u1","username":"mahmoud","is_admin":true}}"#
        let u = try JSONDecoder().decode(User.self, from: Data(json.utf8))
        #expect(u.username == "mahmoud" && u.isAdmin)
    }

    @Test func meCarriesTheHomesName() throws {
        let json = #"{"user_id":"u1","is_admin":false,"libraries":[],"id":"u1","username":"sara","home_name":" Living room ","user":{"id":"u1","username":"sara","is_admin":false}}"#
        let me = try JSONDecoder().decode(Me.self, from: Data(json.utf8))
        #expect(me.user.username == "sara" && !me.user.isAdmin)
        #expect(me.homeName == "Living room")
    }

    /// An older home (or a machine answering for it) does not say its name.
    @Test(arguments: [#"{"id":"u1","username":"sara","is_admin":false}"#,
                      #"{"id":"u1","username":"sara","is_admin":false,"home_name":"  "}"#])
    func meWithoutAName(json: String) throws {
        #expect(try JSONDecoder().decode(Me.self, from: Data(json.utf8)).homeName == nil)
    }

    @Test func videoTitleFallsBackToTheFile() throws {
        let json = #"{"id":"v","library_id":"l","filename":"a.mp4","title":""}"#
        #expect(try JSONDecoder().decode(Video.self, from: Data(json.utf8)).displayTitle == "a.mp4")
    }
}

@MainActor @Suite struct HomeName {
    /// The home's name replaces the address in the homes this app knows, and stays there.
    @Test func keepsTheNameTheHomeSays() throws {
        let suite = "maktabakit.test.\(UUID().uuidString)"
        let defaults = try #require(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        let url = URL(string: "http://192.168.50.177:8088")!
        let session = AppSession(app: "test", defaults: defaults)
        session.use(Home(url: url, name: "192.168.50.177"))
        session.named(nil) // the home did not say
        #expect(session.current?.name == "192.168.50.177")
        session.named("Living room")
        #expect(session.current?.name == "Living room")
        #expect(session.homes.map(\.name) == ["Living room"])
        // picking the home again by its old value adds no second one
        session.use(Home(url: url, name: "192.168.50.177"))
        #expect(session.homes.count == 1)
        #expect(AppSession(app: "test", defaults: defaults).homes.map(\.name) == ["Living room"])
    }
}

@Suite struct Requests {
    @Test func nativeHeaderAndQuery() async {
        let c = APIClient(home: URL(string: "http://h:8088")!, tokens: TokenStore(app: "test", home: URL(string: "http://h:8088")!))
        let r = await c.request("GET", "/api/videos", query: ["library": "L", "limit": "60"])
        #expect(r.value(forHTTPHeaderField: "X-Maktaba-Client") == ClientHeader.value)
        #expect(r.url?.absoluteString == "http://h:8088/api/videos?library=L&limit=60")
    }

    /// Every app names itself (Epic 44): app, version, platform, device.
    @Test func clientHeaderNamesTheApp() {
        #if os(macOS)
        let tail = "(macos; desktop)"
        #expect(ClientHeader.make(bundleID: "com.hamzalabs.maktaba.player.mac", version: "1.3.0") == "player/1.3.0 \(tail)")
        #expect(ClientHeader.make(bundleID: "com.hamzalabs.maktaba.remote", version: "1.0") == "remote/1.0 \(tail)")
        #expect(ClientHeader.make(bundleID: "com.hamzalabs.maktaba.tv", version: "") == "tv/dev \(tail)")
        #elseif os(iOS)
        #expect(ClientHeader.make(bundleID: "com.hamzalabs.maktaba.player", version: "1.3.0") == "player/1.3.0 (ios; phone)")
        #expect(ClientHeader.make(bundleID: "com.hamzalabs.maktaba.remote.tablet", version: "1.0") == "remote/1.0 (ipados; tablet)")
        #endif
        #expect(ClientHeader.value.range(of: #"^(player|remote|tv)/\S+ \([a-z]+; [a-z]+\)$"#, options: .regularExpression) != nil)
    }
}

@Suite struct VideoList {
    /// The home accepts only updated_at, created_at or title (a "-created_at" answered 400
    /// and the Player showed nothing after signing in).
    @Test func asksWithASortTheHomeKnows() {
        let q = APIClient.videoListQuery(library: "L", query: "", cursor: nil)
        #expect(["updated_at", "created_at", "title"].contains(q["sort"] ?? ""))
        #expect(q["library"] == "L" && q["q"] == nil && q["limit"] == "60")
    }
}

@Suite struct Playback {
    @Test func readsAJWTsExpiry() {
        // {"sub":"u1","exp":1793000000}, base64url without padding
        let payload = Data(#"{"sub":"u1","exp":1793000000}"#.utf8).base64EncodedString()
            .replacingOccurrences(of: "+", with: "-").replacingOccurrences(of: "/", with: "_")
            .replacingOccurrences(of: "=", with: "")
        #expect(APIClient.expiry(of: "h.\(payload).s") == Date(timeIntervalSince1970: 1793000000))
        #expect(APIClient.expiry(of: "not-a-jwt") == nil)
    }

    @Test func findsAPlaylistsFirstURI() {
        let master = "#EXTM3U\n#EXT-X-VERSION:6\n#EXT-X-STREAM-INF:BANDWIDTH=1460800,RESOLUTION=854x480\nv0/index.m3u8?sig=a\nv1/index.m3u8?sig=a\n"
        #expect(APIClient.firstURI(inPlaylist: master) == "v0/index.m3u8?sig=a")
        #expect(APIClient.firstURI(inPlaylist: "#EXTM3U\r\n#EXTINF:6,\r\n  seg-0.ts  \r\n") == "seg-0.ts")
        #expect(APIClient.firstURI(inPlaylist: "#EXTM3U\n") == nil)
    }

    @Test func readsTheSignedURLsEnd() throws {
        for at in ["2026-10-25T20:30:00Z", "2026-10-25T20:30:00.123456Z"] {
            let json = #"{"session_id":"s1","mode":"transcode","manifest_url":"/m","expires_at":"\#(at)"}"#
            let s = try JSONDecoder().decode(StreamSession.self, from: Data(json.utf8))
            let want = Date(timeIntervalSince1970: 1792960200)
            #expect(abs((s.expires ?? .distantPast).timeIntervalSince(want)) < 1)
        }
    }
}

@Suite struct Proxy {
    let proxy = try! StreamProxy(client: APIClient(home: URL(string: "http://192.168.50.177:8088")!,
                                                   tokens: TokenStore(app: "test.proxy", home: URL(string: "http://192.168.50.177:8088")!)))

    @Test func servesOnlyItsSecretsStreamPaths() {
        let s = proxy.secret
        #expect(proxy.remote(forTarget: "/\(s)/stream/abc/manifest.m3u8?sig=x")?.absoluteString
            == "http://192.168.50.177:8088/stream/abc/manifest.m3u8?sig=x")
        #expect(proxy.remote(forTarget: "/\(s)/stream/abc/v0/seg-1.ts?sig=x")?.path() == "/stream/abc/v0/seg-1.ts")
        for refused in [
            "/stream/abc/manifest.m3u8",            // no secret: another app on the device
            "/wrong/stream/abc/manifest.m3u8",
            "/\(s)/api/videos",                      // not a stream
            "/\(s)/stream/../api/auth/me",            // climbing out
            "/\(s)/stream/%2E%2E/api/auth/me",
            "/\(s)/streamx/abc",
        ] {
            #expect(proxy.remote(forTarget: refused) == nil, "\(refused)")
        }
    }

    @Test func parsesARequestHead() {
        let r = Request(Data("GET /a/stream/x?sig=1 HTTP/1.1\r\nHost: 127.0.0.1\r\nRange: bytes=0-1\r\n\r\n".utf8))
        #expect(r?.method == "GET" && r?.target == "/a/stream/x?sig=1" && r?.headers["range"] == "bytes=0-1")
        #expect(Request(Data("nonsense\r\n\r\n".utf8)) == nil)
    }
}

@Suite struct ProxyLoopback {
    /// A request the proxy cannot forward (no token here) still gets an answer, not a reset.
    @Test func answersOnTheLoopback() async throws {
        let home = URL(string: "http://127.0.0.1:9")!
        let proxy = try StreamProxy(client: APIClient(home: home, tokens: TokenStore(app: "test.proxy.loop", home: home)))
        try await proxy.start()
        defer { proxy.stop() }
        let local = try #require(proxy.local(URL(string: "http://127.0.0.1:9/stream/abc/manifest.m3u8?sig=x")!))
        let (_, response) = try await URLSession.shared.data(from: local)
        #expect((response as? HTTPURLResponse)?.statusCode == 502)
        let (_, other) = try await URLSession.shared.data(from: URL(string: "http://127.0.0.1:\(proxy.port!)/stream/abc/manifest.m3u8")!)
        #expect((other as? HTTPURLResponse)?.statusCode == 404)
    }
}

@Suite struct TVHome {
    @Test func railsKeepWhereThePersonStopped() throws {
        let json = #"{"rails":[{"id":"r1","title":"x","reason_kind":"continue_watching","items":[{"video_id":"v1","position_sec":30,"duration_sec":120},{"video_id":"v2"}]}],"cache_hit":false}"#
        let rails = try JSONDecoder().decode(Recommendations.self, from: Data(json.utf8)).rails
        #expect(rails.first?.reasonKind == "continue_watching")
        #expect(rails.first?.items.map(\.progress) == [0.25, 0])
    }

    @Test func aVideoSaysWhereThePersonStopped() throws {
        let json = #"{"id":"v","library_id":"l","filename":"a.mp4","playback_state":{"position_sec":754.2,"completed":false,"updated_at":"2026-10-26T00:00:00Z"}}"#
        #expect(try JSONDecoder().decode(Video.self, from: Data(json.utf8)).playbackState?.positionSec == 754.2)
    }

    @Test func aPairingCodeAndItsPage() throws {
        let json = #"{"code":"ABCD-1234","device_code":"secret","expires_at":"2026-10-26T12:10:00Z","verify_path":"/pair?code=ABCD-1234","interval":2}"#
        let p = try JSONDecoder().decode(DevicePairing.self, from: Data(json.utf8))
        #expect(p.code == "ABCD-1234" && p.interval == 2)
        #expect(p.verifyURL(home: URL(string: "http://192.168.50.177:8088")!)?.absoluteString == "http://192.168.50.177:8088/pair?code=ABCD-1234")
    }

    @Test(arguments: [
        ("192.168.50.177", ["web": "8088", "role": "home"], "http://192.168.50.177:8088"),
        ("MahmoudacStudio.local.", [:], "http://MahmoudacStudio.local:8080"),
        ("Mahmouds-Mac-Studio.local.local.", ["web": "8088"], "http://Mahmouds-Mac-Studio.local:8088"),
        ("box.LOCAL.local", [:], "http://box.LOCAL:8080"),
        ("10.0.0.2", ["web": "443", "tls": "1"], "https://10.0.0.2:443"),
    ])
    func anAnnouncedHomesAddress(host: String, txt: [String: String], want: String) {
        #expect(HomeBrowser.homeURL(host: host, apiPort: 8080, txt: txt)?.absoluteString == want)
    }
}

/// Answers every request of a session from `StubProtocol.answer`.
final class StubProtocol: URLProtocol, @unchecked Sendable {
    nonisolated(unsafe) static var answer: (Int, String) = (200, "{}")
    nonisolated(unsafe) static var lastBody: Data?
    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() {
        Self.lastBody = request.httpBody ?? request.httpBodyStream.map { s in
            s.open(); defer { s.close() }
            var d = Data(); var buf = [UInt8](repeating: 0, count: 4096)
            while s.hasBytesAvailable { let n = s.read(&buf, maxLength: buf.count); if n <= 0 { break }; d.append(buf, count: n) }
            return d
        }
        let (code, body) = Self.answer
        client?.urlProtocol(self, didReceive: HTTPURLResponse(url: request.url!, statusCode: code, httpVersion: nil, headerFields: nil)!, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(body.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }
    override func stopLoading() {}
}

@Suite(.serialized) struct TVSignInByCode {
    let home = URL(string: "http://stub.local:8088")!
    func client(_ tokens: TokenStore) -> APIClient {
        let cfg = URLSessionConfiguration.ephemeral
        cfg.protocolClasses = [StubProtocol.self]
        return APIClient(home: home, tokens: tokens, session: URLSession(configuration: cfg))
    }
    let pairing = try! JSONDecoder().decode(DevicePairing.self, from: Data(#"{"code":"ABCD-1234","device_code":"dc","expires_at":"2026-10-26T12:10:00Z","verify_path":"/pair?code=ABCD-1234","interval":2}"#.utf8))

    @Test func aHomeWithoutCodesSignsInByPassword() async throws {
        StubProtocol.answer = (401, #"{"status":401}"#)
        #expect(try await client(TokenStore(app: "test.pair.old", home: home)).startDevicePairing() == nil)
    }

    @Test func waitsThenKeepsTheApproversTokens() async throws {
        let tokens = TokenStore(app: "test.pair.ok", home: home)
        tokens.clear()
        let c = client(tokens)
        StubProtocol.answer = (202, #"{"status":"pending"}"#)
        #expect(try await c.collectDevicePairing(pairing, label: "Living room") == .waiting)
        let sent = try JSONSerialization.jsonObject(with: StubProtocol.lastBody ?? Data()) as? [String: String]
        #expect(sent == ["device_code": "dc", "device_label": "Living room"])
        StubProtocol.answer = (200, #"{"access_token":"a.b.c","refresh_token":"r","user_id":"u1"}"#)
        #expect(try await c.collectDevicePairing(pairing, label: "Living room") == .approved)
        #expect(tokens.access == "a.b.c" && tokens.refresh == "r")
        tokens.clear()
    }

    @Test(arguments: [409, 410])
    func anExpiredCodeAsksForANewOne(code: Int) async throws {
        StubProtocol.answer = (code, #"{"status":\#(code)}"#)
        #expect(try await client(TokenStore(app: "test.pair.gone", home: home)).collectDevicePairing(pairing, label: "") == .expired)
    }
}
