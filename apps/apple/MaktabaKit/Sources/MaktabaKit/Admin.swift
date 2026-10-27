import Foundation

// What Maktaba Remote reads and changes: the home's processing queue, its machines, the
// libraries' scans. Every route is the home admins'; the home refuses anyone else (403),
// whatever an app shows.

// MARK: the processing queue

/// `GET /api/queues`: every stage, its counts and whether it is paused, and the home's
/// "pause all".
public struct QueueSnapshot: Decodable, Sendable {
    public let pausedAll: Bool
    public let issuesCount: Int
    public let stages: [StageInfo]
    public let queues: [QueueSummary]
    public let health: QueueHealth?

    enum CodingKeys: String, CodingKey {
        case stages, queues, health
        case pausedAll = "paused_all"
        case issuesCount = "issues_count"
    }

    /// The stages a person sees (not the hidden ones), in the pipeline's order, each with
    /// its counts.
    public var rows: [QueueRow] {
        let summaries = Dictionary(queues.map { ($0.stage, $0) }, uniquingKeysWith: { a, _ in a })
        return stages
            .filter { !$0.hidden }
            .sorted { ($0.lane, $0.laneOrder, $0.stage) < ($1.lane, $1.laneOrder, $1.stage) }
            .map { QueueRow(info: $0, summary: summaries[$0.stage]) }
    }
}

public struct StageInfo: Decodable, Hashable, Sendable {
    public let stage: String
    public let label: String
    public let lane: String
    public let laneOrder: Int
    public let hidden: Bool
    public let enabled: Bool
    public let paused: Bool

    enum CodingKeys: String, CodingKey {
        case stage, label, lane, hidden, enabled, paused
        case laneOrder = "lane_order"
    }
}

public struct QueueSummary: Decodable, Hashable, Sendable {
    public let stage: String
    public let counts: QueueCounts
    public let ratePerMin: Double
    public let etaSec: Double?
    /// Why nothing runs: "paused", "paused_all", "no_worker", "idle", ...
    public let waitingReason: String?
    public let noWorker: Bool

    enum CodingKeys: String, CodingKey {
        case stage, counts
        case ratePerMin = "rate_per_min"
        case etaSec = "eta_sec"
        case waitingReason = "waiting_reason"
        case noWorker = "no_worker"
    }
}

public struct QueueCounts: Decodable, Hashable, Sendable {
    public let pending: Int
    public let ready: Int
    public let running: Int
    public let failedOpen: Int
    public let done24h: Int

    enum CodingKeys: String, CodingKey {
        case pending, ready, running
        case failedOpen = "failed_open"
        case done24h = "done_24h"
    }
}

/// The health strip of `GET /api/queues`: the backlog and the home's CPU.
public struct QueueHealth: Decodable, Hashable, Sendable {
    public struct CPU: Decodable, Hashable, Sendable {
        public let pct: Double?
    }

    public struct Backlog: Decodable, Hashable, Sendable {
        public let ready: Int
    }

    public let cpu: CPU?
    public let backlog: Backlog?
}

/// One stage as the queue screen lists it.
public struct QueueRow: Identifiable, Hashable, Sendable {
    public let info: StageInfo
    public let summary: QueueSummary?

    public var id: String { info.stage }
    public var waiting: Int { (summary?.counts.pending ?? 0) + (summary?.counts.ready ?? 0) }
    public var running: Int { summary?.counts.running ?? 0 }
    public var failed: Int { summary?.counts.failedOpen ?? 0 }

    /// What the row says about the stage, in a few words.
    public var state: String {
        if !info.enabled { return "Off" }
        if info.paused { return "Paused" }
        switch summary?.waitingReason {
        case "paused_all": return "Paused (all)"
        case "no_worker": return "No worker"
        case "not_configured": return "Not set up"
        case "awaiting_review": return "Awaiting review"
        case "backoff", "service_retrying": return "Retrying"
        default: break
        }
        if running > 0 { return "Running" }
        return waiting > 0 ? "Waiting" : "Idle"
    }
}

// MARK: the machines

/// `GET /api/cluster`: this home and every machine that joined it or asks to.
public struct ClusterOverview: Decodable, Sendable {
    public let home: ClusterHome
    public let nodes: [Machine]

    /// Pending requests first (the ones an admin can still answer), then the approved
    /// machines, then the rest (rejected, revoked, expired); by name within each.
    public var sortedNodes: [Machine] {
        nodes.sorted { ($0.group.rawValue, $0.name.lowercased()) < ($1.group.rawValue, $1.name.lowercased()) }
    }
}

public struct ClusterHome: Decodable, Sendable {
    public let name: String
    public let acceptJoins: Bool
    public let acceptJoinsUntil: Date?
    public let rolesSupported: Bool
    public let rolesReason: String?
    public let webURL: String?
    public let version: String?

    enum CodingKeys: String, CodingKey {
        case name, version
        case acceptJoins = "accept_joins"
        case acceptJoinsUntil = "accept_joins_until"
        case rolesSupported = "roles_supported"
        case rolesReason = "roles_reason"
        case webURL = "web_url"
    }
}

/// A machine of the home (a node): one asking to join, approved, or let go.
public struct Machine: Decodable, Identifiable, Hashable, Sendable {
    public let id: String
    public let name: String
    /// "pending", "approved", "rejected" or "revoked".
    public let state: String
    public let expired: Bool
    public let online: Bool
    public let os: String?
    public let arch: String?
    public let version: String?
    public let remoteAddr: String?
    public let attemptsLeft: Int?
    public let expiresAt: Date?
    public let lastSeenAt: Date?
    public let fullNode: Bool?

    enum CodingKeys: String, CodingKey {
        case id, name, state, expired, online, os, arch, version
        case remoteAddr = "remote_addr"
        case attemptsLeft = "attempts_left"
        case expiresAt = "expires_at"
        case lastSeenAt = "last_seen_at"
        case fullNode = "full_node"
    }

    public enum Group: Int, Sendable {
        case asking, approved, other
    }

    public var group: Group {
        switch state {
        case "pending" where !expired: .asking
        case "approved": .approved
        default: .other
        }
    }

    /// The machine's platform in a few words ("darwin arm64").
    public var platform: String? {
        let p = [os, arch].compactMap { $0 }.joined(separator: " ")
        return p.isEmpty ? nil : p
    }
}

/// The code a joining machine shows: six digits, typed with or without a space or dash.
public enum PairingCode {
    /// "123 456", "123-456" -> "123456"; anything else -> nil (as the web's
    /// `normalizePairingCode`).
    public static func normalize(_ raw: String) -> String? {
        let digits = raw.filter { $0 != " " && $0 != "-" }
        guard digits.count == 6, digits.allSatisfy({ $0.isASCII && $0.isNumber }) else { return nil }
        return digits
    }
}

/// Why approving a machine did not work, in the words a person acts on.
public enum ApproveFailure: Equatable, Sendable {
    /// The code is not the one the machine shows (nil: the home did not say the tries left).
    case wrongCode(attemptsLeft: Int?)
    /// The request is too old (or used its tries up): the machine must ask again.
    case expired
    case other(String)

    public init(_ error: Error) {
        guard let e = error as? APIError else {
            self = .other(HomeFailure(error).message())
            return
        }
        switch (e.problem?.code, e) {
        case ("wrong_code", _): self = .wrongCode(attemptsLeft: e.problem?.attemptsLeft)
        case ("expired", _), (_, .status(410, _, _)): self = .expired
        default: self = .other(e.message)
        }
    }

    public var message: String {
        switch self {
        case let .wrongCode(left?) where left > 0:
            "That is not the code the machine shows. \(left) \(left == 1 ? "try" : "tries") left."
        case .wrongCode(0): "Too many wrong codes: the request was turned down. Ask again from the machine."
        case .wrongCode: "That is not the code the machine shows."
        case .expired: "The request has expired. Ask again from the machine."
        case let .other(why): why
        }
    }
}

extension JSONDecoder {
    /// The home's times: RFC 3339, with or without fractions of a second (Go writes up to
    /// nine digits), in UTC or with an offset.
    static let home: JSONDecoder = {
        let d = JSONDecoder()
        d.dateDecodingStrategy = .custom { decoder in
            let s = try decoder.singleValueContainer().decode(String.self)
            guard let date = homeTime(s) else {
                throw DecodingError.dataCorrupted(.init(codingPath: decoder.codingPath, debugDescription: "not a time: \(s)"))
            }
            return date
        }
        return d
    }()
}

/// ISO8601DateFormatter is thread-safe once set up.
nonisolated(unsafe) private let withFractions: ISO8601DateFormatter = {
    let f = ISO8601DateFormatter()
    f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
    return f
}()

nonisolated(unsafe) private let wholeSeconds = ISO8601DateFormatter()

func homeTime(_ s: String) -> Date? {
    withFractions.date(from: s) ?? wholeSeconds.date(from: s)
}
