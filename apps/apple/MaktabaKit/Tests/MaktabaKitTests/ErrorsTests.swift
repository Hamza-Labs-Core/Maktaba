import Foundation
import Testing
@testable import MaktabaKit

/// The words this audit added to ErrorStrings, and the home picker's "no answer".
@Suite struct ErrorWordsMore {
    @Test func theSavedSignInCheckIsSaidInBothLanguages() {
        #expect(ErrorStrings(.en).text(.restoreFailed) == "You're still signed in, but your home couldn't be checked.")
        #expect(ErrorStrings(.ar).text(.restoreFailed) == "ما زلت مسجّل الدخول، لكن تعذّر التحقق من الجهاز الرئيسي.")
        for key in ErrorStrings.Key.allCases {
            #expect(ErrorStrings.en[key] != nil && ErrorStrings.ar[key] != nil, "\(key)")
        }
    }

    /// Not "Could not reach the home: The request timed out." in English inside Arabic.
    @Test func aHomeThatDoesNotAnswerIsSaidInTheAppsWords() {
        #expect(ConnectStrings(.en).reach(.unreachable("The request timed out.")) == ErrorStrings(.en).text(.unreachable))
        #expect(ConnectStrings(.ar).reach(.unreachable("The request timed out.")) == ErrorStrings(.ar).text(.unreachable))
    }
}

@Suite struct Cancelling {
    @Test func aCancelledRequestIsNotAFailure() {
        #expect(Log.isCancellation(CancellationError()))
        #expect(Log.isCancellation(URLError(.cancelled)))
        #expect(Log.isCancellation(NSError(domain: NSURLErrorDomain, code: NSURLErrorCancelled)))
        #expect(!Log.isCancellation(URLError(.timedOut)))
        #expect(!Log.isCancellation(APIError.signedOut))
    }
}

@Suite struct Logging {
    @Test func routesLoseTheirIds() {
        #expect(Log.route("/api/videos/01J9ZK3M4N5P6Q7R8S9T0V1W2X/poster") == "/api/videos/:id/poster")
        #expect(Log.route("/api/pairing/device/ABCD1234") == "/api/pairing/device/:id")
        #expect(Log.route("/api/queues/transcode/failures") == "/api/queues/transcode/failures")
        #expect(Log.route("/api/stream/sessions/3f2a-9c/progress") == "/api/stream/sessions/:id/progress")
        #expect(Log.route("/maktaba/api/libraries") == "/maktaba/api/libraries")
    }

    @Test func aRequestIsDescribedWithoutItsQuery() {
        var r = URLRequest(url: URL(string: "http://h:8088/api/videos?q=my%20secret%20search&cursor=abc")!)
        r.httpMethod = "GET"
        #expect(APIClient.describe(r) == "GET /api/videos")
    }
}

@Suite struct Crashes {
    static let payload = #"""
    {"timeStampBegin":"2026-10-26 10:00:00","crashDiagnostics":[{"callStackTree":{"callStacks":[]},
    "diagnosticMetaData":{"appVersion":"0.1.0","appBuildVersion":"7","osVersion":"iPhone OS 26.1",
    "exceptionType":1,"exceptionCode":0,"signal":11,"terminationReason":"Namespace SIGNAL, Code 11"}}]}
    """#

    @Test func aCrashIsSummedUpInOneLine() {
        let lines = CrashReports.summaries(fromPayload: Data(Self.payload.utf8))
        #expect(lines == ["exception=1 code=0 signal=11 reason=Namespace SIGNAL, Code 11 app=0.1.0 build=7 os=iPhone OS 26.1"])
        #expect(CrashReports.summaries(fromPayload: Data(#"{"hangDiagnostics":[]}"#.utf8)).isEmpty)
        #expect(CrashReports.summaries(fromPayload: Data("not json".utf8)).isEmpty)
    }

    @Test func theLastFewReportsAreKept() throws {
        let dir = FileManager.default.temporaryDirectory.appending(path: "maktaba-crash-\(UUID().uuidString)")
        defer { try? FileManager.default.removeItem(at: dir) }
        for i in 0..<(CrashReports.keep + 3) {
            try CrashReports.store(Data(Self.payload.utf8), in: dir, now: Date(timeIntervalSince1970: TimeInterval(1_000_000 + i)))
        }
        let files = try FileManager.default.contentsOfDirectory(atPath: dir.path()).sorted()
        #expect(files.count == CrashReports.keep)
        #expect(files.first?.hasPrefix("crash-1000003-") == true)
    }
}

@Suite struct SavedHomes {
    @Test func anUnreadableListIsKeptAsideNotLost() throws {
        let defaults = try #require(UserDefaults(suiteName: "maktabakit.test.\(UUID().uuidString)"))
        let junk = Data("{not a list".utf8)
        #expect(AppSession.decodeHomes(junk, defaults: defaults).isEmpty)
        #expect(defaults.data(forKey: "homes.unreadable") == junk)
        #expect(AppSession.decodeHomes(nil, defaults: defaults).isEmpty)
    }
}

extension AgainstAStubHome {
    /// A refresh that gets no answer keeps the sign-in (the home was off for a moment);
    /// only a home that turns the refresh token down signs the person out.
    @Suite struct Refreshing {
        static let home = URL(string: "http://refresh.test")!

        func client(_ secrets: FakeSecrets) -> (APIClient, TokenStore) {
            let tokens = TokenStore(service: "test.\(UUID().uuidString)", home: Self.home, primary: secrets, legacy: nil)
            return (APIClient(home: Self.home, tokens: tokens, session: RouteStub.session()), tokens)
        }

        func signedIn() -> FakeSecrets {
            FakeSecrets(["http://refresh.test#access": "old", "http://refresh.test#refresh": "r1"])
        }

        @Test func aHomeThatDoesNotAnswerTheRefreshKeepsTheSignIn() async throws {
            RouteStub.reset()
            RouteStub.on("GET refresh.test/api/libraries", .json(401, #"{"title":"expired"}"#))
            // no answer for the refresh: it cannot connect
            let (c, tokens) = client(signedIn())
            do {
                _ = try await c.libraries()
                Issue.record("a 401 read as libraries")
            } catch {
                #expect(HomeFailure(error).kind == .unreachable)
            }
            #expect(tokens.refresh == "r1")
        }

        @Test func aHomeThatFailsTheRefreshKeepsTheSignIn() async throws {
            RouteStub.reset()
            RouteStub.on("GET refresh.test/api/libraries", .json(401, "{}"))
            RouteStub.on("POST refresh.test/api/auth/refresh", .json(503, "{}"))
            let (c, tokens) = client(signedIn())
            await #expect(throws: APIError.status(503, "{}")) { try await c.libraries() }
            #expect(tokens.refresh == "r1")
        }

        @Test func aRefusedRefreshSignsOut() async throws {
            RouteStub.reset()
            RouteStub.on("GET refresh.test/api/libraries", .json(401, "{}"))
            RouteStub.on("POST refresh.test/api/auth/refresh", .json(401, #"{"title":"refresh token revoked"}"#))
            let (c, tokens) = client(signedIn())
            await #expect(throws: APIError.signedOut) { try await c.libraries() }
            #expect(tokens.refresh == nil && tokens.access == nil)
        }
    }

    /// A saved sign-in the home cannot check at launch stays, and the sign-in screen says why.
    @MainActor @Suite struct Restoring {
        @Test func aHomeThatDoesNotAnswerAtLaunchKeepsTheSignIn() async throws {
            let defaults = try #require(UserDefaults(suiteName: "maktabakit.test.\(UUID().uuidString)"))
            // a host that never resolves: the check fails at once, as with a home that is off
            let home = Home(url: URL(string: "http://restore-\(UUID().uuidString.prefix(8)).invalid")!, name: "Off")
            let app = "test.restore.\(UUID().uuidString.prefix(8))"
            let tokens = TokenStore(app: app, home: home.url)
            tokens.save(AuthTokens(accessToken: "a", refreshToken: "r", user: nil))
            defer { tokens.clear() }
            let s = AppSession(app: app, defaults: defaults, asksHomes: false)
            s.use(home)
            #expect(s.restoring)
            for _ in 0..<100 where s.restoring { try await Task.sleep(for: .milliseconds(100)) }
            #expect(!s.restoring)
            #expect(s.phase == .signingIn)
            #expect(s.restoreProblem?.kind == .unreachable)
            #expect(tokens.refresh == "r")
            s.switchHome()
            #expect(s.restoreProblem == nil)
        }
    }
}
