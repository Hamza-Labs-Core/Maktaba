import Foundation
import Testing
@testable import MaktabaKit

// Epic 57: what became of a child's own request (`GET /api/me/parental/requests`), followed
// on the blocked page and the time's-up screen; and what the home's later answers mean to a
// player (the time that cannot be read, a channel refused for time, a history row that is no
// longer available, media served too fast). Every answer is shaped as the Go handlers write
// it; no home is involved, and no test waits on a real clock.

private let en = Locale(identifier: "en_US")
private let ar = Locale(identifier: "ar_SA")

/// The home's list of the child's own requests, answered in turn (the last answer repeats).
final class FakeOwnRequests: OwnRequestsSource, @unchecked Sendable {
    enum Answer {
        case list([OwnRequest])
        /// A home from before the route.
        case missing
        case down
    }

    private let lock = NSLock()
    private var answers: [Answer]
    private var count = 0

    init(_ answers: [Answer]) { self.answers = answers }

    /// How many times the home was asked.
    var asked: Int { lock.withLock { count } }

    func myParentalRequests() async throws -> [OwnRequest]? {
        let answer: Answer = lock.withLock {
            count += 1
            return answers.count > 1 ? answers.removeFirst() : answers[0]
        }
        switch answer {
        case let .list(list): return list
        case .missing: return nil
        case .down: throw APIError.transport("down")
        }
    }
}

/// A sleep that takes no time: it moves the test's clock on and counts the waits; after
/// `limit` waits it ends as a cancelled task's sleep ends.
final class SteppedSleep: @unchecked Sendable {
    private let lock = NSLock()
    private let clock: Clock
    private let limit: Int
    private var waits: [Duration] = []

    init(_ clock: Clock, limit: Int = .max) {
        self.clock = clock
        self.limit = limit
    }

    var all: [Duration] { lock.withLock { waits } }

    func sleep(_ d: Duration) async throws {
        let over: Bool = lock.withLock {
            if waits.count >= limit { return true }
            waits.append(d)
            clock.now = clock.now.addingTimeInterval(TimeInterval(d.components.seconds))
            return false
        }
        if over { throw CancellationError() }
    }
}

@Suite struct AChildsOwnRequest {
    static let start = homeTime("2026-11-12T10:00:00Z")!
    static let video = ParentalSubject.video("v-16")

    static func request(_ status: OwnRequest.Status, id: String = "r1", kind: String = "video", subject: String = "v-16",
                        expires: String = "2026-11-19T10:00:00Z") -> OwnRequest {
        OwnRequest(id: id, subjectKind: kind, subjectID: subject, status: status, createdAt: start, expiresAt: homeTime(expires))
    }

    @MainActor static func follow(_ source: FakeOwnRequests, _ match: RequestFollow.Match = .item(video),
                                  clock: Clock, sleep: SteppedSleep) -> RequestFollow {
        RequestFollow(source: source, match: match, sleep: { try await sleep.sleep($0) }, now: { clock.now })
    }

    @Test func theHomesListDecodesLeniently() throws {
        let json = #"""
        {"requests":[
          {"id":"r3","subject_kind":"time","subject_id":"30","minutes":30,"status":"denied","created_at":"2026-11-12T09:00:00Z","expires_at":"2026-11-13T04:00:00Z","decided_at":"2026-11-12T09:05:00Z"},
          {"id":"r2","subject_kind":"video","subject_id":"v-16","status":"snoozed","created_at":"2026-11-12T08:00:00Z","expires_at":"2026-11-19T08:00:00Z"},
          {"subject_kind":"video"},
          {"id":"r1","subject_kind":"time","subject_id":"15","status":"pending","expires_at":7}
        ]}
        """#
        struct A: Decodable {
            let requests: [OwnRequest]
            init(from decoder: Decoder) throws {
                requests = try decoder.container(keyedBy: AnyKey.self).lenientList(AnyKey(stringValue: "requests"))
            }
        }
        let list = try JSONDecoder.home.decode(A.self, from: Data(json.utf8)).requests
        #expect(list.map(\.id) == ["r3", "r2", "r1"], "a row with no id is left out; the rest are read")
        #expect(list[0].status == .denied && list[0].isTime && list[0].minutes == 30)
        #expect(list[0].expiresAt == homeTime("2026-11-13T04:00:00Z") && list[0].decidedAt == homeTime("2026-11-12T09:05:00Z"))
        #expect(list[1].status == .expired, "a status this app does not know counts as expired")
        #expect(list[2].status == .pending && list[2].minutes == 15 && list[2].expiresAt == nil)
    }

    /// The entry that decides: the first (newest) for this item, or the first time request.
    @MainActor @Test func theNewestRequestForThisThingDecides() async {
        let clock = Clock(Self.start)
        let sleep = SteppedSleep(clock)
        let list = [Self.request(.pending, id: "r5", subject: "v-99"), Self.request(.denied, id: "r4", kind: "time", subject: "30"),
                    Self.request(.denied, id: "r3"), Self.request(.pending, id: "r2"), Self.request(.pending, id: "r1", kind: "time", subject: "15")]
        let item = Self.follow(FakeOwnRequests([.list(list)]), clock: clock, sleep: sleep)
        #expect(await item.check() == .denied)
        let time = Self.follow(FakeOwnRequests([.list(list)]), .time, clock: clock, sleep: sleep)
        #expect(await time.check() == .denied)
        let channel = Self.follow(FakeOwnRequests([.list(list)]), .item(.channel("v-16")), clock: clock, sleep: sleep)
        #expect(await channel.check() == .none, "the kind counts, not only the id")
        #expect(RequestFollow.Match(.time(minutes: 30)) == .time && RequestFollow.Match(Self.video) == .item(Self.video))
    }

    /// A waiting request is read every 15 seconds until a parent answers; the approval counts
    /// because this screen saw it waiting, and then nothing more is read.
    @MainActor @Test func aWaitingRequestIsReadEveryFifteenSecondsUntilAParentApproves() async {
        let clock = Clock(Self.start)
        let sleep = SteppedSleep(clock)
        let source = FakeOwnRequests([.list([Self.request(.pending)]), .list([Self.request(.pending)]), .list([Self.request(.approved)])])
        let follow = Self.follow(source, clock: clock, sleep: sleep)
        await follow.follow()
        #expect(follow.outcome == .approved && follow.approvals == 1)
        #expect(source.asked == 3 && sleep.all == [.seconds(15), .seconds(15)])
        // followed again (the screen is still up): nothing waits, so the home is read once
        // more at most and no wait begins; the approval is not counted twice
        clock.now = clock.now.addingTimeInterval(60)
        await follow.follow()
        #expect(follow.approvals == 1 && sleep.all.count == 2)
    }

    /// An approval this screen never saw waiting (from days ago, or of an allowance that has
    /// since ended) is not news: the ask buttons, and nothing opens by itself.
    @MainActor @Test func anApprovalNotSeenWaitingDoesNotCount() async {
        let clock = Clock(Self.start)
        let sleep = SteppedSleep(clock)
        let source = FakeOwnRequests([.list([Self.request(.approved)])])
        let follow = Self.follow(source, clock: clock, sleep: sleep)
        await follow.follow()
        #expect(follow.outcome == .none && follow.approvals == 0)
        #expect(source.asked == 1 && sleep.all.isEmpty, "nothing waits: nothing is read again")
    }

    /// The request the child just made counts as seen waiting, and the home is not read
    /// again at once: the answer to the ask was the reading.
    @MainActor @Test func aRequestJustMadeWaitsAndItsApprovalCounts() async throws {
        let clock = Clock(Self.start)
        let sleep = SteppedSleep(clock)
        let source = FakeOwnRequests([.list([Self.request(.approved, id: "r7")])])
        let follow = Self.follow(source, clock: clock, sleep: sleep)
        let made = try JSONDecoder.home.decode(ParentalRequest.self, from: Data(#"{"id":"r7","subject_kind":"video","subject_id":"v-16","status":"pending"}"#.utf8))
        follow.asked(made)
        #expect(follow.outcome == .pending && source.asked == 0)
        await follow.follow()
        #expect(sleep.all == [.seconds(15)], "the first read comes a full wait after the ask")
        #expect(follow.outcome == .approved && follow.approvals == 1 && source.asked == 1)
    }

    /// In the background the screen's task is cancelled: the reading stops at once, and what
    /// was shown stays. Back in front it reads at once (more than 15 seconds have passed).
    @MainActor @Test func theReadingStopsInTheBackgroundAndStartsAgainInFront() async {
        let clock = Clock(Self.start)
        let sleep = SteppedSleep(clock, limit: 1)
        let source = FakeOwnRequests([.list([Self.request(.pending)]), .list([Self.request(.pending)]), .list([Self.request(.denied)])])
        let follow = Self.follow(source, clock: clock, sleep: sleep)
        await follow.follow()
        #expect(follow.outcome == .pending && source.asked == 2 && !follow.rested, "one wait, then the cancelled one ends it")
        clock.now = clock.now.addingTimeInterval(120)
        await follow.follow()
        #expect(follow.outcome == .denied && source.asked == 3)
    }

    /// After 30 minutes with no change the reading stops; Check again still reads once.
    @MainActor @Test func afterThirtyMinutesWithNoChangeOnlyCheckAgainReads() async {
        let clock = Clock(Self.start)
        let sleep = SteppedSleep(clock)
        let source = FakeOwnRequests([.list([Self.request(.pending)])])
        let follow = Self.follow(source, clock: clock, sleep: sleep)
        await follow.follow()
        #expect(follow.rested && follow.outcome == .pending)
        #expect(sleep.all.count == 120 && source.asked == 121, "every 15 seconds for 30 minutes, then no more")
        #expect(clock.now == Self.start.addingTimeInterval(RequestFollow.patience))
        await follow.follow()
        #expect(source.asked == 121 && sleep.all.count == 120 && follow.rested, "asked to follow again, it stays at rest")
        #expect(await follow.check() == .pending && source.asked == 122, "Check again reads once")
    }

    /// A no is said (with Ask again); an expired request, and a status not known, show the
    /// ask buttons again.
    @MainActor @Test func aDenialIsSaidAndAnExpiredRequestCanBeAskedAgain() async {
        let clock = Clock(Self.start)
        let sleep = SteppedSleep(clock)
        let source = FakeOwnRequests([.list([Self.request(.denied)]), .list([Self.request(.expired)]), .list([])])
        let follow = Self.follow(source, clock: clock, sleep: sleep)
        let page = BlockedPage(client: ParentalStub.client(), subject: Self.video, limit: ParentalLimit(restricted: true, maxTier: 9), follow: follow)
        #expect(page.askKey("parental.blocked.ask") == "parental.blocked.ask" && page.askLine(en) == nil)
        await follow.check()
        #expect(page.denied && !page.waiting && page.canPressAsk)
        #expect(page.askKey("parental.blocked.ask") == "parental.ask.again")
        #expect(page.askLine(en) == "A parent said no." && page.askLine(ar) == "رفض أحد الوالدين.")
        #expect(ParentalWords.text("parental.ask.again", en) == "Ask again" && ParentalWords.text("parental.ask.again", ar) == "اطلب مجددًا")
        #expect(ParentalWords.text("parental.ask.check", en) == "Check again" && ParentalWords.text("parental.ask.check", ar) == "تحقّق مجددًا")
        clock.now = clock.now.addingTimeInterval(8 * 86400)
        #expect(follow.outcome == .denied, "an item's denial stays for as long as the home lists it")
        await follow.check()
        #expect(follow.outcome == .expired && page.canPressAsk && page.askLine(en) == nil)
        #expect(page.askKey("parental.blocked.ask") == "parental.blocked.ask")
        await follow.check()
        #expect(follow.outcome == .none)
    }

    /// For time, a denial is dropped once the request would have expired anyway.
    @MainActor @Test func aDeniedTimeRequestIsDroppedOnceItsExpiryHasPassed() async {
        let clock = Clock(Self.start)
        let sleep = SteppedSleep(clock)
        let denied = Self.request(.denied, id: "t1", kind: "time", subject: "30", expires: "2026-11-12T11:00:00Z")
        let follow = Self.follow(FakeOwnRequests([.list([denied])]), .time, clock: clock, sleep: sleep)
        #expect(await follow.check() == .denied)
        clock.now = homeTime("2026-11-12T10:59:59Z")!
        #expect(follow.outcome == .denied)
        clock.now = homeTime("2026-11-12T11:00:00Z")!
        #expect(follow.outcome == .none, "without another read: the time has passed")
        #expect(await follow.check() == .none, "and a read after it says the same")
    }

    /// A home from before the route (404 or 405) is asked once; a request made there stays
    /// "waiting", as before the route existed.
    @MainActor @Test func anOlderHomeIsAskedOnceAndKeepsTodaysBehaviour() async throws {
        let clock = Clock(Self.start)
        let sleep = SteppedSleep(clock)
        let source = FakeOwnRequests([.missing])
        let follow = Self.follow(source, clock: clock, sleep: sleep)
        await follow.follow()
        #expect(follow.unsupported && follow.outcome == .none && source.asked == 1)
        let made = try JSONDecoder.home.decode(ParentalRequest.self, from: Data(#"{"id":"r1","status":"pending"}"#.utf8))
        follow.asked(made)
        await follow.follow()
        await follow.check()
        #expect(follow.outcome == .pending && source.asked == 1 && sleep.all.isEmpty, "never asked again")
    }

    /// A read that fails keeps what was shown, and the following goes on.
    @MainActor @Test func aFailedReadKeepsWhatWasShown() async {
        let clock = Clock(Self.start)
        let sleep = SteppedSleep(clock)
        let source = FakeOwnRequests([.list([Self.request(.pending)]), .down, .list([Self.request(.denied)])])
        let follow = Self.follow(source, clock: clock, sleep: sleep)
        await follow.follow()
        #expect(follow.outcome == .denied && source.asked == 3 && sleep.all.count == 2 && !follow.unsupported)
    }
}

extension AgainstAParentalStubHome {
    // MARK: the child's own requests

    @MainActor @Test func theChildsOwnRequestsAreReadAndAnOlderHomeAnswersNone() async throws {
        ParentalStub.reset()
        ParentalStub.on("GET /api/me/parental/requests", 200,
                        #"{"requests":[{"id":"r1","subject_kind":"video","subject_id":"v-16","status":"pending","created_at":"2026-11-12T10:00:00Z","expires_at":"2026-11-19T10:00:00Z"}]}"#)
        ParentalStub.on("GET /api/me/parental/requests", 404, ParentalFixtures.notFound)
        ParentalStub.on("GET /api/me/parental/requests", 405, #"{"title":"method not allowed","status":405}"#)
        ParentalStub.on("GET /api/me/parental/requests", 403, #"{"title":"forbidden","status":403,"code":"not_restricted"}"#)
        let client = ParentalStub.client()
        let list = try #require(try await client.myParentalRequests())
        #expect(list.map(\.id) == ["r1"] && list[0].status == .pending && list[0].subjectID == "v-16")
        #expect(try await client.myParentalRequests() == nil, "404: a home from before the route")
        #expect(try await client.myParentalRequests() == nil, "405 too")
        await #expect(throws: APIError.self) { _ = try await client.myParentalRequests() }
    }

    /// The blocked page end to end against the home's answers: ask, waiting, a no, Ask again.
    @MainActor @Test func afterANoTheChildAsksAgainAndWaitsAgain() async {
        ParentalStub.reset()
        let made = { (id: String) in #"{"request":{"id":"\#(id)","child_id":"c1","subject_kind":"video","subject_id":"v-16","status":"pending"}}"# }
        ParentalStub.on("POST /api/me/parental/requests", 201, made("r1"))
        ParentalStub.on("POST /api/me/parental/requests", 201, made("r2"))
        let row = { (id: String, status: String) in #"{"id":"\#(id)","subject_kind":"video","subject_id":"v-16","status":"\#(status)","expires_at":"2026-11-19T10:00:00Z"}"# }
        ParentalStub.on("GET /api/me/parental/requests", 200, #"{"requests":[\#(row("r1", "denied"))]}"#)
        ParentalStub.on("GET /api/me/parental/requests", 200, #"{"requests":[\#(row("r2", "approved")),\#(row("r1", "denied"))]}"#)
        let page = BlockedPage(client: ParentalStub.client(), subject: .video("v-16"), limit: ParentalLimit(restricted: true, maxTier: 9))
        await page.askParent()
        #expect(page.waiting && !page.canPressAsk && page.askKey("parental.blocked.ask") == "parental.ask.pending")
        await page.follow.check()
        #expect(page.denied && page.canPressAsk && page.askLine(en) == "A parent said no.")
        await page.askParent()
        #expect(page.waiting && page.askLine(en) == "Request sent. You'll see it here when a parent answers.")
        #expect(ParentalStub.seen("POST /api/me/parental/requests").count == 2)
        await page.follow.check()
        #expect(page.follow.outcome == .approved && page.follow.approvals == 1)
    }

    /// The time's-up screen's minutes share one follower: whatever number was asked for, the
    /// newest time request decides.
    @MainActor @Test func theTimesUpScreensRequestIsFollowedWhateverItsMinutes() async {
        ParentalStub.reset()
        ParentalStub.on("POST /api/me/parental/requests", 201,
                        #"{"request":{"id":"t1","child_id":"c1","subject_kind":"time","subject_id":"60","minutes":60,"status":"pending"}}"#)
        ParentalStub.on("GET /api/me/parental/requests", 200,
                        #"{"requests":[{"id":"t1","subject_kind":"time","subject_id":"60","minutes":60,"status":"approved"}]}"#)
        let page = BlockedPage(client: ParentalStub.client(), subject: .time(minutes: 30), limit: ParentalLimit(restricted: true, maxTier: 9))
        let ask = page.asking(.time(minutes: 60))
        #expect(ask.follow === page.follow && page.follow.match == .time)
        await ask.askParent()
        #expect(page.waiting)
        await page.follow.check()
        #expect(page.follow.approvals == 1 && !page.waiting)
    }

    // MARK: the time that cannot be read (57.14)

    /// 503 `parental_time_unknown`: not an error screen and not time's up; the player waits
    /// as the header says and asks again.
    @MainActor @Test func aTimeThatCannotBeReadIsWaitedForNotAnError() async {
        ParentalStub.reset()
        let unknown = #"{"type":"https://maktaba.tv/problems/unavailable","title":"service unavailable","status":503,"code":"parental_time_unknown"}"#
        ParentalStub.on("POST /api/stream/sessions/s1/progress", 503, unknown, headers: ["Retry-After": "5"])
        ParentalStub.on("GET /api/channels/ch-1/access", 503, unknown, headers: ["Retry-After": "7"])
        let client = ParentalStub.client()
        do {
            try await client.postProgress(session: "s1", positionSec: 10)
            Issue.record("the post was refused")
        } catch {
            #expect(HomeFailure(error).kind == .timeUnknown(retryAfter: 5))
            #expect(PlaybackVerdict.progress(error, limited: true) == .other, "the next post asks again; nothing stops")
            #expect(PlaybackVerdict.open(error, limited: true) == .other)
            #expect(TimeUp(error) == nil && ParentalRefusal(error) == nil, "not the time's-up screen")
        }
        do {
            _ = try await client.channelAccess("ch-1")
            Issue.record("the access was refused")
        } catch {
            #expect(HomeFailure(error).kind.waitRetrySeconds == 7)
        }
        #expect(ErrorKind.timeUnknown(retryAfter: nil).waitRetrySeconds == 5)
        #expect(ErrorKind.timeUnknown(retryAfter: 0).waitRetrySeconds == 1 && ErrorKind.timeUnknown(retryAfter: 900).waitRetrySeconds == 60)
        #expect(ErrorKind.timeUnknown(retryAfter: 5).transcodeRetrySeconds == nil)
        #expect(ErrorKind.transcodeBusy(retryAfter: 10).waitRetrySeconds == 10 && ErrorKind.busy(retryAfter: 10).waitRetrySeconds == nil)
        // a plain 503 is the home being busy, as before
        #expect(APIError.status(503, #"{"title":"service unavailable","status":503}"#, retryAfter: 5).kind == .busy(retryAfter: 5))
        let enWords = ErrorStrings(.en), arWords = ErrorStrings(.ar)
        #expect(enWords.sentence(.timeUnknown(retryAfter: 5), video: true) == "Your home is checking your watching time. This will start in a moment.")
        #expect(arWords.sentence(.timeUnknown(retryAfter: 5)) == "يتحقق جهازك الرئيسي من وقت المشاهدة. سيبدأ التشغيل بعد قليل.")
    }

    // MARK: a channel refused for time (57.14 on 57.10's routes)

    /// `GET /api/channels/{id}/access` and `POST /api/channels/{id}/play` answer 403
    /// `parental-time` when the account's time is over: the time's-up screen, not the slate.
    @MainActor @Test func aChannelRefusedForTimeIsTimesUpNotTheSlate() async {
        ParentalStub.reset()
        ParentalStub.on("GET /api/channels/ch-1/access", 403, ParentalFixtures.timeOver)
        ParentalStub.on("POST /api/channels/ch-1/play", 403,
                        #"{"type":"https://maktaba.tv/problems/parental-time","status":403,"code":"parental_time","reason":"outside_slot"}"#)
        ParentalStub.on("POST /api/channels/ch-2/play", 403,
                        #"{"type":"https://maktaba.tv/problems/restricted","status":403,"code":"restricted","reason":"programme","retry_at":"2026-11-12T20:00:00Z"}"#)
        let client = ParentalStub.client()
        do {
            _ = try await client.channelAccess("ch-1")
            Issue.record("the access was refused")
        } catch {
            #expect(TimeUp(error) == TimeUp(reason: "limit_reached", nextAllowedAt: homeTime("2026-11-12T04:00:00Z")))
        }
        do {
            _ = try await client.playChannel("ch-1")
            Issue.record("the play was refused")
        } catch {
            #expect(TimeUp(error) == TimeUp(reason: "outside_slot", nextAllowedAt: nil))
            #expect(TimeUp(error)?.title(en) == "Not watching time yet")
        }
        do {
            _ = try await client.playChannel("ch-2")
            Issue.record("the play was refused")
        } catch {
            #expect(TimeUp(error) == nil && ParentalRefusal(error) == .programme(retryAt: homeTime("2026-11-12T20:00:00Z")),
                    "a programme above the limit is still the slate")
        }
        #expect(TimeUp(APIError.status(404, ParentalFixtures.notFound)) == nil && TimeUp(APIError.transport("down")) == nil)
    }

    // MARK: media served too fast (57.14)

    /// A restricted account's stream answers 429 (`media-too-fast`, `Retry-After`) to a player
    /// that reads far ahead: the proxy waits as the home said and asks again, and the player
    /// gets the segment, not an error.
    @Test func aStreamAnsweredTooFastIsWaitedOutAndAskedAgain() async throws {
        ParentalStub.reset()
        let tooFast = #"{"type":"https://maktaba.invalid/problems/media-too-fast","title":"Too fast","status":429,"detail":"this account's media is served as it plays; ask again shortly"}"#
        ParentalStub.on("GET /stream/abc/seg-7.m4s", 429, tooFast, headers: ["Retry-After": "30"])
        ParentalStub.on("GET /stream/abc/seg-7.m4s", 429, tooFast)
        ParentalStub.on("GET /stream/abc/seg-7.m4s", 200, "media")
        ParentalStub.on("GET /stream/abc/seg-8.m4s", 429, tooFast, headers: ["Retry-After": "1"])
        let waits = Waits()
        let proxy = try Self.proxy { waits.add($0) }
        try await proxy.start()
        defer { proxy.stop() }
        let local = try #require(proxy.local(URL(string: "http://parental.test/stream/abc/seg-7.m4s?sig=x")!))
        let (data, response) = try await URLSession.shared.data(from: local)
        #expect((response as? HTTPURLResponse)?.statusCode == 200 && String(data: data, encoding: .utf8) == "media")
        #expect(ParentalStub.seen("GET /stream/abc/seg-7.m4s").count == 3)
        #expect(waits.all == [.seconds(25), .seconds(2)], "as the home said, 25 s at most; 2 s when it named no time")

        // a stream that stays too fast is handed to the player after three tries, not held for ever
        let other = try #require(proxy.local(URL(string: "http://parental.test/stream/abc/seg-8.m4s")!))
        let (_, refused) = try await URLSession.shared.data(from: other)
        #expect((refused as? HTTPURLResponse)?.statusCode == 429)
        #expect(ParentalStub.seen("GET /stream/abc/seg-8.m4s").count == 3 && waits.all.count == 4)
        #expect(StreamProxy.Pace.wait(retryAfter: 0) == 1 && StreamProxy.Pace.wait(retryAfter: 10) == 10)
    }

    final class Waits: @unchecked Sendable {
        private let lock = NSLock()
        private var list: [Duration] = []
        func add(_ d: Duration) { lock.withLock { list.append(d) } }
        var all: [Duration] { lock.withLock { list } }
    }

    /// A proxy for the stub home, signed in with a token that has not expired.
    static func proxy(sleep: @escaping @Sendable (Duration) async throws -> Void) throws -> StreamProxy {
        let claims = Data(#"{"exp":4102444800}"#.utf8).base64EncodedString()
            .replacingOccurrences(of: "+", with: "-").replacingOccurrences(of: "/", with: "_").replacingOccurrences(of: "=", with: "")
        let secrets = FakeSecrets(["http://parental.test#access": "h.\(claims).s", "http://parental.test#refresh": "r1"])
        let tokens = TokenStore(service: "test.\(UUID().uuidString)", home: ParentalStub.home, primary: secrets, legacy: nil)
        let c = URLSessionConfiguration.ephemeral
        c.protocolClasses = [ParentalStub.self]
        let session = URLSession(configuration: c)
        return try StreamProxy(client: APIClient(home: ParentalStub.home, tokens: tokens, session: session), session: session, sleep: sleep)
    }
}

/// `GET /api/me/history` under a parental limit (57.03): a video watched before and hidden
/// now stays as a row that names nothing.
@Suite struct HistoryUnderALimit {
    @Test func aRowThatIsNotAvailableNamesNoVideo() throws {
        let json = #"""
        {"items":[
          {"key":"11111111-1111-4111-8111-111111111111","available":true,"video_id":"11111111-1111-4111-8111-111111111111","title":"Seerah 1","duration_sec":1200,"times_watched":2,"last_watched_at":"2026-11-12T09:00:00Z","position_sec":600,"completed":false},
          {"key":"na-1762938000","available":false,"video_id":"","title":"","duration_sec":0,"times_watched":3,"last_watched_at":"2026-11-11T09:00:00Z","position_sec":0,"completed":false},
          {"key":"na-1762851600","available":false,"video_id":"22222222-2222-4222-8222-222222222222","title":"must not show","duration_sec":900,"times_watched":1,"last_watched_at":"2026-11-10T09:00:00Z","position_sec":30,"completed":true},
          {"available":false,"video_id":"","times_watched":1},
          {"video_id":"33333333-3333-4333-8333-333333333333","title":"An older home's row","times_watched":1,"position_sec":0}
        ]}
        """#
        let rows = try JSONDecoder.home.decode(Items<HistoryEntry>.self, from: Data(json.utf8)).items
        #expect(rows.count == 4, "a row with no key and no video cannot be told apart: left out")
        #expect(rows[0].available && rows[0].id == rows[0].videoID && rows[0].title == "Seerah 1" && rows[0].progress == 0.5)
        #expect(!rows[1].available && rows[1].id == "na-1762938000" && rows[1].videoID.isEmpty && rows[1].title.isEmpty)
        #expect(rows[1].timesWatched == 3 && rows[1].lastWatchedAt == homeTime("2026-11-11T09:00:00Z"))
        // whatever the home sent for a row that is not available, nothing of the video is kept
        #expect(!rows[2].available && rows[2].videoID.isEmpty && rows[2].title.isEmpty && rows[2].durationSec == nil)
        #expect(rows[2].positionSec == 0 && !rows[2].completed && rows[2].progress == 0)
        #expect(rows[3].available && rows[3].id == "33333333-3333-4333-8333-333333333333", "a home from before `key`: the video's id")
        #expect(Set(rows.map(\.id)).count == rows.count, "every row has its own identity")
        #expect(ParentalWords.text("live.parental.notAvailable", en) == "Not available")
        #expect(ParentalWords.text("live.parental.notAvailable", ar) == "غير متاح")
    }
}
