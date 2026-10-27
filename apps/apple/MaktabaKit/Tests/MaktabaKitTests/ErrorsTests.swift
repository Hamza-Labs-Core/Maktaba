import Foundation
import Testing
@testable import MaktabaKit

/// Every failure a screen shows is one of a few kinds, said in the app's words (English and
/// Arabic), with its actions; never the home's body, problem detail or a system error's text.
@Suite struct ErrorKinds {
    @Test(arguments: [
        (401, "", "", AppError.Kind.signedOut),
        (403, "", "", .noAccess),
        (404, "", "", .notFound),
        (404, "rendition-not-found", "", .notReady),
        (404, "manifest-not-found", "", .notReady),
        (410, "", "", .notFound),
        (429, "", "", .busy),
        (503, "", "", .busy),
        (503, "", "home_unreachable", .unreachable),
        (502, "", "", .unreachable),
        (504, "", "", .timedOut),
        (504, "", "home_timeout", .timedOut),
        (507, "", "", .diskFull),
        (500, "", "insufficient_storage", .diskFull),
        (400, "", "", .invalid),
        (409, "", "", .invalid),
        (422, "", "", .invalid),
        (500, "", "", .server),
        (418, "", "", .server),
        (200, "home-unreachable", "", .unreachable),
        (0, "", "", .unreachable),
    ])
    func anAnswersKind(status: Int, slug: String, code: String, kind: AppError.Kind) {
        #expect(AppError.kind(status: status, slug: slug, code: code) == kind)
    }

    @Test(arguments: [
        (URLError.Code.notConnectedToInternet, AppError.Kind.offline),
        (.dataNotAllowed, .offline),
        (.timedOut, .timedOut),
        (.cannotFindHost, .unreachable),
        (.cannotConnectToHost, .unreachable),
        (.networkConnectionLost, .unreachable),
        (.dnsLookupFailed, .unreachable),
        (.secureConnectionFailed, .insecure),
        (.serverCertificateUntrusted, .insecure),
        (.appTransportSecurityRequiresSecureConnection, .insecure),
        (.badServerResponse, .badAnswer),
        (.cancelled, .cancelled),
        (.unknown, .unreachable),
    ])
    func aNetworkFailuresKind(code: URLError.Code, kind: AppError.Kind) {
        #expect(AppError(URLError(code)).kind == kind)
        #expect(AppError(APIError.transport(code)).kind == kind)
        #expect(AppError(APIError.transport(URLError(code))).kind == kind)
    }

    @Test func theProblemsTypeAndCodeSortIt() {
        let notReady = APIError.status(404, #"{"type":"https://maktaba.dev/problems/rendition-not-found","title":"x","detail":"open /Volumes/media/a.m3u8: no such file"}"#)
        #expect(AppError(notReady) == AppError(kind: .notReady, status: 404, code: "rendition-not-found"))
        let disk = APIError.status(507, #"{"code":"insufficient_storage","type":"https://maktaba.dev/problems/insufficient-storage"}"#)
        #expect(AppError(disk) == AppError(kind: .diskFull, status: 507, code: "insufficient_storage"))
        // a proxy's HTML page: the status alone
        #expect(AppError(APIError.status(500, "<html>oops</html>")) == AppError(kind: .server, status: 500))
    }

    @Test func otherErrors() {
        #expect(AppError(APIError.signedOut).kind == .signedOut)
        #expect(AppError(APIError.decoding("keyNotFound")).kind == .badAnswer)
        #expect(AppError(CancellationError()).isCancellation)
        #expect(AppError(DecodingError.dataCorrupted(.init(codingPath: [], debugDescription: "x"))).kind == .badAnswer)
        struct Odd: Error {}
        #expect(AppError(Odd()).kind == .server)
        let e = AppError(kind: .busy, status: 429)
        #expect(AppError(e) == e)
    }

    @Test func aWrongPasswordIsNotASignInThatEnded() {
        #expect(AppError.signingIn(APIError.status(401, #"{"title":"invalid credentials"}"#)).kind == .wrongCredentials)
        #expect(AppError.signingIn(APIError.status(429, "")).kind == .busy)
        #expect(AppError.signingIn(URLError(.timedOut)).kind == .timedOut)
    }

    @Test func problemSlugs() {
        #expect(AppError.problemSlug("https://maktaba.dev/problems/rendition-not-found") == "rendition-not-found")
        #expect(AppError.problemSlug("https://maktaba.invalid/problems/home-unreachable?x=1") == "home-unreachable")
        #expect(AppError.problemSlug("about:blank") == "")
        #expect(AppError.problemSlug(nil) == "")
    }

    @Test func actions() {
        #expect(AppError(kind: .unreachable).actions == [.retry, .chooseHome])
        #expect(AppError(kind: .signedOut).actions == [.signIn])
        #expect(AppError(kind: .notFound).actions == [.back])
        #expect(AppError(kind: .server).actions == [.retry])
        for kind in AppError.Kind.allCases where kind != .cancelled {
            #expect(!AppError(kind: kind).systemImage.isEmpty)
        }
    }

    /// The screen offers the actions it can take: a retry it was given, the session's own.
    @Test func offeredActions() {
        #expect(ErrorStateView.offered([.retry, .chooseHome], retry: true, back: false, session: true) == [.retry, .chooseHome])
        #expect(ErrorStateView.offered([.retry, .chooseHome], retry: true, back: false, session: false) == [.retry])
        #expect(ErrorStateView.offered([.signIn], retry: true, back: false, session: true) == [.signIn])
        // nothing of its own: it can still be tried again, but a sign-in is never retried
        #expect(ErrorStateView.offered([.back], retry: true, back: false, session: true) == [.retry])
        #expect(ErrorStateView.offered([.signIn], retry: true, back: false, session: false) == [])
    }
}

@Suite struct ErrorWords {
    @Test func everyKindIsSaidInBothLanguages() {
        for lang in [ErrorStrings.Language.en, .ar] {
            let w = ErrorStrings(lang)
            for kind in AppError.Kind.allCases where kind != .cancelled {
                #expect(!w.message(kind).isEmpty, "\(lang) \(kind)")
                #expect(!w.title(kind).isEmpty)
            }
            for a in [AppError.Action.retry, .signIn, .chooseHome, .back] {
                #expect(!w.action(a).isEmpty)
            }
        }
        for key in ErrorStrings.Key.allCases where key != .cancelled {
            #expect(ErrorStrings.en[key] != nil, "en \(key)")
            #expect(ErrorStrings.ar[key] != nil, "ar \(key)")
            #expect(ErrorStrings.en[key] != ErrorStrings.ar[key], "\(key) is not translated")
        }
        // a key per kind
        for kind in AppError.Kind.allCases { #expect(ErrorStrings.Key(rawValue: kind.rawValue) != nil) }
    }

    @Test func arabicForArabicSpeakers() {
        #expect(ErrorStrings.forPreferredLanguages(["ar-EG", "en"]).language == .ar)
        #expect(ErrorStrings.forPreferredLanguages(["en-GB", "ar"]).language == .en)
        #expect(ErrorStrings(.ar).message(.unreachable) == "تعذّر الوصول إلى الجهاز الرئيسي. تأكد من أنه يعمل ومتصل بالشبكة.")
    }

    @Test func aHomeThatDoesNotAnswerIsSaidInTheAppsWords() {
        let en = ConnectStrings(.en)
        #expect(en.reach(.unreachable(.offline)) == ErrorStrings(.en).message(.offline))
        #expect(en.reach(.unreachable(.cancelled)) == ErrorStrings(.en).message(.unreachable))
        #expect(ConnectStrings(.ar).reach(.unreachable(.timedOut)) == ErrorStrings(.ar).message(.timedOut))
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
                #expect(AppError(error).kind == .unreachable)
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
