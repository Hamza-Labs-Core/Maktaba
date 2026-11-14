import Foundation
import Testing
@testable import MaktabaKit

/// Epic 58 story 58.06 on the Apple Player (T03): what the sender hands Maktaba's receiver,
/// the cast grant, the namespace's messages and following the TV.
@Suite struct CastCustomDataTests {
    /// shared/cast/load-vectors.json, found above this file.
    static func vectorsURL() throws -> URL {
        var dir = URL(fileURLWithPath: #filePath).deletingLastPathComponent()
        while dir.path != "/" {
            let f = dir.appending(path: "shared/cast/load-vectors.json")
            if FileManager.default.fileExists(atPath: f.path) { return f }
            dir = dir.deletingLastPathComponent()
        }
        throw CocoaError(.fileNoSuchFile)
    }

    // T03: every shared vector, the same custom data as the Kotlin and TypeScript senders
    @Test func everyVectorBuildsItsCustomData() throws {
        let data = try Data(contentsOf: try Self.vectorsURL())
        let root = try #require(try JSONSerialization.jsonObject(with: data) as? [String: Any])
        let cases = try #require(root["cases"] as? [[String: Any]])
        #expect(cases.count >= 6)
        var nulls = 0
        for c in cases {
            let name = c["name"] as? String ?? ""
            let item: CastItem = if let q = c["queue_id"] as? String {
                .queue(q, position: c["queue_pos"] as? Int)
            } else {
                .video(c["video_id"] as? String ?? "")
            }
            let got = CastCustomData.build(
                senderBase: (c["sender_base"] as? String).flatMap(URL.init(string:)),
                discoveryURLs: c["discovery_urls"] as? [String] ?? [],
                grant: c["grant"] as? String ?? "", item: item,
                startSec: (c["start_sec"] as? NSNumber)?.doubleValue ?? 0,
                lang: c["lang"] as? String ?? "", apiLevel: c["api_level"] as? Int ?? 0)
            if let expect = c["expect"] as? [String: Any] {
                let built = try #require(got, "\(name): custom data")
                #expect(NSDictionary(dictionary: built).isEqual(to: expect), "\(name): \(built) != \(expect)")
            } else {
                #expect(got == nil, "\(name): no address the TV can reach")
                nulls += 1
            }
        }
        #expect(nulls >= 1)
    }

    // T03: the home address is never a .local name or a bare IP, and never plain HTTP
    @Test func theHomeIsNeverLocalOrABareIP() throws {
        let bad = ["http://h1.relay.maktaba.tv", "https://maktaba.local:8444", "https://MAKTABA.LOCAL.", "https://localhost",
                   "https://192.168.1.20:8444", "https://[fd00::20]:8444", "https://10.0.0.1", "https://local"]
        for u in bad { #expect(CastCustomData.normalized(u) == nil, "\(u)") }
        #expect(CastCustomData.normalized("https://H1.Relay.Maktaba.tv/") == "https://h1.relay.maktaba.tv")
        #expect(CastCustomData.normalized("https://example.org/maktaba/?x=1") == "https://example.org/maktaba")
        #expect(!CastCustomData.castable(senderBase: URL(string: "http://192.168.1.20:8088"), discoveryURLs: bad))
        let video = try #require(CastCustomData.build(senderBase: nil, discoveryURLs: bad + ["https://h1.relay.maktaba.tv"],
                                                      grant: "cast_G", item: .video("v1"), startSec: 10, lang: "ar", apiLevel: 1))
        let queue = try #require(CastCustomData.build(senderBase: nil, discoveryURLs: ["https://h1.relay.maktaba.tv"],
                                                      grant: "cast_G", item: .queue("q1", position: 2), startSec: 0, lang: "en", apiLevel: 1))
        for d in [video, queue] {
            #expect(d["home"] as? String == "https://h1.relay.maktaba.tv")
            #expect(Set(d.keys).isSubset(of: ["home", "urls", "grant", "video_id", "queue_id", "queue_pos", "start_sec", "lang", "api_level"]))
        }
        #expect(video["video_id"] as? String == "v1" && video["queue_id"] == nil)
        #expect(queue["queue_id"] as? String == "q1" && queue["queue_pos"] as? Int == 2 && queue["video_id"] == nil)
    }

    @Test func theLanguageIsEnglishOrArabic() {
        #expect(CastCustomData.language("ar") == "ar")
        #expect(CastCustomData.language("ar-SA") == "ar")
        #expect(CastCustomData.language("en-GB") == "en")
        #expect(CastCustomData.language("fr") == "en")
    }
}

@Suite struct CastConfigTests {
    // an empty receiver id: Cast hidden, no Bonjour service asked for, the SDK never started
    @Test func anEmptyIDHidesCast() {
        #expect(!CastConfig(receiverAppID: "").isEnabled)
        #expect(!CastConfig(receiverAppID: "  ").isEnabled)
        #expect(!CastConfig(receiverAppID: "$(MAKTABA_CAST_APP_ID)").isEnabled, "an unexpanded build setting")
        #expect(CastConfig(receiverAppID: "").bonjourServices.isEmpty)
        #expect(!CastConfig(bundle: .main).isEnabled, "the test runner's Info.plist names no receiver")
    }

    @Test func anIDNamesItsBonjourServices() {
        let c = CastConfig(receiverAppID: "ABCD1234")
        #expect(c.isEnabled)
        #expect(c.bonjourServices == ["_googlecast._tcp", "_ABCD1234._googlecast._tcp"])
    }

    // the build reads the id from shared/cast/app.json: the namespace is the receiver's
    @Test func theNamespaceIsTheSharedOne() throws {
        let app = try CastCustomDataTests.vectorsURL().deletingLastPathComponent().appending(path: "app.json")
        let o = try #require(try JSONSerialization.jsonObject(with: Data(contentsOf: app)) as? [String: Any])
        #expect(o["namespace"] as? String == CastConfig.namespace)
        #expect(o["receiver_app_id"] is String)
    }
}

@Suite struct CastMessageTests {
    // the grant refresh: sender → receiver on the custom namespace
    @Test func theGrantRefreshMessage() throws {
        let text = CastMessage.grant("cast_NEW").text
        let o = try #require(try JSONSerialization.jsonObject(with: Data(text.utf8)) as? [String: String])
        #expect(o == ["type": "grant", "grant": "cast_NEW"])
        #expect(CastMessage(text: text) == .grant("cast_NEW"))
    }

    @Test func theReceiversMessagesAreRead() {
        #expect(CastMessage(text: #"{"type":"state","state":"ready","remote":true,"video_id":"v2","queue_pos":1}"#)
            == .state("ready", videoID: "v2", queuePos: 1))
        #expect(CastMessage(text: #"{"type":"error","error":"grant"}"#) == .error("grant"))
        #expect(CastMessage(text: #"{"type":"other"}"#) == nil)
        #expect(CastMessage(text: "not json") == nil)
    }

    // a new grant 30 minutes before the one in use ends
    @Test func theNextGrantIsMintedHalfAnHourEarly() throws {
        let g = try JSONDecoder().decode(CastGrant.self, from: Data(
            #"{"grant":"cast_x","scope":"cast","expires_at":"2026-11-14T22:41:07Z","video_id":"v1"}"#.utf8))
        let end = try #require(g.expiresAt)
        #expect(g.refreshAt() == end.addingTimeInterval(-1800))
        #expect(g.videoID == "v1" && g.queueID == nil)
        #expect(CastGrant(grant: "g", expiresAt: nil).refreshAt() == nil)
    }

    // the phone stops its own progress posting while the TV plays (the receiver reports)
    @Test func thePhoneDoesNotReportWhileCasting() {
        #expect(!CastProgress.phoneReports(casting: true))
        #expect(CastProgress.phoneReports(casting: false))
    }

    // after a lock, the phone follows what the TV says it plays
    @Test func theReceiversStatusIsFollowed() {
        let s = CastReceiverStatus(state: .playing, position: 812.4, duration: 3120,
                                   customData: ["video_id": "v3", "queue_id": "q1", "queue_pos": NSNumber(value: 2), "lang": "ar"])
        #expect(s.videoID == "v3" && s.queueID == "q1" && s.queuePos == 2)
        #expect(s.isPlaying)
        let none = CastReceiverStatus(state: .idle, position: -.infinity, duration: .nan, customData: nil)
        #expect(none.position == 0 && none.duration == 0 && none.videoID == nil && !none.isPlaying)
    }
}

/// Answers `POST /api/cast/grants` and `GET /api/discovery/home` as the home does, and keeps
/// each request.
final class CastStub: URLProtocol, @unchecked Sendable {
    nonisolated(unsafe) static var answer: (Int, String) = (201, "{}")
    nonisolated(unsafe) static var seen: [URLRequest] = []
    static let lock = NSLock()
    static let home = URL(string: "http://cast.test")!

    static func client() -> APIClient {
        let secrets = FakeSecrets(["http://cast.test#access": "person-token", "http://cast.test#refresh": "r1"])
        let tokens = TokenStore(service: "test.\(UUID().uuidString)", home: home, primary: secrets, legacy: nil)
        let c = URLSessionConfiguration.ephemeral
        c.protocolClasses = [CastStub.self]
        return APIClient(home: home, tokens: tokens, session: URLSession(configuration: c))
    }

    static func on(_ status: Int, _ body: String) {
        lock.withLock {
            answer = (status, body)
            seen = []
        }
    }

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func stopLoading() {}

    override func startLoading() {
        var r = request
        if r.httpBody == nil, let s = request.httpBodyStream {
            s.open()
            var data = Data(), buf = [UInt8](repeating: 0, count: 4096)
            while s.hasBytesAvailable {
                let n = s.read(&buf, maxLength: buf.count)
                if n <= 0 { break }
                data.append(buf, count: n)
            }
            s.close()
            r.httpBody = data
        }
        let (status, body) = Self.lock.withLock {
            Self.seen.append(r)
            return Self.answer
        }
        let isProblem = body.hasPrefix("{")
        let resp = HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: "HTTP/1.1",
                                   headerFields: ["Content-Type": isProblem ? (status >= 400 ? "application/problem+json" : "application/json") : "text/plain"])!
        client?.urlProtocol(self, didReceive: resp, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(body.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }
}

@Suite(.serialized) struct CastGrantClientTests {
    @Test func aGrantIsMintedWithThePersonsOwnToken() async throws {
        CastStub.on(201, #"{"grant":"cast_abc","scope":"cast","expires_at":"2026-11-14T22:41:07Z","video_id":"v1"}"#)
        let m = try await CastStub.client().mintCastGrant(.video("v1"))
        guard case let .granted(g) = m else { Issue.record("not granted: \(m)"); return }
        #expect(g.grant == "cast_abc" && g.scope == "cast" && g.videoID == "v1")
        let r = try #require(CastStub.lock.withLock { CastStub.seen.last })
        #expect(r.httpMethod == "POST" && r.url?.path() == "/api/cast/grants")
        #expect(r.value(forHTTPHeaderField: "Authorization") == "Bearer person-token")
        let body = try #require(try JSONSerialization.jsonObject(with: r.httpBody ?? Data()) as? [String: String])
        #expect(body == ["video_id": "v1"])
    }

    @Test func aQueueIsMintedByItsID() async throws {
        CastStub.on(201, #"{"grant":"cast_q","scope":"cast","expires_at":"2026-11-14T22:41:07Z","queue_id":"q1"}"#)
        _ = try await CastStub.client().mintCastGrant(.queue("q1", position: 3))
        let r = try #require(CastStub.lock.withLock { CastStub.seen.last })
        let body = try #require(try JSONSerialization.jsonObject(with: r.httpBody ?? Data()) as? [String: String])
        #expect(body == ["queue_id": "q1"], "the position goes in the custom data, not the grant")
    }

    // a router's plain 404 for the route itself: a home from before the grant
    @Test func aHomeWithoutTheRouteIsOld() async throws {
        CastStub.on(404, "404 page not found\n")
        #expect(try await CastStub.client().mintCastGrant(.video("v1")) == .oldHome)
        CastStub.on(405, "")
        #expect(try await CastStub.client().mintCastGrant(.video("v1")) == .oldHome)
    }

    // the home's refusals: shown, never cast
    @Test func theHomesRefusalsAreMapped() async throws {
        let cases: [(Int, String, CastMint)] = [
            (404, #"{"title":"not found","status":404}"#, .refused(.notFound)),
            (403, #"{"title":"forbidden","status":403,"code":"cast_via_cloud"}"#, .refused(.viaCloud)),
            (403, #"{"title":"forbidden","status":403,"code":"cast_needs_person"}"#, .refused(.needsPerson)),
            (403, #"{"type":"https://maktaba.dev/problems/access-denied","title":"access denied","status":403}"#, .refused(.denied)),
        ]
        for (status, body, want) in cases {
            CastStub.on(status, body)
            #expect(try await CastStub.client().mintCastGrant(.video("v1")) == want, "\(status) \(body)")
        }
    }

    @Test func aServerFailureThrows() async {
        CastStub.on(500, #"{"title":"internal","status":500}"#)
        await #expect(throws: APIError.self) { try await CastStub.client().mintCastGrant(.video("v1")) }
    }

    @Test func discoveryIsReadAgainAtCastTime() async throws {
        CastStub.on(200, #"{"id":"h1","urls":["http://192.168.1.20:8088","https://h1.relay.maktaba.tv"],"api_level":2}"#)
        let d = try await CastStub.client().discovery()
        #expect(d.urls == ["http://192.168.1.20:8088", "https://h1.relay.maktaba.tv"])
        #expect(d.compat.apiLevel == 2)
    }
}
