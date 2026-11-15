import Foundation
import Testing
@testable import MaktabaKit

// Story 50.27: Watch a broadcast in MaktabaKit. T03: the guest pass store (save, list, remove,
// expire) and what the Keychain holds; the guest states against the shared vectors (the same
// as the watch pages'); what a guest types; the edge client; the proxy's token in the path.

final class GuestClock: @unchecked Sendable {
    private let lock = NSLock()
    private var t: Date
    init(_ t: Date) { self.t = t }
    var now: Date { lock.withLock { t } }
    func advance(_ s: TimeInterval) { lock.withLock { t = t.addingTimeInterval(s) } }
}

/// A credential shaped as the edge's (`mkbg1.<payload>.<sig>`), its payload claiming `exp`.
func credential(_ prefix: String = "mkbg1.", exp: Int) -> String {
    let body = Data(#"{"pub":"pub-1","exp":\#(exp)}"#.utf8).base64EncodedString()
        .replacingOccurrences(of: "+", with: "-").replacingOccurrences(of: "/", with: "_").replacingOccurrences(of: "=", with: "")
    return "\(prefix)\(body).sig"
}

@Suite struct GuestPassStoreTests {
    let start = Date(timeIntervalSince1970: 1_800_000_000)

    func pass(_ pub: String, days: Double, name: String = "") -> GuestPass {
        GuestPass(publisher: pub, name: name, credential: "mkbg1.\(pub).sig", kind: .pass, channels: ["ch1"],
                  expiresAt: start.addingTimeInterval(days * 86400))
    }

    @Test func savesListsNewestFirstAndKeepsOneItem() throws {
        let secrets = FakeSecrets()
        let clock = GuestClock(start)
        let store = GuestPassStore(secrets: secrets, now: { clock.now })
        store.save(pass("pub-a", days: 30, name: "Al-Noor"))
        clock.advance(10)
        store.save(pass("pub-b", days: 30))
        #expect(store.list().map(\.publisher) == ["pub-b", "pub-a"])
        // the Keychain holds one item, the list, with each credential
        #expect(Array(secrets.values.keys) == [GuestPassStore.account])
        let stored = try #require(secrets.values[GuestPassStore.account])
        let json = try #require(try JSONSerialization.jsonObject(with: Data(stored.utf8)) as? [[String: Any]])
        #expect(json.map { $0["publisher"] as? String } == ["pub-b", "pub-a"])
        #expect(json.map { $0["credential"] as? String } == ["mkbg1.pub-b.sig", "mkbg1.pub-a.sig"])
        #expect(json[1]["name"] as? String == "Al-Noor")
    }

    @Test func savingAPublisherAgainReplacesItAndKeepsItsName() {
        let secrets = FakeSecrets()
        let store = GuestPassStore(secrets: secrets, now: { self.start })
        store.save(pass("pub-a", days: 30, name: "Al-Noor"))
        store.save(pass("pub-b", days: 30))
        var again = pass("pub-a", days: 20)
        again.credential = "mkbg1.new.sig"
        store.save(again)
        let list = store.list()
        #expect(list.map(\.publisher) == ["pub-a", "pub-b"])
        #expect(list[0].credential == "mkbg1.new.sig" && list[0].name == "Al-Noor")
    }

    @Test func removeTakesThePassOffTheDevice() {
        let secrets = FakeSecrets()
        let store = GuestPassStore(secrets: secrets, now: { self.start })
        store.save(pass("pub-a", days: 30))
        store.save(pass("pub-b", days: 30))
        store.remove("pub-a")
        #expect(store.list().map(\.publisher) == ["pub-b"])
        #expect(secrets.values[GuestPassStore.account]?.contains("pub-a") == false)
        store.remove("pub-b")
        #expect(store.list().isEmpty)
        #expect(secrets.values.isEmpty, "no empty item is left in the Keychain")
    }

    @Test func anExpiredPassIsDroppedWhenTheListIsRead() {
        let secrets = FakeSecrets()
        let clock = GuestClock(start)
        let store = GuestPassStore(secrets: secrets, now: { clock.now })
        store.save(pass("pub-short", days: 0.5))
        store.save(pass("pub-long", days: 30))
        #expect(store.list().count == 2)
        clock.advance(86400)
        #expect(store.list().map(\.publisher) == ["pub-long"])
        #expect(secrets.values[GuestPassStore.account]?.contains("pub-short") == false, "and is gone from the Keychain")
        #expect(store.pass(for: "pub-short") == nil)
    }

    @Test func aRenewedPassTakesItsNewExpiry() {
        let secrets = FakeSecrets()
        let store = GuestPassStore(secrets: secrets, now: { self.start })
        store.save(pass("pub-a", days: 10))
        let later = Int(start.timeIntervalSince1970) + 30 * 86400
        store.renew("pub-a", credential: credential(exp: later))
        let p = store.pass(for: "pub-a")
        #expect(p?.credential == credential(exp: later))
        #expect(p?.expiresAt == Date(timeIntervalSince1970: TimeInterval(later)))
    }

    @Test func anUnreadableItemIsAnEmptyList() {
        let store = GuestPassStore(secrets: FakeSecrets([GuestPassStore.account: "not json"]), now: { self.start })
        #expect(store.list().isEmpty)
    }

    @Test func aCredentialSaysItsExpiry() {
        #expect(GuestPass.claimedExpiry(credential(exp: 1_900_000_000)) == Date(timeIntervalSince1970: 1_900_000_000))
        #expect(GuestPass.claimedExpiry(credential("mkbo1.", exp: 5)) == Date(timeIntervalSince1970: 5))
        #expect(GuestPass.claimedExpiry("nonsense") == nil)
    }
}

@Suite struct GuestStates {
    static func vectorsURL() throws -> URL {
        var dir = URL(fileURLWithPath: #filePath).deletingLastPathComponent()
        while dir.path != "/" {
            let f = dir.appending(path: "shared/broadcast/viewer-states.json")
            if FileManager.default.fileExists(atPath: f.path) { return f }
            dir = dir.deletingLastPathComponent()
        }
        throw CocoaError(.fileNoSuchFile)
    }

    /// T01/T08 for the apps: every case of the shared vectors, and its words in both languages.
    @Test func everyVectorMapsAsTheWatchPagesDo() throws {
        let root = try #require(try JSONSerialization.jsonObject(with: Data(contentsOf: Self.vectorsURL())) as? [String: Any])
        let cases = try #require(root["cases"] as? [[String: Any]])
        #expect(cases.count >= 20)
        for c in cases {
            let body = c["body"] as? [String: Any]
            let s = GuestViewerState(status: c["status"] as! Int, channelState: body?["channel_state"] as? String,
                                     error: body?["error"] as? String)
            let label = "\(c["status"]!) \(String(describing: body))"
            #expect(s.state.rawValue == c["state"] as? String, "\(label)")
            #expect(s.key == c["key"] as? String, "\(label)")
            #expect(s.plays == c["plays"] as? Bool, "\(label)")
            #expect(s.retry == c["retry"] as? Bool, "\(label)")
            if !s.key.isEmpty {
                for lang in ["en", "ar"] {
                    let word = kitWord(s.key, Locale(identifier: lang))
                    #expect(word != nil && word != s.key, "\(s.key) has its \(lang) words")
                }
            }
        }
    }

    /// Every guest key the screens use has English and Arabic.
    @Test func theGuestWordsAreInBothLanguages() {
        let keys = ["guest.watchBroadcast", "guest.enterCode", "guest.scanQr", "guest.saved", "guest.remove",
                    "guest.onMaktaba", "guest.tryAgain", "guest.publisher", "guest.code", "guest.invite", "guest.watch",
                    "guest.pairCheck", "guest.pairNew", "guest.noSaved", "guest.network", "guest.free", "guest.badCode",
                    "guest.badInvite", "guest.addCode", "guest.addInvite", "guest.pairPhone", "guest.noChannels",
                    "guest.now", "guest.next", "guest.by", "guest.until", "guest.close", "guest.intro", "guest.loading",
                    "guest.publisherHint", "guest.inviteHint", "guest.pairTitle", "guest.channels", "guest.switchHint", "guest.switchHintTV",
                    "broadcast.invite.invalid"]
        for k in keys {
            let en = kitWord(k, Locale(identifier: "en")), ar = kitWord(k, Locale(identifier: "ar"))
            #expect(en != nil && ar != nil && en != ar, "\(k)")
        }
        #expect(kitWord("guest.onMaktaba", Locale(identifier: "ar")) == "%@ على مكتبة")
    }
}

@Suite struct GuestTyping {
    @Test func invitesFromLinksAndTokens() {
        #expect(GuestInput.invite("https://watch.maktaba.tv/i/mkbn1.abc.def") == "mkbn1.abc.def")
        #expect(GuestInput.invite("  mkbn1.abc.def\n") == "mkbn1.abc.def")
        #expect(GuestInput.invite("https://watch.maktaba.tv/i/mkbn1.abc.def?x=1") == "mkbn1.abc.def")
        #expect(GuestInput.invite("https://example.com/page") == nil)
        #expect(GuestInput.invite("hello") == nil)
        #expect(GuestInput.invite(in: URL(string: "https://watch.maktaba.tv/i/mkbn1.a.b")!) == "mkbn1.a.b")
        #expect(GuestInput.invite(in: URL(string: "maktaba://watch/i/mkbn1.a.b")!) == "mkbn1.a.b")
        #expect(GuestInput.invite(in: URL(string: "https://evil.example/i/mkbn1.a.b")!) == nil)
        #expect(GuestInput.invite(in: URL(string: "maktaba://connect?id=x")!) == nil)
    }

    @Test func shortCodesAsTheEdgeWantsThem() {
        #expect(GuestInput.code("MK-7Q2X") == "MK-7Q2X")
        #expect(GuestInput.code("mk 7q2x") == "MK-7Q2X")
        #expect(GuestInput.code("7q2x") == "MK-7Q2X")
        #expect(GuestInput.code("MK-7Q2") == nil)
        #expect(GuestInput.code("MK-٧Q2X") == nil, "Arabic-Indic digits are not the edge's alphabet")
    }

    @Test func publishersByHandleOrAddress() {
        #expect(GuestInput.publisher(" 6f1c-home ") == "6f1c-home")
        #expect(GuestInput.publisher("https://watch.maktaba.tv/ar/6f1c-home/ch1") == "6f1c-home")
        #expect(GuestInput.publisher("https://watch.maktaba.tv/6f1c-home") == "6f1c-home")
        #expect(GuestInput.publisher("a b") == nil)
        #expect(GuestInput.publisher("") == nil)
    }
}

/// The edge for the client's tests: answers by "METHOD host/path" in turn (the last repeats),
/// remembers each request's JSON body and Authorization. Its own, so no other suite's stub is
/// reset under it.
final class GuestStub: URLProtocol, @unchecked Sendable {
    struct Answer: Sendable {
        let status: Int
        let body: String
        var headers: [String: String] = [:]
    }

    struct Seen: Sendable {
        let route: String
        let body: [String: String]?
        let auth: String?
    }

    nonisolated(unsafe) private static var routes: [String: [Answer]] = [:]
    nonisolated(unsafe) private static var seenList: [Seen] = []
    private static let lock = NSLock()

    static func reset() { lock.withLock { routes = [:]; seenList = [] } }
    static func on(_ route: String, _ answers: Answer...) { lock.withLock { routes[route, default: []].append(contentsOf: answers) } }
    static var seen: [Seen] { lock.withLock { seenList } }

    static func session() -> URLSession {
        let c = URLSessionConfiguration.ephemeral
        c.protocolClasses = [GuestStub.self]
        return URLSession(configuration: c)
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
        let route = "\(request.httpMethod ?? "GET") \(url.host() ?? "")\(url.path())"
        let body = data.flatMap { try? JSONSerialization.jsonObject(with: $0) as? [String: String] }
        let answer: Answer? = Self.lock.withLock {
            Self.seenList.append(Seen(route: route, body: body, auth: request.value(forHTTPHeaderField: "Authorization")))
            guard var list = Self.routes[route], !list.isEmpty else { return nil }
            let first = list.count > 1 ? list.removeFirst() : list[0]
            Self.routes[route] = list
            return first
        }
        guard let answer else {
            client?.urlProtocol(self, didFailWithError: URLError(.cannotConnectToHost))
            return
        }
        let resp = HTTPURLResponse(url: url, statusCode: answer.status, httpVersion: "HTTP/1.1",
                                   headerFields: answer.headers.merging(["Content-Type": "application/json"]) { a, _ in a })!
        client?.urlProtocol(self, didReceive: resp, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(answer.body.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }

    override func stopLoading() {}
}

@Suite(.serialized) struct GuestClientTests {
    let edge = URL(string: "https://watch.example")!
    var client: GuestClient { GuestClient(watch: edge, session: GuestStub.session()) }
    let passJSON = #"{"pass":"mkbg1.p.s","publisher":"pub-1","channels":["ch1","ch2"],"expires_at":"2030-01-01T00:00:00Z","reused":false}"#

    @Test func redeemsAnInviteAndACode() async throws {
        GuestStub.reset()
        GuestStub.on("POST watch.example/api/v1/redeem", .init(status: 200, body: passJSON))
        let p = try await client.redeem(invite: "mkbn1.a.b")
        #expect(p.publisher == "pub-1" && p.credential == "mkbg1.p.s" && p.channels == ["ch1", "ch2"] && p.kind == .pass)
        #expect(p.expiresAt == homeTime("2030-01-01T00:00:00Z"))
        _ = try await client.redeem(publisher: "pub-1", code: "MK-7Q2X")
        #expect(GuestStub.seen.map(\.body) == [["invite": "mkbn1.a.b"], ["publisher": "pub-1", "code": "MK-7Q2X"]])
    }

    @Test func aRefusalIsItsStateAndWords() async throws {
        GuestStub.reset()
        GuestStub.on("POST watch.example/api/v1/redeem", .init(status: 410, body: #"{"error":"invite_used"}"#))
        do {
            _ = try await client.redeem(invite: "mkbn1.a.b")
            Issue.record("refused")
        } catch let e as GuestError {
            #expect(e == GuestError(status: 410, code: "invite_used"))
            #expect(e.state.state == .inviteUsed)
            #expect(e.message == kitWord("broadcast.invite.used"))
        }
        GuestStub.reset()
        GuestStub.on("POST watch.example/api/v1/redeem", .init(status: 404, body: #"{"error":"code_unknown"}"#))
        do {
            _ = try await client.redeem(publisher: "pub-1", code: "MK-AAAA")
            Issue.record("refused")
        } catch let e as GuestError {
            #expect(e.message == kitWord("broadcast.invite.invalid"))
        }
    }

    @Test func playsWithThePassAsBearerAndKeepsARenewedOne() async throws {
        GuestStub.reset()
        GuestStub.on("POST watch.example/api/v1/play", .init(
            status: 200,
            body: #"{"session_id":"s1","beat_key":"k1","master_url":"https://live.example/vt1/ch1/master.m3u8","refresh_after":300,"channel_state":"waiting"}"#,
            headers: ["X-Maktaba-Pass": "mkbg1.renewed.s"]))
        let pass = GuestPass(publisher: "pub-1", credential: "mkbg1.p.s", kind: .pass, channels: ["ch1"], expiresAt: .distantFuture)
        let p = try await client.play(pass, channel: "ch1")
        #expect(p.session == "s1" && p.beatKey == "k1" && p.masterURL.absoluteString == "https://live.example/vt1/ch1/master.m3u8")
        #expect(p.state.state == .tuning && p.state.plays)
        #expect(p.renewedPass == "mkbg1.renewed.s")
        #expect(GuestStub.seen.last?.auth == "Bearer mkbg1.p.s")
        #expect(GuestStub.seen.last?.body == ["publisher": "pub-1", "channel": "ch1"])
    }

    @Test func aBeatBringsTheStateAndANewToken() async throws {
        GuestStub.reset()
        GuestStub.on("POST watch.example/api/v1/play/s1/beat", .init(
            status: 200, body: #"{"ok":true,"channel_state":"offline_slate","master_url":"https://live.example/vt2/ch1/master.m3u8"}"#))
        let pass = GuestPass(publisher: "pub-1", credential: "mkbo1.o.s", kind: .open, channels: [], expiresAt: .distantFuture)
        let b = try await client.beat(pass, session: "s1", beatKey: "k1")
        #expect(b.state.state == .offline && b.state.plays, "the slate plays, with the note")
        #expect(b.masterURL?.absoluteString == "https://live.example/vt2/ch1/master.m3u8")
        #expect(GuestStub.seen.last?.body == ["publisher": "pub-1", "beat_key": "k1"])
    }

    @Test func aTVPairsByLongPoll() async throws {
        GuestStub.reset()
        GuestStub.on("POST watch.example/api/v1/pair", .init(
            status: 201, body: #"{"nonce":"n1","poll_key":"pk","code":"Q7XZ","url":"https://watch.example/pair?tv=n1","expires_at":"2030-01-01T00:10:00Z","poll":"/api/v1/pair/n1"}"#))
        GuestStub.on("GET watch.example/api/v1/pair/n1",
                      .init(status: 204, body: ""),
                      .init(status: 200, body: #"{"publisher":"pub-1","credential":"mkbg1.tv.s","kind":"pass","channels":["ch1"],"expires_at":"2030-01-01T00:00:00Z"}"#),
                      .init(status: 410, body: #"{"error":"pair_gone"}"#))
        let pairing = try await client.pair()
        #expect(pairing.code == "Q7XZ" && pairing.pollKey == "pk" && pairing.url.hasSuffix("/pair?tv=n1"))
        #expect(try await client.poll(pairing) == .waiting)
        guard case let .paired(p) = try await client.poll(pairing) else { Issue.record("paired"); return }
        #expect(p.publisher == "pub-1" && p.credential == "mkbg1.tv.s" && p.kind == .pass)
        #expect(try await client.poll(pairing) == .gone)
    }
}

@Suite struct GuestTokenInThePath {
    @Test func theProxyAsksWithTheNewestToken() throws {
        let live = try #require(GuestLive(master: URL(string: "https://live.example/vt1/ch1/master.m3u8")!))
        let proxy = try StreamProxy(live: live)
        let s = proxy.secret
        #expect(proxy.remote(forTarget: "/\(s)/live/ch1/master.m3u8")?.absoluteString == "https://live.example/vt1/ch1/master.m3u8")
        #expect(live.update(URL(string: "https://live.example/vt2/ch1/master.m3u8")!))
        #expect(proxy.remote(forTarget: "/\(s)/live/ch1/480p/12-abcdef01.ts")?.absoluteString == "https://live.example/vt2/ch1/480p/12-abcdef01.ts")
        // only this channel, under the secret, never upwards
        #expect(proxy.remote(forTarget: "/\(s)/live/ch2/master.m3u8") == nil)
        #expect(proxy.remote(forTarget: "/wrong/live/ch1/master.m3u8") == nil)
        #expect(proxy.remote(forTarget: "/\(s)/live/ch1/../ch2/master.m3u8") == nil)
        #expect(proxy.remote(forTarget: "/\(s)/stream/x") == nil)
        // another channel's or edge's address does not replace the token
        #expect(!live.update(URL(string: "https://live.example/vt3/ch2/master.m3u8")!))
        #expect(!live.update(URL(string: "https://evil.example/vt3/ch1/master.m3u8")!))
        #expect(live.currentToken == "vt2")
    }

    @Test func aPlayerReadsTheChannelThroughTheProxy() async throws {
        let home = try await LoopbackHome()
        defer { home.stop() }
        home.on("/vt2/ch1/master.m3u8", .init(headers: ["Content-Type": "application/vnd.apple.mpegurl"], length: 120))
        let live = try #require(GuestLive(master: home.url.appending(path: "vt1/ch1/master.m3u8")))
        let proxy = try StreamProxy(live: live)
        try await proxy.start()
        defer { proxy.stop() }
        live.update(home.url.appending(path: "vt2/ch1/master.m3u8"))
        let local = try #require(proxy.localLive())
        #expect(local.host() == "127.0.0.1" && !local.absoluteString.contains("vt"))
        let (data, response) = try await URLSession.shared.data(from: local)
        #expect((response as? HTTPURLResponse)?.statusCode == 200 && data.count == 120)
        #expect(home.seen.last?.target == "/vt2/ch1/master.m3u8")
        #expect(home.seen.last?.headers["authorization"] == nil, "no header: the token is in the path")
    }
}
