import Foundation
import Testing
@testable import MaktabaKit

// Story 51.11: what the home says is on (`GET /api/capabilities`, answers shaped as
// api/internal/handlers/components' writes them), the store the session keeps, and a 409
// component_off read by the client's error path.

enum CapabilityFixtures {
    static let viewer = #"""
    {"etag":"c-1842-1",
     "features":{"live_tv":"off","transcripts":"on","subtitles":"on","semantic_search":"off",
                 "chapters":"on","youtube":"on","plex":"on","remote_access":"off","recommendations":"on"}}
    """#

    static let admin = #"""
    {"etag":"c-1843-1",
     "features":{"live_tv":"off","transcripts":"on","subtitles":"on","semantic_search":"off",
                 "chapters":"on","youtube":"on","plex":"on","remote_access":"on","recommendations":"on"},
     "admin":{"manage_components":true,
              "off":{"live_tv":{"components":["livetv.slots","livetv.inprocess"],"reason":"switched_off"},
                     "semantic_search":{"components":["search.semantic"],"reason":"dependency_off"}}}}
    """#

    /// A newer home names a feature this app does not know; an older one leaves some out.
    static let mixed = #"""
    {"etag":"c-9-0","features":{"holograms":"off","live_tv":"on","transcripts":"off"}}
    """#

    static let allOn = #"""
    {"etag":"c-1844-1","features":{"live_tv":"on","transcripts":"on","subtitles":"on","semantic_search":"on",
     "chapters":"on","youtube":"on","plex":"on","remote_access":"on","recommendations":"on"}}
    """#
}

@Suite struct CapabilitiesParsing {
    @Test func aViewersAnswer() throws {
        let (c, tag) = try Capabilities.parse(Data(CapabilityFixtures.viewer.utf8))
        #expect(tag == "c-1842-1")
        #expect(c.off == [.liveTV, .semanticSearch, .remoteAccess])
        #expect(!c.isAdmin)
        #expect(c.shown(.liveTV) == .hidden)
        #expect(c.shown(.transcripts) == .on)
        #expect(c.offComponents.isEmpty)
    }

    @Test func anAdminsAnswer() throws {
        let (c, _) = try Capabilities.parse(Data(CapabilityFixtures.admin.utf8))
        #expect(c.isAdmin)
        #expect(c.admin?.manageComponents == true)
        #expect(c.shown(.liveTV) == .explain)
        #expect(c.shown(.remoteAccess) == .on)
        #expect(c.admin?.off["live_tv"]?.components == ["livetv.slots", "livetv.inprocess"])
        #expect(c.admin?.off["semantic_search"]?.reason == "dependency_off")
        // three parts: live TV's slot pair and search by meaning
        #expect(c.offComponents == ["livetv.slots", "livetv.inprocess", "search.semantic"])
    }

    @Test func anUnknownFeatureIsIgnoredAndAMissingOneIsOn() throws {
        let (c, _) = try Capabilities.parse(Data(CapabilityFixtures.mixed.utf8))
        #expect(c.off == [.transcripts])
        for f in Capabilities.Feature.allCases where f != .transcripts {
            #expect(c.isOn(f), "\(f) is not in the answer: on")
        }
    }

    @Test func nothingKnownIsEveryFeatureOn() {
        for f in Capabilities.Feature.allCases { #expect(Capabilities.allOn.shown(f) == .on) }
        #expect(!Capabilities.allOn.isAdmin)
    }

    @Test func theComponentsPage() {
        #expect(Capabilities.componentsPage(URL(string: "http://192.168.1.5:8088")!).absoluteString
            == "http://192.168.1.5:8088/settings/components")
    }

    @Test func theWordsInBothLanguages() {
        let en = ErrorStrings(.en), ar = ErrorStrings(.ar)
        for f in Capabilities.Feature.allCases {
            #expect(en.featureName(f.rawValue) != nil && ar.featureName(f.rawValue) != nil, "\(f) has a name")
        }
        let english = Locale(identifier: "en"), arabic = Locale(identifier: "ar")
        // each feature's own sentence, in both languages, so the verb agrees with its name
        for f in Capabilities.Feature.allCases {
            #expect(kitWord("capabilities.off.\(f.rawValue)", english) != nil, "\(f) off in en")
            #expect(kitWord("capabilities.off.\(f.rawValue)", arabic) != nil, "\(f) off in ar")
            #expect(kitWord("capabilities.unavailable.\(f.rawValue)", english) != nil, "\(f) unavailable in en")
            #expect(kitWord("capabilities.unavailable.\(f.rawValue)", arabic) != nil, "\(f) unavailable in ar")
            #expect(FeatureWords.offOnThisHome(f, english) != FeatureWords.offOnThisHome(f, arabic))
        }
        #expect(FeatureWords.offOnThisHome(.liveTV, english) == "Live TV is off on this home.")
        #expect(FeatureWords.offOnThisHome(.transcripts, english) == "Transcripts are off on this home.")
        #expect(FeatureWords.offOnThisHome(.liveTV, arabic) == "البث المباشر متوقف على الجهاز الرئيسي.")
        #expect(FeatureWords.offOnThisHome(.transcripts, arabic) == "نصوص الفيديو متوقفة على الجهاز الرئيسي.")
        #expect(FeatureWords.unavailable(.liveTV, english) == "Live TV isn't available on this home.")
        #expect(FeatureWords.unavailable(.chapters, english) == "Chapters aren't available on this home.")
        #expect(FeatureWords.unavailable(.liveTV, arabic) == "البث المباشر غير متاح على الجهاز الرئيسي.")
        // a feature with no sentence here reads through the frame whose head noun agrees; the
        // name is isolated (U+2068 … U+2069), so a Latin name stays whole in an Arabic line
        #expect(FeatureWords.offSentence("holograms", english) == "The holograms feature is off on this home.")
        #expect(FeatureWords.offSentence("holograms", arabic) == "ميزة \u{2068}holograms\u{2069} متوقفة على الجهاز الرئيسي.")
        #expect(kit("Turn it on in Components", arabic) == "شغّل من صفحة المكوّنات")
        #expect(kit("Searching by words only. Search by meaning is off.", arabic) == "البحث بالكلمات فقط. البحث بالمعنى متوقف.")
        #expect(kit("\(3) parts are off", english) == "3 parts are off")
        #expect(kit("\(1) parts are off", english) == "1 part is off")
        #expect(kit("\(3) parts are off", arabic).contains("أجزاء متوقفة"))
        #expect(kit("\(2) parts are off", arabic) == "جزءان متوقفان")
    }
}

/// A home that answers from a list, in turn, and remembers the ETag each ask sent.
final class FakeCapabilities: CapabilitiesSource, @unchecked Sendable {
    private let lock = NSLock()
    private var answers: [Result<CapabilitiesAnswer, Error>]
    private var sentList: [String?] = []

    init(_ answers: [Result<CapabilitiesAnswer, Error>]) { self.answers = answers }

    var sent: [String?] { lock.withLock { sentList } }

    func capabilities(etag: String?) async throws -> CapabilitiesAnswer {
        let next: Result<CapabilitiesAnswer, Error> = lock.withLock {
            sentList.append(etag)
            return answers.count > 1 ? answers.removeFirst() : answers[0]
        }
        return try next.get()
    }
}

@MainActor @Suite struct CapabilitiesStoreTests {
    static func caps(_ json: String) -> Capabilities { try! Capabilities.parse(Data(json.utf8)).0 }

    /// T06: a 200 replaces, a 304 keeps what is known and its ETag, a failure keeps the last good.
    @Test func a304KeepsTheETagAndAFailureKeepsTheLastGood() async {
        let viewer = Self.caps(CapabilityFixtures.viewer)
        let home = FakeCapabilities([
            .success(.changed(viewer, etag: #""c-1842-1""#)),
            .success(.notModified),
            .failure(APIError.transport("timed out")),
            .success(.changed(Self.caps(CapabilityFixtures.allOn), etag: #""c-1844-1""#)),
        ])
        let store = CapabilitiesStore(gate: true)
        #expect(store.current == .allOn, "every feature on until the home answers")
        store.use(home)
        await store.refresh()
        #expect(store.current == viewer)
        #expect(store.etag == #""c-1842-1""#)
        await store.refresh()
        #expect(store.current == viewer, "304: the same")
        #expect(store.etag == #""c-1842-1""#)
        await store.refresh()
        #expect(store.current == viewer, "the home did not answer: the last good answer stays")
        await store.refresh()
        #expect(store.current.isOn(.liveTV), "a new answer replaces it")
        #expect(store.etag == #""c-1844-1""#)
        #expect(home.sent == [nil, #""c-1842-1""#, #""c-1842-1""#, #""c-1842-1""#], "If-None-Match with the kept ETag")
    }

    @Test func aFirstFailureLeavesEveryFeatureOn() async {
        let store = CapabilitiesStore(gate: true)
        store.use(FakeCapabilities([.failure(APIError.status(500, "{}"))]))
        await store.refresh()
        #expect(store.current == .allOn)
    }

    /// A home from before capabilities answers 404: every feature on, the ETag forgotten.
    @Test func anOldHomeIsEveryFeatureOn() async {
        let store = CapabilitiesStore(gate: true)
        store.use(FakeCapabilities([.success(.changed(Self.caps(CapabilityFixtures.viewer), etag: "e1")), .success(.notOnThisHome)]))
        await store.refresh()
        #expect(!store.current.isOn(.liveTV))
        await store.refresh()
        #expect(store.current == .allOn)
        #expect(store.etag == nil)
    }

    /// The build switch off: nothing is asked, every feature on.
    @Test func theGateOffAsksNothing() async {
        let home = FakeCapabilities([.success(.changed(Self.caps(CapabilityFixtures.viewer), etag: "e1"))])
        let store = CapabilitiesStore(gate: false)
        store.use(home)
        store.start()
        await store.refresh()
        #expect(store.current == .allOn)
        #expect(home.sent.isEmpty)
    }

    /// Another home (or signing out) starts again from every feature on; an answer for the
    /// home before is dropped.
    @Test func anotherHomeStartsOver() async {
        let store = CapabilitiesStore(gate: true)
        store.use(FakeCapabilities([.success(.changed(Self.caps(CapabilityFixtures.admin), etag: "e1"))]))
        await store.refresh()
        #expect(store.current.isAdmin)
        let next = FakeCapabilities([.success(.notModified)])
        store.use(next)
        #expect(store.current == .allOn)
        #expect(store.etag == nil)
        await store.refresh()
        #expect(next.sent == [nil], "the new home is asked without the old home's ETag")
        store.reset()
        await store.refresh()
        #expect(next.sent.count == 1, "signed out: nothing asked")
    }

    /// The timer: asks at once, then after each wait.
    @Test func theTimerAsksEveryInterval() async throws {
        let ticks = Ticks()
        let waits = Counter()
        let store = CapabilitiesStore(gate: true, every: .seconds(300)) { d in
            #expect(d == .seconds(300))
            waits.add()
            // one wait per tick the test sends
            await ticks.wait()
            try Task.checkCancellation()
        }
        let home = FakeCapabilities([.success(.changed(Self.caps(CapabilityFixtures.viewer), etag: "e1")), .success(.notModified)])
        store.use(home)
        store.start()
        try await until { home.sent.count == 1 }
        ticks.tick()
        try await until { home.sent.count == 2 }
        #expect(home.sent == [nil, "e1"])
        store.reset()
        ticks.tick()
        #expect(waits.value >= 1)
    }

    private func until(_ done: () -> Bool) async throws {
        for _ in 0..<200 where !done() { try await Task.sleep(for: .milliseconds(10)) }
        #expect(done())
    }
}

/// A wait that ends when the test says (`tick`), not after a time.
final class Ticks: @unchecked Sendable {
    private let lock = NSLock()
    private var waiting: CheckedContinuation<Void, Never>?
    private var banked = 0

    func wait() async {
        await withCheckedContinuation { (c: CheckedContinuation<Void, Never>) in
            let now: Bool = lock.withLock {
                if banked > 0 { banked -= 1; return true }
                waiting = c
                return false
            }
            if now { c.resume() }
        }
    }

    func tick() {
        let c: CheckedContinuation<Void, Never>? = lock.withLock {
            if let w = waiting { waiting = nil; return w }
            banked += 1
            return nil
        }
        c?.resume()
    }
}

final class Counter: @unchecked Sendable {
    private let lock = NSLock()
    private var n = 0
    func add() { lock.withLock { n += 1 } }
    var value: Int { lock.withLock { n } }
}

/// The client: `GET /api/capabilities` over a stubbed home.
@Suite(.serialized) struct CapabilitiesClient {
    init() { CapStub.reset() }

    @Test func aFirstAskThenA304() async throws {
        CapStub.on(200, CapabilityFixtures.viewer, headers: ["ETag": #""c-1842-1""#])
        let client = CapStub.client()
        guard case let .changed(c, tag) = try await client.capabilities(etag: nil) else {
            Issue.record("a 200 is an answer"); return
        }
        #expect(tag == #""c-1842-1""#)
        #expect(!c.isOn(.liveTV))
        CapStub.on(304, "")
        #expect(try await client.capabilities(etag: tag) == .notModified)
        #expect(CapStub.seen.map(\.ifNoneMatch) == [nil, #""c-1842-1""#])
    }

    @Test func noETagHeaderTakesTheBodys() async throws {
        CapStub.on(200, CapabilityFixtures.admin)
        guard case let .changed(c, tag) = try await CapStub.client().capabilities(etag: nil) else {
            Issue.record("a 200 is an answer"); return
        }
        #expect(tag == #""c-1843-1""#)
        #expect(c.isAdmin)
    }

    @Test func anOldHomesNotFound() async throws {
        CapStub.on(404, "404 page not found")
        #expect(try await CapStub.client().capabilities(etag: nil) == .notOnThisHome)
    }

    @Test func aFailureThrows() async {
        CapStub.on(500, #"{"title":"internal error","status":500}"#)
        await #expect(throws: APIError.self) { try await CapStub.client().capabilities(etag: nil) }
    }

    /// T14: a 409 component_off through the client's error path is the `off` kind, never a
    /// crash or an answer that cannot be read; an app that knew no `off` kind would read it
    /// as its generic sentence (the kinds have a sentence each).
    @Test func a409ComponentOffIsAKind() async throws {
        let body = #"{"type":"https://maktaba.tv/problems/component-off","title":"This feature is off","status":409,"detail":"livetv is turned off on this home","instance":"/api/channels/now","code":"component_off","component":"livetv","feature":"live_tv"}"#
        CapStub.on(409, body)
        do {
            _ = try await CapStub.client().channelsNow()
            Issue.record("a 409 is an error")
        } catch let e as APIError {
            #expect(e.kind == .off)
            #expect(e.problem?.feature == "live_tv")
            #expect(HomeFailure(e).kind == .off)
            #expect(!(e.errorDescription ?? "").isEmpty)
        }
    }
}

final class CapStub: URLProtocol, @unchecked Sendable {
    struct Seen: Sendable { let path: String; let ifNoneMatch: String? }

    nonisolated(unsafe) private static var answers: [(Int, String, [String: String])] = []
    nonisolated(unsafe) private static var seenList: [Seen] = []
    private static let lock = NSLock()
    static let home = URL(string: "http://192.168.50.177:8088")!

    static func reset() { lock.withLock { answers = []; seenList = [] } }
    static func on(_ code: Int, _ body: String, headers: [String: String] = [:]) {
        lock.withLock { answers.append((code, body, headers)) }
    }
    static var seen: [Seen] { lock.withLock { seenList } }

    static func client() -> APIClient {
        let c = URLSessionConfiguration.ephemeral
        c.protocolClasses = [CapStub.self]
        let tokens = TokenStore(service: "test.caps", home: home,
                                primary: FakeSecrets(["\(home.absoluteString)#access": "a-caps"]), legacy: nil)
        return APIClient(home: home, tokens: tokens, session: URLSession(configuration: c))
    }

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        let url = request.url!
        let answer: (Int, String, [String: String]) = Self.lock.withLock {
            Self.seenList.append(Seen(path: url.path(), ifNoneMatch: request.value(forHTTPHeaderField: "If-None-Match")))
            return Self.answers.isEmpty ? (599, "no stub", [:]) : Self.answers.removeFirst()
        }
        let resp = HTTPURLResponse(url: url, statusCode: answer.0, httpVersion: "HTTP/1.1",
                                   headerFields: ["Content-Type": "application/json"].merging(answer.2) { $1 })!
        client?.urlProtocol(self, didReceive: resp, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(answer.1.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }

    override func stopLoading() {}
}
