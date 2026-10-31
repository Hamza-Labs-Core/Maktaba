import Foundation
import Network

/// A home for the UI tests, served by the test runner on 127.0.0.1 (the app on the same
/// simulator reaches it): `/api/discovery/home`, a sign-in code that is approved when the
/// test says so, and empty lists for the screens after signing in. No real home and no
/// real account is involved.
final class MockHome: @unchecked Sendable {
    private let listener: NWListener
    private let queue = DispatchQueue(label: "maktaba.uitests.mockhome")
    private let lock = NSLock()
    private var approved = false
    private var seenRoutes: [String] = []
    private(set) var port: UInt16 = 0

    static let code = "TEST-2345"

    init() throws {
        let params = NWParameters.tcp
        params.requiredLocalEndpoint = .hostPort(host: "127.0.0.1", port: .any)
        listener = try NWListener(using: params)
    }

    var url: String { "http://127.0.0.1:\(port)" }
    var seen: [String] { lock.withLock { seenRoutes } }

    func start() throws {
        let ready = DispatchSemaphore(value: 0)
        listener.stateUpdateHandler = { if case .ready = $0 { ready.signal() } }
        listener.newConnectionHandler = { [weak self] c in self?.serve(c) }
        listener.start(queue: queue)
        guard ready.wait(timeout: .now() + 5) == .success, let p = listener.port?.rawValue else {
            throw URLError(.cannotConnectToHost)
        }
        port = p
    }

    /// The person approved the code on another device.
    func approve() { lock.withLock { approved = true } }

    func stop() { listener.cancel() }

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
                    let path = parts.count > 1 ? String(parts[1].split(separator: "?", maxSplits: 1)[0]) : "/"
                    let (status, body) = answer(method, path)
                    let response = "HTTP/1.1 \(status) OK\r\nContent-Type: application/json\r\nContent-Length: \(body.utf8.count)\r\nConnection: close\r\n\r\n\(body)"
                    c.send(content: Data(response.utf8), completion: .contentProcessed { _ in c.cancel() })
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

    private func answer(_ method: String, _ path: String) -> (Int, String) {
        let ok = lock.withLock {
            seenRoutes.append("\(method) \(path)")
            return approved
        }
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
        case ("GET", "/api/libraries"), ("GET", "/api/videos"):
            return (200, #"{"items":[],"next":null}"#)
        case ("GET", "/api/recommendations"):
            return (200, #"{"rails":[]}"#)
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
            return (404, #"{"title":"not found","status":404}"#)
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
