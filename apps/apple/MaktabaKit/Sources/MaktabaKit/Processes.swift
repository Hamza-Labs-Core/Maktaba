import Foundation

// The home's processes and running jobs as Maktaba Remote shows them (read only).
//
//   GET /api/system/processes   ProcessesDoc: the supervisor's processes and recent events
//                               (api/internal/handlers/processes, the supervisor's
//                               cmd/maktaba-server/internal/supervisor/state.go)
//   GET /api/queues/running     RunningJobs: the jobs running now, oldest first
//                               (api/internal/handlers/queues/running.go)

/// `GET /api/system/processes`. Without the supervisor (the API not run by maktaba-server,
/// or the supervisor not answering) `available` is false and `reason` says why.
public struct ProcessesDoc: Decodable, Sendable {
    public let available: Bool
    public let reason: String?
    public let host: String?
    public let processes: [ProcessStatus]
    public let events: [ProcessEvent]

    enum CodingKeys: String, CodingKey {
        case available, reason, host, processes, events
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        available = c.lenient(.available) ?? false
        reason = c.lenient(.reason)
        host = c.lenient(.host)
        processes = c.lenientList(.processes)
        events = c.lenientList(.events)
    }

    /// The newest events first, at most `limit`.
    public func recentEvents(_ limit: Int = 20) -> [ProcessEvent] {
        Array(events.sorted { $0.seq > $1.seq }.prefix(limit))
    }
}

/// One process the supervisor runs: a role (api, streaming, pipeline, postgres, tv-slot,
/// ...) and, for the TV's slots, its instance (blue, green, amber).
public struct ProcessStatus: Decodable, Identifiable, Sendable {
    public let role: String
    public let instance: String?
    /// An on-demand process asked for (the amber TV slot; a stopped standby).
    public let wanted: Bool
    /// starting, running, unhealthy, backoff, failed, exited, stopped (a newer one raw).
    public let state: String
    public let pid: Int?
    public let restarts: Int
    public let crashLoop: Bool
    public let uptimeSec: Double?
    public let lastExit: ProcessExit?
    public let nextRestartAt: Date?
    public let lastProbe: ProcessProbe?
    /// What it waits on before it starts, or why that failed (English).
    public let detail: String?
    /// The same as a code from a fixed list (port_taken, venv_building, db_migrating, ...).
    public let detailCode: String?
    public let adopted: Bool

    public var id: String { instance.map { "\(role)/\($0)" } ?? role }

    enum CodingKeys: String, CodingKey {
        case role, instance, wanted, state, pid, restarts, detail, adopted
        case crashLoop = "crash_loop"
        case uptimeSec = "uptime_sec"
        case lastExit = "last_exit"
        case nextRestartAt = "next_restart_at"
        case lastProbe = "last_probe"
        case detailCode = "detail_code"
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        role = c.lenient(.role) ?? ""
        let inst: String? = c.lenient(.instance)
        instance = (inst ?? "").isEmpty ? nil : inst
        wanted = c.lenient(.wanted) ?? false
        state = c.lenient(.state) ?? ""
        pid = c.lenient(.pid)
        restarts = c.lenient(.restarts) ?? 0
        crashLoop = c.lenient(.crashLoop) ?? false
        uptimeSec = c.lenient(.uptimeSec)
        lastExit = c.lenient(.lastExit)
        nextRestartAt = c.lenient(.nextRestartAt)
        lastProbe = c.lenient(.lastProbe)
        let d: String? = c.lenient(.detail)
        detail = (d ?? "").isEmpty ? nil : d
        let code: String? = c.lenient(.detailCode)
        detailCode = (code ?? "").isEmpty ? nil : code
        adopted = c.lenient(.adopted) ?? false
    }

    /// Its name: the web's for the role, a TV slot by its colour ("TV slot blue").
    public var name: String {
        let base = role == "tv-slot" ? "TV slot" : words(StatusWords.processRoles, role)
        return instance.map { "\(base) \($0)" } ?? base
    }

    public var stateText: String { words(StatusWords.processStates, state) }

    /// Its detail in words: the English the supervisor wrote, else its code.
    public var detailText: String? {
        detail ?? detailCode.map { $0.replacingOccurrences(of: "_", with: " ") }
    }
}

/// How a process's last run ended.
public struct ProcessExit: Decodable, Sendable {
    public let at: Date?
    public let code: Int
    public let signal: String?
    /// crash, clean, hang, start_failed, shutdown, stopped, restarted.
    public let reason: String
    public let error: String?
    public let uptimeSec: Double?

    enum CodingKeys: String, CodingKey {
        case at, code, signal, reason, error
        case uptimeSec = "uptime_sec"
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        at = c.lenient(.at)
        code = c.lenient(.code) ?? -1
        signal = c.lenient(.signal)
        reason = c.lenient(.reason) ?? ""
        error = c.lenient(.error)
        uptimeSec = c.lenient(.uptimeSec)
    }

    /// Whether it went down by itself (worth showing as a crash).
    public var failed: Bool { ["crash", "hang", "start_failed"].contains(reason) }

    /// The web's health.processes.reason.* words.
    public var sentence: String {
        ProcessExit.sentence(reason: reason, code: code, signal: signal)
    }

    static func sentence(reason: String, code: Int, signal: String?) -> String {
        switch reason {
        case "hang": return "stopped responding and was restarted"
        case "start_failed": return "could not start"
        case "clean": return "exited on its own"
        case "shutdown": return "stopped by the server's shutdown"
        case "stopped": return "stopped on request"
        case "restarted": return "restarted"
        default:
            if let signal, !signal.isEmpty { return "killed by \(signal)" }
            if reason == "crash" || reason.isEmpty { return "exited with code \(code)" }
            return reason.replacingOccurrences(of: "_", with: " ")
        }
    }
}

/// A process's last liveness probe.
public struct ProcessProbe: Decodable, Sendable {
    public let ok: Bool
    public let error: String?
    public let failures: Int

    enum CodingKeys: String, CodingKey {
        case ok, error
        case failures = "consecutive_failures"
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        ok = c.lenient(.ok) ?? true
        error = c.lenient(.error)
        failures = c.lenient(.failures) ?? 0
    }
}

/// One entry of the supervisor's event log.
public struct ProcessEvent: Decodable, Identifiable, Sendable {
    public let seq: Int
    public let at: Date?
    public let role: String
    public let instance: String?
    /// started, exited, restarting, failed, hung.
    public let kind: String
    public let code: Int?
    public let signal: String?
    public let reason: String?
    public let message: String?

    public var id: Int { seq }

    enum CodingKeys: String, CodingKey {
        case seq, at, role, instance, kind, code, signal, reason, message
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        seq = c.lenient(.seq) ?? 0
        at = c.lenient(.at)
        role = c.lenient(.role) ?? ""
        let inst: String? = c.lenient(.instance)
        instance = (inst ?? "").isEmpty ? nil : inst
        kind = c.lenient(.kind) ?? ""
        code = c.lenient(.code)
        signal = c.lenient(.signal)
        reason = c.lenient(.reason)
        message = c.lenient(.message)
    }

    public var processName: String {
        let base = role == "tv-slot" ? "TV slot" : words(StatusWords.processRoles, role)
        return instance.map { "\(base) \($0)" } ?? base
    }

    /// What happened, in a few words.
    public var sentence: String {
        switch kind {
        case "started": return "started"
        case "exited": return ProcessExit.sentence(reason: reason ?? "", code: code ?? -1, signal: signal)
        case "restarting": return "restarting"
        case "failed": return "crash loop: retried slowly"
        case "hung": return "stopped responding"
        default: return message ?? kind.replacingOccurrences(of: "_", with: " ")
        }
    }

    public var isFailure: Bool {
        kind == "failed" || kind == "hung" || (kind == "exited" && ["crash", "hang", "start_failed"].contains(reason ?? ""))
    }
}

// MARK: running jobs

/// `GET /api/queues/running`: the jobs running now (claimed, running, resuming), oldest
/// first.
public struct RunningJobs: Decodable, Sendable {
    public let jobs: [RunningJob]

    enum CodingKeys: String, CodingKey { case jobs }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        jobs = c.lenientList(.jobs)
    }
}

/// A job that runs now.
public struct RunningJob: Decodable, Identifiable, Sendable {
    public let id: Int
    public let stage: String
    public let state: String
    public let subjectKind: String?
    public let subjectLabel: String
    public let attempts: Int
    public let maxAttempts: Int
    public let startedAt: Date?
    public let progressStep: String?
    public let doneUnits: Int?
    public let totalUnits: Int?
    public let claimedBy: String?

    enum CodingKeys: String, CodingKey {
        case id, stage, state, attempts, progress
        case subjectKind = "subject_kind"
        case subjectID = "subject_id"
        case subjectLabel = "subject_label"
        case maxAttempts = "max_attempts"
        case startedAt = "started_at"
        case claimedBy = "claimed_by"
    }

    private struct Progress: Decodable {
        let step: String?
        let doneUnits: Int?
        let totalUnits: Int?
        enum CodingKeys: String, CodingKey {
            case step
            case doneUnits = "done_units"
            case totalUnits = "total_units"
        }

        init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            step = c.lenient(.step)
            doneUnits = c.lenient(.doneUnits)
            totalUnits = c.lenient(.totalUnits)
        }
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(Int.self, forKey: .id)
        stage = c.lenient(.stage) ?? ""
        state = c.lenient(.state) ?? ""
        subjectKind = c.lenient(.subjectKind)
        let label: String? = c.lenient(.subjectLabel)
        let subjectID: String? = c.lenient(.subjectID)
        subjectLabel = (label ?? "").isEmpty ? (subjectID ?? "") : label!
        attempts = c.lenient(.attempts) ?? 0
        maxAttempts = c.lenient(.maxAttempts) ?? 0
        startedAt = c.lenient(.startedAt)
        let p: Progress? = c.lenient(.progress)
        progressStep = p?.step
        doneUnits = p?.doneUnits
        totalUnits = p?.totalUnits
        claimedBy = c.lenient(.claimedBy)
    }

    /// How long it has run (nil before it started).
    public func runningFor(now: Date = Date()) -> TimeInterval? {
        startedAt.map { max(0, now.timeIntervalSince($0)) }
    }

    /// The share done, 0...1, when its progress has units.
    public var fraction: Double? {
        guard let d = doneUnits, let t = totalUnits, t > 0 else { return nil }
        return min(1, max(0, Double(d) / Double(t)))
    }
}

/// "running for 1h 5m" (the web's queue.running.for with health.processes.dur.*).
public func runningText(_ secs: TimeInterval) -> String {
    "running for \(shortDuration(secs))"
}

/// 2d 3h, 1h 5m, 12m, 40s (the web's health.processes.dur.*).
public func shortDuration(_ secs: TimeInterval) -> String {
    let s = max(0, Int(secs))
    let d = s / 86400, h = (s % 86400) / 3600, m = (s % 3600) / 60
    if d > 0 { return "\(d)d \(h)h" }
    if h > 0 { return "\(h)h \(m)m" }
    if m > 0 { return "\(m)m" }
    return "\(s)s"
}
