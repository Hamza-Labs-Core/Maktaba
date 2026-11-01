import Foundation
import Testing
@testable import MaktabaKit

// Epic 48 story 48.37: every step connecting publishes, against the stub home (`RouteStub`,
// the fake transport) and a fake clock whose sleeps wait on a latch the test opens.

/// A clock that never reads the wall clock. `sleep` parks until the test calls `advance`
/// (`nextSleep` waits, on a latch, for the code to park); `autoAdvance` makes each sleep
/// move the time on at once instead (a code's polling until it expires).
final class LatchClock: @unchecked Sendable {
    private let lock = NSLock()
    private var current: Date
    private let autoAdvance: Bool
    private var parked: [(id: UUID, length: Duration, resume: CheckedContinuation<Void, Error>)] = []
    private var requests: [Duration] = []
    private var requestWaiter: CheckedContinuation<Duration, Never>?

    init(_ start: Date = Date(timeIntervalSince1970: 1_900_000_000), autoAdvance: Bool = false) {
        current = start
        self.autoAdvance = autoAdvance
    }

    var now: Date { lock.withLock { current } }

    var clock: ConnectClock {
        ConnectClock(now: { [self] in now }, sleep: { [self] d in try await sleep(d) })
    }

    func sleep(_ d: Duration) async throws {
        if autoAdvance {
            lock.withLock { current = current.addingTimeInterval(Self.seconds(d)) }
            try Task.checkCancellation()
            return
        }
        let id = UUID()
        try await withTaskCancellationHandler {
            try await withCheckedThrowingContinuation { (c: CheckedContinuation<Void, Error>) in
                let waiter: CheckedContinuation<Duration, Never>? = lock.withLock {
                    parked.append((id, d, c))
                    if let w = requestWaiter {
                        requestWaiter = nil
                        return w
                    }
                    requests.append(d)
                    return nil
                }
                waiter?.resume(returning: d)
            }
        } onCancel: { [self] in
            let c = lock.withLock { () -> CheckedContinuation<Void, Error>? in
                guard let i = parked.firstIndex(where: { $0.id == id }) else { return nil }
                return parked.remove(at: i).resume
            }
            c?.resume(throwing: CancellationError())
        }
    }

    /// Waits until the code sleeps, and says for how long.
    func nextSleep() async -> Duration {
        await withCheckedContinuation { (c: CheckedContinuation<Duration, Never>) in
            let ready: Duration? = lock.withLock {
                if !requests.isEmpty { return requests.removeFirst() }
                requestWaiter = c
                return nil
            }
            if let ready { c.resume(returning: ready) }
        }
    }

    /// Moves the time on by the first parked sleep's length and wakes it.
    func advance() {
        let c = lock.withLock { () -> CheckedContinuation<Void, Error>? in
            guard !parked.isEmpty else { return nil }
            let first = parked.removeFirst()
            current = current.addingTimeInterval(Self.seconds(first.length))
            return first.resume
        }
        c?.resume()
    }

    static func seconds(_ d: Duration) -> TimeInterval {
        TimeInterval(d.components.seconds) + TimeInterval(d.components.attoseconds) / 1e18
    }
}

/// Every progress published, in order.
@MainActor final class ProgressLog {
    private(set) var steps: [ConnectProgress?] = []
    private var waiting: (test: (ConnectProgress?) -> Bool, resume: CheckedContinuation<Void, Never>)?

    func add(_ p: ConnectProgress?) {
        steps.append(p)
        if let w = waiting, w.test(p) {
            waiting = nil
            w.resume.resume()
        }
    }

    /// Returns once a step `test` accepts is published (a latch on the log, not a timer).
    func until(_ test: @escaping (ConnectProgress?) -> Bool) async {
        if let last = steps.last, test(last) { return }
        await withCheckedContinuation { waiting = (test, $0) }
    }
}

extension AgainstAStubHome {
    @MainActor @Suite struct ConnectProgressSteps {
        static let me = #"{"id":"u1","username":"tester","is_admin":false,"home_name":"Living room"}"#

        func session(_ clock: LatchClock = LatchClock()) throws -> (AppSession, ProgressLog) {
            let defaults = try #require(UserDefaults(suiteName: "maktabakit.progress.\(UUID().uuidString)"))
            let s = AppSession(app: "test.progress", defaults: defaults, probe: HomeProbe(session: RouteStub.session(), timeout: 2),
                               asksHomes: false, homeSession: RouteStub.session(), clock: clock.clock)
            let log = ProgressLog()
            s.progressObserver = { log.add($0) }
            return (s, log)
        }

        // MARK: finding the home

        @Test func findingThenEachAddressThenFound() async throws {
            RouteStub.reset()
            RouteStub.on("GET relay.test/api/discovery/home", .json(200, AgainstAStubHome.discovery))
            let (s, log) = try session()
            let lan = URL(string: "http://lan.test:8088")!, relay = URL(string: "https://relay.test")!
            #expect(await s.connect(to: [lan, relay]) == nil)
            #expect(log.steps == [
                ConnectProgress(.finding),
                ConnectProgress(.trying(address: "http://lan.test:8088", index: 1, count: 2)),
                ConnectProgress(.trying(address: "https://relay.test", index: 2, count: 2)),
                ConnectProgress(.found(name: "Living room")),
                nil, // no saved sign-in: the sign-in form, nothing in flight
            ])
            #expect(s.progress == nil && s.phase == .signingIn)
        }

        @Test func nothingAnswersThenRetryCountsTheAttempt() async throws {
            RouteStub.reset()
            let (s, log) = try session()
            let gone = URL(string: "http://gone.test:8088")!
            #expect(await s.connect(to: [gone]) != nil)
            let failed = ConnectProgress(.failed(reason: .unreachable, tried: ["http://gone.test:8088"]))
            #expect(log.steps == [ConnectProgress(.finding), ConnectProgress(.trying(address: "http://gone.test:8088", index: 1, count: 1)), failed])
            #expect(s.progress == failed && s.connectProblem != nil)

            // Retry: the same steps again, as attempt 2; then the home is up for attempt 3
            #expect(await s.retryConnect() != nil)
            #expect(Array(log.steps.suffix(3)) == [
                ConnectProgress(.finding, attempt: 2),
                ConnectProgress(.trying(address: "http://gone.test:8088", index: 1, count: 1), attempt: 2),
                ConnectProgress(.failed(reason: .unreachable, tried: ["http://gone.test:8088"]), attempt: 2),
            ])
            RouteStub.on("GET gone.test/api/discovery/home", .json(200, AgainstAStubHome.discovery))
            #expect(await s.retryConnect() == nil)
            #expect(log.steps.contains(ConnectProgress(.found(name: "Living room"), attempt: 3)))
            // a new address the person gives starts again at 1
            RouteStub.reset()
            s.switchHome()
            _ = await s.connect(to: [gone])
            #expect(s.progress?.attempt == 1)
        }

        @Test func aDifferentHomeAndNotAHome() async throws {
            RouteStub.reset()
            RouteStub.on("GET lan.test/api/discovery/home", .json(200, #"{"id":"neighbour","urls":[]}"#))
            RouteStub.on("GET router.test/api/discovery/home", .text(200, "<html>router</html>"))
            let (s, _) = try session()
            _ = await s.connect(to: [URL(string: "http://lan.test:8088")!], homeID: "home-1")
            #expect(s.progress == ConnectProgress(.failed(reason: .otherHome, tried: ["http://lan.test:8088"])))
            _ = await s.connect(to: [URL(string: "http://router.test")!])
            #expect(s.progress == ConnectProgress(.failed(reason: .notAHome, tried: ["http://router.test"])))
            // the screen clearing the problem clears its progress
            s.connectProblem = nil
            #expect(s.progress == nil)
        }

        // MARK: restoring the saved sign-in

        /// A home answering 503 with Retry-After says so with the time of the next try, and
        /// is asked again by itself then, as the next attempt.
        @Test func aBusyHomeIsAskedAgainWhenItSays() async throws {
            RouteStub.reset()
            RouteStub.on("GET busy.test/api/auth/me",
                         .init(status: 503, body: #"{"title":"starting"}"#, headers: ["Retry-After": "30"]),
                         .json(200, Self.me))
            let clock = LatchClock()
            let start = clock.now
            let (s, log) = try session(clock)
            let home = URL(string: "http://busy.test:8088")!
            let tokens = TokenStore(app: "test.progress", home: home)
            tokens.save(AuthTokens(accessToken: "saved.access.token", refreshToken: "r", user: nil))
            defer { tokens.clear() }

            s.use(Home(url: home, name: "Living room"))
            #expect(s.progress == ConnectProgress(.reconnecting(name: "Living room")))
            // the latch: the restore waits on the clock now
            #expect(await clock.nextSleep() == .seconds(30))
            let busy = ConnectProgress(.homeBusy(retryAt: start.addingTimeInterval(30)))
            #expect(s.progress == busy && s.restoring)
            #expect(busy.detail(now: start.addingTimeInterval(18), Locale(identifier: "en")) == "Next try in 0:12")
            clock.advance()
            await log.until { $0 == nil }
            #expect(s.phase == .signedIn(User(id: "u1", username: "tester", isAdmin: false)))
            #expect(log.steps == [
                ConnectProgress(.reconnecting(name: "Living room")),
                busy,
                ConnectProgress(.reconnecting(name: "Living room"), attempt: 2),
                nil,
            ])
        }

        @Test func aHomeThatDoesNotAnswerFailsThenRetrySignsIn() async throws {
            RouteStub.reset()
            let (s, log) = try session()
            let home = URL(string: "http://off.test:8088")!
            let tokens = TokenStore(app: "test.progress", home: home)
            tokens.save(AuthTokens(accessToken: "saved.access.token", refreshToken: "r", user: nil))
            defer { tokens.clear() }

            s.use(Home(url: home, name: "Living room"))
            await log.until { $0?.isWorking == false }
            let failed = ConnectProgress(.failed(reason: .unreachable, tried: ["http://off.test:8088"]))
            #expect(s.progress == failed && s.restoreProblem != nil)

            RouteStub.on("GET off.test/api/auth/me", .json(200, Self.me))
            await s.retryRestore()
            #expect(s.signedIn)
            #expect(Array(log.steps.suffix(2)) == [ConnectProgress(.reconnecting(name: "Living room"), attempt: 2), nil])
        }

        // MARK: signing in by password

        @Test func aWrongPasswordThenTheNextAttempt() async throws {
            RouteStub.reset()
            RouteStub.on("POST pw.test/api/auth/login", .json(401, #"{"title":"invalid credentials"}"#),
                         .json(200, #"{"access_token":"a.b.c","refresh_token":"r"}"#))
            RouteStub.on("GET pw.test/api/auth/me", .json(200, Self.me))
            let (s, log) = try session()
            let home = URL(string: "http://pw.test:8088")!
            TokenStore(app: "test.progress", home: home).clear()
            defer { TokenStore(app: "test.progress", home: home).clear() }
            s.use(Home(url: home, name: "Living room"))

            await #expect(throws: APIError.self) { try await s.signIn(username: "tester", password: "wrong") }
            #expect(s.progress == ConnectProgress(.failed(reason: .wrongPassword, tried: [])))
            try await s.signIn(username: "tester", password: "right")
            #expect(s.signedIn && s.progress == nil)
            #expect(log.steps == [
                ConnectProgress(.signingIn(home: "Living room")),
                ConnectProgress(.failed(reason: .wrongPassword, tried: [])),
                ConnectProgress(.signingIn(home: "Living room"), attempt: 2),
                nil,
            ])
        }

        // MARK: signing in by code

        static let code = URL(string: "http://code.test:8088")!

        func codeFlow(_ clock: LatchClock, tokens: TokenStore) -> (CodeSignIn, ProgressLog) {
            let client = APIClient(home: Self.code, tokens: tokens, session: RouteStub.session())
            let f = CodeSignIn(client: client, label: "iPhone", homeName: "Living room", clock: clock.clock) {}
            let log = ProgressLog()
            f.progressObserver = { log.add($0) }
            return (f, log)
        }

        /// A code nobody approves in time: said so, then a new one, as attempt 2.
        @Test func anApprovalThatExpiresThenANewCode() async throws {
            let clock = LatchClock(Date(timeIntervalSince1970: 1_900_000_000), autoAdvance: true)
            let first = ISO8601DateFormatter().string(from: clock.now.addingTimeInterval(60))
            RouteStub.reset()
            RouteStub.on("POST code.test/api/pairing/device",
                         .json(201, #"{"code":"AAAA-1111","device_code":"dc1","expires_at":"\#(first)","interval":30}"#),
                         .json(201, #"{"code":"BBBB-2222","device_code":"dc2","expires_at":"2099-01-01T00:00:00Z","interval":30}"#))
            RouteStub.on("POST code.test/api/pairing/device/token",
                         .json(202, #"{"status":"pending"}"#), .json(202, #"{"status":"pending"}"#),
                         .json(200, #"{"access_token":"a.b.c","refresh_token":"r"}"#))
            let tokens = TokenStore(app: "test.progress.code", home: Self.code)
            tokens.clear()
            defer { tokens.clear() }
            let (f, log) = codeFlow(clock, tokens: tokens)
            await f.run()
            let expiry1 = try #require(homeTime(first))
            let expiry2 = try #require(homeTime("2099-01-01T00:00:00Z"))
            #expect(f.status == .approved)
            #expect(log.steps == [
                ConnectProgress(.signingIn(home: "Living room")),
                ConnectProgress(.awaitingApproval(code: "AAAA-1111", expiresAt: expiry1)),
                ConnectProgress(.failed(reason: .approvalExpired, tried: [])),
                ConnectProgress(.signingIn(home: "Living room"), attempt: 2),
                ConnectProgress(.awaitingApproval(code: "BBBB-2222", expiresAt: expiry2), attempt: 2),
                ConnectProgress(.signingIn(home: "Living room"), attempt: 2),
            ])
            // the code's countdown, as the screen shows it
            #expect(ConnectProgress(.awaitingApproval(code: "AAAA-1111", expiresAt: expiry1))
                .detail(now: expiry1.addingTimeInterval(-65), Locale(identifier: "en")) == "Code \u{2068}AAAA-1111\u{2069} · 1:05 left")
        }

        /// A home that is starting answers the code request 503 with Retry-After: the wait
        /// shows, then the code, as attempt 2.
        @Test func aBusyHomeBeforeTheCode() async throws {
            let clock = LatchClock()
            let start = clock.now
            RouteStub.reset()
            RouteStub.on("POST code.test/api/pairing/device",
                         .init(status: 503, body: "", headers: ["Retry-After": "20"]),
                         .json(201, #"{"code":"CCCC-3333","device_code":"dc3","expires_at":"2099-01-01T00:00:00Z","interval":5}"#))
            RouteStub.on("POST code.test/api/pairing/device/token", .json(202, #"{"status":"pending"}"#))
            let tokens = TokenStore(app: "test.progress.busy", home: Self.code)
            let (f, log) = codeFlow(clock, tokens: tokens)
            let run = Task { await f.run() }
            #expect(await clock.nextSleep() == .seconds(20))
            #expect(f.progress == ConnectProgress(.homeBusy(retryAt: start.addingTimeInterval(20))))
            clock.advance()
            // the code shows, then it polls every 5 s: parked again on the clock
            #expect(await clock.nextSleep() == .seconds(5))
            #expect(f.progress == ConnectProgress(.awaitingApproval(code: "CCCC-3333", expiresAt: try #require(homeTime("2099-01-01T00:00:00Z"))), attempt: 2))
            run.cancel()
            await run.value
            #expect(log.steps.map { $0?.attempt } == [1, 1, 2, 2])
        }

        /// A home that does not answer the code request: why, and the address tried.
        @Test func aHomeThatDoesNotAnswerTheCodeRequest() async throws {
            let clock = LatchClock()
            RouteStub.reset()
            let (f, _) = codeFlow(clock, tokens: TokenStore(app: "test.progress.off", home: Self.code))
            let run = Task { await f.run() }
            #expect(await clock.nextSleep() == .seconds(5))
            #expect(f.progress == ConnectProgress(.failed(reason: .unreachable, tried: ["http://code.test:8088"])))
            run.cancel()
            await run.value
        }
    }
}

// MARK: - the words

@Suite struct ConnectProgressWords {
    let en = Locale(identifier: "en"), ar = Locale(identifier: "ar")
    let at = Date(timeIntervalSince1970: 1_900_000_000)

    var every: [ConnectProgress] {
        [
            ConnectProgress(.finding),
            ConnectProgress(.trying(address: "http://192.168.1.20:8088", index: 1, count: 3)),
            ConnectProgress(.found(name: "Living room")),
            ConnectProgress(.signingIn(home: "Living room")),
            ConnectProgress(.awaitingApproval(code: "ABCD-1234", expiresAt: at)),
            ConnectProgress(.reconnecting(name: "Living room")),
            ConnectProgress(.homeBusy(retryAt: at)),
        ] + [ConnectProgress.Failure.unreachable, .notAHome, .otherHome, .wrongPassword, .approvalExpired, .busy, .server]
            .map { ConnectProgress(.failed(reason: $0, tried: ["http://192.168.1.20:8088"]), attempt: 2) }
    }

    @Test func eachStepSaysWhatIsHappening() {
        #expect(ConnectProgress(.finding).headline(en) == "Looking for your home…")
        #expect(ConnectProgress(.trying(address: "http://192.168.1.20:8088", index: 1, count: 3)).headline(en)
            == "Trying \u{2068}http://192.168.1.20:8088\u{2069} — 1 of 3")
        #expect(ConnectProgress(.found(name: "Living room")).headline(en) == "Found \u{2068}Living room\u{2069}")
        #expect(ConnectProgress(.reconnecting(name: "Living room")).headline(en) == "Reconnecting to \u{2068}Living room\u{2069}…")
        #expect(ConnectProgress(.homeBusy(retryAt: at)).detail(now: at.addingTimeInterval(-75), en) == "Next try in 1:15")
        let failed = ConnectProgress(.failed(reason: .unreachable, tried: ["http://a.test", "https://b.test"]), attempt: 2)
        #expect(failed.headline(en) == "Your home didn't answer.")
        #expect(failed.detail(now: at, en) == "Tried: \u{2068}http://a.test\u{2069}, \u{2068}https://b.test\u{2069}")
        #expect(failed.attemptLabel(en) == "Attempt 2")
        #expect(ConnectProgress(.finding).attemptLabel(en) == nil)
    }

    /// Every step in Arabic, never the English words (the catalog's «الجهاز الرئيسي»).
    @Test func everyStepInArabic() {
        for p in every {
            let a = p.headline(ar), e = p.headline(en)
            #expect(a != e, "\(p.stepName) has its Arabic")
            #expect(a.unicodeScalars.contains { (0x0600...0x06FF).contains($0.value) }, "\(a)")
            if let d = p.detail(now: at.addingTimeInterval(-30), ar) {
                #expect(d != p.detail(now: at.addingTimeInterval(-30), en))
            }
        }
        #expect(ConnectProgress(.finding).headline(ar) == "جارٍ البحث عن الجهاز الرئيسي…")
        #expect(ConnectProgress(.finding, attempt: 3).attemptLabel(ar) == "المحاولة 3")
        for busy in [ConnectProgress.Busy.lookingUpCode, .approving, .cloudSignIn] {
            #expect(busy.text(ar) != busy.text(en))
        }
    }

    @Test func theCountdownNeverGoesBelowZero() {
        #expect(ConnectProgress.countdown(to: at, now: at.addingTimeInterval(5)) == "0:00")
        #expect(ConnectProgress.countdown(to: at, now: at.addingTimeInterval(-0.2)) == "0:01")
        #expect(ConnectProgress.countdown(to: at, now: at.addingTimeInterval(-600)) == "10:00")
    }
}

/// No connect, sign-in, restore or retry screen waits with a bare spinner: each one draws
/// the progress (`ConnectProgressView`) or a spinner with its words (`ConnectBusyLabel`).
@Suite struct NoBareSpinner {
    static let screens = ["StartViews.swift", "CloudViews.swift", "ApproveSignIn.swift", "CodeSignIn.swift"]

    @Test(arguments: screens) func theStartScreensHaveNone(file: String) throws {
        let sources = URL(fileURLWithPath: #filePath)
            .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
            .appending(path: "Sources/MaktabaKit")
        let text = try String(contentsOf: sources.appending(path: file), encoding: .utf8)
        // SwiftUI's own ProgressView, not ConnectProgressView
        let bare = text.range(of: #"(?<![A-Za-z])ProgressView\("#, options: .regularExpression)
        #expect(bare == nil, "\(file) waits with a spinner and no words: use ConnectProgressView or ConnectBusyLabel")
    }
}
