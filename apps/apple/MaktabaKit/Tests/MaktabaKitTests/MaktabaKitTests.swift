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
        #expect(r.value(forHTTPHeaderField: "X-Maktaba-Client") == "native")
        #expect(r.url?.absoluteString == "http://h:8088/api/videos?library=L&limit=60")
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
