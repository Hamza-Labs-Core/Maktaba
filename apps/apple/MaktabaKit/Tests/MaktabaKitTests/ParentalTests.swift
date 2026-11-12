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
    private var asked = 0

    func set(_ json: String) { lock.withLock { limit = .success(try! JSONDecoder.home.decode(ParentalLimit.self, from: Data(json.utf8))) } }
    func set(missing: Void) { lock.withLock { limit = .success(nil) } }
    func fail() { lock.withLock { limit = .failure(.transport("down")) } }
    func set(children n: Int) {
        lock.withLock { children = (0..<n).map { ParentalChild(id: "c\($0)", username: "child\($0)", policy: ParentalPolicy(userID: "c\($0)", maxTier: 9)) } }
    }
    func set(time t: ParentalTime?) { lock.withLock { time = t } }
    var limitAsked: Int { lock.withLock { asked } }

    func myParental() async throws -> ParentalLimit? {
        try lock.withLock {
            asked += 1
            return try limit.get()
        }
    }
    func parentalChildren() async throws -> [ParentalChild] { lock.withLock { children } }
    func myParentalTime(library: String?) async throws -> ParentalTime? { lock.withLock { time } }
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

    @MainActor @Test func theAnswerIsKeptForTheNextLaunchAndForgottenWhenSignedOut() async {
        ParentalStub.reset()
        ParentalStub.on("POST /api/me/age-signal", 200, #"{"tier_limit":9,"pending_looser":false,"refresh":true}"#)
        ParentalStub.on("POST /api/auth/refresh", 200, ParentalFixtures.tokens)
        let client = ParentalStub.client()
        let source = FixedAgeSignal(.shared(lower: 9, upper: 12, declaration: "guardian_declared"))
        let (first, defaults) = sender(source, client: client)
        await first.sync()

        // the next launch: the same sign-in sends the same range without asking Apple again
        let second = AgeSignalSender(defaults: defaults)
        second.use(source)
        second.signedIn(client: client, user: "u1")
        #expect(second.range == .shared(lower: 9, upper: 12, declaration: "guardian_declared"))
        await second.sync()
        #expect(source.asked == 1)
        #expect(ParentalStub.seen("POST /api/me/age-signal").count == 2)

        // Profile > Age range asks again
        source.answer = .declined
        await second.sync(askAgain: true)
        #expect(source.asked == 2 && second.range == .declined)

        second.signedOut(forget: true)
        let third = AgeSignalSender(defaults: defaults)
        third.use(source)
        third.signedIn(client: client, user: "u1")
        #expect(third.range == nil, "a new sign-in asks afresh")
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
        #expect(PlaybackVerdict.progress(gone) == .other, "a progress post that fails otherwise is left to the next one")
        #expect(PlaybackVerdict.progress(APIError.transport("down")) == .other)
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

    @Test func aProgrammeAboveTheLimitIsMaskedToItsTime() throws {
        let json = #"{"channel_id":"ch-1","kind":"programme","start":"2026-11-11T20:00:00Z","stop":"2026-11-11T21:00:00Z","restricted":true}"#
        let b = try JSONDecoder.home.decode(GuideBlock.self, from: Data(json.utf8))
        #expect(b.restricted && b.title.isEmpty)
        #expect(b.displayTitle(en) == "Not available" && b.displayTitle(ar) == "غير متاح")
        let open = try JSONDecoder.home.decode(GuideBlock.self, from: Data(#"{"channel_id":"ch-1","kind":"programme","start":"2026-11-11T20:00:00Z","stop":"2026-11-11T21:00:00Z","title":"News"}"#.utf8))
        #expect(!open.restricted && open.displayTitle(en) == "News")
    }
}
