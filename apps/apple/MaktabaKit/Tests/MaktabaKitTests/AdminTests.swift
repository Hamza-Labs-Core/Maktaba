import Foundation
import Testing
@testable import MaktabaKit

@Suite struct QueueScreen {
    static let snapshot = #"""
    {"at":"2026-10-25T20:30:00.123456789Z","paused_all":false,"issues_count":3,
     "stages":[
       {"stage":"transcribe","label":"Transcribe","lane":"b","lane_order":1,"hidden":false,"enabled":true,"paused":true},
       {"stage":"probe","label":"Probe","lane":"a","lane_order":2,"hidden":false,"enabled":true,"paused":false},
       {"stage":"scan","label":"Scan","lane":"a","lane_order":1,"hidden":false,"enabled":true,"paused":false},
       {"stage":"legacy","label":"Old","lane":"a","lane_order":0,"hidden":true,"enabled":true,"paused":false},
       {"stage":"plex","label":"Plex","lane":"c","lane_order":1,"hidden":false,"enabled":false,"paused":false}],
     "queues":[
       {"stage":"probe","counts":{"pending":2,"ready":3,"running":1,"paused":0,"failed_open":4,"done_24h":9,"skipped_24h":0,"awaiting_review":0},
        "rate_per_min":1.5,"eta_sec":null,"waiting_reason":null,"no_worker":false},
       {"stage":"scan","counts":{"pending":0,"ready":5,"running":0,"paused":0,"failed_open":0,"done_24h":0,"skipped_24h":0,"awaiting_review":0},
        "rate_per_min":0,"eta_sec":null,"waiting_reason":"no_worker","no_worker":true}],
     "health":{"cpu":{"pct":12.5},"backlog":{"ready":8,"oldest_waiting_sec":null},"nas":{}}}
    """#

    @Test func listsTheVisibleStagesInThePipelinesOrder() throws {
        let s = try JSONDecoder.home.decode(QueueSnapshot.self, from: Data(Self.snapshot.utf8))
        #expect(s.issuesCount == 3 && !s.pausedAll && s.health?.cpu?.pct == 12.5)
        #expect(s.rows.map(\.id) == ["scan", "probe", "transcribe", "plex"])
        let probe = try #require(s.rows.first { $0.id == "probe" })
        #expect(probe.waiting == 5 && probe.running == 1 && probe.failed == 4 && probe.state == "Running")
        #expect(s.rows.map(\.state) == ["No worker", "Running", "Paused", "Off"])
    }

    /// A stage the snapshot has no counts for yet still shows, idle.
    @Test func aStageWithoutCountsIsIdle() throws {
        let s = try JSONDecoder.home.decode(QueueSnapshot.self, from: Data(Self.snapshot.utf8))
        let t = try #require(s.rows.first { $0.id == "transcribe" })
        #expect(t.summary == nil && t.waiting == 0 && t.failed == 0)
    }
}

@Suite struct Machines {
    static let overview = #"""
    {"home":{"instance_id":"i","name":"Living room","accept_joins":true,"accept_joins_until":"2026-10-25T21:30:00Z",
             "roles_supported":true,"roles_reason":null,"web_url":"http://192.168.50.177:8088","version":"0.36.2"},
     "nodes":[
       {"id":"n1","name":"zeta","state":"approved","expired":false,"online":true,"os":"darwin","arch":"arm64","version":"0.36.2",
        "code_attempts":0,"attempts_left":5,"created_at":"2026-10-01T00:00:00Z","last_seen_at":"2026-10-25T20:29:58.5Z","updated_at":"2026-10-01T00:00:00Z"},
       {"id":"n2","name":"Basement","state":"pending","expired":false,"online":false,"remote_addr":"192.168.50.20",
        "attempts_left":3,"expires_at":"2026-10-25T20:45:00Z","created_at":"2026-10-25T20:30:00Z","updated_at":"2026-10-25T20:30:00Z"},
       {"id":"n3","name":"old","state":"revoked","expired":false,"online":false,"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"},
       {"id":"n4","name":"Attic","state":"pending","expired":true,"online":false,"created_at":"2026-10-25T00:00:00Z","updated_at":"2026-10-25T00:00:00Z"},
       {"id":"n5","name":"alpha","state":"approved","expired":false,"online":false,"os":"linux","created_at":"2026-10-01T00:00:00Z","updated_at":"2026-10-01T00:00:00Z"}]}
    """#

    @Test func asksFirstThenApprovedThenTheRest() throws {
        let o = try JSONDecoder.home.decode(ClusterOverview.self, from: Data(Self.overview.utf8))
        #expect(o.home.name == "Living room" && o.home.acceptJoins && o.home.version == "0.36.2")
        #expect(o.home.acceptJoinsUntil == Date(timeIntervalSince1970: 1792963800))
        #expect(o.sortedNodes.map(\.id) == ["n2", "n5", "n1", "n4", "n3"])
        let basement = try #require(o.nodes.first { $0.id == "n2" })
        #expect(basement.group == .asking && basement.attemptsLeft == 3 && basement.remoteAddr == "192.168.50.20")
        #expect(o.nodes.first { $0.id == "n1" }?.platform == "darwin arm64")
        #expect(o.nodes.first { $0.id == "n3" }?.platform == nil)
        // fractions of a second, as Go writes them
        #expect(o.nodes.first { $0.id == "n1" }?.lastSeenAt == Date(timeIntervalSince1970: 1792960198.5))
    }

    /// Why the home cannot take machines: its code's words in the app's language, else the
    /// home's English (an older home, or a code the app does not know).
    @Test func saysWhyTheHomeCannotTakeMachines() throws {
        func home(_ extra: String) throws -> ClusterHome {
            let json = #"{"name":"h","accept_joins":false,"roles_supported":false,"roles_reason":"english reason"\#(extra)}"#
            return try JSONDecoder.home.decode(ClusterHome.self, from: Data(json.utf8))
        }
        let ar = Locale(identifier: "ar")
        #expect(try home(#","roles_reason_code":"cannot_create_roles""#).reasonText(locale: ar) == "مستخدم قاعدة البيانات لا يستطيع إنشاء أدوار.")
        #expect(try home(#","roles_reason_code":"check_failed","roles_reason_params":{"error":"refused"}"#).reasonText(locale: ar)
            == "تعذّر فحص صلاحيات مستخدم قاعدة البيانات: refused")
        #expect(try home(#","roles_reason_code":"brand_new""#).reasonText(locale: ar) == "english reason")
        #expect(try home("").reasonText(locale: ar) == "english reason")
    }

    /// Go writes up to nine digits of a second; a time the app cannot read would hide the machine.
    @Test(arguments: ["2026-10-25T20:29:58.123456789Z", "2026-10-25T20:29:58.123456Z", "2026-10-25T23:29:58+03:00"])
    func readsTheHomesTimes(at: String) throws {
        let json = #"{"id":"n","name":"n","state":"approved","expired":false,"online":true,"last_seen_at":"\#(at)"}"#
        let seen = try #require(try JSONDecoder.home.decode(Machine.self, from: Data(json.utf8)).lastSeenAt)
        #expect(abs(seen.timeIntervalSince1970 - 1792960198) < 1)
    }

    @Test(arguments: [("123456", "123456"), ("123 456", "123456"), ("123-456", "123456"), (" 12 34 56 ", "123456")])
    func readsAPairingCode(raw: String, want: String) {
        #expect(PairingCode.normalize(raw) == want)
    }

    @Test(arguments: ["", "12345", "1234567", "12a456", "١٢٣٤٥٦", "123_456"])
    func refusesWhatIsNotACode(raw: String) {
        #expect(PairingCode.normalize(raw) == nil)
    }
}

@Suite struct Approving {
    @Test func tellsAWrongCodeAnExpiredRequestAndTheRest() {
        let wrong = APIError.status(400, #"{"title":"wrong code","code":"wrong_code","attempts_left":1}"#)
        #expect(ApproveFailure(wrong) == .wrongCode(attemptsLeft: 1))
        #expect(ApproveFailure(wrong).message.hasSuffix("1 try left."))
        #expect(ApproveFailure(APIError.status(400, #"{"code":"wrong_code","attempts_left":0}"#)).message.hasPrefix("Too many wrong codes"))
        #expect(ApproveFailure(APIError.status(400, #"{"code":"wrong_code"}"#)).message == "That is not the code the machine shows.")
        #expect(ApproveFailure(APIError.status(410, #"{"title":"request expired","code":"expired"}"#)) == .expired)
        #expect(ApproveFailure(APIError.status(410, "gone")) == .expired)
        // anything else in the one sentence every screen says, never the home's own words
        #expect(ApproveFailure(APIError.status(409, #"{"title":"not pending","detail":"the machine is approved","code":"not_pending"}"#))
            == .other(ErrorStrings.forPreferredLanguages().sentence(.server)))
        #expect(ApproveFailure(URLError(.timedOut)) == .other(ErrorStrings.forPreferredLanguages().sentence(.unreachable)))
    }
}

@Suite struct Problems {
    @Test func readsTheHomesProblem() {
        let e = APIError.status(400, #"{"type":"about:blank","title":"wrong code","status":422,"detail":"the code does not match","code":"wrong_code","attempts_left":2}"#)
        #expect(e.problem == Problem(code: "wrong_code", title: "wrong code", detail: "the code does not match", attemptsLeft: 2,
                                     type: "about:blank", status: 422))
        #expect(e.message == ErrorStrings.forPreferredLanguages().sentence(.server))
    }

    @Test func fallsBackToTheErrorsOwnWords() {
        #expect(APIError.status(502, "<html>bad gateway</html>").problem == nil)
        #expect(APIError.status(502, "<html>bad gateway</html>").message == ErrorStrings.forPreferredLanguages().sentence(.server))
        #expect(APIError.signedOut.message == ErrorStrings.forPreferredLanguages().sentence(.signedOut))
    }
}

/// What the Remote sends: each action's method, path and body (a stub answers in place of
/// the home).
@Suite(.serialized) struct AdminRequests {
    static let home = URL(string: "http://admin.test:8088")!

    func client() -> APIClient {
        let config = URLSessionConfiguration.ephemeral
        config.protocolClasses = [Stub.self]
        return APIClient(home: Self.home, tokens: TokenStore(app: "test.admin", home: Self.home), session: URLSession(configuration: config))
    }

    @Test func eachActionReachesItsRoute() async throws {
        let c = client()
        let cases: [(String, (APIClient) async throws -> Void, String, String, [String: AnyHashable]?)] = [
            ("pause", { try await $0.setPaused(stage: "probe", true) }, "POST", "/api/queues/probe/pause", nil),
            ("resume", { try await $0.setPaused(stage: "probe", false) }, "POST", "/api/queues/probe/resume", nil),
            ("pause all", { try await $0.setPausedAll(true) }, "POST", "/api/queues/pause-all", nil),
            ("resume all", { try await $0.setPausedAll(false) }, "POST", "/api/queues/resume-all", nil),
            ("retry", { try await $0.retry(stage: "probe", cause: "nas_timeout") }, "POST", "/api/queues/probe/retry", ["cause": "nas_timeout"]),
            ("scan", { try await $0.scan(library: "4f1c") }, "POST", "/api/libraries/4f1c/scan", nil),
            ("joins", { try await $0.setAcceptJoins(true) }, "PUT", "/api/cluster/settings", ["accept_joins": true]),
            ("approve", { try await $0.approve(machine: "n2", code: "123456") }, "POST", "/api/cluster/nodes/n2/approve", ["code": "123456"]),
            ("reject", { try await $0.reject(machine: "n2") }, "POST", "/api/cluster/nodes/n2/reject", nil),
            ("revoke", { try await $0.revoke(machine: "n1") }, "POST", "/api/cluster/nodes/n1/revoke", nil),
        ]
        for (name, call, method, path, body) in cases {
            Stub.answer(204, "")
            try await call(c)
            let r = try #require(Stub.last, "\(name)")
            #expect(r.method == method && r.path == path, "\(name): \(r.method) \(r.path)")
            #expect(r.native, "\(name)")
            if let body {
                #expect(r.json == body, "\(name): \(String(describing: r.json))")
            }
        }
    }

    /// A machine id cannot reach another route.
    @Test func anIDStaysOneSegment() async throws {
        Stub.answer(204, "")
        try await client().revoke(machine: "../../queues/pause-all")
        let r = try #require(Stub.last)
        #expect(r.path.hasPrefix("/api/cluster/nodes/") && r.path.hasSuffix("/revoke"))
        #expect(r.path.split(separator: "/").count == 5, "\(r.path)")
    }

    @Test func aWrongCodeSaysHowManyTriesAreLeft() async throws {
        Stub.answer(400, #"{"title":"wrong code","detail":"the code does not match","code":"wrong_code","attempts_left":4}"#)
        do {
            try await client().approve(machine: "n2", code: "000000")
            Issue.record("approved with a wrong code")
        } catch let e as APIError {
            #expect(e.problem?.code == "wrong_code" && e.problem?.attemptsLeft == 4)
        }
    }

    /// A viewer who reaches the Remote anyway: the home's 403, said as no access.
    @Test func aNonAdminIsRefusedByTheHome() async throws {
        Stub.answer(403, #"{"title":"Forbidden","status":403,"detail":"admin-only"}"#)
        do {
            _ = try await client().queues()
            Issue.record("a 403 decoded as a snapshot")
        } catch let e as APIError {
            #expect(e.kind == .noAccess && e.problem?.detail == "admin-only")
            #expect(e.message == ErrorStrings.forPreferredLanguages().sentence(.noAccess))
        }
    }

    /// A long problem (the request id, the type URI) is read whole, not cut mid-JSON.
    @Test func readsALongProblem() async throws {
        let long = #"{"code":"insufficient_storage","detail":"the server that streams it has too little free on its disk to transcode it","instance":"/api/stream/sessions","requestId":"01a178df-2dc4-734f-9927-fee2cf9f1c82","status":507,"title":"not enough disk space to prepare this video","type":"https://maktaba.tv/problems/insufficient-storage"}"#
        #expect(long.utf8.count > 300)
        Stub.answer(507, long)
        do {
            _ = try await client().queues()
            Issue.record("a 507 decoded")
        } catch let e as APIError {
            #expect(e.problem?.detail == "the server that streams it has too little free on its disk to transcode it")
            #expect(HomeFailure(e).details?.contains("507 · insufficient-storage") == true)
        }
    }

    @Test func readsTheQueueAndTheMachines() async throws {
        Stub.answer(200, QueueScreen.snapshot)
        #expect(try await client().queues().rows.count == 4)
        #expect(Stub.last?.path == "/api/queues" && Stub.last?.method == "GET")
        Stub.answer(200, Machines.overview)
        #expect(try await client().cluster().nodes.count == 5)
        #expect(Stub.last?.path == "/api/cluster")
        Stub.answer(200, #"{"groups":[{"stage":"probe","cause":"nas_timeout","label":"NAS timed out","count":2,"action":"retry","sample_message":"x"}]}"#)
        #expect(try await client().failures(stage: "probe").map(\.id) == ["probe:nas_timeout"])
        #expect(Stub.last?.path == "/api/queues/probe/failures")
    }
}

/// Answers every request with one canned response and remembers the request.
final class Stub: URLProtocol, @unchecked Sendable {
    struct Seen {
        let method: String
        let path: String
        let native: Bool
        let json: [String: AnyHashable]?
    }

    nonisolated(unsafe) private static var canned = (200, Data())
    nonisolated(unsafe) private(set) static var last: Seen?
    private static let lock = NSLock()

    static func answer(_ code: Int, _ body: String) {
        lock.withLock {
            canned = (code, Data(body.utf8))
            last = nil
        }
    }

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        var body = request.httpBody
        if body == nil, let stream = request.httpBodyStream {
            stream.open()
            var d = Data()
            var buf = [UInt8](repeating: 0, count: 4096)
            while stream.hasBytesAvailable {
                let n = stream.read(&buf, maxLength: buf.count)
                if n <= 0 { break }
                d.append(buf, count: n)
            }
            stream.close()
            body = d
        }
        let json = body.flatMap { try? JSONSerialization.jsonObject(with: $0) as? [String: AnyHashable] }
        let (code, data) = Self.lock.withLock {
            Self.last = Seen(method: request.httpMethod ?? "GET", path: request.url?.path(percentEncoded: true) ?? "",
                             native: request.value(forHTTPHeaderField: "X-Maktaba-Client") == ClientHeader.value, json: json)
            return Self.canned
        }
        let resp = HTTPURLResponse(url: request.url!, statusCode: code, httpVersion: "HTTP/1.1",
                                   headerFields: ["Content-Type": "application/json"])!
        client?.urlProtocol(self, didReceive: resp, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: data)
        client?.urlProtocolDidFinishLoading(self)
    }

    override func stopLoading() {}
}
