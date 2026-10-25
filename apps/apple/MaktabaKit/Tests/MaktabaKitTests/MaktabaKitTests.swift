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

    @Test func videoTitleFallsBackToTheFile() throws {
        let json = #"{"id":"v","library_id":"l","filename":"a.mp4","title":""}"#
        #expect(try JSONDecoder().decode(Video.self, from: Data(json.utf8)).displayTitle == "a.mp4")
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
