import Foundation
import Observation

// The recovery console on the Remote (Epic 53 stories 53.04 and 53.05): the routes the web's
// recovery page calls, `/recovery/api/*` on the home's address, served by the supervisor
// itself, so they answer with the API or the database down. The console takes the app's
// recovery pass (a 7-day JWT the API gives an admin while it is up) or its access token as a
// bearer; anyone else gets 401. Its refusals never sign the app out.

/// `GET /recovery/api/state`: the status board, each part with its detail, the disk, the
/// version a rollback goes back to and whether the home is in safe mode.
public struct RecoveryState: Decodable, Equatable, Sendable {
    public var status: HomeStatusDoc
    public var components: [RecoveryPart]
    public var diskFree: Int64
    public var logsBytes: Int64
    /// The previous version kept ("" or nil: none to go back to).
    public var rollback: String?
    public var safeMode: Bool

    enum CodingKeys: String, CodingKey {
        case status, components, rollback
        case diskFree = "disk_free"
        case logsBytes = "logs_bytes"
        case safeMode = "safe_mode"
    }

    public init(status: HomeStatusDoc = HomeStatusDoc(), components: [RecoveryPart] = [], diskFree: Int64 = -1,
                logsBytes: Int64 = 0, rollback: String? = nil, safeMode: Bool = false) {
        self.status = status
        self.components = components
        self.diskFree = diskFree
        self.logsBytes = logsBytes
        self.rollback = rollback
        self.safeMode = safeMode
    }

    public init(from d: Decoder) throws {
        let c = try d.container(keyedBy: CodingKeys.self)
        status = try c.decodeIfPresent(HomeStatusDoc.self, forKey: .status) ?? HomeStatusDoc()
        components = try c.decodeIfPresent([RecoveryPart].self, forKey: .components) ?? []
        diskFree = try c.decodeIfPresent(Int64.self, forKey: .diskFree) ?? -1
        logsBytes = try c.decodeIfPresent(Int64.self, forKey: .logsBytes) ?? 0
        rollback = try c.decodeIfPresent(String.self, forKey: .rollback).flatMap { $0.isEmpty ? nil : $0 }
        safeMode = try c.decodeIfPresent(Bool.self, forKey: .safeMode) ?? false
    }

    /// The status groups in the board's order (the supervisor's `StatusGroups`), then any other.
    public static let groupOrder = ["database", "api", "streaming", "processing", "search", "logs"]

    /// The groups the status names, in order, with their states.
    public var groups: [(name: String, state: String)] {
        let known = Self.groupOrder.filter { status.components[$0] != nil }
        let others = status.components.keys.filter { !Self.groupOrder.contains($0) }.sorted()
        return (known + others).map { ($0, status.components[$0] ?? "ok") }
    }

    /// Every part is working (the page then says so).
    public var allWell: Bool { status.overall == "ok" }
}

/// One part (an Epic 51 catalog component) on the console.
public struct RecoveryPart: Decodable, Equatable, Sendable, Identifiable {
    public var id: String
    public var group: String
    public var phase: String?
    public var reason: String?
    /// ok, starting, degraded, restarting, down or off
    public var state: String
    public var restarts: Int
    public var cutOff: Bool
    /// RFC 3339
    public var lastExit: String?
    public var lastCode: Int?
    public var lastError: String?
    /// A part held or turned off is not restarted from the console.
    public var canRestart: Bool

    enum CodingKeys: String, CodingKey {
        case id, group, phase, reason, state, restarts
        case cutOff = "cut_off"
        case lastExit = "last_exit"
        case lastCode = "last_code"
        case lastError = "last_error"
        case canRestart = "can_restart"
    }

    public init(id: String, group: String = "other", phase: String? = nil, reason: String? = nil, state: String = "ok",
                restarts: Int = 0, cutOff: Bool = false, lastExit: String? = nil, lastCode: Int? = nil,
                lastError: String? = nil, canRestart: Bool = true) {
        self.id = id
        self.group = group
        self.phase = phase
        self.reason = reason
        self.state = state
        self.restarts = restarts
        self.cutOff = cutOff
        self.lastExit = lastExit
        self.lastCode = lastCode
        self.lastError = lastError
        self.canRestart = canRestart
    }

    public init(from d: Decoder) throws {
        let c = try d.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        group = try c.decodeIfPresent(String.self, forKey: .group) ?? "other"
        phase = try c.decodeIfPresent(String.self, forKey: .phase)
        reason = try c.decodeIfPresent(String.self, forKey: .reason)
        state = try c.decodeIfPresent(String.self, forKey: .state) ?? "ok"
        restarts = try c.decodeIfPresent(Int.self, forKey: .restarts) ?? 0
        cutOff = try c.decodeIfPresent(Bool.self, forKey: .cutOff) ?? false
        lastExit = try c.decodeIfPresent(String.self, forKey: .lastExit)
        lastCode = try c.decodeIfPresent(Int.self, forKey: .lastCode)
        lastError = try c.decodeIfPresent(String.self, forKey: .lastError)
        canRestart = try c.decodeIfPresent(Bool.self, forKey: .canRestart) ?? false
    }

    /// When it last stopped, if it did.
    public var lastExitDate: Date? { lastExit.flatMap(homeTime) }

    /// A state that needs the admin (the page marks it).
    public var troubled: Bool { ["degraded", "restarting", "down"].contains(state) }
}

/// One line of `GET /recovery/api/logs` (the 53.01 schema, read from the log files).
public struct RecoveryLine: Equatable, Sendable, Identifiable {
    public var id: Int
    public var ts: Date?
    public var level: String
    public var service: String
    public var component: String
    public var msg: String

    /// The line's words: its message, a raw line's text, and its error.
    static func parse(_ object: [String: Any], index: Int) -> RecoveryLine {
        var msg = object["msg"] as? String ?? ""
        if msg == "raw_line", let text = (object["attrs"] as? [String: Any])?["text"] as? String { msg = text }
        if let e = object["err"] as? String, !e.isEmpty {
            msg += ": " + e
        } else if let e = (object["err"] as? [String: Any])?["msg"] as? String, !e.isEmpty {
            msg += ": " + e
        }
        return RecoveryLine(id: index, ts: (object["ts"] as? String).flatMap(homeTime),
                            level: object["level"] as? String ?? "", service: object["service"] as? String ?? "",
                            component: object["component"] as? String ?? "", msg: msg)
    }
}

/// What the console can do from the Remote (story 53.04 §4). Restoring a backup and the
/// diagnostics download stay on the web page.
public enum RecoveryAction: Equatable, Sendable, Hashable {
    case restart(String)
    case restartAll
    case safeMode
    case staySafe
    case leaveSafe
    case rollback(String)

    /// How many times the person confirms: twice to go back a version (the home asks twice
    /// for every action through the relay, and says so with 428).
    public var confirms: Int {
        if case .rollback = self { return 2 }
        return 1
    }

    var body: [String: Any] {
        switch self {
        case .restart(let id): ["action": "restart", "component": id]
        case .restartAll: ["action": "restart_all"]
        case .safeMode: ["action": "safe_mode"]
        case .staySafe: ["action": "stay_safe"]
        case .leaveSafe: ["action": "leave_safe"]
        case .rollback: ["action": "rollback"]
        }
    }
}

/// Why the console did not answer as asked.
public enum RecoveryFailure: Error, Equatable, Sendable {
    /// 401 or 403: not an admin of this home, a pass that expired or was revoked, or too many
    /// tries (the console locks an address for 5 minutes).
    case denied
    /// 428: the home wants `n` confirmations (an action through the relay).
    case confirm(Int)
    /// The home refused the action, in its words (409: held, nothing to roll back to, …).
    case refused(String)
    /// 404: a home from before the recovery console.
    case notHere
    /// No answer.
    case unreachable
}

extension APIClient {
    /// Asks the API for a recovery pass and keeps it (admins only, while the API is up). Best
    /// effort: false when there is none (a viewer, the API down, an older home).
    @discardableResult
    public func refreshRecoveryPass() async -> Bool {
        struct Pass: Decodable { let pass: String? }
        guard let p: Pass = try? await send(request("POST", "/api/auth/recovery-pass")),
              let pass = p.pass, !pass.isEmpty else { return false }
        tokens.saveRecoveryPass(pass)
        return true
    }

    public func recoveryState() async throws(RecoveryFailure) -> RecoveryState {
        let data = try await recoveryCall(request("GET", "/recovery/api/state"))
        do {
            return try JSONDecoder().decode(RecoveryState.self, from: data)
        } catch {
            throw .refused(String(describing: error))
        }
    }

    /// The warnings and errors of the last `minutes`, newest first.
    public func recoveryLines(minutes: Int = 15) async throws(RecoveryFailure) -> [RecoveryLine] {
        let data = try await recoveryCall(request("GET", "/recovery/api/logs", query: ["minutes": String(minutes)]))
        let lines = ((try? JSONSerialization.jsonObject(with: data)) as? [String: Any])?["lines"] as? [Any] ?? []
        return lines.enumerated().compactMap { i, l in (l as? [String: Any]).map { RecoveryLine.parse($0, index: i) } }
    }

    /// Runs `action`, confirmed `confirm` times.
    public func recoveryAction(_ action: RecoveryAction, confirm: Int) async throws(RecoveryFailure) {
        var body = action.body
        body["confirm"] = confirm
        var r = request("POST", "/recovery/api/action", body: body)
        // the console's guard against a cross-site post: a header only its callers send
        r.setValue("1", forHTTPHeaderField: "X-Maktaba-Recovery")
        _ = try await recoveryCall(r)
    }

    /// Sends `r` to the console with the pass, else the access token (refreshed once). A
    /// credential the console turns down is never a sign-out: a pass it refuses is dropped
    /// (each refusal counts toward its lockout), and the screen says only admins get in.
    func recoveryCall(_ r: URLRequest) async throws(RecoveryFailure) -> Data {
        var r = r
        r.timeoutInterval = 10
        r.cachePolicy = .reloadIgnoringLocalCacheData
        var refreshed = false
        var credentials = [tokens.recoveryPass, tokens.access].compactMap { $0 }
        while !credentials.isEmpty {
            let credential = credentials.removeFirst()
            r.setValue("Bearer \(credential)", forHTTPHeaderField: "Authorization")
            let data: Data, code: Int
            do {
                let answer = try await exchange(r, authorized: false, answered: Set(300..<600))
                (data, code) = (answer.data, answer.response?.statusCode ?? 0)
            } catch {
                throw .unreachable
            }
            switch code {
            case 200..<300:
                return data
            case 401:
                if credential == tokens.recoveryPass {
                    tokens.saveRecoveryPass(nil)
                } else if credentials.isEmpty, !refreshed, tokens.refresh != nil {
                    // an access token that expired: one refresh, while the API answers
                    refreshed = true
                    if let fresh = try? await refreshOnce() { credentials.append(fresh) }
                }
            case 403:
                throw .denied
            case 404:
                throw .notHere
            case 428:
                let need = ((try? JSONSerialization.jsonObject(with: data)) as? [String: Any])?["confirm"] as? Int
                throw .confirm(need ?? 2)
            default:
                let said = ((try? JSONSerialization.jsonObject(with: data)) as? [String: Any])?["error"] as? String
                throw .refused(said ?? "HTTP \(code)")
            }
        }
        throw .denied
    }
}

/// The Remote's recovery screen's state: what the console says, the lines, and an action
/// being confirmed or run.
@MainActor
@Observable
public final class RecoveryModel {
    public enum Phase: Equatable, Sendable {
        case loading, ready, denied, notHere, unreachable
    }

    /// An action waiting for the person's confirmation `step` of `need`.
    public struct Asking: Equatable, Sendable {
        public let action: RecoveryAction
        public let step: Int
        public let need: Int
    }

    /// The last action's outcome.
    public enum Outcome: Equatable, Sendable {
        case done
        case failed(String)
    }

    public private(set) var phase = Phase.loading
    public private(set) var state: RecoveryState?
    public private(set) var lines: [RecoveryLine] = []
    public private(set) var minutes = 15
    public private(set) var running: RecoveryAction?
    public var asking: Asking?
    public var outcome: Outcome?

    let client: APIClient

    public init(client: APIClient) { self.client = client }

    /// Reads the state and the lines.
    public func load() async {
        do {
            state = try await client.recoveryState()
            lines = try await client.recoveryLines(minutes: minutes)
            phase = .ready
        } catch {
            switch error {
            case .denied: phase = .denied
            case .notHere: phase = .notHere
            case .unreachable, .confirm, .refused:
                // what was read stays on screen while the home does not answer for a moment
                if state == nil { phase = .unreachable }
            }
        }
    }

    /// 15 more minutes of lines.
    public func loadMore() async {
        minutes = min(minutes + 15, 24 * 60)
        if let more = try? await client.recoveryLines(minutes: minutes) { lines = more }
    }

    /// Reads again every `every` (the web page's 10 s) until cancelled, or the console turns the app down.
    public func run(every: Duration = .seconds(10)) async {
        while !Task.isCancelled {
            await load()
            if phase == .denied || phase == .notHere { return }
            try? await Task.sleep(for: every)
        }
    }

    /// The person chose `action`: the first confirmation.
    public func ask(_ action: RecoveryAction) {
        outcome = nil
        asking = Asking(action: action, step: 1, need: action.confirms)
    }

    public func cancel() { asking = nil }

    /// The person confirmed `confirmed` (the confirmation on screen when nil; an alert's
    /// button passes the one it showed, as the alert clears `asking` while it closes): the
    /// next confirmation, or the action.
    public func confirm(_ confirmed: Asking? = nil) async {
        guard let a = confirmed ?? asking else { return }
        if a.step < a.need {
            asking = Asking(action: a.action, step: a.step + 1, need: a.need)
            return
        }
        asking = nil
        running = a.action
        defer { running = nil }
        do {
            try await client.recoveryAction(a.action, confirm: a.need)
            outcome = .done
        } catch {
            switch error {
            case .confirm(let n) where n > a.need:
                // through the relay: one more confirmation
                asking = Asking(action: a.action, step: a.need + 1, need: n)
                return
            case .denied: outcome = .failed(""); phase = .denied
            case .refused(let why): outcome = .failed(why)
            case .notHere, .unreachable, .confirm: outcome = .failed("")
            }
        }
        await load()
    }
}
