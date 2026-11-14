import Foundation
import Testing
@testable import MaktabaKit

/// The home's answer while a transcode has not written its rendition yet (what Android
/// showed as is: "The home answered 404: {…}").
let renditionNotFound = #"{"type":"https://maktaba.invalid/problems/rendition-not-found","title":"rendition index not on disk","status":404,"detail":"open /Volumes/Media/.cache/abc/v0/index.m3u8: no such file or directory"}"#

@Suite struct ErrorKinds {
    @Test(arguments: [
        (404, renditionNotFound, ErrorKind.notReady),
        (404, #"{"type":"https://maktaba.tv/problems/manifest-not-found","status":404}"#, .notReady),
        (404, #"{"type":"https://maktaba.tv/problems/video-not-found","title":"no such video","status":404}"#, .notFound),
        (404, "404 page not found", .notFound),
        (410, #"{"title":"gone"}"#, .notFound),
        (401, #"{"type":"https://maktaba.invalid/problems/unauthorized","status":401,"code":"signin_required"}"#, .signedOut),
        (403, #"{"title":"Forbidden","status":403,"detail":"admin-only"}"#, .noAccess),
        (503, #"{"type":"https://maktaba.invalid/problems/home-unreachable","status":503,"code":"home_unreachable"}"#, .unreachable),
        (503, #"{"title":"home unreachable","code":"home_unreachable"}"#, .unreachable),
        (504, #"{"title":"home timed out","code":"home_timeout"}"#, .unreachable),
        (409, #"{"type":"https://maktaba.tv/problems/video-unavailable-offline","status":409}"#, .unreachable),
        (503, #"{"title":"paused","code":"paused"}"#, .busy(retryAfter: nil)),
        (429, #"{"title":"too many requests","status":429}"#, .busy(retryAfter: nil)),
        (500, #"{"title":"internal error","status":500}"#, .server),
        (502, "<html>bad gateway</html>", .server),
        (504, "", .server),
        (400, #"{"title":"bad request"}"#, .server),
    ])
    func eachAnswersKind(status: Int, body: String, want: ErrorKind) {
        #expect(APIError.status(status, body).kind == want)
    }

    @Test func aRetryAfterIsKept() {
        #expect(APIError.status(503, #"{"title":"busy"}"#, retryAfter: 7).kind == .busy(retryAfter: 7))
        #expect(APIError.status(429, "", retryAfter: 2).kind == .busy(retryAfter: 2))
    }

    @Test func readsARetryAfterHeader() throws {
        func answer(_ v: String?) -> HTTPURLResponse {
            HTTPURLResponse(url: URL(string: "http://h")!, statusCode: 503, httpVersion: nil,
                            headerFields: v.map { ["Retry-After": $0] } ?? [:])!
        }
        #expect(APIClient.retryAfter(answer("12")) == 12)
        #expect(APIClient.retryAfter(answer(nil)) == nil)
        #expect(APIClient.retryAfter(answer("soon")) == nil)
        #expect(APIClient.retryAfter(answer("Thu, 01 Jan 1970 00:00:00 GMT")) == 0)
    }

    @Test func theOtherErrors() {
        #expect(APIError.transport("The request timed out.").kind == .unreachable)
        #expect(APIError.decoding("keyNotFound").kind == .server)
        #expect(APIError.signedOut.kind == .signedOut)
        #expect(APIError.notReady("").kind == .notReady)
        #expect(HomeFailure(URLError(.notConnectedToInternet)).kind == .unreachable)
        #expect(HomeFailure(CocoaError(.fileReadUnknown)).kind == .server)
    }

    @Test(arguments: ["https://maktaba.invalid/problems/rendition-not-found", "https://maktaba.tv/problems/rendition-not-found",
                      "https://x.example/problems/rendition-not-found/", "https://x.example/problems/Rendition-Not-Found?x=1"])
    func theTypesNameWhateverTheDomain(type: String) {
        #expect(Problem(type: type).slug == "rendition-not-found")
    }

    @Test func noSlugWithoutProblems() {
        #expect(Problem(type: "about:blank").slug == nil)
        #expect(Problem().slug == nil)
    }
}

@Suite struct FriendlyWords {
    /// The message is one sentence: never the JSON, the type URI or the home's detail.
    @Test(arguments: [PermissionsStrings.Language.en, .ar])
    func neverTheHomesAnswer(lang: PermissionsStrings.Language) {
        let words = ErrorStrings(lang)
        for e in [APIError.status(404, renditionNotFound), .notReady(renditionNotFound),
                  .status(500, #"{"title":"panic","detail":"nil pointer"}"#), .status(502, "<html>bad gateway</html>"),
                  .transport("Could not connect to the server."), .decoding("keyNotFound(\"id\")")] {
            let f = HomeFailure(e)
            for said in [f.message(words), f.message(words, video: true), words.text(.playerTitle)] {
                #expect(!said.contains("{") && !said.contains("problems/") && !said.contains("/Volumes")
                        && !said.contains("<html>") && !said.contains("404"), "\(said)")
            }
        }
    }

    @Test func theRenditionNotFoundAnswerSaysItIsBeingPrepared() {
        let e = APIError.status(404, renditionNotFound)
        let description = e.errorDescription ?? ""
        #expect(!description.contains("{") && !description.contains("problems/"))
        #expect(description == ErrorStrings.forPreferredLanguages().sentence(.notReady))
        #expect(e.localizedDescription == description)
        #expect(HomeFailure(e).message(ErrorStrings(.en)) == "Your home is still preparing this video. Try again in a moment.")
        #expect(HomeFailure(e).message(ErrorStrings(.ar)) == "ما زال الجهاز الرئيسي يجهّز هذا الفيديو. حاول مجددًا بعد قليل.")
    }

    /// What the home said is behind Details: the status, the type, its title and detail.
    @Test func theDetailsKeepWhatTheHomeSaid() throws {
        let details = try #require(HomeFailure(APIError.status(404, renditionNotFound)).details)
        #expect(details.hasPrefix("404 · rendition-not-found"))
        #expect(details.contains("rendition index not on disk") && details.contains("no such file or directory"))
        let raw = try #require(HomeFailure(APIError.status(502, "<html>bad gateway</html>")).details)
        #expect(raw == "502\n<html>bad gateway</html>")
        #expect(HomeFailure(APIError.status(500, String(repeating: "x", count: 5000))).raw?.count == 2048)
    }

    @Test func eachKindsActions() {
        #expect(HomeFailure(kind: .notReady).actions == [.tryAgain, .back])
        #expect(HomeFailure(kind: .busy(retryAfter: 3)).actions == [.tryAgain, .back])
        #expect(HomeFailure(kind: .server).actions == [.tryAgain, .back])
        #expect(HomeFailure(kind: .unreachable).actions == [.tryAgain, .chooseAnotherHome, .back])
        #expect(HomeFailure(kind: .signedOut).actions == [.signInAgain, .back])
        #expect(HomeFailure(kind: .notFound).actions == [.back])
        #expect(HomeFailure(kind: .noAccess).actions == [.back])
        #expect(HomeFailure(kind: .off).actions == [.back])
    }

    /// Story 51.09 T14: a 409 component_off is `.off`, with the part, its feature and words.
    @Test func aPartThatIsOff() throws {
        let body = #"{"type":"https://maktaba.tv/problems/component-off","title":"This feature is off","status":409,"code":"component_off","component":"livetv","feature":"live_tv"}"#
        #expect(ErrorKind.of(status: 409, problem: Problem.parse(body)) == .off)
        #expect(APIError.status(409, body).kind == .off)
        let p = try #require(Problem.parse(body))
        #expect(p.component == "livetv" && p.feature == "live_tv")
        #expect(ErrorKind.of(status: 409, problem: Problem(code: "stale_generation")) == .server)
        let en = ErrorStrings(.en), ar = ErrorStrings(.ar)
        #expect(en.sentence(.off) == "This feature is turned off on this home.")
        #expect(ar.sentence(.off) == "هذه الميزة متوقفة على الجهاز الرئيسي.")
        #expect(en.offSentence(feature: "live_tv", admin: true) == "Live TV is off on this home. Turn it on in Components.")
        #expect(ar.offSentence(feature: "live_tv", admin: true) == "البث المباشر متوقف على الجهاز الرئيسي. شغّل من صفحة المكوّنات.")
        // a plural name agrees with its verb
        #expect(en.offSentence(feature: "transcripts", admin: true) == "Transcripts are off on this home. Turn it on in Components.")
        #expect(en.offSentence(feature: "live_tv", admin: false) == en.text(.off))
        #expect(en.offSentence(feature: "holograms", admin: true) == en.text(.off))
    }

    @Test func everyFeatureHasItsNameInBothLanguages() {
        #expect(Set(ErrorStrings.featuresEN.keys) == Set(ErrorStrings.featuresAR.keys))
        // the catalog's ten (plugin_panels, story 54.20)
        #expect(ErrorStrings.featuresEN.count == 10)
        for (id, en) in ErrorStrings.featuresEN where id != "plex" {
            #expect(ErrorStrings.featuresAR[id] != en, "ar \(id) is not translated")
        }
    }

    @Test func everyWordIsInBothLanguages() {
        for key in ErrorStrings.Key.allCases {
            let en = ErrorStrings.en[key], ar = ErrorStrings.ar[key]
            #expect(en?.isEmpty == false, "en \(key)")
            #expect(ar?.isEmpty == false, "ar \(key)")
            #expect(en != ar, "ar \(key) is not translated")
        }
        #expect(ErrorStrings(.ar).isRightToLeft && !ErrorStrings(.en).isRightToLeft)
        #expect(ErrorStrings.forPreferredLanguages(["ar-EG"]).language == .ar)
    }

    @Test func theSharedWords() {
        let en = ErrorStrings(.en), ar = ErrorStrings(.ar)
        #expect(en.text(.playerTitle) == "This video can't play right now")
        #expect(ar.text(.playerTitle) == "لا يمكن تشغيل هذا الفيديو الآن")
        #expect(en.sentence(.notFound, video: true) == "This video or its file is no longer on your home.")
        #expect(en.sentence(.notFound) == "We couldn't find what you were looking for.")
        #expect(ar.action(.chooseAnotherHome) == "اختيار جهاز رئيسي آخر")
        #expect(ar.sentence(.unreachable).contains("الجهاز الرئيسي"))
    }

    /// A wrong password is said as one, not as a sign-in that ended.
    @Test func aWrongPassword() {
        let en = ConnectStrings(.en)
        #expect(en.signInProblem(APIError.status(401, #"{"title":"invalid credentials"}"#)) == en.text(.wrongPassword))
        #expect(en.signInProblem(APIError.transport("offline")) == ErrorStrings(.en).sentence(.unreachable))
    }
}

// MARK: - waiting for a stream

/// Answers by host and path, in turn (the last answer repeats), with headers; every test
/// uses its own host, so suites running at once do not share answers.
final class PlaylistStub: URLProtocol, @unchecked Sendable {
    struct Answer: Sendable {
        let status: Int
        let body: String
        var headers: [String: String] = [:]
    }

    nonisolated(unsafe) private static var routes: [String: [Answer]] = [:]
    nonisolated(unsafe) private static var asked: [String: Int] = [:]
    private static let lock = NSLock()

    static func on(_ host: String, _ path: String, _ answers: Answer...) {
        lock.withLock { routes["\(host)\(path)"] = answers }
    }

    static func count(_ host: String, _ path: String) -> Int { lock.withLock { asked["\(host)\(path)"] ?? 0 } }

    static func session() -> URLSession {
        let c = URLSessionConfiguration.ephemeral
        c.protocolClasses = [PlaylistStub.self]
        return URLSession(configuration: c)
    }

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        let url = request.url!
        let key = "\(url.host() ?? "")\(url.path())"
        let answer: Answer? = Self.lock.withLock {
            Self.asked[key, default: 0] += 1
            guard var list = Self.routes[key], !list.isEmpty else { return nil }
            let first = list.count > 1 ? list.removeFirst() : list[0]
            Self.routes[key] = list
            return first
        }
        guard let answer else {
            client?.urlProtocol(self, didFailWithError: URLError(.cannotConnectToHost))
            return
        }
        var headers = answer.headers
        headers["Content-Type"] = headers["Content-Type"] ?? "application/problem+json"
        let resp = HTTPURLResponse(url: url, statusCode: answer.status, httpVersion: "HTTP/1.1", headerFields: headers)!
        client?.urlProtocol(self, didReceive: resp, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(answer.body.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }

    override func stopLoading() {}
}

/// A clock that never waits: `sleep` notes the wait and moves `now` on at once (by
/// `stretch` times the wait, a device too busy to wake on time), so the attempts and the
/// deadline do not depend on how fast the machine running the tests is.
final class TestClock: Swift.Clock, @unchecked Sendable {
    struct Instant: InstantProtocol {
        var offset: Duration
        func advanced(by d: Duration) -> Instant { Instant(offset: offset + d) }
        func duration(to other: Instant) -> Duration { other.offset - offset }
        static func < (a: Instant, b: Instant) -> Bool { a.offset < b.offset }
    }

    private let lock = NSLock()
    private var current = Instant(offset: .zero)
    private var slept: [Duration] = []
    private let stretch: Int

    init(stretch: Int = 1) { self.stretch = stretch }

    var now: Instant { lock.withLock { current } }
    var minimumResolution: Duration { .nanoseconds(1) }
    /// The waits it was asked for (none are slept).
    var waits: [Duration] { lock.withLock { slept } }

    func sleep(until deadline: Instant, tolerance: Duration?) async throws {
        lock.withLock {
            slept.append(current.duration(to: deadline))
            current = current.advanced(by: current.duration(to: deadline) * stretch)
        }
    }
}

@Suite struct WaitingForAStream {
    static let master = "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1460800\nv0/index.m3u8?sig=a\n"
    static let rendition = "#EXTM3U\n#EXTINF:6,\nseg-0.ts\n"

    func client(_ host: String) -> APIClient {
        let home = URL(string: "http://\(host):8088")!
        return APIClient(home: home, tokens: TokenStore(app: "test.wait", home: home), session: PlaylistStub.session())
    }

    func master(_ host: String) -> URL { URL(string: "http://\(host):8088/stream/s1/master.m3u8?sig=a")! }

    @Test func playsOnceTheRenditionIsWritten() async throws {
        let host = "ready-after-3.test"
        PlaylistStub.on(host, "/stream/s1/master.m3u8", .init(status: 200, body: Self.master))
        PlaylistStub.on(host, "/stream/s1/v0/index.m3u8",
                        .init(status: 404, body: renditionNotFound), .init(status: 404, body: renditionNotFound),
                        .init(status: 404, body: renditionNotFound), .init(status: 200, body: Self.rendition))
        let clock = TestClock()
        try await client(host).waitUntilPlayable(master(host), headers: [:], clock: clock)
        #expect(PlaylistStub.count(host, "/stream/s1/v0/index.m3u8") == 4)
        #expect(clock.waits == [.milliseconds(500), .seconds(1), .seconds(2)])
    }

    /// The master itself 404s while the transcode starts (and a plain 404 from something in
    /// between is waited out too).
    @Test func waitsForTheMasterToo() async throws {
        let host = "master-later.test"
        PlaylistStub.on(host, "/stream/s1/master.m3u8",
                        .init(status: 404, body: #"{"type":"https://maktaba.invalid/problems/manifest-not-found","status":404}"#),
                        .init(status: 404, body: "404 page not found"), .init(status: 200, body: Self.master))
        PlaylistStub.on(host, "/stream/s1/v0/index.m3u8", .init(status: 200, body: Self.rendition))
        try await client(host).waitUntilPlayable(master(host), headers: [:], clock: TestClock())
        #expect(PlaylistStub.count(host, "/stream/s1/master.m3u8") == 3)
    }

    @Test func neverReadyIsNotReady() async throws {
        let host = "never-ready.test"
        PlaylistStub.on(host, "/stream/s1/master.m3u8", .init(status: 200, body: Self.master))
        PlaylistStub.on(host, "/stream/s1/v0/index.m3u8", .init(status: 404, body: renditionNotFound))
        let clock = TestClock()
        do {
            try await client(host).waitUntilPlayable(master(host), headers: [:], timeout: .seconds(5), clock: clock)
            Issue.record("ready without a rendition")
        } catch let e as APIError {
            #expect(e.kind == .notReady)
            #expect(e == .notReady(renditionNotFound))
            #expect(!(e.errorDescription ?? "{").contains("{"))
        }
        // 0.5 + 1 + 2 = 3.5 s waited; another 2 s would pass the 5 s
        #expect(clock.waits == [.milliseconds(500), .seconds(1), .seconds(2)])
        #expect(PlaylistStub.count(host, "/stream/s1/v0/index.m3u8") == 4)
    }

    @Test func theWaitsGrowToTwoSeconds() async throws {
        let host = "capped.test"
        PlaylistStub.on(host, "/stream/s1/master.m3u8", .init(status: 404, body: "404 page not found"))
        let clock = TestClock()
        await #expect(throws: APIError.notReady("404 page not found")) {
            try await client(host).waitUntilPlayable(master(host), headers: [:], clock: clock)
        }
        let all = clock.waits
        #expect(all.prefix(3) == [.milliseconds(500), .seconds(1), .seconds(2)])
        #expect(all.dropFirst(2).allSatisfy { $0 == .seconds(2) })
        #expect(all.reduce(.zero, +) <= .seconds(30) && all.reduce(.zero, +) > .seconds(28))
    }

    /// The deadline is kept on the clock it is given, not by adding up the waits: when each
    /// wait runs twice as long (a busy device), 0.5 s ends at 1 s and 1 s at 3 s, so a 2 s wait
    /// would pass the 4 s deadline and it stops after two (the waits alone add up to 3.5 s).
    @Test func theDeadlineIsKeptOnTheGivenClock() async throws {
        let host = "slow-device.test"
        PlaylistStub.on(host, "/stream/s1/master.m3u8", .init(status: 404, body: "404 page not found"))
        let clock = TestClock(stretch: 2)
        await #expect(throws: APIError.notReady("404 page not found")) {
            try await client(host).waitUntilPlayable(master(host), headers: [:], timeout: .seconds(4), clock: clock)
        }
        #expect(clock.waits == [.milliseconds(500), .seconds(1)])
        #expect(clock.now.offset == .seconds(3))
        #expect(PlaylistStub.count(host, "/stream/s1/master.m3u8") == 3)
    }

    @Test(arguments: [(403, ErrorKind.noAccess), (401, .signedOut), (500, .server)])
    func anyOtherAnswerFailsAtOnce(status: Int, kind: ErrorKind) async throws {
        let host = "refused-\(status).test"
        PlaylistStub.on(host, "/stream/s1/master.m3u8", .init(status: 200, body: Self.master))
        PlaylistStub.on(host, "/stream/s1/v0/index.m3u8", .init(status: status, body: #"{"title":"no","status":\#(status)}"#))
        let clock = TestClock()
        do {
            try await client(host).waitUntilPlayable(master(host), headers: [:], clock: clock)
            Issue.record("played after a \(status)")
        } catch let e as APIError {
            #expect(e.kind == kind)
        }
        #expect(clock.waits.isEmpty)
        #expect(PlaylistStub.count(host, "/stream/s1/v0/index.m3u8") == 1)
    }

    /// Another 404 (the session is gone) is not waited out.
    @Test func aGoneSessionIsNotWaitedFor() async throws {
        let host = "gone.test"
        PlaylistStub.on(host, "/stream/s1/master.m3u8",
                        .init(status: 404, body: #"{"type":"https://maktaba.invalid/problems/session-not-found","status":404}"#))
        let clock = TestClock()
        await #expect(throws: APIError.self) {
            try await client(host).waitUntilPlayable(master(host), headers: [:], clock: clock)
        }
        #expect(clock.waits.isEmpty)
        #expect(PlaylistStub.count(host, "/stream/s1/master.m3u8") == 1)
    }

    @Test func aBusyHomeSaysWhenToAskAgain() async throws {
        let host = "busy.test"
        PlaylistStub.on(host, "/stream/s1/master.m3u8", .init(status: 503, body: #"{"title":"busy"}"#, headers: ["Retry-After": "9"]))
        do {
            try await client(host).waitUntilPlayable(master(host), headers: [:], clock: TestClock())
            Issue.record("played while busy")
        } catch let e as APIError {
            #expect(e.kind == .busy(retryAfter: 9))
        }
    }

    @Test func noAnswerIsUnreachable() async throws {
        do {
            try await client("nobody.test").waitUntilPlayable(master("nobody.test"), headers: [:], clock: TestClock())
            Issue.record("played without a home")
        } catch let e as APIError {
            #expect(e.kind == .unreachable)
        }
    }

    /// The REST calls keep a Retry-After too.
    @Test func aRateLimitedCall() async throws {
        let host = "limited.test"
        PlaylistStub.on(host, "/api/libraries", .init(status: 429, body: #"{"title":"slow down","status":429}"#, headers: ["Retry-After": "3"]))
        do {
            _ = try await client(host).libraries()
            Issue.record("a 429 decoded")
        } catch let e as APIError {
            #expect(e.kind == .busy(retryAfter: 3))
        }
    }
}

/// Epic 50 story 50.19: the relay's refusals of a plan's play, read from its answers and told
/// in English and Arabic, with what a screen offers.
@Suite struct RelayPlans {
    @Test func theRelaysRefusals() {
        let needs = #"{"error":"relay_needs_plan","message":"remote play through the relay needs a Basic or Pro plan"}"#
        let limit = #"{"error":"stream_limit","message":"2 devices are already playing through the relay, the most this plan allows"}"#
        let busy = #"{"type":"https://maktaba.tv/problems/home-busy","title":"busy","status":503,"code":"home_busy"}"#
        #expect(APIError.status(402, needs).kind == .relay(.needsPlan))
        #expect(APIError.status(429, limit).kind == .relay(.streamLimit(2)))
        #expect(APIError.status(503, busy).kind == .relay(.homeBusy))
        // other 429s and 503s stay busy
        #expect(APIError.status(429, #"{"error":"rate_limited"}"#, retryAfter: 4).kind == .busy(retryAfter: 4))
        let en = ErrorStrings(.en), ar = ErrorStrings(.ar)
        #expect(en.sentence(.relay(.needsPlan)).hasPrefix("Remote play through the relay needs a Basic or Pro plan."))
        #expect(en.sentence(.relay(.streamLimit(2))).hasPrefix("2 devices are already playing through the relay"))
        #expect(ar.sentence(.relay(.homeBusy)).hasPrefix("جهازك الرئيسي مشغول جدًا"))
        #expect(!ar.sentence(.relay(.needsPlan)).isEmpty && ar.sentence(.relay(.needsPlan)) != en.sentence(.relay(.needsPlan)))
        #expect(HomeFailure(kind: .relay(.needsPlan)).actions == [.back])
        #expect(HomeFailure(kind: .relay(.streamLimit(2))).actions == [.tryAgain, .back])
        #expect(HomeFailure(kind: .relay(.homeBusy)).actions == [.tryAgain, .back])
    }
}

/// Epic 59 story 59.05: a stream open that finds every transcode slot taken (503
/// `transcode_busy`, `Retry-After: 10`) is its own kind: one sentence in English and Arabic,
/// and the player asks again by itself after the home's wait.
@Suite struct TranscodeBusy {
    @Test func noTranscodeSlotFreeIsTranscodeBusy() {
        let body = #"{"type":"https://maktaba.tv/problems/transcode-busy","title":"Another video is being converted","status":503,"code":"transcode_busy"}"#
        #expect(APIError.status(503, body, retryAfter: 10).kind == .transcodeBusy(retryAfter: 10))
        #expect(APIError.status(503, #"{"type":"https://home.example/problems/transcode-busy","status":503}"#).kind == .transcodeBusy(retryAfter: nil))
        // another 503 stays busy; the code on another status is the server's
        #expect(APIError.status(503, #"{"status":503,"code":"draining"}"#, retryAfter: 3).kind == .busy(retryAfter: 3))
        #expect(APIError.status(500, #"{"status":500,"code":"transcode_busy"}"#).kind == .server)
        let en = ErrorStrings(.en), ar = ErrorStrings(.ar)
        #expect(en.sentence(.transcodeBusy(retryAfter: 10)) == "Another video is being converted on this home. Yours will start when it finishes.")
        #expect(ar.sentence(.transcodeBusy(retryAfter: nil), video: true) == "يجري تحويل فيديو آخر على هذا الجهاز الرئيسي، وسيبدأ الفيديو الخاص بك عند انتهائه.")
        #expect(HomeFailure(kind: .transcodeBusy(retryAfter: 10)).actions == [.tryAgain, .back])
        #expect(HomeFailure(kind: .transcodeBusy(retryAfter: 10)).symbol == "hourglass")
    }

    @Test func thePlayerAsksAgainAfterTheHomesWait() {
        #expect(ErrorKind.transcodeBusy(retryAfter: 10).transcodeRetrySeconds == 10)
        #expect(ErrorKind.transcodeBusy(retryAfter: nil).transcodeRetrySeconds == 10)
        #expect(ErrorKind.transcodeBusy(retryAfter: 0).transcodeRetrySeconds == 1)
        #expect(ErrorKind.transcodeBusy(retryAfter: 3600).transcodeRetrySeconds == 60)
        #expect(ErrorKind.busy(retryAfter: 10).transcodeRetrySeconds == nil)
        #expect(ErrorKind.relay(.homeBusy).transcodeRetrySeconds == nil)
    }
}
