import Foundation
import Network

/// A home for the UI tests, served by the test runner on 127.0.0.1 (the app on the same
/// simulator reaches it). No real home and no real account is involved.
/// - Connecting and signing in: `/api/discovery/home`, a sign-in code that is approved when
///   the test says so, `/api/auth/me`.
/// - The Remote's screens: made-up queues, updates, processes and running jobs (below).
/// - The Player's screens (Epic 48): every browse and live route answers from
///   `UITests/Fixtures` (`routes.json` maps a method and a path, `{id}` matching one segment,
///   to a file shaped as the Go handler answers). `{{now}}` / `{{now-600}}` in a fixture
///   become RFC 3339 times relative to now, so what is on Live TV straddles the clock.
///   Posters, backdrops and channel logos are a small PNG.
/// - A test holds a route's answers until it says (`hold`, `release`): a latch, not a timer.
/// - A test changes an answer with `stub` (a status, a body, a delay) and checks what the
///   app asked with `requests` (method, path, query, body) or `seen` ("GET /api/home").
final class MockHome: @unchecked Sendable {
    /// One request the app made.
    struct Request: Sendable {
        let method: String
        let path: String
        /// The query string, without "?" ("library=lib-docs&cursor=c2"), or "".
        let query: String
        let body: Data

        /// One query parameter's value.
        func value(_ name: String) -> String? {
            URLComponents(string: "/?\(query)")?.queryItems?.first { $0.name == name }?.value
        }
    }

    private struct Answer {
        var status: Int
        var body: Data
        var contentType = "application/json"
        var delay: TimeInterval = 0
    }

    private struct Route: Decodable {
        let method: String
        let path: String
        let status: Int
        let file: String?
    }

    private let listener: NWListener
    private let queue = DispatchQueue(label: "maktaba.uitests.mockhome", attributes: .concurrent)
    private let lock = NSLock()
    private var approved = false
    private var log: [Request] = []
    private var stubs: [String: Answer] = [:]
    /// Latency added to `METHOD /path` after its answer is chosen (`delay`).
    private var delays: [String: TimeInterval] = [:]
    /// `METHOD /path`s whose answers wait for `release` (`hold`).
    private var held: Set<String> = []
    /// The answers waiting for `release`, ready to send.
    private var parked: [() -> Void] = []
    private let routes: [Route]
    private let fixtures: Bundle
    private(set) var port: UInt16 = 0
    private var startFailure: Error?

    static let code = "TEST-2345"

    init() throws {
        let params = NWParameters.tcp
        params.requiredLocalEndpoint = .hostPort(host: "127.0.0.1", port: .any)
        listener = try NWListener(using: params)
        fixtures = Bundle(for: MockHome.self)
        routes = fixtures.url(forResource: "routes", withExtension: "json")
            .flatMap { try? Data(contentsOf: $0) }
            .flatMap { try? JSONDecoder().decode([Route].self, from: $0) } ?? []
    }

    var url: String { "http://127.0.0.1:\(port)" }
    /// Every request so far as "METHOD /path" (no query).
    var seen: [String] { lock.withLock { log.map { "\($0.method) \($0.path)" } } }
    /// Every request so far, in order.
    var requests: [Request] { lock.withLock { log } }

    /// Waits for the listener: 30s, as a loaded CI simulator took over 5s to be ready, and a
    /// listener that fails says why instead of reading as "cannot connect".
    func start() throws {
        let settled = DispatchSemaphore(value: 0)
        listener.stateUpdateHandler = { [weak self] state in
            switch state {
            case .ready: settled.signal()
            case .failed(let error): self?.lock.withLock { self?.startFailure = error }; settled.signal()
            default: break
            }
        }
        listener.newConnectionHandler = { [weak self] c in self?.serve(c) }
        listener.start(queue: queue)
        guard settled.wait(timeout: .now() + 30) == .success else {
            throw URLError(.timedOut, userInfo: [NSLocalizedDescriptionKey: "the mock home was not ready in 30s"])
        }
        if let failure = lock.withLock({ startFailure }) { throw failure }
        guard let p = listener.port?.rawValue else { throw URLError(.cannotConnectToHost) }
        port = p
    }

    /// The person approved the code on another device.
    func approve() { lock.withLock { approved = true } }

    /// From now on, `method path` (exact, no query) answers `status` and `body` (JSON),
    /// after `delay` seconds. Wins over the fixtures.
    func stub(_ method: String, _ path: String, status: Int = 200, body: String = "", delay: TimeInterval = 0) {
        lock.withLock { stubs["\(method) \(path)"] = Answer(status: status, body: Data(body.utf8), delay: delay) }
    }

    /// From now on, `method path` (any query) answers as it would anyway, but after `delay`
    /// seconds. Only the timing is kept here: the answer is still chosen per request, so a
    /// query (`cursor=c2`) or a stub still decides what comes back.
    func delay(_ method: String, _ path: String, by delay: TimeInterval) {
        lock.withLock { delays["\(method) \(path)"] = delay }
    }

    /// From now on, `method path` (any query) is answered only when the test calls `release`:
    /// a latch, so "before the home answers" never races a timer on a slow runner.
    func hold(_ method: String, _ path: String) {
        lock.withLock { _ = held.insert("\(method) \(path)") }
    }

    /// Sends every held answer and stops holding.
    func release() {
        let waiting = lock.withLock {
            held = []
            defer { parked = [] }
            return parked
        }
        waiting.forEach { $0() }
    }

    /// Forgets every stub and delay, and lets held answers go.
    func reset() {
        lock.withLock { stubs = [:]; delays = [:] }
        release()
    }

    /// A fixture's JSON as it is served (times filled in), for a test to compare with.
    func fixture(named name: String) -> String? {
        fixtures.url(forResource: name, withExtension: "json")
            .flatMap { try? Data(contentsOf: $0) }
            .map { Self.fillTimes(String(decoding: $0, as: UTF8.self)) }
    }

    func stop() {
        release()
        listener.cancel()
    }

    private func serve(_ c: NWConnection) {
        c.start(queue: queue)
        receive(c, Data())
    }

    private func receive(_ c: NWConnection, _ sofar: Data) {
        c.receive(minimumIncompleteLength: 1, maximumLength: 65536) { [weak self] data, _, done, error in
            guard let self else { return }
            var buf = sofar
            if let data { buf.append(data) }
            if let end = buf.range(of: Data("\r\n\r\n".utf8)) {
                let head = String(decoding: buf[..<end.lowerBound], as: UTF8.self)
                let lines = head.components(separatedBy: "\r\n")
                let length = lines.first { $0.lowercased().hasPrefix("content-length:") }
                    .flatMap { Int($0.dropFirst("content-length:".count).trimmingCharacters(in: .whitespaces)) } ?? 0
                if buf.count - end.upperBound >= length || done {
                    let parts = (lines.first ?? "").split(separator: " ")
                    let method = parts.first.map(String.init) ?? ""
                    let target = parts.count > 1 ? parts[1].split(separator: "?", maxSplits: 1).map(String.init) : ["/"]
                    let request = Request(method: method, path: target[0], query: target.count > 1 ? target[1] : "",
                                          body: Data(buf[end.upperBound...].prefix(length)))
                    let a = answer(request)
                    var response = Data("HTTP/1.1 \(a.status) \(a.status < 400 ? "OK" : "Error")\r\nContent-Type: \(a.contentType)\r\nContent-Length: \(a.body.count)\r\nConnection: close\r\n\r\n".utf8)
                    response.append(a.body)
                    let send = { c.send(content: response, completion: .contentProcessed { _ in c.cancel() }) }
                    let parked = lock.withLock {
                        guard held.contains("\(request.method) \(request.path)") else { return false }
                        self.parked.append(send)
                        return true
                    }
                    if parked { return }
                    if a.delay > 0 {
                        queue.asyncAfter(deadline: .now() + a.delay, execute: send)
                    } else {
                        send()
                    }
                    return
                }
            }
            if done || error != nil {
                c.cancel()
                return
            }
            receive(c, buf)
        }
    }

    private func answer(_ r: Request) -> Answer {
        let (ok, latency) = lock.withLock {
            log.append(r)
            return (approved, delays["\(r.method) \(r.path)"])
        }
        var a = resolve(r, approved: ok)
        if let latency { a.delay = max(a.delay, latency) }
        return a
    }

    /// What a request gets, before any `delay`: a stub, the connect and Remote routes, an
    /// image, the fixture for this path and query, or 404.
    private func resolve(_ r: Request, approved ok: Bool) -> Answer {
        if let stubbed = lock.withLock({ stubs["\(r.method) \(r.path)"] }) { return stubbed }
        if let fixed = remote(r.method, r.path, approved: ok) {
            return Answer(status: fixed.0, body: Data(fixed.1.utf8))
        }
        if r.method == "GET", Self.isImage(r.path) {
            return Answer(status: 200, body: Self.png, contentType: "image/png")
        }
        if let a = fixture(r.method, r.path, query: r.query) { return a }
        return Answer(status: 404, body: Data(#"{"title":"not found","status":404}"#.utf8))
    }

    /// The Player's browse and live routes, from `Fixtures/routes.json`.
    private func fixture(_ method: String, _ path: String, query: String) -> Answer? {
        // the videos' second (last) page
        if method == "GET", path == "/api/videos", query.contains("cursor=c2") {
            return file("videos-page2").map { Answer(status: 200, body: $0) }
        }
        guard let route = routes.first(where: { $0.method == method && Self.matches($0.path, path) }) else { return nil }
        guard let name = route.file else { return Answer(status: route.status, body: Data()) }
        return file((name as NSString).deletingPathExtension).map { Answer(status: route.status, body: $0) }
    }

    private func file(_ name: String) -> Data? {
        fixture(named: name).map { Data($0.utf8) }
    }

    /// `/api/videos/{id}/chapters` matches `/api/videos/v1/chapters`.
    private static func matches(_ pattern: String, _ path: String) -> Bool {
        let p = pattern.split(separator: "/", omittingEmptySubsequences: false)
        let s = path.split(separator: "/", omittingEmptySubsequences: false)
        guard p.count == s.count else { return false }
        return zip(p, s).allSatisfy { a, b in (a.hasPrefix("{") && a.hasSuffix("}") && !b.isEmpty) || a == b }
    }

    private static func isImage(_ path: String) -> Bool {
        path.hasPrefix("/api/") && (path.hasSuffix("/poster") || path.hasSuffix("/backdrop") || path.hasSuffix("/logo") || path.hasSuffix("/art"))
    }

    /// `{{now}}`, `{{now-600}}`, `{{now+1800}}`: RFC 3339 times, seconds from now.
    static func fillTimes(_ json: String, now: Date = Date()) -> String {
        let f = ISO8601DateFormatter()
        var out = ""
        var rest = Substring(json)
        while let open = rest.range(of: "{{now") {
            out += rest[..<open.lowerBound]
            guard let close = rest[open.upperBound...].range(of: "}}") else { break }
            let offset = TimeInterval(rest[open.upperBound ..< close.lowerBound].replacingOccurrences(of: "+", with: "")) ?? 0
            out += f.string(from: now.addingTimeInterval(offset))
            rest = rest[close.upperBound...]
        }
        return out + rest
    }

    /// A 32 × 48 poster-shaped PNG in the theme's brass-brown.
    static let png = Data(base64Encoded: "iVBORw0KGgoAAAANSUhEUgAAACAAAAAwCAIAAAD/zu84AAAAMElEQVR42u3NMQ0AAAgDsElBAjf+haGCg6RJ/2a6TkUgEAgEAoFAIBAIBAKBQPApWAeEPC5/X6goAAAAAElFTkSuQmCC")!

    /// Connecting, signing in and the Remote's screens.
    private func remote(_ method: String, _ path: String, approved ok: Bool) -> (Int, String)? {
        switch (method, path) {
        case ("GET", "/api/discovery/home"):
            return (200, #"{"id":"ui-home","name":"UI Test Home","version":"0.0.0","urls":[],"sign_in":{"password":true,"code":true}}"#)
        case ("POST", "/api/pairing/device"):
            let exp = ISO8601DateFormatter().string(from: Date().addingTimeInterval(600))
            return (201, #"{"code":"\#(Self.code)","device_code":"dc","expires_at":"\#(exp)","verify_path":"/pair?code=\#(Self.code)","interval":1}"#)
        case ("POST", "/api/pairing/device/token"):
            return ok ? (200, #"{"access_token":"ui.test.token","refresh_token":"ui-refresh"}"#) : (202, #"{"status":"pending"}"#)
        case ("GET", "/api/auth/me"):
            return (200, #"{"id":"u1","username":"tester","is_admin":true,"home_name":"UI Test Home"}"#)
        case ("GET", "/api/issues"):
            return (200, #"{"groups":[]}"#)
        case ("GET", "/api/queues"):
            // two stages for the Remote's queue (story 42.8: the list beside a stage's page)
            return (200, #"{"paused_all":false,"issues_count":0,"stages":[{"stage":"probe","label":"Probe","lane":"a","lane_order":1,"hidden":false,"enabled":true,"paused":false},{"stage":"transcribe","label":"Transcribe","lane":"a","lane_order":2,"hidden":false,"enabled":true,"paused":false}],"queues":[{"stage":"probe","counts":{"pending":3,"ready":1,"running":1,"failed_open":0,"done_24h":12},"rate_per_min":0,"no_worker":false}]}"#)
        case ("GET", "/api/queues/probe/failures"), ("GET", "/api/queues/transcribe/failures"):
            return (200, #"{"groups":[]}"#)
        case ("GET", "/api/cluster"):
            return (200, #"{"home":{"name":"UI Test Home","accept_joins":false,"roles_supported":true},"nodes":[]}"#)
        // the Remote's running work (updates, processes, running jobs): made-up fixtures
        case ("GET", "/api/cluster/updates"):
            return (200, Self.updates)
        case ("GET", "/api/cluster/updates/runs/r10"):
            return (200, Self.updateRun)
        case ("GET", "/api/system/processes"):
            return (200, Self.processes)
        case ("GET", "/api/queues/running"):
            return (200, Self.running)
        default:
            return nil
        }
    }

    // MARK: fixtures for the Remote's running work

    private static func time(_ offset: TimeInterval) -> String {
        ISO8601DateFormatter().string(from: Date().addingTimeInterval(offset))
    }

    static var updates: String {
        #"""
        {"current_version":"0.3.40","watermark":"0.3.40",
         "policy":{"auto_install":true,"channel":"stable","window":"03:00-05:00"},
         "plan":{"target":{"version":"0.3.43","verify_state":"verified"},"newest":{"version":"0.3.44","verify_state":"verified"},
                 "held":false,"stepping_stone":true,"auto_install":true,"due":false,"waiting":"tv_busy"},
         "run":{"id":"r10","version":"0.3.43","from_version":"0.3.40","trigger":"manual","state":"rolling_out","step":"watch",
                "started_at":"\#(time(-1500))"},
         "catalog":[{"version":"0.3.45","verify_state":"failed","blocked_reason":"verify"},
                    {"version":"0.3.42","verify_state":"verified","blocked_reason":"rolled_back"},
                    {"version":"0.3.43","verify_state":"verified"},{"version":"0.3.44","verify_state":"verified"}],
         "last_check_ok":"\#(time(-3600))","check_stale":false,
         "machines":[
           {"id":"home","name":"Mac mini","role":"home","version":"0.3.43","state":"updating","platform":"darwin/arm64",
            "last_update":{"at":"\#(time(-86400 * 2))","ended_at":"\#(time(-86400 * 2))","source":"run","to":"0.3.40","outcome":"installed"}},
           {"id":"n1","name":"Basement PC","role":"node","version":"0.3.40","state":"app_staged","target":"0.3.43","platform":"windows/amd64"},
           {"id":"n2","name":"Attic","role":"node","version":"0.3.40","state":"needs_person","target":"0.3.43","platform":"linux/amd64",
            "home_db":{"ok":false,"code":"no_hba","detail":"pg_hba.conf has no entry for host 192.168.1.20"}}],
         "tv_held":{"run_id":"r9","version":"0.3.40","outcome":"held","number":12,"held_on":"0.3.39","state":"held",
                    "failed":["slot.health"],"checks":[{"check":"slot.health","result":"fail","code":"slot.release_mismatch",
                              "value":"the staging slot runs another release (release_mismatch)"}]}}
        """#
    }

    static var updateRun: String {
        #"""
        {"id":"r10","version":"0.3.43","from_version":"0.3.40","trigger":"manual","state":"rolling_out","step":"watch",
         "started_at":"\#(time(-1500))","install_kind":"appbundle","detail":{},
         "steps":[
           {"seq":1,"host":"home","step":"stage","state":"done","started_at":"\#(time(-1500))","detail":{"detail":"downloaded 118 MB"}},
           {"seq":2,"host":"home","step":"backup","state":"done","started_at":"\#(time(-1300))"},
           {"seq":3,"host":"home","step":"switch","state":"done","started_at":"\#(time(-1100))"},
           {"seq":4,"host":"home","step":"watch","state":"running","started_at":"\#(time(-900))"}],
         "targets":[
           {"node_id":"home","name":"Mac mini","ord":0,"platform":"darwin/arm64","state":"watching","from_version":"0.3.40","to_version":"0.3.43","attempts":1},
           {"node_id":"n1","name":"Basement PC","ord":1,"platform":"windows/amd64","state":"app_staged","from_version":"0.3.40","to_version":"0.3.43","attempts":1},
           {"node_id":"n2","name":"Attic","ord":2,"platform":"linux/amd64","state":"needs_person","from_version":"0.3.40","to_version":"0.3.43","attempts":2,
            "error":{"code":"old_launcher","detail":"Run the installer once on this machine: curl -fsSL https://get.maktaba.app | sh"}}]}
        """#
    }

    static var processes: String {
        #"""
        {"available":true,"host":"mac-mini","processes":[
           {"role":"api","state":"running","pid":101,"restarts":0,"crash_loop":false,"uptime_sec":93784},
           {"role":"streaming","state":"running","pid":102,"restarts":1,"crash_loop":false,"uptime_sec":3725},
           {"role":"pipeline","state":"starting","pid":103,"restarts":0,"crash_loop":false,"uptime_sec":12,
            "detail":"building the pipeline's Python environment (4 of 9)","detail_code":"venv_building"},
           {"role":"tv-slot","instance":"blue","state":"running","pid":104,"restarts":0,"crash_loop":false,"uptime_sec":93700},
           {"role":"tv-slot","instance":"green","state":"stopped","restarts":0,"crash_loop":false},
           {"role":"tv-slot","instance":"amber","wanted":true,"state":"backoff","restarts":4,"crash_loop":true,
            "next_restart_at":"\#(time(240))","detail":"port 9104 is held by pid 77 (maktaba-streaming)","detail_code":"port_taken",
            "last_exit":{"at":"\#(time(-60))","code":-1,"reason":"start_failed","error":"listen tcp :9104: bind: address already in use","uptime_sec":0}},
           {"role":"postgres","state":"running","pid":99,"restarts":0,"crash_loop":false,"uptime_sec":93800}],
         "events":[
           {"seq":51,"at":"\#(time(-3725))","role":"streaming","kind":"exited","code":-1,"signal":"SIGKILL","reason":"crash"},
           {"seq":52,"at":"\#(time(-3724))","role":"streaming","kind":"restarting"},
           {"seq":53,"at":"\#(time(-600))","role":"tv-slot","instance":"amber","kind":"exited","code":-1,"reason":"start_failed"},
           {"seq":54,"at":"\#(time(-60))","role":"tv-slot","instance":"amber","kind":"failed"}]}
        """#
    }

    static var running: String {
        #"""
        {"host":"","jobs":[
          {"id":7,"stage":"probe","state":"running","subject_kind":"video","subject_id":"v1","subject_label":"Seerah, lecture 12",
           "attempts":1,"max_attempts":3,"started_at":"\#(time(-3905))","progress":{"step":"ffprobe","steps":[],"done_units":3,"total_units":4}},
          {"id":8,"stage":"transcribe","state":"running","subject_kind":"video","subject_id":"v2","subject_label":"Tafsir, Surat al-Kahf part 2",
           "attempts":2,"max_attempts":3,"started_at":"\#(time(-725))","progress":{"step":"whisper","steps":[],"done_units":30,"total_units":120}},
          {"id":9,"stage":"probe","state":"claimed","subject_kind":"video","subject_id":"v3","subject_label":"Fiqh of fasting",
           "attempts":0,"max_attempts":3,"started_at":null,"progress":null}]}
        """#
    }
}
