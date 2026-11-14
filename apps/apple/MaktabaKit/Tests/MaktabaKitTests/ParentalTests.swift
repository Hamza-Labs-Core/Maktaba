import Foundation
import Testing
@testable import MaktabaKit

// Epic 57 on the Apple Player: the caller's limit and its store (57.05), the device's signal
// (57.04), Ask a parent and the parent PIN (57.06), Family and Activity (57.07, 57.08), time
// (57.15) and live TV (57.10). Every answer is shaped as the Go handlers write it
// (`api/internal/handlers/parental`); no home is involved.

/// Answers by method and path, in turn (the last answer repeats), and keeps each request's
/// body as it was sent.
final class ParentalStub: URLProtocol, @unchecked Sendable {
    struct Seen: Sendable {
        let route: String
        let query: String
        let body: Data
        var json: [String: Any] { (try? JSONSerialization.jsonObject(with: body) as? [String: Any]) ?? [:] }
    }

    nonisolated(unsafe) private static var routes: [String: [(Int, String, [String: String])]] = [:]
    nonisolated(unsafe) private static var seenList: [Seen] = []
    private static let lock = NSLock()

    static func reset() {
        lock.withLock {
            routes = [:]
            seenList = []
        }
    }

    static func on(_ route: String, _ status: Int, _ body: String = "{}", headers: [String: String] = [:]) {
        lock.withLock { routes[route, default: []].append((status, body, headers)) }
    }

    static var seen: [Seen] { lock.withLock { seenList } }
    static func seen(_ route: String) -> [Seen] { seen.filter { $0.route == route } }

    static let home = URL(string: "http://parental.test")!

    /// A signed-in client of the stub home.
    static func client() -> APIClient {
        let secrets = FakeSecrets(["http://parental.test#access": "a1", "http://parental.test#refresh": "r1"])
        let tokens = TokenStore(service: "test.\(UUID().uuidString)", home: home, primary: secrets, legacy: nil)
        let c = URLSessionConfiguration.ephemeral
        c.protocolClasses = [ParentalStub.self]
        return APIClient(home: home, tokens: tokens, session: URLSession(configuration: c))
    }

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        var data = request.httpBody ?? Data()
        if data.isEmpty, let s = request.httpBodyStream {
            s.open()
            var buf = [UInt8](repeating: 0, count: 4096)
            while s.hasBytesAvailable {
                let n = s.read(&buf, maxLength: buf.count)
                if n <= 0 { break }
                data.append(buf, count: n)
            }
            s.close()
        }
        let url = request.url!
        let route = "\(request.httpMethod ?? "GET") \(url.path())"
        let answer: (Int, String, [String: String])? = Self.lock.withLock {
            Self.seenList.append(Seen(route: route, query: url.query() ?? "", body: data))
            guard var list = Self.routes[route], !list.isEmpty else { return nil }
            let first = list.count > 1 ? list.removeFirst() : list[0]
            Self.routes[route] = list
            return first
        }
        guard let (status, body, headers) = answer else {
            client?.urlProtocol(self, didFailWithError: URLError(.cannotConnectToHost))
            return
        }
        let type = status >= 400 ? "application/problem+json" : "application/json"
        let resp = HTTPURLResponse(url: url, statusCode: status, httpVersion: "HTTP/1.1",
                                   headerFields: headers.merging(["Content-Type": type]) { a, _ in a })!
        client?.urlProtocol(self, didReceive: resp, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(body.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }

    override func stopLoading() {}
}

private let en = Locale(identifier: "en_US")
private let ar = Locale(identifier: "ar_SA")
/// An argument as an Arabic sentence carries it: inside a first-strong isolate, so a Latin
/// name or a tier label keeps its own direction in the right-to-left line.
private func iso(_ s: String) -> String { "\u{2068}\(s)\u{2069}" }

enum ParentalFixtures {
    static let child = #"{"restricted":true,"max_tier":9,"unrated":"hide","version":4}"#
    static let childLoosened = #"{"restricted":true,"max_tier":13,"unrated":"hide","version":5}"#
    static let unrestricted = #"{"restricted":false}"#
    static let deviceLimited = #"{"restricted":false,"max_tier":9,"device_limit":9,"device_pending":true}"#
    static let childOnStricterDevice = #"{"restricted":true,"max_tier":9,"unrated":"hide","version":2,"device_limit":9}"#
    static let childOnLooserDevice = #"{"restricted":true,"max_tier":13,"unrated":"hide","version":2,"device_limit":16}"#
    static let tokens = #"{"access_token":"a2","refresh_token":"r2"}"#
    static let timeOver = #"{"type":"https://maktaba.tv/problems/parental-time","title":"forbidden","status":403,"detail":"this account's watching time is over for now","code":"parental_time","reason":"limit_reached","next_allowed_at":"2026-11-12T04:00:00Z"}"#
    static let notFound = #"{"type":"https://maktaba.tv/problems/not-found","title":"not found","status":404}"#
}

/// A source the store asks, answering what the test set.
final class FakeParentalSource: ParentalSource, @unchecked Sendable {
    private let lock = NSLock()
    private var limit: Result<ParentalLimit?, APIError> = .success(ParentalLimit.none)
    private var children: [ParentalChild] = []
    private var time: ParentalTime?
    private var libraryTime: [String: ParentalTime] = [:]
    private var timeQuestions: [String?] = []
    private var asked = 0

    func set(_ json: String) { lock.withLock { limit = .success(try! JSONDecoder.home.decode(ParentalLimit.self, from: Data(json.utf8))) } }
    func set(missing: Void) { lock.withLock { limit = .success(nil) } }
    func fail() { lock.withLock { limit = .failure(.transport("down")) } }
    func set(children n: Int) {
        lock.withLock { children = (0..<n).map { ParentalChild(id: "c\($0)", username: "child\($0)", policy: ParentalPolicy(userID: "c\($0)", maxTier: 9)) } }
    }
    func set(time t: ParentalTime?) { lock.withLock { time = t } }
    /// The answer when the home is told `library` (its own rules count then).
    func set(time t: ParentalTime, library: String) { lock.withLock { libraryTime[library] = t } }
    /// The library of each time question, in order (nil: asked without one).
    var timeAsked: [String?] { lock.withLock { timeQuestions } }
    var limitAsked: Int { lock.withLock { asked } }

    func myParental() async throws -> ParentalLimit? {
        try lock.withLock {
            asked += 1
            return try limit.get()
        }
    }
    func parentalChildren() async throws -> [ParentalChild] { lock.withLock { children } }
    func myParentalTime(library: String?) async throws -> ParentalTime? {
        lock.withLock {
            timeQuestions.append(library)
            return library.flatMap { libraryTime[$0] } ?? time
        }
    }
}

// MARK: 57.05

@Suite struct TheCallersLimit {
    @Test func aRestrictedAccountsAnswer() throws {
        let l = try JSONDecoder.home.decode(ParentalLimit.self, from: Data(ParentalFixtures.child.utf8))
        #expect(l.restricted && l.maxTier == 9 && l.unrated == "hide" && l.version == 4 && l.deviceLimit == nil)
        #expect(l.limited)
        #expect(l.reason == .account(tier: 9))
        #expect(l.reasonLine(en) == "Your account is limited to 9+.")
    }

    @Test func anUnrestrictedAccountHasNoLimitAndNoReason() throws {
        let l = try JSONDecoder.home.decode(ParentalLimit.self, from: Data(ParentalFixtures.unrestricted.utf8))
        #expect(l == ParentalLimit.none)
        #expect(!l.limited && l.reason == nil && l.reasonLine(en) == nil)
    }

    @Test func aDeviceLimitOnAnUnrestrictedAccountIsTheDevicesReason() throws {
        let l = try JSONDecoder.home.decode(ParentalLimit.self, from: Data(ParentalFixtures.deviceLimited.utf8))
        #expect(!l.restricted && l.limited && l.devicePending)
        #expect(l.reason == .device(tier: 9))
        #expect(l.reasonLine(en) == "This device is limited to 9+.")
    }

    @Test func theStricterOfTheAccountAndTheDeviceIsTheReason() throws {
        let stricter = try JSONDecoder.home.decode(ParentalLimit.self, from: Data(ParentalFixtures.childOnStricterDevice.utf8))
        #expect(stricter.reason == .device(tier: 9))
        let looser = try JSONDecoder.home.decode(ParentalLimit.self, from: Data(ParentalFixtures.childOnLooserDevice.utf8))
        #expect(looser.reason == .account(tier: 13))
    }

    @Test func tierLabelsReadInBothLanguagesWithThePlusOnItsSide() {
        #expect(AgeTier.label(0, en) == "All ages")
        #expect(AgeTier.label(13, en) == "13+")
        #expect(AgeTier.label(-1, en) == "Nothing unless allowed")
        #expect(AgeTier.label(0, ar) == "لكل الأعمار")
        // a left-to-right mark, then the plus, then the digits: «‎+13»
        #expect(AgeTier.label(13, ar) == "\u{200E}+13")
        #expect(AgeTier.contentLabel(nil, ar) == "بلا تصنيف")
        #expect(ParentalLimit(restricted: true, maxTier: 9).reasonLine(ar) == "حسابك مقيَّد بـ \(iso("\u{200E}+9")).")
    }

    @Test func everyKeyTheScreensUseHasItsEnglishAndItsArabic() {
        for key in ["parental.blocked.title", "parental.blocked.ask", "parental.blocked.pin", "parental.blocked.back",
                    "parental.blocked.a11y", "parental.profile.managed", "parental.profile.limit", "parental.player.stopped",
                    "ageSignal.profile.title", "ageSignal.limit.pending", "parental.pin.enter", "family.title",
                    "activity.kept", "parental.time.up.title", "live.parental.slate", "family.readOnlyCloud"] {
            let e = ParentalWords.text(key, en), a = ParentalWords.text(key, ar)
            #expect(e != key, "\(key) has its English")
            #expect(a != key && a != e, "\(key) has its Arabic")
        }
        #expect(ParentalWords.text("parental.blocked.title", ar) == "هذا غير متاح")
    }
}

@Suite struct TheParentalStore {
    @MainActor private func store(_ source: FakeParentalSource, admin: Bool = false) -> ParentalStore {
        let s = ParentalStore(every: .seconds(3600), sleep: { _ in try await Task.sleep(for: .seconds(3600)) },
                              now: { Date(timeIntervalSince1970: 1_000) })
        s.use(source, isAdmin: admin)
        return s
    }

    /// 57.05 T01: a change of `version` moves the epoch, which clears what the screens kept.
    @MainActor @Test func aChangedVersionMovesTheEpochAndTheFirstAnswerDoesNot() async {
        let source = FakeParentalSource()
        source.set(ParentalFixtures.child)
        let s = store(source)
        var cleared = 0
        s.onChange = { cleared += 1 }
        await s.refresh()
        #expect(s.answered && s.current.restricted && s.current.maxTier == 9)
        #expect(s.epoch == 0 && cleared == 0)
        await s.refresh()
        #expect(s.epoch == 0, "the same answer changes nothing")
        source.set(ParentalFixtures.childLoosened)
        await s.refresh()
        #expect(s.current.maxTier == 13 && s.epoch == 1 && cleared == 1)
    }

    @MainActor @Test func aHomeFromBeforeEpic57HasNoLimitAndIsNotAskedAgain() async {
        let source = FakeParentalSource()
        source.set(missing: ())
        let s = store(source)
        await s.refresh()
        await s.refresh()
        #expect(s.answered && s.current == .none && !s.canManage)
        #expect(source.limitAsked == 1)
        // an admin of such a home has no Family either: the home has none of its routes
        let admin = store(source, admin: true)
        await admin.refresh()
        #expect(!admin.canManage)
    }

    /// A limit that came while the app was closed: the device remembers the sign-in was not
    /// limited, so the first answer drops what was kept from before (the shelves on disk).
    @MainActor @Test func aLimitThatCameWhileTheAppWasClosedDropsWhatWasKept() async {
        let defaults = UserDefaults(suiteName: "parental-\(UUID().uuidString)")!
        let home = URL(string: "http://parental.test")!
        let key = ParentalStore.key(home: home, user: "u1")
        #expect(!ParentalStore.wasLimited(home: home, user: "u1", defaults: defaults))

        let source = FakeParentalSource()
        source.set(ParentalFixtures.unrestricted)
        let first = ParentalStore(defaults: defaults)
        first.use(source, isAdmin: false, key: key)
        var cleared = 0
        first.onChange = { cleared += 1 }
        await first.refresh()
        #expect(cleared == 0 && first.epoch == 0)
        #expect(!ParentalStore.wasLimited(home: home, user: "u1", defaults: defaults))

        // the next launch: a guardian restricted the account meanwhile
        source.set(ParentalFixtures.child)
        let second = ParentalStore(defaults: defaults)
        second.use(source, isAdmin: false, key: key)
        second.onChange = { cleared += 1 }
        await second.refresh()
        #expect(second.current.restricted && second.epoch == 1 && cleared == 1)
        #expect(ParentalStore.wasLimited(home: home, user: "u1", defaults: defaults), "kept for the launch after")

        // and the launch after that: limited before, limited now, nothing to drop
        let third = ParentalStore(defaults: defaults)
        third.use(source, isAdmin: false, key: key)
        third.onChange = { cleared += 1 }
        await third.refresh()
        #expect(third.epoch == 0 && cleared == 1)
    }

    /// Reads overlap (the timer, the app coming to the front, a blocked page's poll, the
    /// age signal's answer): one at a time, so an older answer never lands after a newer
    /// one. At sign-in the read from before the device's signal ("no limit") used to come
    /// back after the one from after it and put the sign-in back to unlimited.
    @MainActor @Test func anOlderAnswerNeverLandsAfterANewerOne() async {
        let source = HeldLimitSource()
        let s = ParentalStore(every: .seconds(3600), sleep: { _ in try await Task.sleep(for: .seconds(3600)) })
        s.use(source, isAdmin: false)
        // the sign-in's first read: the home has not heard the device's signal yet
        let first = Task { await s.refresh() }
        await source.waitForQuestions(1)
        // the signal was taken: the limit is read again while the first read is still out
        let second = Task { await s.refresh() }
        for _ in 0..<1_000 { await Task.yield() }
        #expect(source.questions == 1, "the second read waits for the first: one question at a time")
        source.answer(ParentalFixtures.unrestricted)
        await source.waitForQuestions(2)
        source.answer(ParentalFixtures.deviceLimited)
        await first.value
        await second.value
        #expect(s.current.limited && s.current.deviceLimit == 9, "the newer answer stands")
        #expect(source.questions == 2, "and the home was asked again after the second call, once")

        // signing out drops a read under way: the next sign-in's read does not wait for it
        let old = Task { await s.refresh() }
        await source.waitForQuestions(3)
        s.reset()
        let other = HeldLimitSource()
        s.use(other, isAdmin: false)
        let fresh = Task { await s.refresh() }
        await other.waitForQuestions(1)
        other.answer(ParentalFixtures.unrestricted)
        await fresh.value
        #expect(s.answered && !s.current.limited)
        source.answer(ParentalFixtures.child)
        await old.value
        #expect(!s.current.limited, "the last sign-in's answer changes nothing")
    }

    @MainActor @Test func aHomeThatDoesNotAnswerKeepsTheLastLimit() async {
        let source = FakeParentalSource()
        source.set(ParentalFixtures.child)
        let s = store(source)
        await s.refresh()
        source.fail()
        await s.refresh()
        #expect(s.current.restricted && s.current.maxTier == 9 && s.epoch == 0)
    }

    /// 57.07 T01: Family shows to an admin and to a guardian, never to a child or a viewer.
    @MainActor @Test func familyShowsToAdminsAndGuardiansOnly() async {
        let viewer = FakeParentalSource()
        viewer.set(ParentalFixtures.unrestricted)
        let v = store(viewer)
        await v.refresh()
        #expect(!v.canManage)

        let guardian = FakeParentalSource()
        guardian.set(ParentalFixtures.unrestricted)
        guardian.set(children: 2)
        let g = store(guardian)
        await g.refresh()
        #expect(g.canManage)

        let admin = FakeParentalSource()
        admin.set(ParentalFixtures.unrestricted)
        let a = store(admin, admin: true)
        #expect(a.canManage, "an admin manages before the home answers")
        await a.refresh()
        #expect(a.canManage)

        let child = FakeParentalSource()
        child.set(ParentalFixtures.child)
        child.set(children: 1) // never asked: a restricted account looks after no one
        let c = store(child)
        await c.refresh()
        #expect(!c.canManage)
    }

    @MainActor @Test func signingOutForgetsEverything() async {
        let source = FakeParentalSource()
        source.set(ParentalFixtures.child)
        let s = store(source)
        await s.refresh()
        s.reset()
        #expect(s.current == .none && !s.answered && !s.canManage && s.time == nil)
    }

    /// 57.15: only a restricted account's app asks for the time, and keeps when it was read.
    @MainActor @Test func theTimeIsReadForARestrictedAccountOnly() async {
        let source = FakeParentalSource()
        source.set(ParentalFixtures.child)
        source.set(time: ParentalTime(allowed: true, limited: true, remainingSeconds: 600))
        let s = store(source)
        await s.refresh()
        #expect(s.time?.state.remainingSeconds == 600)
        #expect(s.time?.at == Date(timeIntervalSince1970: 1_000))

        let other = FakeParentalSource()
        other.set(ParentalFixtures.unrestricted)
        other.set(time: ParentalTime(allowed: true, limited: true, remainingSeconds: 600))
        let o = store(other)
        await o.refresh()
        #expect(o.time == nil)
    }
}

/// A source whose limit question waits until the test answers it, oldest first.
final class HeldLimitSource: ParentalSource, @unchecked Sendable {
    private let lock = NSLock()
    private var waiting: [CheckedContinuation<ParentalLimit?, Never>] = []
    private var asked = 0

    /// How many limit questions were asked.
    var questions: Int { lock.withLock { asked } }

    func myParental() async throws -> ParentalLimit? {
        await withCheckedContinuation { c in
            lock.withLock {
                asked += 1
                waiting.append(c)
            }
        }
    }

    func parentalChildren() async throws -> [ParentalChild] { [] }
    func myParentalTime(library: String?) async throws -> ParentalTime? { nil }

    /// Waits until `n` questions were asked.
    func waitForQuestions(_ n: Int) async {
        while questions < n { await Task.yield() }
    }

    /// Answers the oldest question under way.
    func answer(_ json: String) {
        let limit = try! JSONDecoder.home.decode(ParentalLimit.self, from: Data(json.utf8))
        lock.withLock { waiting.removeFirst() }.resume(returning: limit)
    }
}

/// A sleep the test lets through one wait at a time.
final class HeldWaits: @unchecked Sendable {
    private let lock = NSLock()
    private var waiting: [CheckedContinuation<Void, Error>] = []
    private var started = 0

    /// How many waits began.
    var count: Int { lock.withLock { started } }

    func sleep(_: Duration) async throws {
        try await withCheckedThrowingContinuation { (c: CheckedContinuation<Void, Error>) in
            lock.withLock {
                started += 1
                waiting.append(c)
            }
        }
    }

    /// Ends the wait under way (waits for one to begin first).
    func pass() async {
        while lock.withLock({ waiting.isEmpty }) { await Task.yield() }
        lock.withLock { waiting.removeFirst() }.resume()
    }

    /// Ends every wait as a cancelled task's sleep ends.
    func cancel() async {
        while lock.withLock({ waiting.isEmpty }) { await Task.yield() }
        for c in lock.withLock({ let all = waiting; waiting = []; return all }) { c.resume(throwing: CancellationError()) }
    }
}

/// 57.15: the time of what plays is the playing library's, and a stop for time is never
/// followed by a resume the home did not allow for that library.
@Suite struct TheTimeOfWhatPlays {
    private let over = ParentalTime(allowed: false, limited: true, reason: "limit_reached", remainingSeconds: 0)
    private let open = ParentalTime(allowed: true, limited: true, remainingSeconds: 3600)

    /// A rule on library X is used up: asked without the library the home says allowed, asked
    /// for X it says no. The stopped video is not resumed, however long it waits.
    @MainActor @Test func aLibrarysOwnRuleThatSaysNoIsNotResumed() async {
        let source = FakeParentalSource()
        source.set(time: open)
        source.set(time: over, library: "lib-x")
        let waits = HeldWaits()
        let playing = PlayingTime(source: source, library: "lib-x", sleep: waits.sleep)
        let resumed = Task { await playing.waitUntilAllowed() }
        for _ in 0..<4 { await waits.pass() }
        await waits.cancel()
        #expect(await resumed.value == false, "the library's rule says no: nothing resumes")
        #expect(source.timeAsked.count >= 2)
        #expect(source.timeAsked.allSatisfy { $0 == "lib-x" }, "every question names the playing video's library")
        #expect(playing.reading?.state.allowed == false)
    }

    /// The home just refused: it is not asked, and nothing resumes, before one full wait. Once
    /// a parent gave time, the next question after a wait resumes.
    @MainActor @Test func nothingIsAskedOrResumedBeforeTheFirstWait() async {
        let source = FakeParentalSource()
        source.set(time: open) // even an answer that would say yes
        let waits = HeldWaits()
        let playing = PlayingTime(source: source, library: "lib-x", sleep: waits.sleep)
        let resumed = Task { await playing.waitUntilAllowed() }
        while waits.count == 0 { await Task.yield() }
        for _ in 0..<20 { await Task.yield() }
        #expect(source.timeAsked.isEmpty, "no question before the first wait ends")
        await waits.pass()
        #expect(await resumed.value == true)
        #expect(source.timeAsked == ["lib-x"])
    }

    /// Time given while it waits: no on the first question, yes on a later one.
    @MainActor @Test func timeGivenLaterResumesAtTheNextQuestion() async {
        let source = FakeParentalSource()
        source.set(time: over, library: "lib-x")
        let waits = HeldWaits()
        let playing = PlayingTime(source: source, library: "lib-x", sleep: waits.sleep)
        let resumed = Task { await playing.waitUntilAllowed() }
        await waits.pass() // the first wait
        while waits.count < 2 { await Task.yield() } // the home said no; the wait after it began
        #expect(source.timeAsked.count == 1 && playing.reading?.state.allowed == false)
        source.set(time: open, library: "lib-x")
        await waits.pass()
        #expect(await resumed.value == true)
        #expect(source.timeAsked.count == 2)
    }

    /// The app came back to the front after the first wait: the home is asked at once.
    @MainActor @Test func afterTheFirstWaitAComebackAsksAtOnce() async {
        let source = FakeParentalSource()
        source.set(time: over, library: "lib-x")
        let waits = HeldWaits()
        let playing = PlayingTime(source: source, library: "lib-x", sleep: waits.sleep)
        let first = Task { await playing.waitUntilAllowed() }
        await waits.pass()
        await waits.cancel()
        #expect(await first.value == false)
        source.set(time: open, library: "lib-x")
        let before = waits.count
        #expect(await playing.waitUntilAllowed() == true)
        #expect(waits.count == before, "no second first-wait")
    }

    /// Over the video: asked at once and after every wait, for the library, and the reading
    /// is the library's (the countdown and warnings come from it).
    @MainActor @Test func overTheVideoTheLibrarysTimeIsFollowed() async {
        let source = FakeParentalSource()
        source.set(time: open)
        source.set(time: ParentalTime(allowed: true, limited: true, remainingSeconds: 240), library: "lib-x")
        let waits = HeldWaits()
        let at = Date(timeIntervalSince1970: 5_000)
        let playing = PlayingTime(source: source, library: "lib-x", sleep: waits.sleep, now: { at })
        let following = Task { await playing.follow() }
        await waits.pass()
        await waits.cancel()
        await following.value
        #expect(source.timeAsked == ["lib-x", "lib-x"])
        #expect(TimeLeft.seconds(playing.reading, at: at.addingTimeInterval(40)) == 200)
        #expect(TimeLeft.warning(from: nil, to: TimeLeft.seconds(playing.reading, at: at)) == 300)
    }
}

@Suite(.serialized) struct AgainstAParentalStubHome {
    // MARK: 57.05 T02

    /// A 404 on a video by link, while restricted, is the blocked page and not an error.
    @MainActor @Test func aHiddenVideoIsTheBlockedPageNotAnError() async throws {
        ParentalStub.reset()
        ParentalStub.on("GET /api/videos/v-16", 404, ParentalFixtures.notFound)
        ParentalStub.on("GET /api/me/parental/guardians", 200, #"{"guardians":[{"id":"g1","username":"huda"}]}"#)
        let client = ParentalStub.client()
        let page = VideoPage(client: client, id: "v-16")
        await page.load()
        #expect(page.notAvailable && page.problem == nil && page.video == nil)

        let limit = ParentalLimit(restricted: true, maxTier: 9, version: 1)
        let blocked = BlockedPage(client: client, subject: .video("v-16"), limit: limit)
        #expect(blocked.reasonLine == ParentalWords.text("parental.blocked.account", AgeTier.label(9)))
        #expect(blocked.canAsk && !blocked.canEnterPIN, "no PIN until the home lists someone who has one")
        await blocked.load()
        #expect(blocked.canEnterPIN && blocked.unlockers.map(\.username) == ["huda"])
    }

    @MainActor @Test func aSignInLimitedOnlyByItsDeviceSeesTheReasonButCannotAsk() async {
        ParentalStub.reset()
        let limit = ParentalLimit(restricted: false, maxTier: 9, deviceLimit: 9)
        let blocked = BlockedPage(client: ParentalStub.client(), subject: .video("v-16"), limit: limit)
        await blocked.load()
        #expect(!blocked.canAsk && !blocked.canEnterPIN)
        #expect(blocked.limit.reasonLine(en) == "This device is limited to 9+.")
        await blocked.askParent()
        #expect(blocked.ask == .idle)
        #expect(ParentalStub.seen.isEmpty, "the home is not asked for what it would refuse")
    }

    /// A page made before the limit was read is made again once the home says the account
    /// is restricted (#1309: the time's-up screen never showed Ask for more time); one made
    /// with a read limit is kept, and nothing is made again for a looser or device-only one.
    @MainActor @Test func aPageMadeBeforeTheLimitWasReadIsOutdatedOnceItIs() {
        let restricted = ParentalLimit(restricted: true, maxTier: 9, version: 1)
        let unread = BlockedPage(client: ParentalStub.client(), subject: .time(minutes: 15), limit: .none)
        #expect(unread.outdated(by: restricted))
        #expect(!unread.outdated(by: .none))
        #expect(!unread.outdated(by: ParentalLimit(maxTier: 9, deviceLimit: 9)), "a device limit gives no Ask")
        let read = BlockedPage(client: ParentalStub.client(), subject: .time(minutes: 15), limit: restricted)
        #expect(!read.outdated(by: restricted))
        #expect(!read.outdated(by: ParentalLimit(restricted: true, maxTier: 12, version: 2)))
    }

    // MARK: 57.06

    @MainActor @Test func askingAParentSendsTheIdTheChildHadAndWaits() async {
        ParentalStub.reset()
        ParentalStub.on("POST /api/me/parental/requests", 201,
                        #"{"request":{"id":"r1","child_id":"c1","subject_kind":"video","subject_id":"v-16","status":"pending","created_at":"2026-11-11T10:00:00Z","expires_at":"2026-11-18T10:00:00Z"}}"#)
        let blocked = BlockedPage(client: ParentalStub.client(), subject: .video("v-16"),
                                  limit: ParentalLimit(restricted: true, maxTier: 9))
        await blocked.askParent()
        #expect(blocked.ask == .pending)
        #expect(blocked.askLine(en) == "Request sent. You'll see it here when a parent answers.")
        let sent = ParentalStub.seen("POST /api/me/parental/requests")
        #expect(sent.count == 1)
        #expect(sent.first?.json["subject_kind"] as? String == "video")
        #expect(sent.first?.json["subject_id"] as? String == "v-16")
    }

    /// Ask for more time: the kind is `time` and the id the minutes; a guardian's PIN on the
    /// child's device gives the time at once.
    @MainActor @Test func askingForMoreTimeSendsTheMinutesAndAPINGivesThem() async {
        ParentalStub.reset()
        ParentalStub.on("POST /api/me/parental/requests", 201,
                        #"{"request":{"id":"r9","child_id":"c1","subject_kind":"time","subject_id":"30","minutes":30,"status":"pending","created_at":"2026-11-11T10:00:00Z","expires_at":"2026-11-12T01:00:00Z"}}"#)
        ParentalStub.on("POST /api/me/parental/unlock", 200,
                        #"{"grant":{"id":"g1","user_id":"c1","day":"2026-11-11","minutes":30,"used_seconds":0,"granted_by":"g1","created_at":"2026-11-11T10:00:00Z"}}"#)
        let client = ParentalStub.client()
        #expect(ParentalSubject.time(minutes: 30) == ParentalSubject(kind: "time", id: "30"))
        // a child's device sends only 15, 30 or 60 minutes: the home refuses anything else
        #expect(ParentalSubject.time(minutes: 9000).id == "60" && ParentalSubject.time(minutes: 0).id == "15")
        #expect(ParentalSubject.time(minutes: 20).id == "30" && ParentalSubject.time(minutes: 45).id == "60")
        #expect(ParentalSubject.timeChoices.map { ParentalSubject.time(minutes: $0).id } == ["15", "30", "60"])
        let page = BlockedPage(client: client, subject: .time(minutes: 15), limit: ParentalLimit(restricted: true, maxTier: 9))
        let ask = page.asking(.time(minutes: 30))
        await ask.askParent()
        #expect(ask.ask == .pending)
        let sent = ParentalStub.seen("POST /api/me/parental/requests").first?.json
        #expect(sent?["subject_kind"] as? String == "time" && sent?["subject_id"] as? String == "30")

        let entry = PinEntry(client: client, subject: .time(minutes: 30), guardian: ParentalPerson(id: "g1", username: "huda"))
        [1, 2, 3, 4, 5, 6].forEach(entry.press)
        await entry.submit()
        #expect(entry.state == .unlocked && entry.line(en) == "More time given.")
        let unlock = ParentalStub.seen("POST /api/me/parental/unlock").first?.json
        #expect(unlock?["subject_kind"] as? String == "time" && unlock?["subject_id"] as? String == "30")
    }

    /// A guardian's list has a time request with its minutes; Approve gives them for today.
    @MainActor @Test func aTimeRequestIsListedWithItsMinutesAndApprovedForToday() async {
        ParentalStub.reset()
        ParentalStub.on("GET /api/parental/children", 200, #"{"children":[]}"#)
        ParentalStub.on("GET /api/parental/requests", 200,
                        #"{"requests":[{"id":"r9","child_id":"c2","subject_kind":"time","subject_id":"30","minutes":30,"status":"pending","created_at":"2026-11-11T10:00:00Z","expires_at":"2026-11-12T01:00:00Z","child":{"id":"c2","username":"Sara"},"subject":{"kind":"time","id":"30"}}]}"#)
        ParentalStub.on("GET /api/me/parental-pin", 200, #"{"set":true}"#)
        ParentalStub.on("POST /api/parental/requests/r9/approve", 200, #"{"request":{"id":"r9","status":"approved"},"grant":{"id":"g1","minutes":30}}"#)
        let family = FamilyModel(client: ParentalStub.client(), isAdmin: false)
        await family.load()
        let item = family.requests[0]
        #expect(item.request.isTime && item.request.minutes == 30)
        #expect(item.posterVideo == nil, "more time has no poster")
        #expect(item.line(en) == "Sara asks for 30 min more time")
        #expect(item.line(ar).hasPrefix("يطلب "))
        await family.approve(item, for: .always)
        #expect(ParentalStub.seen("POST /api/parental/requests/r9/approve").first?.json["expires_at"] == nil, "time is for today: no expiry sent")
        #expect(family.requests.isEmpty)
        // a time event has minutes and no subject
        let asked = ParentalEvent(id: "e", kind: "asked", at: .now, minutes: 30)
        #expect(ActivityModel.line(asked, en) == "Asked for 30 min more time")
        #expect(ActivityModel.line(ParentalEvent(id: "g", kind: "time_granted", at: .now, minutes: 15), en) == "A parent gave 15 min more time")
        #expect(ActivityModel.line(ParentalEvent(id: "g", kind: "time_granted", at: .now), en) == "A parent gave more time")
    }

    @MainActor @Test func aTwentyFirstRequestSaysTooMany() async {
        ParentalStub.reset()
        ParentalStub.on("POST /api/me/parental/requests", 429,
                        #"{"type":"https://maktaba.tv/problems/parental-too-many-requests","status":429,"code":"too_many_requests"}"#)
        let blocked = BlockedPage(client: ParentalStub.client(), subject: .video("v-16"),
                                  limit: ParentalLimit(restricted: true, maxTier: 9))
        await blocked.askParent()
        #expect(blocked.ask == .tooMany)
        #expect(blocked.askLine(ar) == "لديك طلبات كثيرة قيد الانتظار.")
    }

    @MainActor @Test func aWrongPINClearsTheDigitsAFifthLocksAndARightOneUnlocks() async {
        ParentalStub.reset()
        ParentalStub.on("POST /api/me/parental/unlock", 403,
                        #"{"type":"https://maktaba.tv/problems/parental-pin-wrong","status":403,"code":"pin_wrong"}"#)
        ParentalStub.on("POST /api/me/parental/unlock", 429,
                        #"{"type":"https://maktaba.tv/problems/parental-pin-locked","status":429,"code":"pin_locked","retry_after":900}"#,
                        headers: ["Retry-After": "900"])
        ParentalStub.on("POST /api/me/parental/unlock", 200,
                        #"{"rule":{"id":"ru1","user_id":"c1","effect":"allow","subject_kind":"video","subject_id":"v-16","expires_at":"2026-11-12T04:00:00Z","created_at":"2026-11-11T10:00:00Z"}}"#)
        let clock = Clock(Date(timeIntervalSince1970: 10_000))
        let entry = PinEntry(client: ParentalStub.client(), subject: .video("v-16"),
                             guardian: ParentalPerson(id: "g1", username: "huda")) { clock.now }
        func type(_ pin: [Int]) { pin.forEach(entry.press) }

        type([1, 2, 3, 4, 5])
        #expect(!entry.complete)
        await entry.submit()
        #expect(ParentalStub.seen.isEmpty, "five digits are not a PIN")
        type([6, 7]) // the seventh is dropped
        #expect(entry.digits == "123456" && entry.complete)
        await entry.submit()
        #expect(entry.state == .wrong && entry.digits.isEmpty)
        #expect(entry.line(en) == "That PIN isn't right.")
        let sent = ParentalStub.seen("POST /api/me/parental/unlock").first?.json
        #expect(sent?["pin"] as? String == "123456" && sent?["guardian_id"] as? String == "g1")
        #expect(sent?["subject_kind"] as? String == "video" && sent?["subject_id"] as? String == "v-16")

        type([1, 1, 1, 1, 1, 1])
        await entry.submit()
        #expect(entry.state == .locked(until: clock.now.addingTimeInterval(900)))
        #expect(entry.line(en) == "Too many tries. Try again in 15 min.")
        #expect(entry.line(ar)?.hasPrefix("محاولات كثيرة. أعد المحاولة بعد ") == true)
        #expect(!entry.canType)
        entry.press(1)
        #expect(entry.digits.isEmpty, "no key takes while PIN entry is closed")

        clock.now = clock.now.addingTimeInterval(901)
        #expect(entry.canType)
        type([6, 5, 4, 3, 2, 1])
        await entry.submit()
        #expect(entry.state == .unlocked)
        #expect(entry.line(en) == "Unlocked for today.")
        #expect(ParentalStub.seen("POST /api/me/parental/unlock").count == 3)
    }

    /// 57.06 T09: after too many tries PIN entry is closed, the line counts the time left
    /// down as it passes, and when the lock ends the pad takes keys again by itself (the
    /// state goes back to entering), without the sheet being closed and opened.
    @MainActor @Test func aLockedPadCountsDownAndOpensAgainWhenTheLockEnds() async {
        ParentalStub.reset()
        ParentalStub.on("POST /api/me/parental/unlock", 429,
                        #"{"type":"https://maktaba.tv/problems/parental-pin-locked","status":429,"code":"pin_locked","retry_after":900}"#,
                        headers: ["Retry-After": "900"])
        let start = Date(timeIntervalSince1970: 10_000)
        let clock = Clock(start)
        let slept = Clock(start) // .now: the end of the last wait asked for
        let entry = PinEntry(client: ParentalStub.client(), subject: .video("v-16"), guardian: ParentalPerson(id: "g1", username: "huda"),
                             now: { clock.now }, sleep: { d in
                                 // the wait passes: the clock is where it ends
                                 clock.now = clock.now.addingTimeInterval(TimeInterval(d.components.seconds))
                                 slept.now = clock.now
                             })
        [1, 1, 1, 1, 1, 1].forEach(entry.press)
        await entry.submit()
        #expect(entry.lockedUntil == start.addingTimeInterval(900) && !entry.canType)
        #expect(entry.line(en) == "Too many tries. Try again in 15 min.")
        // the line is drawn again as time passes: ten minutes on, five are left; then one
        #expect(entry.line(en, at: start.addingTimeInterval(600)) == "Too many tries. Try again in 5 min.")
        #expect(entry.line(en, at: start.addingTimeInterval(601)) == "Too many tries. Try again in 5 min.")
        #expect(entry.line(en, at: start.addingTimeInterval(661)) == "Too many tries. Try again in 4 min.")
        #expect(entry.line(en, at: start.addingTimeInterval(899)) == "Too many tries. Try again in 1 min.")
        #expect(entry.state == .locked(until: start.addingTimeInterval(900)), "drawing the line changes nothing")

        await entry.reopenWhenLockEnds()
        #expect(slept.now == start.addingTimeInterval(900), "it waited until the lock ended, no less")
        #expect(entry.state == .entering && entry.lockedUntil == nil && entry.canType)
        #expect(entry.line(en) == nil)
        entry.press(4)
        #expect(entry.digits == "4", "the keys take again")
    }

    /// A wait that was cancelled (the sheet closed) opens nothing; nor does a pad that is
    /// not locked wait.
    @MainActor @Test func aCancelledWaitLeavesThePadLocked() async {
        ParentalStub.reset()
        ParentalStub.on("POST /api/me/parental/unlock", 429, #"{"status":429,"code":"pin_locked","retry_after":900}"#,
                        headers: ["Retry-After": "900"])
        let clock = Clock(Date(timeIntervalSince1970: 10_000))
        let entry = PinEntry(client: ParentalStub.client(), subject: .video("v-16"), guardian: ParentalPerson(id: "g1", username: "huda"),
                             now: { clock.now }, sleep: { _ in throw CancellationError() })
        await entry.reopenWhenLockEnds()
        #expect(entry.state == .entering, "not locked: nothing to wait for")
        [1, 1, 1, 1, 1, 1].forEach(entry.press)
        await entry.submit()
        await entry.reopenWhenLockEnds()
        #expect(entry.lockedUntil != nil && !entry.canType)
    }

    // MARK: 57.04

    @MainActor private func sender(_ source: FixedAgeSignal, client: APIClient, user: String = "u1") -> (AgeSignalSender, UserDefaults) {
        let defaults = UserDefaults(suiteName: "age-signal-\(UUID().uuidString)")!
        let s = AgeSignalSender(defaults: defaults)
        s.use(source)
        s.signedIn(client: client, user: user)
        return (s, defaults)
    }

    /// T01 and the acceptance: a guardian-declared 9–12 is posted, the token refreshed, and
    /// the home's limit kept.
    @MainActor @Test func aSharedRangeIsPostedThenTheTokenIsRefreshed() async {
        ParentalStub.reset()
        ParentalStub.on("POST /api/me/age-signal", 200, #"{"tier_limit":9,"pending_looser":false,"refresh":true}"#)
        ParentalStub.on("POST /api/auth/refresh", 200, ParentalFixtures.tokens)
        let source = FixedAgeSignal(.shared(lower: 9, upper: 12, declaration: "guardian_declared"), movie: 1000, tv: 400, region: "sa")
        let (s, _) = sender(source, client: ParentalStub.client())
        var applied = 0
        s.onApplied = { applied += 1 }
        await s.sync()
        let posts = ParentalStub.seen("POST /api/me/age-signal")
        #expect(posts.count == 1)
        let body = posts.first?.json ?? [:]
        let range = body["age_range"] as? [String: Any]
        #expect(range?["lower"] as? Int == 9 && range?["upper"] as? Int == 12)
        #expect(range?["declaration"] as? String == "guardian_declared")
        #expect(body["movie_ceiling"] as? Int == 1000 && body["tv_ceiling"] as? Int == 400)
        #expect(body["region"] as? String == "SA")
        #expect(s.answer == AgeSignalAnswer(tierLimit: 9, pendingLooser: false, refresh: true))
        #expect(ParentalStub.seen("POST /api/auth/refresh").count == 1, "the next token carries the limit")
        #expect(applied == 1)
        #expect(s.rangeText(en) == "\u{200E}9–12")
        // the order: the signal, then the refresh
        #expect(ParentalStub.seen.map(\.route) == ["POST /api/me/age-signal", "POST /api/auth/refresh"])
    }

    @MainActor @Test func eachDeclarationAndAnAdultRangeAreSentAsRead() async {
        for (answer, lower, upper, declaration) in [
            (AgeRangeAnswer.shared(lower: 13, upper: 17, declaration: "self_declared"), 13 as Int?, 17 as Int?, "self_declared" as String?),
            (.shared(lower: 18, upper: nil, declaration: "confirmed"), 18, nil, "confirmed"),
            (.shared(lower: 0, upper: 8, declaration: nil), 0, 8, nil),
        ] {
            ParentalStub.reset()
            ParentalStub.on("POST /api/me/age-signal", 200, #"{"tier_limit":null,"pending_looser":false,"refresh":true}"#)
            ParentalStub.on("POST /api/auth/refresh", 200, ParentalFixtures.tokens)
            let (s, _) = sender(FixedAgeSignal(answer), client: ParentalStub.client())
            await s.sync()
            let range = ParentalStub.seen("POST /api/me/age-signal").first?.json["age_range"] as? [String: Any]
            #expect(range?["lower"] as? Int == lower)
            #expect(range?["upper"] as? Int == upper)
            #expect(range?["declaration"] as? String == declaration)
            #expect(ParentalStub.seen("POST /api/me/age-signal").first?.json["movie_ceiling"] == nil)
        }
    }

    @MainActor @Test func declinedSharingWithNoScreenTimeLimitSendsNothing() async {
        ParentalStub.reset()
        let source = FixedAgeSignal(.declined)
        let (s, _) = sender(source, client: ParentalStub.client())
        await s.sync()
        await s.sync()
        #expect(ParentalStub.seen.isEmpty)
        #expect(source.asked == 1, "asked once per sign-in")
        #expect(s.rangeText(en) == "Not shared")
    }

    /// T02: a ceiling that changes between two foregrounds is one more post, then a refresh;
    /// a foreground with nothing new sends nothing; the age range is not asked for again.
    @MainActor @Test func aCeilingThatChangesBetweenForegroundsIsPostedOnce() async {
        ParentalStub.reset()
        ParentalStub.on("POST /api/me/age-signal", 200, #"{"tier_limit":13,"pending_looser":false,"refresh":true}"#)
        ParentalStub.on("POST /api/me/age-signal", 200, #"{"tier_limit":9,"pending_looser":false,"refresh":true}"#)
        ParentalStub.on("POST /api/auth/refresh", 200, ParentalFixtures.tokens)
        let source = FixedAgeSignal(.declined, movie: 300, tv: 500)
        let (s, _) = sender(source, client: ParentalStub.client())
        await s.sync()
        await s.sync() // a foreground with nothing new
        #expect(ParentalStub.seen("POST /api/me/age-signal").count == 1)
        source.tv = 400 // the parent set TV-PG
        await s.sync()
        let posts = ParentalStub.seen("POST /api/me/age-signal")
        #expect(posts.count == 2)
        #expect(posts.last?.json["tv_ceiling"] as? Int == 400 && posts.last?.json["age_range"] == nil)
        #expect(ParentalStub.seen("POST /api/auth/refresh").count == 2)
        #expect(s.answer?.tierLimit == 9)
        #expect(source.asked == 1)
    }

    @MainActor @Test func aLooserStatementWaitsForAParent() async {
        ParentalStub.reset()
        ParentalStub.on("POST /api/me/age-signal", 200, #"{"tier_limit":9,"pending_looser":true,"refresh":true}"#)
        ParentalStub.on("POST /api/auth/refresh", 200, ParentalFixtures.tokens)
        let (s, _) = sender(FixedAgeSignal(.shared(lower: 18, upper: nil, declaration: "self_declared")), client: ParentalStub.client())
        await s.sync()
        #expect(s.answer?.pendingLooser == true && s.answer?.tierLimit == 9)
    }

    @MainActor @Test func aTokenThatNamesNoSignInIsRefreshedAndTheSignalSentAgain() async {
        ParentalStub.reset()
        ParentalStub.on("POST /api/me/age-signal", 409, #"{"type":"https://maktaba.tv/problems/conflict","status":409,"code":"no_sign_in"}"#)
        ParentalStub.on("POST /api/me/age-signal", 200, #"{"tier_limit":9,"pending_looser":false,"refresh":true}"#)
        ParentalStub.on("POST /api/auth/refresh", 200, ParentalFixtures.tokens)
        let (s, _) = sender(FixedAgeSignal(.shared(lower: 9, upper: 12, declaration: "guardian_declared")), client: ParentalStub.client())
        await s.sync()
        #expect(ParentalStub.seen.map(\.route) == ["POST /api/me/age-signal", "POST /api/auth/refresh",
                                                    "POST /api/me/age-signal", "POST /api/auth/refresh"])
        #expect(s.answer?.tierLimit == 9)
    }

    @MainActor @Test func aSecondNoSignInIsNotSentAThirdTime() async {
        ParentalStub.reset()
        ParentalStub.on("POST /api/me/age-signal", 409, #"{"status":409,"code":"no_sign_in"}"#)
        ParentalStub.on("POST /api/auth/refresh", 200, ParentalFixtures.tokens)
        let (s, _) = sender(FixedAgeSignal(.shared(lower: 9, upper: 12, declaration: nil)), client: ParentalStub.client())
        await s.sync()
        #expect(ParentalStub.seen("POST /api/me/age-signal").count == 2)
        #expect(s.answer == nil)
    }

    @MainActor @Test func aHomeFromBeforeTheRouteIsNotAskedAgain() async {
        ParentalStub.reset()
        ParentalStub.on("POST /api/me/age-signal", 404, ParentalFixtures.notFound)
        let source = FixedAgeSignal(.shared(lower: 9, upper: 12, declaration: nil))
        let (s, _) = sender(source, client: ParentalStub.client())
        await s.sync()
        source.tv = 400
        await s.sync()
        #expect(!s.supported)
        #expect(ParentalStub.seen("POST /api/me/age-signal").count == 1)
    }

    /// Story 57.04: the age band is kept in memory only. Nothing about an age is written to
    /// the device (a preferences file is unencrypted and goes into backups); what an earlier
    /// build wrote there is removed at sign-in; the next launch asks Apple again.
    @MainActor @Test func theAgeBandIsKeptInMemoryOnlyAndAnEarlierBuildsCopyIsRemoved() async {
        ParentalStub.reset()
        ParentalStub.on("POST /api/me/age-signal", 200, #"{"tier_limit":9,"pending_looser":false,"refresh":true}"#)
        ParentalStub.on("POST /api/auth/refresh", 200, ParentalFixtures.tokens)
        let client = ParentalStub.client()
        let source = FixedAgeSignal(.shared(lower: 9, upper: 12, declaration: "guardian_declared"))
        let defaults = UserDefaults(suiteName: "age-signal-\(UUID().uuidString)")!
        // what a build before this one left behind
        let legacy = AgeSignalSender.legacyKey(home: ParentalStub.home, user: "u1")
        defaults.set(try! JSONEncoder().encode(AgeRangeAnswer.shared(lower: 13, upper: 17, declaration: "self_declared")), forKey: legacy)

        let first = AgeSignalSender(defaults: defaults)
        first.use(source)
        first.signedIn(client: client, user: "u1")
        #expect(defaults.object(forKey: legacy) == nil, "the earlier build's copy is removed")
        #expect(first.range == nil, "and not read: Apple is asked")
        await first.sync()
        #expect(first.range == .shared(lower: 9, upper: 12, declaration: "guardian_declared"))
        let written = defaults.dictionaryRepresentation().keys.filter { $0.lowercased().contains("agesignal") }
        #expect(written.isEmpty, "nothing about an age is written to the device: \(written)")

        // the next launch: nothing was kept, so Apple is asked again and the same range sent
        let second = AgeSignalSender(defaults: defaults)
        second.use(source)
        second.signedIn(client: client, user: "u1")
        #expect(second.range == nil)
        await second.sync()
        #expect(source.asked == 2)
        #expect(ParentalStub.seen("POST /api/me/age-signal").count == 2)

        // Profile > Age range asks again
        source.answer = .declined
        await second.sync(askAgain: true)
        #expect(source.asked == 3 && second.range == .declined)

        second.signedOut(forget: true)
        #expect(second.range == nil, "a new sign-in asks afresh")
    }

    /// "Not available" is not an answer (the first sync of a launch can run before there is
    /// a window to ask from): the range is asked for again at the next sync, and an answer
    /// already had is kept over a later "not available".
    @MainActor @Test func anAgeRangeThatCouldNotBeAskedForIsAskedForAgain() async {
        ParentalStub.reset()
        ParentalStub.on("POST /api/me/age-signal", 200, #"{"tier_limit":9,"pending_looser":false,"refresh":true}"#)
        ParentalStub.on("POST /api/auth/refresh", 200, ParentalFixtures.tokens)
        let source = FixedAgeSignal(.notAvailable)
        let (s, _) = sender(source, client: ParentalStub.client())
        await s.sync()
        #expect(source.asked == 1 && s.range == nil, "nothing was asked: nothing is kept")
        #expect(ParentalStub.seen.isEmpty)
        // the app came to the front, with a window now
        source.answer = .shared(lower: 9, upper: 12, declaration: "guardian_declared")
        await s.sync()
        #expect(source.asked == 2, "asked again in the same launch")
        #expect(s.range == .shared(lower: 9, upper: 12, declaration: "guardian_declared"))
        #expect(ParentalStub.seen("POST /api/me/age-signal").count == 1)
        // an answer is not asked for again by itself, and "not available" does not replace it
        await s.sync()
        #expect(source.asked == 2)
        source.answer = .notAvailable
        await s.sync(askAgain: true)
        #expect(source.asked == 3 && s.range == .shared(lower: 9, upper: 12, declaration: "guardian_declared"))
    }

    /// Profile > Share age range again, tapped while a sync is under way (the one the app
    /// coming to the front started): the ask is not dropped, the pass that follows makes it.
    @MainActor @Test func askingForTheAgeRangeAgainDuringASyncIsNotDropped() async {
        ParentalStub.reset()
        ParentalStub.on("POST /api/me/age-signal", 200, #"{"tier_limit":9,"pending_looser":false,"refresh":true}"#)
        ParentalStub.on("POST /api/auth/refresh", 200, ParentalFixtures.tokens)
        let source = HeldAgeSignal()
        let s = AgeSignalSender(defaults: UserDefaults(suiteName: "age-signal-\(UUID().uuidString)")!)
        s.use(source)
        s.signedIn(client: ParentalStub.client(), user: "u1")
        let foreground = Task { await s.sync() }
        while source.asked < 1 { await Task.yield() }
        // the first pass waits on Apple; the person taps "Share age range again"
        await s.sync(askAgain: true)
        #expect(source.asked == 1, "one question at a time")
        source.answer(.declined)
        // nothing is sent for "declined", so the next question (if any) comes without a wait
        var turns = 0
        while source.asked < 2, turns < 100_000 {
            await Task.yield()
            turns += 1
        }
        #expect(source.asked == 2, "the tap's question was asked once the first pass ended")
        if source.asked == 2 { source.answer(.shared(lower: 13, upper: 17, declaration: "self_declared")) }
        await foreground.value
        #expect(s.range == .shared(lower: 13, upper: 17, declaration: "self_declared"))
        let range = ParentalStub.seen("POST /api/me/age-signal").last?.json["age_range"] as? [String: Any]
        #expect(range?["lower"] as? Int == 13, "and what it answered was sent")
    }

    // MARK: 57.07

    @MainActor @Test func aGuardiansFamilyListsTheirChildrenByNameAndAnswersRequests() async {
        ParentalStub.reset()
        ParentalStub.on("GET /api/parental/children", 200,
                        #"{"children":[{"id":"c2","username":"Sara","user_id":"c2","max_tier":13,"unrated":"hide","allow_only":false,"version":3,"updated_at":"2026-11-10T10:00:00Z"},{"id":"c1","username":"omar","user_id":"c1","max_tier":0,"unrated":"hide","allow_only":true,"version":1,"updated_at":"2026-11-10T10:00:00Z"}]}"#)
        ParentalStub.on("GET /api/parental/requests", 200,
                        #"{"requests":[{"id":"r1","child_id":"c2","subject_kind":"video","subject_id":"v-16","status":"pending","created_at":"2026-11-11T10:00:00Z","expires_at":"2026-11-18T10:00:00Z","child":{"id":"c2","username":"Sara"},"subject":{"kind":"video","id":"v-16","title":"The long night","poster_url":"/api/videos/v-16/poster","tier":16}},{"id":"r2","child_id":"c2","subject_kind":"series","subject_id":"s1","status":"pending","created_at":"2026-11-11T09:00:00Z","expires_at":"2026-11-18T09:00:00Z","child":{"id":"c2","username":"Sara"},"subject":{"kind":"series","id":"s1","hidden":true}}]}"#)
        ParentalStub.on("GET /api/me/parental-pin", 200, #"{"set":false}"#)
        ParentalStub.on("POST /api/parental/requests/r1/approve", 200, #"{"request":{"id":"r1","status":"approved"}}"#)
        ParentalStub.on("POST /api/parental/requests/r2/deny", 410, #"{"status":410,"code":"request_expired"}"#)
        let family = FamilyModel(client: ParentalStub.client(), isAdmin: false)
        await family.load()
        #expect(family.children.value?.map(\.username) == ["omar", "Sara"])
        #expect(family.children.value?.last?.policy.maxTier == 13)
        #expect(family.waiting(for: "c2") == 2 && family.waiting(for: "c1") == 0)
        #expect(family.pinSet == false && !family.isAdmin)
        #expect(family.requests.first?.subject.displayTitle(en) == "The long night")
        #expect(family.requests.last?.subject.displayTitle(en) == "Series", "a subject this guardian cannot open has no title")
        #expect(ParentalStub.seen("GET /api/parental/requests").first?.query == "status=pending")
        // a request's row shows the poster of a video the guardian may see, and nothing else's
        #expect(family.requests.first?.posterVideo == "v-16")
        #expect(family.requests.last?.posterVideo == nil, "a hidden series has no poster")

        await family.approve(family.requests[0], for: .always)
        #expect(family.requests.map(\.id) == ["r2"])
        #expect(ParentalStub.seen("POST /api/parental/requests/r1/approve").first?.json["expires_at"] == nil)
        // gone meanwhile: it leaves the list, and nothing is said
        await family.deny(family.requests[0])
        #expect(family.requests.isEmpty && family.notice == nil)
    }

    @Test func approvalsEndTodayAtFourThisWeekInSevenDaysOrNever() {
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = TimeZone(identifier: "UTC")!
        let afternoon = homeTime("2026-11-11T16:00:00Z")!
        #expect(ApprovalLength.always.expiresAt(from: afternoon, calendar: calendar) == nil)
        #expect(ApprovalLength.week.expiresAt(from: afternoon, calendar: calendar) == afternoon.addingTimeInterval(7 * 86400))
        // 04:00 the next morning
        #expect(ApprovalLength.today.expiresAt(from: afternoon, calendar: calendar) == afternoon.addingTimeInterval(12 * 3600))
        let night = afternoon.addingTimeInterval(10 * 3600) // 02:00: the day line is two hours away
        #expect(ApprovalLength.today.expiresAt(from: night, calendar: calendar) == night.addingTimeInterval(2 * 3600))
    }

    /// What the home is told for each length: Today is the home's own day's end.
    @Test func todayIsTheHomesDayEndNotThisDevicesClock() {
        let now = homeTime("2026-11-11T22:30:00Z")!
        #expect(ApprovalLength.today.ending(from: now) == .dayEnd)
        #expect(ApprovalLength.week.ending(from: now) == .at(now.addingTimeInterval(7 * 86400)))
        #expect(ApprovalLength.always.ending(from: now) == .never)
        #expect(ParentalEnding.dayEnd.body["until"] as? String == "day_end" && ParentalEnding.dayEnd.body["expires_at"] == nil)
        #expect(ParentalEnding.at(now).body["expires_at"] as? String == "2026-11-11T22:30:00Z" && ParentalEnding.at(now).body["until"] == nil)
        #expect(ParentalEnding.never.body.isEmpty)
        // the fallback's day line is the home's: 22:30 UTC is 01:30 in Riyadh, so the day
        // ends in two and a half hours there (a device on UTC would have said 04:00 UTC)
        let riyadh = ApprovalLength.homeCalendar(timeZone: "Asia/Riyadh")
        #expect(ApprovalLength.today.expiresAt(from: now, calendar: riyadh) == homeTime("2026-11-12T01:00:00Z"))
        #expect(ApprovalLength.homeCalendar(timeZone: nil).timeZone == Calendar(identifier: .gregorian).timeZone)
        #expect(ApprovalLength.homeCalendar(timeZone: "Not/AZone").timeZone == Calendar(identifier: .gregorian).timeZone)
    }

    private static let oneRequest = #"{"requests":[{"id":"r1","child_id":"c2","subject_kind":"video","subject_id":"v-16","status":"pending","created_at":"2026-11-11T10:00:00Z","expires_at":"2026-11-18T10:00:00Z","child":{"id":"c2","username":"Sara"},"subject":{"kind":"video","id":"v-16","title":"The long night","poster_url":"/api/videos/v-16/poster","tier":16}}]}"#

    @MainActor private func familyWithOneRequest() async -> FamilyModel {
        ParentalStub.on("GET /api/parental/children", 200, #"{"children":[]}"#)
        ParentalStub.on("GET /api/parental/requests", 200, Self.oneRequest)
        ParentalStub.on("GET /api/me/parental-pin", 200, #"{"set":false}"#)
        let family = FamilyModel(client: ParentalStub.client(), isAdmin: false)
        await family.load()
        return family
    }

    /// Approve for Today sends `until: day_end` and no time of this device's.
    @MainActor @Test func approvingForTodayLetsTheHomeWorkOutItsDaysEnd() async {
        ParentalStub.reset()
        ParentalStub.on("POST /api/parental/requests/r1/approve", 200, #"{"request":{"id":"r1","status":"approved"}}"#)
        let family = await familyWithOneRequest()
        await family.approve(family.requests[0], for: .today)
        let sent = ParentalStub.seen("POST /api/parental/requests/r1/approve")
        #expect(sent.count == 1)
        #expect(sent.first?.json["until"] as? String == "day_end")
        #expect(sent.first?.json["expires_at"] == nil, "no time from this device's clock")
        #expect(family.requests.isEmpty && family.notice == nil)
        #expect(ParentalStub.seen("GET /api/parental/users/c2/time-rules").isEmpty, "the home's zone is not needed")
    }

    /// A home from before `until` refuses it: the day's end is then worked out in the home's
    /// time zone and sent as `expires_at`.
    @MainActor @Test func aHomeThatDoesNotTakeUntilGetsTheDaysEndInItsOwnZone() async {
        for (status, body) in [(422, #"{"status":422,"errors":[{"field":"until","message":"must be day_end"}]}"#),
                               (400, #"{"status":400,"title":"invalid json","detail":"json: unknown field \"until\""}"#)] {
            ParentalStub.reset()
            ParentalStub.on("POST /api/parental/requests/r1/approve", status, body)
            ParentalStub.on("POST /api/parental/requests/r1/approve", 200, #"{"request":{"id":"r1","status":"approved"}}"#)
            ParentalStub.on("GET /api/parental/users/c2/time-rules", 200, #"{"rules":[],"time_zone":"Pacific/Kiritimati"}"#)
            let family = await familyWithOneRequest()
            let before = Date()
            await family.approve(family.requests[0], for: .today)
            let sent = ParentalStub.seen("POST /api/parental/requests/r1/approve")
            #expect(sent.count == 2)
            #expect(sent.first?.json["until"] as? String == "day_end")
            #expect(sent.last?.json["until"] == nil)
            let end = (sent.last?.json["expires_at"] as? String).flatMap(homeTime)
            #expect(end != nil, "an instant is sent: never an approval with no end")
            if let end {
                // 04:00 on the home's clock (UTC+14, as far from any test machine as a zone gets)
                let home = ApprovalLength.homeCalendar(timeZone: "Pacific/Kiritimati")
                let parts = home.dateComponents([.hour, .minute], from: end)
                #expect(parts.hour == 4 && parts.minute == 0)
                #expect(end > before && end <= before.addingTimeInterval(86400 + 1))
            }
            #expect(family.requests.isEmpty && family.notice == nil)
        }
    }

    /// For a week and for good never use `until`, and a refusal of them is said, not retried.
    @MainActor @Test func aRefusedApprovalThatIsNotForTodayIsSaid() async {
        ParentalStub.reset()
        ParentalStub.on("POST /api/parental/requests/r1/approve", 422, #"{"status":422}"#)
        let family = await familyWithOneRequest()
        await family.approve(family.requests[0], for: .week)
        let sent = ParentalStub.seen("POST /api/parental/requests/r1/approve")
        #expect(sent.count == 1 && sent.first?.json["until"] == nil && sent.first?.json["expires_at"] != nil)
        #expect(family.notice != nil && family.requests.count == 1)
    }

    /// The home moves the limit's version when a rule is added or removed: the page reads
    /// the account again, so the guardian's next change of the limit sends that version and
    /// is not refused as someone else's (409 `stale`).
    @MainActor @Test func afterARuleChangeTheLimitIsSentWithTheHomesNewVersion() async {
        ParentalStub.reset()
        // every answer of the route is set before the first call: read at load (version 3),
        // again after the rule was added (4), again after one was removed (5)
        ParentalStub.on("GET /api/users/c2/parental", 200, Self.account)
        ParentalStub.on("GET /api/users/c2/parental", 200, Self.account.replacingOccurrences(of: #""version":3"#, with: #""version":4"#))
        ParentalStub.on("GET /api/users/c2/parental", 200, Self.account.replacingOccurrences(of: #""version":3"#, with: #""version":5"#))
        ParentalStub.on("GET /api/users/c2/parental/rules", 200,
                        #"{"rules":[{"id":"ru2","user_id":"c2","effect":"block","subject_kind":"video","subject_id":"v9","created_at":"2026-11-10T10:00:00Z"}]}"#)
        ParentalStub.on("POST /api/users/c2/parental/rules", 201,
                        #"{"id":"ru3","user_id":"c2","effect":"allow","subject_kind":"series","subject_id":"s1","created_at":"2026-11-11T10:00:00Z"}"#)
        ParentalStub.on("DELETE /api/users/c2/parental/rules/ru2", 204, "")
        ParentalStub.on("PUT /api/users/c2/parental", 200, Self.account.replacingOccurrences(of: #""version":3"#, with: #""version":6"#))
        let m = ChildModel(client: ParentalStub.client(), child: ParentalPerson(id: "c2", username: "Sara"))
        await m.load()
        #expect(m.policy?.version == 3)

        await m.addRule("allow", ParentalSubject(kind: "series", id: "s1"))
        #expect(m.policy?.version == 4, "read again after the rule was added")
        await m.setLimit(maxTier: 13)
        #expect(ParentalStub.seen("PUT /api/users/c2/parental").last?.json["version"] as? Int == 4)
        #expect(m.notice == nil)

        await m.remove(m.rules("block")[0])
        #expect(m.policy?.version == 5, "read again after the rule was removed")
        await m.setLimit(unrated: "tier_9")
        #expect(ParentalStub.seen("PUT /api/users/c2/parental").last?.json["version"] as? Int == 5)
        #expect(m.rules("allow").map(\.id) == ["ru3"] && m.rules("block").isEmpty && m.notice == nil)
    }

    static let account = #"{"policy":{"user_id":"c2","max_tier":9,"unrated":"hide","allow_only":false,"version":3,"updated_at":"2026-11-10T10:00:00Z"},"access":"guardian","guardians":[{"id":"g1","username":"huda","created_at":"2026-11-01T10:00:00Z"}],"signals":[{"refresh_family_id":"f1","lower":9,"upper":12,"declaration":"guardian_declared","tv_ceiling":400,"region":"US","tier_limit":9,"pending_looser":{"lower":18,"tier_limit":null,"observed_at":"2026-11-11T09:00:00Z"},"observed_at":"2026-11-10T09:00:00Z"}]}"#

    @MainActor private func childModel() async -> ChildModel {
        ParentalStub.on("GET /api/users/c2/parental", 200, Self.account)
        ParentalStub.on("GET /api/users/c2/parental/rules", 200,
                        #"{"rules":[{"id":"ru1","user_id":"c2","effect":"allow","subject_kind":"library","subject_id":"lib-1","created_at":"2026-11-10T10:00:00Z"},{"id":"ru2","user_id":"c2","effect":"block","subject_kind":"video","subject_id":"v9","expires_at":"2026-11-12T04:00:00Z","created_at":"2026-11-10T10:00:00Z"}]}"#)
        ParentalStub.on("GET /api/parental/users/c2/time-rules", 200,
                        #"{"rules":[{"id":"t1","days":127,"slot_start":"07:00","slot_end":"20:00","daily_minutes":60}],"time_zone":"Asia/Riyadh"}"#)
        ParentalStub.on("GET /api/parental/users/c2/time-usage", 200,
                        #"{"from":"2026-11-05","to":"2026-11-11","time_zone":"Asia/Riyadh","today":"2026-11-11","usage":[{"day":"2026-11-11","library_id":"lib-1","seconds":1500},{"day":"2026-11-11","library_id":"lib-2","seconds":900},{"day":"2026-11-10","library_id":"lib-1","seconds":3600}],"grants":[{"id":"gr1","user_id":"c2","day":"2026-11-11","minutes":30,"used_seconds":0,"created_at":"2026-11-11T15:00:00Z"}]}"#)
        ParentalStub.on("GET /api/libraries/summary", 200, #"{"items":[]}"#)
        let m = ChildModel(client: ParentalStub.client(), child: ParentalPerson(id: "c2", username: "Sara"))
        await m.load()
        return m
    }

    /// 57.07 T02: a guardian's child page has the limit, rules, devices and time, and no
    /// admin's controls.
    @MainActor @Test func aChildsPageReadsTheLimitTheRulesTheDevicesAndTheTime() async {
        ParentalStub.reset()
        let m = await childModel()
        #expect(m.policy?.maxTier == 9 && m.policy?.version == 3)
        #expect(!m.isAdmin, "a guardian gets no Guardians and no Lift restriction")
        #expect(m.account.value?.guardians.map(\.username) == ["huda"])
        let signal = m.account.value?.signals.first
        #expect(signal?.id == "f1" && signal?.tierLimit == 9 && signal?.fromAgeRange == true && signal?.fromScreenTime == true)
        #expect(signal?.pendingLooser != nil && signal?.pendingLooser?.tierLimit == nil, "a looser statement waits: no limit at all")
        #expect(m.rules("allow").map(\.id) == ["ru1"] && m.rules("block").map(\.id) == ["ru2"])
        #expect(m.rules("block").first?.expiresAt != nil)
        #expect(m.timeRules.first?.slotStart == "07:00" && m.timeRules.first?.dailyMinutes == 60 && m.timeZone == "Asia/Riyadh")
        #expect(m.usage?.seconds(on: "2026-11-11") == 2400 && m.usage?.granted(on: "2026-11-11") == 30)
    }

    @MainActor @Test func settingTheLimitSendsTheVersionLastRead() async {
        ParentalStub.reset()
        let m = await childModel()
        ParentalStub.on("PUT /api/users/c2/parental", 200, Self.account.replacingOccurrences(of: #""max_tier":9"#, with: #""max_tier":13"#)
            .replacingOccurrences(of: #""version":3"#, with: #""version":4"#))
        await m.setLimit(maxTier: 13)
        let sent = ParentalStub.seen("PUT /api/users/c2/parental").first?.json
        #expect(sent?["max_tier"] as? Int == 13 && sent?["version"] as? Int == 3)
        #expect(sent?["unrated"] as? String == "hide" && sent?["allow_only"] as? Bool == false)
        #expect(m.policy?.maxTier == 13 && m.policy?.version == 4 && m.notice == nil)
    }

    /// Two guardians edit at once: the second write is 409, the page reads again and says so.
    @MainActor @Test func aChangeOverSomeoneElsesReadsThePageAgainAndSaysSo() async {
        ParentalStub.reset()
        let m = await childModel()
        ParentalStub.on("PUT /api/users/c2/parental", 409, #"{"status":409,"code":"stale"}"#)
        let before = ParentalStub.seen("GET /api/users/c2/parental").count
        await m.setLimit(maxTier: 16)
        #expect(m.notice == .conflict)
        #expect(m.notice?.line(en) == "Someone else changed this. Showing the latest.")
        #expect(ParentalStub.seen("GET /api/users/c2/parental").count == before + 1)
        #expect(m.policy?.maxTier == 9)
    }

    /// A play-only sign-in through Maktaba Cloud: the home refuses the write, the page says why.
    @MainActor @Test func aRefusedWriteSaysTheScreenIsReadOnly() async {
        ParentalStub.reset()
        let m = await childModel()
        ParentalStub.on("POST /api/users/c2/parental/rules", 403, #"{"status":403,"code":"admin_only"}"#)
        await m.addRule("allow", ParentalSubject(kind: "series", id: "s1"))
        #expect(m.notice == .readOnly)
        #expect(m.notice?.line(en) == "Connect at home, or ask an admin for manage access, to change this.")
        #expect(m.rules.count == 2)
    }

    /// 57.07 T05: Devices > Approve calls the clear route for that sign-in.
    @MainActor @Test func approvingADevicesLooserLimitClearsItsSignal() async throws {
        ParentalStub.reset()
        let m = await childModel()
        ParentalStub.on("POST /api/users/c2/parental/signals/f1/clear", 200, #"{"tier_limit":null,"removed":false}"#)
        let signal = try #require(m.account.value?.signals.first)
        await m.clear(signal)
        #expect(ParentalStub.seen("POST /api/users/c2/parental/signals/f1/clear").count == 1)
        #expect(m.notice == nil)
    }

    @MainActor @Test func rulesAreAddedAndRemovedAndTimeIsGivenAndSaved() async {
        ParentalStub.reset()
        let m = await childModel()
        ParentalStub.on("POST /api/users/c2/parental/rules", 201,
                        #"{"id":"ru3","user_id":"c2","effect":"allow","subject_kind":"series","subject_id":"s1","created_at":"2026-11-11T10:00:00Z"}"#)
        ParentalStub.on("DELETE /api/users/c2/parental/rules/ru2", 204, "")
        ParentalStub.on("POST /api/parental/users/c2/time-grants", 201, #"{"id":"gr2","minutes":30}"#)
        ParentalStub.on("PUT /api/parental/users/c2/time-rules", 200, #"{"rules":[{"id":"t9","days":65,"daily_minutes":120}],"time_zone":"Asia/Riyadh"}"#)
        await m.addRule("allow", ParentalSubject(kind: "series", id: "s1"))
        #expect(m.rules("allow").map(\.id) == ["ru1", "ru3"])
        let added = ParentalStub.seen("POST /api/users/c2/parental/rules").first?.json
        #expect(added?["effect"] as? String == "allow" && added?["subject_kind"] as? String == "series" && added?["expires_at"] == nil)
        await m.remove(m.rules("block")[0])
        #expect(m.rules("block").isEmpty)
        await m.grant(minutes: 30)
        #expect(ParentalStub.seen("POST /api/parental/users/c2/time-grants").first?.json["minutes"] as? Int == 30)

        var weekend = ParentalTimeRule(days: 0, dailyMinutes: 120)
        weekend.set(day: 0, true) // Sunday
        weekend.set(day: 6, true) // Saturday
        #expect(weekend.days == 65 && weekend.covers(day: 6) && !weekend.covers(day: 1))
        #expect(await m.saveTimeRules([weekend]))
        let put = ParentalStub.seen("PUT /api/parental/users/c2/time-rules").first?.json["rules"] as? [[String: Any]]
        #expect(put?.count == 1 && put?.first?["days"] as? Int == 65 && put?.first?["daily_minutes"] as? Int == 120)
        #expect(put?.first?["id"] == nil && put?.first?["slot_start"] == nil, "a new rule has no id and no empty slot")
        #expect(m.timeRules.map(\.id) == ["t9"])

        // a rule the home would refuse (neither hours nor a limit) is never sent
        let empty = ParentalTimeRule(days: 65)
        #expect(await m.saveTimeRules([weekend, empty]) == false)
        #expect(ParentalStub.seen("PUT /api/parental/users/c2/time-rules").count == 1, "nothing more was sent")
        #expect(m.timeRules.map(\.id) == ["t9"], "and the rules stay as the home has them")
    }

    @MainActor @Test func settingAPINNeedsSixDigitsAndTheRightPassword() async {
        ParentalStub.reset()
        ParentalStub.on("PUT /api/me/parental-pin", 403, #"{"status":403,"code":"password_wrong"}"#)
        ParentalStub.on("PUT /api/me/parental-pin", 204, "")
        let family = FamilyModel(client: ParentalStub.client(), isAdmin: false)
        #expect(await family.setPIN("12345", password: "pw") == false)
        #expect(await family.setPIN("12a456", password: "pw") == false)
        #expect(ParentalStub.seen.isEmpty, "a PIN that is not six digits is never sent")
        #expect(await family.setPIN("123456", password: "wrong") == false)
        #expect(family.notice == .wrongPassword && family.pinSet != true)
        // typed on an Arabic keyboard: sent as Western digits
        #expect(await family.setPIN("١٢٣٤٥٦", password: "right"))
        #expect(family.pinSet == true)
        #expect(ParentalStub.seen("PUT /api/me/parental-pin").last?.json["pin"] as? String == "123456")
    }

    // MARK: 57.08

    @MainActor @Test func activityIsReadByKindAndPagedByCursor() async {
        ParentalStub.reset()
        ParentalStub.on("GET /api/users/c2/parental/activity", 200,
                        #"{"events":[{"id":"e3","kind":"watched","at":"2026-11-11T15:00:00Z","subject":{"kind":"video","id":"v1","title":"Seerah 1","tier":0}},{"id":"e2","kind":"denied","at":"2026-11-11T09:00:00Z","subject":{"kind":"video","id":"v2","title":"Night"},"guardian":{"id":"g1","username":"huda"}}],"next_cursor":"c-2"}"#)
        ParentalStub.on("GET /api/users/c2/parental/activity", 200,
                        #"{"events":[{"id":"e1","kind":"blocked","at":"2026-11-10T20:00:00Z","subject":{"kind":"video","id":"v3","removed":true}}]}"#)
        ParentalStub.on("GET /api/users/c2/parental/activity", 200, #"{"events":[]}"#)
        let m = ActivityModel(client: ParentalStub.client(), child: ParentalPerson(id: "c2", username: "Sara"))
        await m.load()
        #expect(m.events.value?.map(\.id) == ["e3", "e2"] && m.next == "c-2")
        await m.more()
        #expect(m.events.value?.map(\.id) == ["e3", "e2", "e1"] && m.next == nil)
        #expect(ParentalStub.seen("GET /api/users/c2/parental/activity").map(\.query) == ["", "cursor=c-2"])
        await m.show(.blocked)
        #expect(ParentalStub.seen("GET /api/users/c2/parental/activity").last?.query == "kind=blocked")
        #expect(m.events.value?.isEmpty == true)
    }

    /// More, tapped twice: the page is read once and its rows are listed once.
    @MainActor @Test func aSecondTapOnMoreDoesNotAddThePageTwice() async {
        ParentalStub.reset()
        ParentalStub.on("GET /api/users/c2/parental/activity", 200,
                        #"{"events":[{"id":"e3","kind":"watched","at":"2026-11-11T15:00:00Z"}],"next_cursor":"c-2"}"#)
        ParentalStub.on("GET /api/users/c2/parental/activity", 200,
                        #"{"events":[{"id":"e2","kind":"blocked","at":"2026-11-10T20:00:00Z"}],"next_cursor":"c-3"}"#)
        // (were the cursor sent twice, the home would answer the same page again)
        ParentalStub.on("GET /api/users/c2/parental/activity", 200,
                        #"{"events":[{"id":"e2","kind":"blocked","at":"2026-11-10T20:00:00Z"}],"next_cursor":"c-3"}"#)
        let m = ActivityModel(client: ParentalStub.client(), child: ParentalPerson(id: "c2", username: "Sara"))
        await m.load()
        async let first: Void = m.more()
        async let second: Void = m.more()
        _ = await (first, second)
        #expect(m.events.value?.map(\.id) == ["e3", "e2"], "each row once")
        #expect(ParentalStub.seen("GET /api/users/c2/parental/activity").map(\.query) == ["", "cursor=c-2"], "the page was asked for once")
        #expect(!m.readingMore && m.next == "c-3")
        // and a page the home repeats adds nothing
        await m.more()
        #expect(m.events.value?.map(\.id) == ["e3", "e2"])
    }

    /// Requests are picked on the device from pages of every kind: with newer pages that
    /// hold none, the reading goes on to the first request rather than say "nothing yet"
    /// above a More button.
    @MainActor @Test func theRequestsFilterReadsOnToTheFirstRequest() async {
        ParentalStub.reset()
        func page(_ ids: [String], kind: String, next: String?) -> String {
            let events = ids.map { #"{"id":"\#($0)","kind":"\#(kind)","at":"2026-11-11T15:00:00Z"}"# }.joined(separator: ",")
            return #"{"events":[\#(events)]\#(next.map { #","next_cursor":"\#($0)""# } ?? "")}"#
        }
        let route = "GET /api/users/c2/parental/activity"
        ParentalStub.on(route, 200, page(["w1", "w2"], kind: "watched", next: "c-2"))   // load (All)
        ParentalStub.on(route, 200, page(["w1", "w2"], kind: "watched", next: "c-2"))   // Requests: nothing here
        ParentalStub.on(route, 200, page(["w3", "w4"], kind: "watched", next: "c-3"))   // nor here
        ParentalStub.on(route, 200, page(["a1"], kind: "asked", next: "c-4"))           // the first request
        ParentalStub.on(route, 200, page(["d1"], kind: "denied", next: nil))            // More
        let m = ActivityModel(client: ParentalStub.client(), child: ParentalPerson(id: "c2", username: "Sara"))
        await m.load()
        #expect(ParentalStub.seen(route).count == 1, "All shows every kind: one page is enough")
        await m.show(.requests)
        let utc = Calendar(identifier: .gregorian)
        #expect(m.days(calendar: utc).flatMap(\.events).map(\.id) == ["a1"], "read on to the first request")
        #expect(ParentalStub.seen(route).map(\.query) == ["", "", "cursor=c-2", "cursor=c-3"])
        #expect(m.next == "c-4", "and no further: More reads the rest")
        await m.more()
        #expect(Set(m.days(calendar: utc).flatMap(\.events).map(\.id)) == ["a1", "d1"] && m.next == nil)
    }

    // MARK: what a rule may name, a play-only sign-in

    /// 57.07: a collection, a YouTube source and a plugin can be picked for a rule, each from
    /// what the guardian's own account sees, by name in the app's language's order.
    @MainActor @Test func aRuleMayNameACollectionAYouTubeSourceOrAPlugin() async throws {
        ParentalStub.reset()
        ParentalStub.on("GET /api/libraries/summary", 200,
                        #"{"items":[{"id":"l1","name":"Lectures","videos":3},{"id":"l2","name":"Docs","videos":1}]}"#)
        ParentalStub.on("GET /api/libraries/l1/collections", 200,
                        #"{"items":[{"kind":"curated","id":"c1","name":"Seerah"},{"kind":"topic","id":"t1","name":"A topic"},{"kind":"youtube","id":"c2","name":"Fiqh"}]}"#)
        ParentalStub.on("GET /api/libraries/l2/collections", 200, #"{"items":[{"kind":"clip","id":"k1","name":"Clips"}]}"#)
        ParentalStub.on("GET /api/libraries/l1/youtube/sources", 200,
                        #"{"items":[{"id":"y1","library_id":"l1","kind":"channel","url":"https://youtube.example/@seerah","folder":"Seerah channel"},{"id":"y2","library_id":"l1","kind":"playlist","url":"https://youtube.example/playlist?list=PL1","folder":null}]}"#)
        ParentalStub.on("GET /api/libraries/l2/youtube/sources", 200, #"{"items":[]}"#)
        ParentalStub.on("GET /api/plugins", 200,
                        #"{"plugins":[{"id":"com.example.intro","names":{"name":{"en":"Intro skipper","ar":"متخطي المقدمة"}}},{"id":"com.example.bare","names":{"name":{}},"source":"file"},{"id":"tv.maktaba.subtitles","names":{"name":{"en":"Subtitle files"}},"source":"release"}],"policy":{},"core":[]}"#)
        let client = ParentalStub.client()

        let collections = try await RuleChoices.load(client, kind: "collection", en)
        #expect(collections.map(\.id) == ["c2", "c1"], "the home's collections only, by title; topic and clip groups are not rules' subjects")
        #expect(collections.map(\.title) == ["Lectures · Fiqh", "Lectures · Seerah"])

        let sources = try await RuleChoices.load(client, kind: "youtube_source", en)
        #expect(sources.map(\.id) == ["y2", "y1"])
        #expect(sources.map(\.title) == ["Lectures · https://youtube.example/playlist?list=PL1", "Lectures · Seerah channel"],
                "a source by its folder, else its address")

        let plugins = try await RuleChoices.load(client, kind: "plugin", ar)
        #expect(plugins.first { $0.id == "com.example.intro" }?.title == "متخطي المقدمة", "a plugin by its name in the app's language")
        #expect(plugins.first { $0.id == "com.example.bare" }?.title == "com.example.bare", "one with no name by its id")
        #expect(try await RuleChoices.load(client, kind: "plugin", en).map(\.title) == ["com.example.bare", "Intro skipper"])
        #expect(!plugins.contains { $0.id == "tv.maktaba.subtitles" }, "a plugin that came with Maktaba is not installed, so no rule can name it")

        #expect(try await RuleChoices.load(client, kind: "video", query: "a", en).isEmpty, "a video is searched for from two characters")
        #expect(RuleChoices.kinds == ParentalSubject.kinds, "every kind a rule may name can be added")

        // the rule goes to the home with the kind and the id picked
        ParentalStub.on("GET /api/users/c2/parental", 200, #"{"policy":{"user_id":"c2","max_tier":9,"unrated":"hide","version":3},"access":"admin","guardians":[],"signals":[]}"#)
        ParentalStub.on("GET /api/users/c2/parental/rules", 200, #"{"rules":[]}"#)
        ParentalStub.on("POST /api/users/c2/parental/rules", 201,
                        #"{"id":"ru9","user_id":"c2","effect":"block","subject_kind":"plugin","subject_id":"com.example.intro","created_at":"2026-11-11T10:00:00Z"}"#)
        let m = ChildModel(client: client, child: ParentalPerson(id: "c2", username: "Sara"))
        await m.load()
        await m.addRule("block", ParentalSubject(kind: "plugin", id: plugins[0].id))
        let sent = ParentalStub.seen("POST /api/users/c2/parental/rules").first?.json
        #expect(sent?["effect"] as? String == "block" && sent?["subject_kind"] as? String == "plugin")
        #expect(m.rules("block").map(\.subjectID) == ["com.example.intro"])
        #expect(m.title(of: m.rules("block")[0], en) == "Plugin")
    }

    /// The plugins are an admin's to list: a guardian who is not one gets the home's
    /// refusal, which the picker shows.
    @MainActor @Test func aGuardianWhoIsNotAnAdminCannotListPlugins() async {
        ParentalStub.reset()
        ParentalStub.on("GET /api/plugins", 403, #"{"status":403,"code":"admin_only"}"#)
        await #expect(throws: APIError.self) { _ = try await RuleChoices.load(ParentalStub.client(), kind: "plugin", en) }
    }

    /// 57.07: an admin makes one of the home's accounts restricted and names a guardian,
    /// from the accounts the home lists to admins (`GET /api/admin/library-acl`).
    @MainActor @Test func anAdminMakesAnAccountRestrictedAndNamesAGuardian() async {
        ParentalStub.reset()
        ParentalStub.on("GET /api/parental/children", 200, #"{"children":[{"id":"c2","username":"Sara","user_id":"c2","max_tier":9,"unrated":"hide","version":3}]}"#)
        ParentalStub.on("GET /api/parental/requests", 200, #"{"requests":[]}"#)
        ParentalStub.on("GET /api/me/parental-pin", 200, #"{"set":false}"#)
        ParentalStub.on("GET /api/admin/library-acl", 200,
                        #"{"users":[{"id":"u1","username":"admin"},{"id":"c2","username":"Sara"},{"id":"u3","username":"Omar"},{"id":"u4","username":"huda"}],"libraries":[],"grants":[]}"#)
        let family = FamilyModel(client: ParentalStub.client(), isAdmin: true)
        await family.load()
        #expect(family.accounts.map(\.username) == ["admin", "huda", "Omar", "Sara"], "by name")
        #expect(family.restrictable(me: "u1").map(\.id) == ["u4", "u3"], "not the admin, not one restricted already")

        ParentalStub.on("PUT /api/users/u3/parental", 200, #"{"policy":{"user_id":"u3","max_tier":13,"unrated":"hide","version":1},"access":"admin","guardians":[],"signals":[]}"#)
        #expect(await family.restrict(family.accounts[2], maxTier: 13))
        let put = ParentalStub.seen("PUT /api/users/u3/parental").first?.json
        #expect(put?["max_tier"] as? Int == 13 && put?["unrated"] as? String == "hide" && put?["version"] as? Int == 0, "a new limit: version 0")

        // an admin's account cannot be restricted: the home says so, and so does the screen
        ParentalStub.on("PUT /api/users/u4/parental", 409, #"{"status":409,"code":"restricted_admin"}"#)
        #expect(await family.restrict(family.accounts[1], maxTier: 9) == false)
        #expect(family.notice == .adminAccount)
        #expect(FamilyNotice.adminAccount.line(en) == "An admin cannot be restricted." && FamilyNotice.adminAccount.line(ar) == "لا يمكن تقييد حساب مشرف.")

        // a guardian for Sara: not Sara, not who looks after her already, not another restricted account
        ParentalStub.on("GET /api/users/c2/parental", 200, #"{"policy":{"user_id":"c2","max_tier":9,"unrated":"hide","version":3},"access":"admin","guardians":[{"id":"u4","username":"huda"}],"signals":[]}"#)
        ParentalStub.on("GET /api/users/c2/parental/rules", 200, #"{"rules":[]}"#)
        let child = ChildModel(client: ParentalStub.client(), child: ParentalPerson(id: "c2", username: "Sara"))
        await child.load()
        #expect(child.guardianCandidates(family.accounts, restricted: ["c2", "u3"]).map(\.id) == ["u1"])
        // the answers in turn: the first is taken, the second refused
        ParentalStub.on("POST /api/users/c2/parental/guardians", 201, #"{"guardian_id":"u1"}"#)
        ParentalStub.on("POST /api/users/c2/parental/guardians", 409, #"{"status":409,"code":"restricted_guardian"}"#)
        await child.addGuardian(ParentalPerson(id: "u1", username: "admin"))
        #expect(ParentalStub.seen("POST /api/users/c2/parental/guardians").first?.json["guardian_id"] as? String == "u1")
        #expect(child.notice == nil)
        await child.addGuardian(ParentalPerson(id: "u3", username: "Omar"))
        #expect(child.notice == .restrictedGuardian)
        #expect(FamilyNotice.restrictedGuardian.line(ar) == "لا يمكن لحساب مقيَّد رعاية حساب آخر.")
    }

    /// A guardian who is not an admin is not listed the home's accounts, and is never asked.
    @MainActor @Test func aGuardianIsNotListedTheHomesAccounts() async {
        ParentalStub.reset()
        ParentalStub.on("GET /api/parental/children", 200, #"{"children":[]}"#)
        ParentalStub.on("GET /api/parental/requests", 200, #"{"requests":[]}"#)
        ParentalStub.on("GET /api/me/parental-pin", 200, #"{"set":false}"#)
        let family = FamilyModel(client: ParentalStub.client(), isAdmin: false)
        await family.load()
        #expect(family.accounts.isEmpty && ParentalStub.seen("GET /api/admin/library-acl").isEmpty)
    }

    /// Rate libraries shows the tier the home has stored where it says it: a tier, none
    /// (null), or nothing known (a home from before the field).
    @MainActor @Test func aLibrarysStoredTierIsReadWhereTheHomeSaysIt() async throws {
        ParentalStub.reset()
        ParentalStub.on("GET /api/libraries/manage", 200, #"{"items":[{"id":"l1","name":"A","age_tier":9},{"id":"l2","name":"B","age_tier":null},{"id":"l3","name":"C"}]}"#)
        let tiers = try await ParentalStub.client().libraryAgeTiers()
        #expect(tiers["l1"] == .some(9))
        #expect(tiers.keys.contains("l2") && tiers["l2"] == .some(nil), "stored as not rated")
        #expect(!tiers.keys.contains("l3"), "not said: not known")
    }

    /// A sign-in through Maktaba Cloud says its link scope in `GET /api/auth/me`: a play-only
    /// one is known before anything is tried.
    @Test func aPlayOnlySignInIsKnownFromWhoAmI() throws {
        let play = try JSONDecoder().decode(Me.self, from: Data(#"{"id":"u1","username":"huda","is_admin":false,"cloud_scope":"play"}"#.utf8))
        #expect(play.cloudScope == "play")
        let home = try JSONDecoder().decode(Me.self, from: Data(#"{"id":"u1","username":"huda","is_admin":true}"#.utf8))
        #expect(home.cloudScope == nil, "a sign-in on the home has no scope")
    }

    /// 57.11 T01: an Apple TV says its Screen Time limits and no age range (tvOS has no
    /// Declared Age Range): `tv_ceiling: 400`, nothing about an age.
    @MainActor @Test func anAppleTVSendsItsScreenTimeLimitsAndNoAgeRange() async {
        ParentalStub.reset()
        ParentalStub.on("POST /api/me/age-signal", 200, #"{"tier_limit":9,"pending_looser":false,"refresh":true}"#)
        ParentalStub.on("POST /api/auth/refresh", 200, ParentalFixtures.tokens)
        let tv = FixedAgeSignal(.notAvailable, movie: 1000, tv: 400)
        let sender = AgeSignalSender(defaults: UserDefaults(suiteName: "tv.\(UUID().uuidString)")!)
        sender.use(tv)
        sender.signedIn(client: ParentalStub.client(), user: "u1")
        await sender.sync()
        let sent = ParentalStub.seen("POST /api/me/age-signal").first?.json
        #expect(sent?["tv_ceiling"] as? Int == 400 && sent?["movie_ceiling"] as? Int == 1000)
        #expect(sent?["age_range"] == nil, "no age range from a TV")
        #expect(sender.answer?.tierLimit == 9, "the sign-in is limited to 9+")
        #expect(ParentalStub.seen("POST /api/auth/refresh").count == 1, "then a token that carries the limit")
        // the fixed double the TV's UI tests launch with says the same
        let spec = FixedAgeSignal(spec: "none::1000:400")
        #expect(spec?.answer == .notAvailable && spec?.tv == 400 && spec?.movie == 1000)
    }
}

/// An age signal source whose age-range question waits until the test answers it.
@MainActor
final class HeldAgeSignal: AgeSignalSource {
    private var waiting: [CheckedContinuation<AgeRangeAnswer, Never>] = []
    private(set) var asked = 0
    var region: String? { "US" }

    func requestAgeRange() async -> AgeRangeAnswer {
        asked += 1
        return await withCheckedContinuation { waiting.append($0) }
    }

    func ratingCeilings() -> (movie: Int?, tv: Int?) { (nil, nil) }

    /// Answers the question under way.
    func answer(_ a: AgeRangeAnswer) {
        waiting.removeFirst().resume(returning: a)
    }
}

final class Clock: @unchecked Sendable {
    var now: Date
    init(_ now: Date) { self.now = now }
}

@Suite struct ActivityByDay {
    private func events() -> [ParentalEvent] {
        func at(_ s: String) -> Date { homeTime(s)! }
        return [
            ParentalEvent(id: "e1", kind: "watched", at: at("2026-11-11T15:00:00Z"), subject: .init(kind: "video", id: "v1", title: "Seerah 1")),
            ParentalEvent(id: "e2", kind: "asked", at: at("2026-11-11T09:00:00Z"), subject: .init(kind: "video", id: "v2", title: "Night")),
            ParentalEvent(id: "e3", kind: "blocked", at: at("2026-11-10T20:00:00Z"), subject: .init(kind: "video", id: "v3", removed: true)),
            ParentalEvent(id: "e4", kind: "approved", at: at("2026-11-08T08:00:00Z"), subject: .init(kind: "video", id: "v2", title: "Night"),
                          guardian: ParentalPerson(id: "g1", username: "Huda")),
            ParentalEvent(id: "e5", kind: "pin_locked", at: at("2026-11-08T07:00:00Z"), guardian: ParentalPerson(id: "g1", username: "Huda")),
            ParentalEvent(id: "e6", kind: "signal", at: at("2026-11-08T06:00:00Z"), device: "Sara's iPad"),
        ]
    }

    private func calendar(_ id: Calendar.Identifier = .gregorian, _ locale: Locale = en) -> Calendar {
        var c = Calendar(identifier: id)
        c.timeZone = TimeZone(identifier: "Asia/Riyadh")!
        c.locale = locale
        return c
    }

    /// 57.08 T06: events across days group by the home's day, newest first, and the day
    /// headers read in the app's language, calendar and digits.
    @Test func eventsGroupByDayNewestFirstWithHeadingsInTheAppsLanguage() {
        let now = homeTime("2026-11-11T18:00:00Z")!
        let days = ActivityModel.days(events(), calendar: calendar())
        #expect(days.map { $0.events.map(\.id) } == [["e1", "e2"], ["e3"], ["e4", "e5", "e6"]])
        #expect(days.map { ActivityModel.heading($0.id, now: now, calendar: calendar(), en) } == ["Today", "Yesterday", "Sunday, November 8, 2026"])

        let arabic = Locale(identifier: "ar_SA@numbers=arab")
        let arCal = calendar(.gregorian, arabic)
        let headings = days.map { ActivityModel.heading($0.id, now: now, calendar: arCal, arabic) }
        #expect(headings[0] == "اليوم" && headings[1] == "أمس")
        #expect(headings[2].contains("الأحد") && headings[2].contains("٨") && headings[2].contains("٢٠٢٦"), "Arabic words and digits: \(headings[2])")

        // the Islamic calendar when it is the app's
        let hijri = ActivityModel.heading(days[2].id, now: now, calendar: calendar(.islamicUmmAlQura, arabic), arabic)
        #expect(hijri.contains("١٤٤٨"), "the app's calendar: \(hijri)")
    }

    @Test func aLateEveningEventFallsOnTheHomesDay() {
        // 22:30 UTC is 01:30 the next day in Riyadh
        let e = ParentalEvent(id: "x", kind: "watched", at: homeTime("2026-11-10T22:30:00Z")!)
        let day = ActivityModel.days([e], calendar: calendar()).first!.id
        #expect(calendar().component(.day, from: day) == 11)
    }

    @Test func theFiltersPickTheirKinds() {
        #expect(ActivityModel.days(events(), filter: .watched, calendar: calendar()).flatMap(\.events).map(\.id) == ["e1"])
        #expect(ActivityModel.days(events(), filter: .blocked, calendar: calendar()).flatMap(\.events).map(\.id) == ["e3"])
        #expect(ActivityModel.days(events(), filter: .requests, calendar: calendar()).flatMap(\.events).map(\.id) == ["e2", "e4"])
    }

    @Test func eachKindHasItsSentenceInEnglishAndArabic() {
        let e = events()
        #expect(ActivityModel.line(e[0], en) == "Watched Seerah 1")
        #expect(ActivityModel.line(e[1], en) == "Asked for Night")
        #expect(ActivityModel.line(e[2], en) == "Tried to open something blocked")
        #expect(ActivityModel.line(e[3], en) == "Huda allowed Night")
        #expect(ActivityModel.line(e[4], en) == "Huda's PIN locked after wrong tries")
        #expect(ActivityModel.line(e[5], en) == "Sara's iPad asks for a looser age limit")
        #expect(ActivityModel.line(e[0], ar) == "شاهد \(iso("Seerah 1"))")
        #expect(ActivityModel.line(e[3], ar) == "سمح \(iso("Huda")) بـ \(iso("Night"))")
        // a subject deleted since
        let gone = ParentalEvent(id: "g", kind: "watched", at: .now, subject: .init(kind: "video", id: "v", removed: true))
        #expect(ActivityModel.line(gone, en) == "Watched Removed" && ActivityModel.line(gone, ar) == "شاهد \(iso("محذوف"))")
        for kind in ["watched", "blocked", "asked", "approved", "denied", "pin_locked", "signal", "time_up", "time_granted"] {
            #expect(ActivityModel.symbol(kind) != "circle")
            let line = ActivityModel.line(ParentalEvent(id: kind, kind: kind, at: .now), ar)
            #expect(!line.contains("activity."), "\(kind) has its Arabic: \(line)")
        }
    }
}

// MARK: 57.15, 57.10

@Suite struct WatchingTime {
    private func reading(_ t: ParentalTime, at: TimeInterval = 0) -> ParentalStore.TimeReading {
        .init(t, at: Date(timeIntervalSince1970: at))
    }

    @Test func theHomesAnswerDecodes() throws {
        let json = #"{"allowed":true,"limited":true,"remaining_seconds":840,"slot_ends_at":"2026-11-11T17:00:00Z","next_allowed_at":null,"now":"2026-11-11T15:46:00Z"}"#
        let t = try JSONDecoder.home.decode(ParentalTime.self, from: Data(json.utf8))
        #expect(t.allowed && t.limited && t.remainingSeconds == 840 && t.slotEndsAt != nil && t.nextAllowedAt == nil && t.reason == nil)
        let none = try JSONDecoder.home.decode(ParentalTime.self, from: Data(#"{"allowed":true,"limited":false,"remaining_seconds":null,"slot_ends_at":null,"next_allowed_at":null,"now":"2026-11-11T15:46:00Z"}"#.utf8))
        #expect(none.allowed && !none.limited && none.remainingSeconds == nil)
    }

    /// 57.15 T01: time left, the warnings, time's up, all from what the home said.
    @Test func timeLeftShowsInTheLastFifteenMinutesAndCountsFromTheHomesAnswer() {
        let r = reading(ParentalTime(allowed: true, limited: true, remainingSeconds: 1200))
        let t0 = Date(timeIntervalSince1970: 0)
        #expect(TimeLeft.seconds(r, at: t0) == 1200)
        #expect(TimeLeft.line(r, at: t0, en) == nil, "twenty minutes left: nothing shown yet")
        #expect(TimeLeft.seconds(r, at: t0.addingTimeInterval(360)) == 840)
        #expect(TimeLeft.line(r, at: t0.addingTimeInterval(360), en) == "14 min left today")
        #expect(TimeLeft.line(r, at: t0.addingTimeInterval(360), ar)?.hasPrefix("بقي ") == true)
        #expect(TimeLeft.seconds(r, at: t0.addingTimeInterval(5000)) == 0)
        // no rule covers now: no countdown, whatever the number
        #expect(TimeLeft.seconds(reading(ParentalTime(allowed: true, limited: false)), at: t0) == nil)
        #expect(TimeLeft.line(nil, at: t0, en) == nil)
    }

    @Test func theWarningsComeOnceAtFiveMinutesAndAtOne() {
        #expect(TimeLeft.warning(from: 400, to: 310) == nil)
        #expect(TimeLeft.warning(from: 310, to: 299) == 300)
        #expect(TimeLeft.warning(from: 299, to: 250) == nil, "said once")
        #expect(TimeLeft.warning(from: 70, to: 58) == 60)
        #expect(TimeLeft.warning(from: nil, to: 200) == 300, "a first answer already under five minutes")
        #expect(TimeLeft.warning(from: 400, to: 30) == 60, "the nearer one when both were crossed")
        #expect(TimeLeft.warning(from: 30, to: nil) == nil)
        #expect(TimeLeft.warningLine(300, en) == "5 min left today")
    }

    @Test func aRefusedStreamSaysWhyAndWhenWatchingOpensAgain() throws {
        let refusal = try #require(ParentalRefusal(APIError.status(403, ParentalFixtures.timeOver)))
        guard case let .time(reason, next) = refusal else {
            Issue.record("not a time refusal: \(refusal)")
            return
        }
        #expect(reason == "limit_reached" && next == homeTime("2026-11-12T04:00:00Z"))
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = TimeZone(identifier: "UTC")!
        let up = TimeUp(reason: reason, nextAllowedAt: next)
        #expect(up.title(en) == "Time's up for today" && up.title(ar) == "انتهى وقت اليوم")
        let sameDay = homeTime("2026-11-12T01:00:00Z")!
        #expect(up.nextLine(en, now: sameDay, calendar: calendar) == "You can watch again at 4:00\u{202F}AM")
        let dayBefore = homeTime("2026-11-11T20:00:00Z")!
        #expect(up.nextLine(en, now: dayBefore, calendar: calendar)?.contains("Nov 12") == true)
        #expect(up.nextLine(ar, now: sameDay, calendar: calendar)?.hasPrefix("يمكنك المشاهدة مجددًا عند ") == true)

        let outside = TimeUp(reason: "outside_slot", nextAllowedAt: nil)
        #expect(outside.title(en) == "Not watching time yet" && outside.nextLine(en) == nil)
        // the home's own time answer makes the same screen
        #expect(TimeUp(ParentalTime(allowed: false, limited: true, reason: "outside_slot", remainingSeconds: 0, nextAllowedAt: next)) == TimeUp(reason: "outside_slot", nextAllowedAt: next))
        #expect(TimeUp(ParentalTime(allowed: true, limited: true, remainingSeconds: 60)) == nil)
        #expect(TimeLeft.isUp(reading(ParentalTime(allowed: false, limited: true, reason: "limit_reached", remainingSeconds: 0))))
    }

    @Test func otherRefusalsAreToldApart() {
        func refusal(_ status: Int, _ body: String) -> ParentalRefusal? { ParentalRefusal(APIError.status(status, body)) }
        #expect(refusal(403, #"{"code":"restricted"}"#) == .restricted)
        #expect(refusal(403, #"{"code":"restricted","reason":"programme","retry_at":"2026-11-11T20:00:00Z"}"#) == .programme(retryAt: homeTime("2026-11-11T20:00:00Z")))
        #expect(refusal(403, #"{"code":"restricted","reason":"programme"}"#) == .programme(retryAt: nil))
        #expect(refusal(403, #"{"code":"not_restricted"}"#) == .notRestricted)
        #expect(refusal(403, #"{"code":"device_limited"}"#) == .deviceLimited)
        #expect(refusal(404, ParentalFixtures.notFound) == nil, "a hidden thing answers as a missing one: no refusal to read")
        #expect(refusal(403, #"{"title":"forbidden"}"#) == nil)
        #expect(ParentalRefusal(APIError.transport("down")) == nil)
        // a 403 with the parental-time type and no code is still the time refusal
        #expect(refusal(403, #"{"type":"https://maktaba.tv/problems/parental-time","reason":"outside_slot"}"#) == .time(reason: "outside_slot", nextAllowedAt: nil))
    }

    /// 57.05 T05 and 57.15 T02, the player's decisions: a refused renewal under a limit is the
    /// blocked page, time over is the time's-up screen, and neither is tried again.
    @Test func thePlayersVerdictOnARefusedStreamOrHeartbeat() {
        let time = APIError.status(403, ParentalFixtures.timeOver)
        let gone = APIError.status(404, ParentalFixtures.notFound)
        let up = TimeUp(reason: "limit_reached", nextAllowedAt: homeTime("2026-11-12T04:00:00Z"))
        #expect(PlaybackVerdict.open(time, limited: true) == .timeUp(up))
        #expect(PlaybackVerdict.open(time, limited: false) == .timeUp(up), "the home's answer, whatever the app knows")
        #expect(PlaybackVerdict.open(gone, limited: true) == .blocked)
        #expect(PlaybackVerdict.open(gone, limited: false) == .other, "no limit: a missing video is a missing video")
        #expect(PlaybackVerdict.open(APIError.transport("down"), limited: true) == .other)
        #expect(PlaybackVerdict.open(APIError.status(503, #"{"code":"transcode_busy"}"#), limited: true) == .other)
        // a playlist not written yet is waited for, not a block
        #expect(PlaybackVerdict.open(APIError.notReady(#"{"type":"https://maktaba.tv/problems/manifest-not-found"}"#), limited: true) == .other)

        #expect(PlaybackVerdict.progress(time) == .timeUp(up))
        #expect(PlaybackVerdict.progress(APIError.status(403, #"{"code":"parental_time","reason":"no_heartbeat"}"#)) == .reopen)
        #expect(PlaybackVerdict.progress(gone) == .other, "with no limit a failed post is left to the next one")
        #expect(PlaybackVerdict.progress(gone, limited: true) == .blocked, "the video was hidden while it played: the stream has ended")
        #expect(PlaybackVerdict.progress(time, limited: true) == .timeUp(up))
        #expect(PlaybackVerdict.progress(APIError.transport("down")) == .other)
    }

    /// What the editor's Save goes by, as the home's own check (`TimeRule.validate`).
    @Test func aTimeRuleCanBeSavedOnlyWithADayAndHoursOrALimit() {
        #expect(ParentalTimeRule(dailyMinutes: 60).canBeSaved, "a daily limit alone")
        #expect(ParentalTimeRule(slotStart: "07:00", slotEnd: "20:00").canBeSaved, "watching hours alone")
        #expect(ParentalTimeRule.suggested.canBeSaved, "both")
        #expect(ParentalTimeRule(slotStart: "22:00", slotEnd: "06:00").canBeSaved, "hours past midnight")
        #expect(ParentalTimeRule(dailyMinutes: 0).canBeSaved, "no watching at all is a limit")
        #expect(ParentalTimeRule(dailyMinutes: 1440).canBeSaved)

        #expect(!ParentalTimeRule().canBeSaved, "neither hours nor a limit")
        #expect(!ParentalTimeRule(days: 0, dailyMinutes: 60).canBeSaved, "no day")
        #expect(!ParentalTimeRule(days: 128, dailyMinutes: 60).canBeSaved, "a day that is not one")
        #expect(!ParentalTimeRule(slotStart: "07:00", slotEnd: "07:00").canBeSaved, "hours that start and end together")
        #expect(!ParentalTimeRule(slotStart: "07:00", slotEnd: "07:00", dailyMinutes: 60).canBeSaved, "even with a limit")
        #expect(!ParentalTimeRule(slotStart: "07:00", dailyMinutes: 60).canBeSaved, "a start with no end")
        #expect(!ParentalTimeRule(slotStart: "24:00", slotEnd: "07:00").canBeSaved, "not a time of day")
        #expect(!ParentalTimeRule(slotStart: "7:00", slotEnd: "20:00").canBeSaved)
        #expect(!ParentalTimeRule(slotStart: "+7:00", slotEnd: "20:00").canBeSaved)
        #expect(!ParentalTimeRule(dailyMinutes: 1441).canBeSaved)
        #expect(!ParentalTimeRule(dailyMinutes: -15).canBeSaved)
    }

    /// The editor's form: its two switches decide what the rule carries.
    @Test func theEditorsFormIsARuleOnlyWithHoursOrALimitOn() {
        var rule = ParentalTimeRule(id: "t1", libraryID: "lib", days: 127 & ~(1 << 5), slotStart: "07:00", slotEnd: "20:00", dailyMinutes: 60)
        let neither = rule.with(slot: nil, dailyMinutes: nil)
        #expect(!neither.canBeSaved && neither.slotStart == nil && neither.slotEnd == nil && neither.dailyMinutes == nil)
        #expect(neither.id == "t1" && neither.libraryID == "lib" && neither.days == 95, "the rest of the rule is kept")
        let limit = rule.with(slot: nil, dailyMinutes: 75)
        #expect(limit.canBeSaved && limit.dailyMinutes == 75 && limit.slotStart == nil)
        let hours = rule.with(slot: ("08:00", "19:30"), dailyMinutes: nil)
        #expect(hours.canBeSaved && hours.slotStart == "08:00" && hours.slotEnd == "19:30" && hours.dailyMinutes == nil)
        #expect(!rule.with(slot: ("08:00", "08:00"), dailyMinutes: 75).canBeSaved, "hours of no length")
        for day in 0 ..< 7 { rule.set(day: day, false) }
        #expect(!rule.with(slot: nil, dailyMinutes: 75).canBeSaved, "every day switched off")
    }

    @Test func aTimeRuleReadsInOneLine() {
        let rule = ParentalTimeRule(days: ParentalTimeRule.everyDay, slotStart: "07:00", slotEnd: "20:00", dailyMinutes: 60)
        #expect(TimeRuleWords.summary(rule, en) == "Every day · \u{200E}07:00–20:00 · 1 hr")
        #expect(TimeRuleWords.summary(rule, ar).hasPrefix("كل يوم · "))
        let weekend = ParentalTimeRule(days: 65, dailyMinutes: 90)
        #expect(TimeRuleWords.summary(weekend, library: "Cartoons", en) == "Sun, Sat · 1 hr, 30 min · Cartoons")
        #expect(TimeRuleWords.days(en).map(\.name) == ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"])
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = TimeZone(identifier: "UTC")!
        #expect(TimeRuleWords.clock(TimeRuleWords.date("19:30", calendar: calendar), calendar: calendar) == "19:30")
    }
}

@Suite struct ThePINPadsWords {
    /// VoiceOver's line for the dots names both numbers in one script: in Arabic the count
    /// was formatted and the 6 was a literal, read as two kinds of digit.
    @MainActor @Test func theDigitsEnteredAreCountedInTheLanguagesOwnDigits() {
        let entry = PinEntry(client: ParentalStub.client(), subject: .video("v2"), guardian: ParentalPerson(id: "g1", username: "Huda"))
        [4, 2, 7].forEach(entry.press)
        #expect(entry.progressLabel(en) == "3 of 6 digits entered")
        let arabic = entry.progressLabel(ar)
        let digits = arabic.filter { $0.wholeNumberValue != nil }
        #expect(digits.count == 2, "the count and the length: \(arabic)")
        #expect(digits.map(\.wholeNumberValue) == [3, 6])
        #expect(digits.allSatisfy(\.isASCII) || digits.allSatisfy { !$0.isASCII }, "one script for both numbers: \(arabic)")
        #expect(arabic.hasPrefix("أُدخل"))
    }
}

@Suite struct LiveTVUnderALimit {
    @Test func theAccessAnswerSaysWhenToAskAgain() throws {
        let json = #"{"channel_id":"ch-1","restricted":true,"allowed":false,"until":"2026-11-11T20:00:00Z"}"#
        let a = try JSONDecoder.home.decode(ChannelAccess.self, from: Data(json.utf8))
        #expect(a.restricted && !a.allowed && a.until == homeTime("2026-11-11T20:00:00Z"))
        #expect(a.askAgain(after: homeTime("2026-11-11T19:59:00Z")!) == 61)
        #expect(a.askAgain(after: homeTime("2026-11-11T18:00:00Z")!) == 300, "never longer than five minutes: a parent may allow it meanwhile")
        #expect(a.askAgain(after: homeTime("2026-11-11T20:00:30Z")!) == 5)
        let open = try JSONDecoder.home.decode(ChannelAccess.self, from: Data(#"{"channel_id":"ch-1","restricted":false,"allowed":true,"until":null}"#.utf8))
        #expect(open.allowed && open.until == nil && open.askAgain() == 300)
    }

    /// The answer carries the home's own time (`now`): the wait is `until` minus `now`, both
    /// the home's, whatever this device's clock says. A home that sends no `now` is measured
    /// against the device's clock as before.
    @Test func theWaitIsMeasuredOnTheHomesClockNotTheDevices() throws {
        let json = #"{"channel_id":"ch-1","restricted":true,"allowed":false,"until":"2026-11-11T20:00:00Z","now":"2026-11-11T19:58:00Z"}"#
        let a = try JSONDecoder.home.decode(ChannelAccess.self, from: Data(json.utf8))
        #expect(a.now == homeTime("2026-11-11T19:58:00Z"))
        // a device clock that is right, ten minutes fast, a day slow, a year fast: 2 minutes
        for device in ["2026-11-11T19:58:00Z", "2026-11-11T20:08:00Z", "2026-11-10T19:58:00Z", "2027-11-11T19:58:00Z"] {
            #expect(a.askAgain(after: homeTime(device)!) == 121, "device clock \(device)")
        }
        // the bounds hold on the home's clock too
        let far = ChannelAccess(restricted: true, allowed: true, until: homeTime("2026-11-11T20:00:00Z"), now: homeTime("2026-11-11T18:00:00Z"))
        #expect(far.askAgain(after: homeTime("2026-11-11T19:59:59Z")!) == 300)
        let past = ChannelAccess(restricted: true, allowed: false, until: homeTime("2026-11-11T20:00:00Z"), now: homeTime("2026-11-11T20:00:30Z"))
        #expect(past.askAgain(after: homeTime("2026-11-11T19:00:00Z")!) == 5)
        // the home's time with a fraction of a second is read too
        let fraction = try JSONDecoder.home.decode(ChannelAccess.self, from: Data(#"{"restricted":true,"allowed":true,"until":"2026-11-11T20:00:00Z","now":"2026-11-11T19:59:30.250Z"}"#.utf8))
        #expect(fraction.askAgain(after: .distantPast) == 30.75)
        // nothing changes in the next 12 hours: five minutes, with or without the home's time
        #expect(ChannelAccess(restricted: true, allowed: true, now: homeTime("2026-11-11T19:58:00Z")).askAgain() == 300)
        // an older home sends no `now`, or one that cannot be read: the device's clock
        for old in [#"{"restricted":true,"allowed":false,"until":"2026-11-11T20:00:00Z"}"#,
                    #"{"restricted":true,"allowed":false,"until":"2026-11-11T20:00:00Z","now":null}"#,
                    #"{"restricted":true,"allowed":false,"until":"2026-11-11T20:00:00Z","now":"soon"}"#] {
            let b = try JSONDecoder.home.decode(ChannelAccess.self, from: Data(old.utf8))
            #expect(b.now == nil)
            #expect(b.askAgain(after: homeTime("2026-11-11T19:59:00Z")!) == 61)
            #expect(b.askAgain(after: homeTime("2026-11-11T20:10:00Z")!) == 5)
        }
    }

    /// The home stores the position of a progress post it refuses because the time is over,
    /// so "saved at" may say the position sent; a session it is not counting stores nothing.
    @Test func aPostRefusedForTimeKeepsItsPosition() {
        #expect(TimeUp(reason: "limit_reached", nextAllowedAt: nil).keepsRefusedPosition)
        #expect(TimeUp(reason: "outside_slot", nextAllowedAt: nil).keepsRefusedPosition)
        #expect(!TimeUp(reason: "no_heartbeat", nextAllowedAt: nil).keepsRefusedPosition)
        #expect(!TimeUp(reason: "something_new", nextAllowedAt: nil).keepsRefusedPosition, "an unknown reason promises nothing")
    }

    /// The home's own refusal of the programme on now (the channel's play or its stream) is
    /// the slate, whatever this device's clock made of the access answer's `until`; the
    /// account's time being over, a hidden channel and a plain failure are not.
    @Test func theHomesRefusalOfTheProgrammeIsTheSlate() {
        let programme = APIError.status(403, #"{"type":"https://maktaba.tv/problems/parental-restricted","status":403,"code":"restricted","reason":"programme","retry_at":"2026-11-11T20:00:00Z"}"#)
        #expect(ChannelAccess.programmeRefused(programme))
        #expect(ParentalRefusal(programme) == .programme(retryAt: homeTime("2026-11-11T20:00:00Z")))
        let noTime = APIError.status(403, #"{"status":403,"code":"restricted","reason":"programme"}"#)
        #expect(ChannelAccess.programmeRefused(noTime), "with no time to ask again at, still the slate")
        #expect(!ChannelAccess.programmeRefused(APIError.status(403, #"{"status":403,"code":"restricted"}"#)), "a restricted account's refusal of something else")
        #expect(!ChannelAccess.programmeRefused(APIError.status(403, ParentalFixtures.timeOver)), "time's up is its own screen")
        #expect(!ChannelAccess.programmeRefused(APIError.status(404, ParentalFixtures.notFound)), "a hidden channel is the blocked page")
        #expect(!ChannelAccess.programmeRefused(APIError.transport("down")))
        // a device clock ten minutes fast, or slow: the wait stays within its bounds
        let a = ChannelAccess(restricted: true, allowed: true, until: homeTime("2026-11-11T20:00:00Z"))
        #expect(a.askAgain(after: homeTime("2026-11-11T20:10:00Z")!) == 5)
        #expect(a.askAgain(after: homeTime("2026-11-11T19:50:00Z")!) == 300)
    }

    @Test func aProgrammeAboveTheLimitIsMaskedToItsTime() throws {
        let json = #"{"channel_id":"ch-1","kind":"programme","start":"2026-11-11T20:00:00Z","stop":"2026-11-11T21:00:00Z","restricted":true}"#
        let b = try JSONDecoder.home.decode(GuideBlock.self, from: Data(json.utf8))
        #expect(b.restricted && b.title.isEmpty)
        #expect(b.displayTitle(en) == "Not available" && b.displayTitle(ar) == "غير متاح")
        let open = try JSONDecoder.home.decode(GuideBlock.self, from: Data(#"{"channel_id":"ch-1","kind":"programme","start":"2026-11-11T20:00:00Z","stop":"2026-11-11T21:00:00Z","title":"News"}"#.utf8))
        #expect(!open.restricted && open.displayTitle(en) == "News")
    }
}
