import Foundation
import Testing
@testable import MaktabaKit

// Epic 53 stories 53.04 and 53.05 on the Remote: the recovery console's routes, the pass the
// API gives an admin's app, the confirmations an action needs, and refusals that never sign
// the app out.

/// Answers each "METHOD /path" from a queue and keeps what the app sent (its bearer too).
final class RecoveryStub: URLProtocol, @unchecked Sendable {
    struct Seen {
        let route: String
        let bearer: String?
        let headers: [String: String]
        let query: String?
        let body: Data
        var json: [String: Any]? { try? JSONSerialization.jsonObject(with: body) as? [String: Any] }
    }

    nonisolated(unsafe) private static var answers: [String: [(Int, String)]] = [:]
    nonisolated(unsafe) private static var seenList: [Seen] = []
    private static let lock = NSLock()
    static let home = URL(string: "http://192.168.50.178:8088")!

    static func reset() { lock.withLock { answers = [:]; seenList = [] } }
    static func on(_ route: String, _ code: Int = 200, _ body: String = "{}") {
        lock.withLock { answers[route, default: []].append((code, body)) }
    }
    static var seen: [Seen] { lock.withLock { seenList } }

    /// A client whose store has `pass`, `access` and `refresh` (each when given).
    static func client(pass: String? = nil, access: String? = "a1", refresh: String? = nil) -> (APIClient, TokenStore) {
        let c = URLSessionConfiguration.ephemeral
        c.protocolClasses = [RecoveryStub.self]
        var kept: [String: String] = [:]
        let h = home.absoluteString
        if let pass { kept["\(h)#recovery"] = pass }
        if let access { kept["\(h)#access"] = access }
        if let refresh { kept["\(h)#refresh"] = refresh }
        let tokens = TokenStore(service: "test.recovery.\(UUID().uuidString)", home: home, primary: FakeSecrets(kept), legacy: nil)
        return (APIClient(home: home, tokens: tokens, session: URLSession(configuration: c)), tokens)
    }

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        var body = request.httpBody ?? Data()
        if body.isEmpty, let stream = request.httpBodyStream {
            stream.open()
            var buf = [UInt8](repeating: 0, count: 65536)
            while stream.hasBytesAvailable {
                let n = stream.read(&buf, maxLength: buf.count)
                if n <= 0 { break }
                body.append(buf, count: n)
            }
            stream.close()
        }
        let url = request.url!
        let route = "\(request.httpMethod ?? "GET") \(url.path(percentEncoded: true))"
        let auth = request.value(forHTTPHeaderField: "Authorization")
        let (code, text): (Int, String) = Self.lock.withLock {
            Self.seenList.append(Seen(route: route, bearer: auth.map { String($0.dropFirst("Bearer ".count)) },
                                      headers: request.allHTTPHeaderFields ?? [:], query: url.query, body: body))
            if var queue = Self.answers[route], !queue.isEmpty {
                let a = queue.count > 1 ? queue.removeFirst() : queue[0]
                Self.answers[route] = queue
                return a
            }
            return (599, "no stub for \(route)")
        }
        let resp = HTTPURLResponse(url: url, statusCode: code, httpVersion: "HTTP/1.1", headerFields: ["Content-Type": "application/json"])!
        client?.urlProtocol(self, didReceive: resp, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(text.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }

    override func stopLoading() {}
}

@Suite(.serialized)
struct RecoveryTests {
    static let state = """
    {"status":{"v":1,"mode":"safe","overall":"down","components":{"logs":"ok","database":"down","api":"restarting","search":"off"}},
     "components":[
      {"id":"core.postgres","group":"database","phase":"failed","reason":"crash_loop","state":"down","restarts":3,"cut_off":true,
       "last_exit":"2026-11-07T09:12:00Z","last_code":1,"last_error":"could not open file pg_wal","can_restart":true},
      {"id":"search.index","group":"search","phase":"stopped","reason":"held","state":"off","restarts":0,"can_restart":false}],
     "disk_free":120000000000,"logs_bytes":52428800,"rollback":"0.3.44","safe_mode":true}
    """

    init() { RecoveryStub.reset() }

    @Test func readsTheStateWithThePass() async throws {
        RecoveryStub.on("GET /recovery/api/state", 200, Self.state)
        let (client, _) = RecoveryStub.client(pass: "p1")
        let s = try await client.recoveryState()
        #expect(s.safeMode && s.rollback == "0.3.44" && s.diskFree == 120_000_000_000)
        #expect(s.groups.map(\.name) == ["database", "api", "search", "logs"], "the board's order")
        let pg = try #require(s.components.first)
        #expect(pg.cutOff && pg.troubled && pg.lastCode == 1 && pg.lastExitDate != nil && pg.canRestart)
        #expect(s.components[1].state == "off" && !s.components[1].canRestart && !s.components[1].troubled)
        #expect(RecoveryStub.seen.map(\.bearer) == ["p1"], "the pass first")
    }

    @Test func aRefusedPassIsDroppedAndTheAccessTokenTried() async throws {
        RecoveryStub.on("GET /recovery/api/state", 401, #"{"error":"unauthorized"}"#)
        RecoveryStub.on("GET /recovery/api/state", 200, Self.state)
        let (client, tokens) = RecoveryStub.client(pass: "stale", access: "a1")
        _ = try await client.recoveryState()
        #expect(RecoveryStub.seen.map(\.bearer) == ["stale", "a1"])
        #expect(tokens.recoveryPass == nil, "a refused pass is not sent again (each refusal counts toward the lockout)")
        #expect(tokens.access == "a1")
    }

    @Test func refusedEverywhereIsDeniedNeverASignOut() async {
        RecoveryStub.on("GET /recovery/api/state", 401, #"{"error":"unauthorized"}"#)
        let (client, tokens) = RecoveryStub.client(access: "viewer")
        await #expect(throws: RecoveryFailure.denied) { try await client.recoveryState() }
        #expect(tokens.access == "viewer", "still signed in to the home")
    }

    @Test func anExpiredAccessTokenIsRefreshedOnce() async throws {
        RecoveryStub.on("GET /recovery/api/state", 401, #"{"error":"unauthorized"}"#)
        RecoveryStub.on("GET /recovery/api/state", 200, Self.state)
        RecoveryStub.on("POST /api/auth/refresh", 200, #"{"access_token":"a2","refresh_token":"r2"}"#)
        let (client, _) = RecoveryStub.client(access: "a1", refresh: "r1")
        _ = try await client.recoveryState()
        #expect(RecoveryStub.seen.filter { $0.route.hasPrefix("GET") }.map(\.bearer) == ["a1", "a2"])
    }

    @Test func aForbiddenAnswerIsDenied() async {
        RecoveryStub.on("GET /recovery/api/state", 403, #"{"error":"forbidden"}"#)
        let (client, tokens) = RecoveryStub.client(pass: "p1")
        await #expect(throws: RecoveryFailure.denied) { try await client.recoveryState() }
        #expect(tokens.recoveryPass == "p1")
    }

    @Test func anOlderHomeHasNoConsole() async {
        RecoveryStub.on("GET /recovery/api/state", 404, "404 page not found")
        let (client, _) = RecoveryStub.client()
        await #expect(throws: RecoveryFailure.notHere) { try await client.recoveryState() }
    }

    @Test func linesSayTheirMessageRawTextAndError() async throws {
        RecoveryStub.on("GET /recovery/api/logs", 200, """
        {"lines":[{"ts":"2026-11-07T09:12:00Z","level":"error","service":"postgres","component":"core.postgres","msg":"raw_line","attrs":{"text":"FATAL: could not open file"}},
                  {"ts":"2026-11-07T09:11:00Z","level":"warn","service":"api","component":"core.api","msg":"db_connect","err":{"msg":"connection refused"}},
                  {"level":"error","service":"api","msg":"start","err":"exit 1"}],"truncated":false}
        """)
        let (client, _) = RecoveryStub.client()
        let lines = try await client.recoveryLines(minutes: 30)
        #expect(lines.map(\.msg) == ["FATAL: could not open file", "db_connect: connection refused", "start: exit 1"])
        #expect(lines[0].ts != nil && lines[0].component == "core.postgres" && lines[2].ts == nil)
        #expect(RecoveryStub.seen.first?.query == "minutes=30")
    }

    @Test func anActionCarriesItsGuardHeaderAndConfirmations() async throws {
        RecoveryStub.on("POST /recovery/api/action", 200, #"{"result":"ok"}"#)
        let (client, _) = RecoveryStub.client(pass: "p1")
        try await client.recoveryAction(.restart("core.postgres"), confirm: 1)
        let sent = try #require(RecoveryStub.seen.first)
        #expect(sent.headers["X-Maktaba-Recovery"] == "1")
        #expect(sent.json?["action"] as? String == "restart" && sent.json?["component"] as? String == "core.postgres")
        #expect(sent.json?["confirm"] as? Int == 1)
    }

    @Test func theHomesRefusalIsSaid() async {
        RecoveryStub.on("POST /recovery/api/action", 409, #"{"error":"no previous version is kept"}"#)
        let (client, _) = RecoveryStub.client(pass: "p1")
        await #expect(throws: RecoveryFailure.refused("no previous version is kept")) {
            try await client.recoveryAction(.rollback("0.3.44"), confirm: 2)
        }
    }

    @Test func thePassIsKeptFromTheAPI() async {
        RecoveryStub.on("POST /api/auth/recovery-pass", 200, #"{"expires_at":"2026-11-14T09:00:00Z","pass":"p-new"}"#)
        let (client, tokens) = RecoveryStub.client()
        #expect(await client.refreshRecoveryPass())
        #expect(tokens.recoveryPass == "p-new")
        // a viewer: 403, nothing kept
        RecoveryStub.reset()
        RecoveryStub.on("POST /api/auth/recovery-pass", 403, #"{"title":"forbidden"}"#)
        let (viewer, vt) = RecoveryStub.client()
        #expect(await viewer.refreshRecoveryPass() == false)
        #expect(vt.recoveryPass == nil && vt.access == "a1")
    }

    @Test func signingOutForgetsThePass() {
        let (_, tokens) = RecoveryStub.client(pass: "p1")
        tokens.clear()
        #expect(tokens.recoveryPass == nil)
    }

    @MainActor @Test func rollingBackIsConfirmedTwice() async throws {
        RecoveryStub.on("GET /recovery/api/state", 200, Self.state)
        RecoveryStub.on("GET /recovery/api/logs", 200, #"{"lines":[]}"#)
        RecoveryStub.on("POST /recovery/api/action", 200, #"{"result":"ok"}"#)
        let (client, _) = RecoveryStub.client(pass: "p1")
        let model = RecoveryModel(client: client)
        model.ask(.rollback("0.3.44"))
        #expect(model.asking == RecoveryModel.Asking(action: .rollback("0.3.44"), step: 1, need: 2))
        await model.confirm()
        #expect(model.asking?.step == 2, "a second confirmation")
        #expect(!RecoveryStub.seen.contains { $0.route == "POST /recovery/api/action" })
        await model.confirm()
        let sent = try #require(RecoveryStub.seen.first { $0.route == "POST /recovery/api/action" })
        #expect(sent.json?["action"] as? String == "rollback" && sent.json?["confirm"] as? Int == 2)
        #expect(model.outcome == .done && model.phase == .ready && model.state?.rollback == "0.3.44")
    }

    @MainActor @Test func throughTheRelayTheHomeAsksASecondConfirmation() async throws {
        RecoveryStub.on("POST /recovery/api/action", 428, #"{"error":"confirm","confirm":2}"#)
        RecoveryStub.on("POST /recovery/api/action", 200, #"{"result":"ok"}"#)
        RecoveryStub.on("GET /recovery/api/state", 200, Self.state)
        RecoveryStub.on("GET /recovery/api/logs", 200, #"{"lines":[]}"#)
        let (client, _) = RecoveryStub.client(pass: "p1")
        let model = RecoveryModel(client: client)
        model.ask(.restartAll)
        await model.confirm()
        #expect(model.asking == RecoveryModel.Asking(action: .restartAll, step: 2, need: 2))
        #expect(model.outcome == nil)
        await model.confirm()
        let posts = RecoveryStub.seen.filter { $0.route == "POST /recovery/api/action" }
        #expect(posts.compactMap { $0.json?["confirm"] as? Int } == [1, 2])
        #expect(model.outcome == .done)
    }

    @MainActor @Test func aDeniedConsoleStopsAsking() async {
        RecoveryStub.on("GET /recovery/api/state", 401, #"{"error":"unauthorized"}"#)
        let (client, tokens) = RecoveryStub.client(access: "viewer")
        let model = RecoveryModel(client: client)
        await model.run(every: .milliseconds(1))
        #expect(model.phase == .denied)
        #expect(RecoveryStub.seen.count == 1, "no polling once turned down (each try counts toward the lockout)")
        #expect(tokens.access == "viewer")
    }

    @MainActor @Test func aHomeThatDoesNotAnswerKeepsWhatWasRead() async {
        RecoveryStub.on("GET /recovery/api/state", 200, Self.state)
        RecoveryStub.on("GET /recovery/api/logs", 200, #"{"lines":[]}"#)
        let (client, _) = RecoveryStub.client(pass: "p1")
        let model = RecoveryModel(client: client)
        await model.load()
        RecoveryStub.reset()
        RecoveryStub.on("GET /recovery/api/state", 502, "bad gateway")
        await model.load()
        #expect(model.phase == .ready && model.state?.safeMode == true)
    }

    // restore and the diagnostics download (after #918, on the same routes as the web page)

    static let backups = """
    {"backups":[{"name":"maktaba-20261106-030000.dump","at":"2026-11-06T03:00:00Z","size":73400320},
                {"name":"maktaba-20261105-030000.dump","at":"2026-11-05T03:00:00Z","size":72351744}]}
    """

    @Test func readsTheBackups() async throws {
        RecoveryStub.on("GET /recovery/api/backups", 200, Self.backups)
        let (client, _) = RecoveryStub.client(pass: "p1")
        let list = try await client.recoveryBackups()
        #expect(list.map(\.name) == ["maktaba-20261106-030000.dump", "maktaba-20261105-030000.dump"])
        #expect(list.first?.size == 73400320 && list.first?.at == homeTime("2026-11-06T03:00:00Z"))
        #expect(RecoveryStub.seen.map(\.bearer) == ["p1"])
    }

    @MainActor @Test func restoringIsChosenThenConfirmedTwice() async throws {
        RecoveryStub.on("GET /recovery/api/backups", 200, Self.backups)
        RecoveryStub.on("POST /recovery/api/action", 200, #"{"result":"ok"}"#)
        RecoveryStub.on("GET /recovery/api/state", 200, Self.state)
        RecoveryStub.on("GET /recovery/api/logs", 200, #"{"lines":[]}"#)
        let (client, _) = RecoveryStub.client(pass: "p1")
        let model = RecoveryModel(client: client)
        await model.chooseBackup()
        let first = try #require(model.backups?.first)
        model.pick(first)
        #expect(model.backups == nil, "the list closes")
        #expect(model.asking == RecoveryModel.Asking(action: .restore(first.name), step: 1, need: 2))
        await model.confirm()
        #expect(model.asking?.step == 2, "a second confirmation, as the web page asks")
        #expect(!RecoveryStub.seen.contains { $0.route == "POST /recovery/api/action" })
        await model.confirm()
        let sent = try #require(RecoveryStub.seen.first { $0.route == "POST /recovery/api/action" })
        #expect(sent.headers["X-Maktaba-Recovery"] == "1")
        #expect(sent.json?["action"] as? String == "restore" && sent.json?["backup"] as? String == first.name)
        #expect(sent.json?["confirm"] as? Int == 2)
        #expect(model.outcome == .restoring)
    }

    @MainActor @Test func noBackupsIsSaid() async {
        RecoveryStub.on("GET /recovery/api/backups", 200, #"{"backups":[]}"#)
        let (client, _) = RecoveryStub.client(pass: "p1")
        let model = RecoveryModel(client: client)
        await model.chooseBackup()
        #expect(model.backups == nil && model.outcome == .noBackups)
    }

    @MainActor @Test func aRefusedPassWhileChoosingIsDroppedAndDenied() async {
        // the pass and then the access token turned down: each counts toward the lockout, so
        // the pass is dropped and the screen says only admins get in, never signing out
        RecoveryStub.on("GET /recovery/api/backups", 401, #"{"error":"unauthorized"}"#)
        let (client, tokens) = RecoveryStub.client(pass: "p1", access: "viewer")
        let model = RecoveryModel(client: client)
        await model.chooseBackup()
        #expect(model.phase == .denied && model.backups == nil)
        #expect(RecoveryStub.seen.map(\.bearer) == ["p1", "viewer"])
        #expect(tokens.recoveryPass == nil && tokens.access == "viewer")
    }

    @MainActor @Test func theDiagnosticsBundleIsKeptOnlyUntilShared() async throws {
        RecoveryStub.on("GET /recovery/api/diagnostics", 200, "bundle-bytes")
        let (client, _) = RecoveryStub.client(pass: "p1")
        let model = RecoveryModel(client: client)
        await model.fetchDiagnostics()
        let file = try #require(model.diagnostics)
        #expect(file.lastPathComponent.hasPrefix("maktaba-recovery-") && file.pathExtension == "gz")
        #expect(try Data(contentsOf: file) == Data("bundle-bytes".utf8))
        #expect(file.path().hasPrefix(RecoveryModel.diagnosticsFolder.path()))
        model.doneSharing()
        #expect(model.diagnostics == nil)
        #expect(!FileManager.default.fileExists(atPath: file.deletingLastPathComponent().path()), "deleted once shared")
    }

    @MainActor @Test func aDiagnosticsFailureIsSaid() async {
        RecoveryStub.on("GET /recovery/api/diagnostics", 500, #"{"error":"no space left"}"#)
        let (client, _) = RecoveryStub.client(pass: "p1")
        let model = RecoveryModel(client: client)
        await model.fetchDiagnostics()
        #expect(model.diagnostics == nil && model.outcome == .failed("no space left"))
    }
}
