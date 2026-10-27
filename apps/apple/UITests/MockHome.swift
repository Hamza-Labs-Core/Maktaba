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
        default:
            return (404, #"{"title":"not found","status":404}"#)
        }
    }
}
