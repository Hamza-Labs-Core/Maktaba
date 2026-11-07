import Foundation

// The home's diagnostics as the Remote reads them (Epic 53 stories 53.10 to 53.12, 53.15):
// error groups and their status, the log index's search and "Follow a request", the Overview
// and Storage dashboards, and the problem-report inbox. The routes are the web's
// (web/src/lib/logIndex.ts, dashboards.ts, problemReports.ts); every admin route is refused
// to anyone else by the home (403), whatever an app shows.

// MARK: error groups (53.10)

/// One error group: the lines of one fingerprint (`GET /api/admin/errors`).
public struct ErrorGroup: Decodable, Identifiable, Hashable, Sendable {
    public var id: String { fp }
    public let fp: String
    public let service: String
    public let component: String?
    public let code: String?
    public let title: String
    public let firstSeen: Date?
    public let lastSeen: Date?
    public let count: Int
    public let count24h: Int
    public let users: Int
    /// new, seen, resolved or muted
    public let status: String
    public let resolvedIn: String?
    public let mutedUntil: Date?
    public let lastVersion: String?
    public let lastRequestID: String?
    /// The last 14 days' counts, oldest first.
    public let days: [Int]

    enum CodingKeys: String, CodingKey {
        case fp, service, component, code, title, count, users, status, days
        case firstSeen = "first_seen", lastSeen = "last_seen", count24h = "count_24h"
        case resolvedIn = "resolved_in", mutedUntil = "muted_until", lastVersion = "last_version"
        case lastRequestID = "last_request_id"
    }

    public init(from d: Decoder) throws {
        let c = try d.container(keyedBy: CodingKeys.self)
        fp = try c.decode(String.self, forKey: .fp)
        service = try c.decodeIfPresent(String.self, forKey: .service) ?? ""
        component = try c.decodeIfPresent(String.self, forKey: .component).flatMap { $0.isEmpty ? nil : $0 }
        code = try c.decodeIfPresent(String.self, forKey: .code)
        title = try c.decodeIfPresent(String.self, forKey: .title) ?? fp
        firstSeen = (try? c.decodeIfPresent(String.self, forKey: .firstSeen)).flatMap { $0.flatMap(homeTime) }
        lastSeen = (try? c.decodeIfPresent(String.self, forKey: .lastSeen)).flatMap { $0.flatMap(homeTime) }
        count = try c.decodeIfPresent(Int.self, forKey: .count) ?? 0
        count24h = try c.decodeIfPresent(Int.self, forKey: .count24h) ?? 0
        users = try c.decodeIfPresent(Int.self, forKey: .users) ?? 0
        status = try c.decodeIfPresent(String.self, forKey: .status) ?? "new"
        resolvedIn = try c.decodeIfPresent(String.self, forKey: .resolvedIn)
        mutedUntil = (try? c.decodeIfPresent(String.self, forKey: .mutedUntil)).flatMap { $0.flatMap(homeTime) }
        lastVersion = try c.decodeIfPresent(String.self, forKey: .lastVersion)
        lastRequestID = try c.decodeIfPresent(String.self, forKey: .lastRequestID)
        days = try c.decodeIfPresent([Int].self, forKey: .days) ?? []
    }
}

/// The list's status filter: new and seen (the default, as the web's), or one status.
public enum ErrorFilter: String, CaseIterable, Hashable, Sendable {
    case open, resolved, muted, all

    /// The `status` the home's list takes.
    public var query: String? {
        switch self {
        case .open: "new,seen"
        case .resolved: "resolved"
        case .muted: "muted"
        case .all: nil
        }
    }
}

/// The list's order.
public enum ErrorSort: String, CaseIterable, Hashable, Sendable {
    case lastSeen = "last_seen", count, users
}

/// What the admin does to a group.
public enum ErrorGroupAction: Hashable, Sendable {
    case seen
    case resolve(version: String?)
    /// muted for 24 hours, 7 days, or until it is resolved (nil)
    case mute(until: Date?)

    var body: [String: Any] {
        switch self {
        case .seen: return ["status": "seen"]
        case let .resolve(v):
            var b: [String: Any] = ["status": "resolved"]
            if let v, !v.isEmpty { b["version"] = v }
            return b
        case let .mute(until):
            var b: [String: Any] = ["status": "muted"]
            if let until { b["muted_until"] = LogRing.timestamp(until) }
            return b
        }
    }
}

/// `GET /api/admin/errors/{fp}`: the group, its hours, its recent lines.
public struct ErrorGroupDetail: Decodable, Sendable {
    public let group: ErrorGroup
    public let hours: [String: Int]
    public let samples: LogSearch

    enum CodingKeys: String, CodingKey { case group, hours, samples }

    public init(from d: Decoder) throws {
        let c = try d.container(keyedBy: CodingKeys.self)
        group = try c.decode(ErrorGroup.self, forKey: .group)
        hours = try c.decodeIfPresent([String: Int].self, forKey: .hours) ?? [:]
        samples = try c.decodeIfPresent(LogSearch.self, forKey: .samples) ?? LogSearch(lines: [], ids: [])
    }

    /// The hours as (time, count), oldest first. A key is a unix hour or a time.
    public var hourly: [(at: Date, count: Int)] {
        hours.compactMap { k, v -> (Date, Int)? in
            if let n = Double(k) { return (Date(timeIntervalSince1970: n < 10_000_000 ? n * 3600 : n), v) }
            return homeTime(k).map { ($0, v) }
        }
        .sorted { $0.0 < $1.0 }
        .map { (at: $0.0, count: $0.1) }
    }

    /// The releases and machines its lines came from.
    public var versions: [String] { Array(Set(samples.lines.compactMap(\.version))).sorted() }
    public var machines: [String] { Array(Set(samples.lines.compactMap(\.node))).sorted() }
}

// MARK: the log index (53.09)

/// One line as the shared schema writes it (story 53.01). Every other field is kept, as
/// text, for the line's page.
public struct LogLine: Decodable, Hashable, Sendable {
    public let ts: Date?
    public let level: String
    public let service: String
    public let component: String?
    public let node: String?
    public let msg: String
    public let requestID: String?
    public let version: String?
    public let userH: String?
    /// Every field of the line, as the home wrote it (objects as JSON), by name.
    public let fields: [String: String]

    struct Key: CodingKey {
        var stringValue: String
        var intValue: Int? { nil }
        init(stringValue: String) { self.stringValue = stringValue }
        init?(intValue: Int) { nil }
    }

    public init(from d: Decoder) throws {
        let c = try d.container(keyedBy: Key.self)
        var f: [String: String] = [:]
        for k in c.allKeys {
            if let s = try? c.decode(String.self, forKey: k) {
                f[k.stringValue] = s
            } else if let n = try? c.decode(Double.self, forKey: k) {
                f[k.stringValue] = n.rounded() == n && abs(n) < 1e15 ? String(Int64(n)) : String(n)
            } else if let b = try? c.decode(Bool.self, forKey: k) {
                f[k.stringValue] = b ? "true" : "false"
            } else if let j = try? c.decode(JSONValue.self, forKey: k) {
                f[k.stringValue] = j.text
            }
        }
        fields = f
        ts = f["ts"].flatMap(homeTime)
        level = f["level"] ?? "info"
        service = f["service"] ?? ""
        component = f["component"]
        node = f["node"]
        msg = f["msg"] ?? ""
        requestID = f["request_id"]
        version = f["version"] ?? f["app_version"]
        userH = f["user_h"]
    }

    /// Whether the line is a warning or worse.
    public var isWarning: Bool { ["warn", "error", "fatal"].contains(level) }
    public var isError: Bool { ["error", "fatal"].contains(level) }
}

/// Any JSON value, kept to be shown as text.
indirect enum JSONValue: Decodable, Hashable, Sendable {
    case string(String), number(Double), bool(Bool), null
    case array([JSONValue]), object([String: JSONValue])

    init(from d: Decoder) throws {
        let c = try d.singleValueContainer()
        if c.decodeNil() { self = .null } else if let b = try? c.decode(Bool.self) { self = .bool(b) } else if let n = try? c.decode(Double.self) {
            self = .number(n)
        } else if let s = try? c.decode(String.self) { self = .string(s) } else if let a = try? c.decode([JSONValue].self) {
            self = .array(a)
        } else { self = .object(try c.decode([String: JSONValue].self)) }
    }

    var text: String {
        switch self {
        case let .string(s): return "\"\(s)\""
        case let .number(n): return n.rounded() == n && abs(n) < 1e15 ? String(Int64(n)) : String(n)
        case let .bool(b): return b ? "true" : "false"
        case .null: return "null"
        case let .array(a): return "[" + a.map(\.text).joined(separator: ",") + "]"
        case let .object(o): return "{" + o.keys.sorted().map { "\"\($0)\":\(o[$0]!.text)" }.joined(separator: ",") + "}"
        }
    }
}

/// `GET /api/admin/logs/search`: lines, newest first, with each line's id (for "Lines around
/// this") and the cursor of the next page.
public struct LogSearch: Decodable, Sendable {
    public let lines: [LogLine]
    public let ids: [Int64]
    public let cursor: String?
    public let partial: Bool

    public init(lines: [LogLine], ids: [Int64], cursor: String? = nil, partial: Bool = false) {
        self.lines = lines
        self.ids = ids
        self.cursor = cursor
        self.partial = partial
    }

    enum CodingKeys: String, CodingKey { case lines, ids, cursor, partial }

    public init(from d: Decoder) throws {
        let c = try d.container(keyedBy: CodingKeys.self)
        lines = try c.decodeIfPresent([LogLine].self, forKey: .lines) ?? []
        ids = try c.decodeIfPresent([Int64].self, forKey: .ids) ?? []
        cursor = try c.decodeIfPresent(String.self, forKey: .cursor)
        partial = try c.decodeIfPresent(Bool.self, forKey: .partial) ?? false
    }

    /// Each line with its id (nil when the home sent fewer ids).
    public var rows: [(id: Int64?, line: LogLine)] {
        lines.enumerated().map { (i, l) in (i < ids.count ? ids[i] : nil, l) }
    }
}

/// What the Logs screen asks for.
public struct LogQuery: Hashable, Sendable {
    public var text = ""
    /// at least this level ("" for all)
    public var level = ""
    public var window: LogWindow = .day
    public var requestID: String?

    public init(text: String = "", level: String = "", window: LogWindow = .day, requestID: String? = nil) {
        self.text = text
        self.level = level
        self.window = window
        self.requestID = requestID
    }

    /// A reference (the first 8 characters an error screen shows) or a whole request id: the
    /// search box then follows that request.
    public static func requestReference(_ s: String) -> String? {
        let v = s.trimmingCharacters(in: .whitespaces).lowercased()
        let hex = CharacterSet(charactersIn: "0123456789abcdef")
        if v.count == 8, v.unicodeScalars.allSatisfy(hex.contains) { return v }
        let parts = v.split(separator: "-", omittingEmptySubsequences: false)
        if parts.map(\.count) == [8, 4, 4, 4, 12], parts.allSatisfy({ $0.unicodeScalars.allSatisfy(hex.contains) }) { return v }
        return nil
    }

    func items(now: Date, cursor: String?) -> [String: String] {
        var q: [String: String] = [:]
        if let r = requestID {
            q["request_id"] = r
        } else {
            let t = text.trimmingCharacters(in: .whitespaces)
            if !t.isEmpty { q["q"] = t }
            q["since"] = LogRing.timestamp(now.addingTimeInterval(-window.seconds))
        }
        if !level.isEmpty { q["level"] = level }
        if let cursor { q["cursor"] = cursor }
        return q
    }
}

/// The Logs screen's time choices (7 days: the default retention).
public enum LogWindow: String, CaseIterable, Hashable, Sendable {
    case quarter = "15m", hour = "1h", day = "24h", week = "7d"

    public var seconds: TimeInterval {
        switch self {
        case .quarter: 15 * 60
        case .hour: 3600
        case .day: 86400
        case .week: 7 * 86400
        }
    }
}

/// Where each line is more than a second after the one before it (Follow a request).
public func gapsOverASecond(_ lines: [LogLine]) -> [Bool] {
    lines.indices.map { i in
        guard i > 0, let a = lines[i - 1].ts, let b = lines[i].ts else { return false }
        return b.timeIntervalSince(a) > 1
    }
}

// MARK: dashboards (53.12)

/// The boards the Remote shows (read only): Overview and Storage.
public enum Board: String, CaseIterable, Hashable, Sendable {
    case overview, storage
}

/// The ranges a board takes.
public enum BoardRange: String, CaseIterable, Hashable, Sendable {
    case hour = "1h", day = "24h", week = "7d", month = "30d", quarter = "90d"
}

/// `GET /api/admin/dashboards/{board}?range=`: the board's series in one call.
public struct BoardDoc: Decodable, Sendable {
    public struct Series: Decodable, Hashable, Sendable {
        public let name: String
        public let labels: String?
        public let kind: String
        public let points: [Point]

        enum CodingKeys: String, CodingKey { case name, labels, kind, points }

        public init(from d: Decoder) throws {
            let c = try d.container(keyedBy: CodingKeys.self)
            name = try c.decode(String.self, forKey: .name)
            labels = try c.decodeIfPresent(String.self, forKey: .labels).flatMap { $0.isEmpty ? nil : $0 }
            kind = try c.decodeIfPresent(String.self, forKey: .kind) ?? "gauge"
            let raw = try c.decodeIfPresent([[Double]].self, forKey: .points) ?? []
            points = raw.compactMap { p in p.count >= 2 ? Point(at: Date(timeIntervalSince1970: p[0] > 1e11 ? p[0] / 1000 : p[0]), value: p[1]) : nil }
        }

        /// The legend: its labels' values ("core.api · home").
        public var legend: String {
            guard let labels else { return "" }
            return labels.split(separator: ",").compactMap { kv in
                let v = kv.split(separator: "=", maxSplits: 1).dropFirst().first.map(String.init) ?? ""
                return v.isEmpty ? nil : v
            }.joined(separator: " · ")
        }
    }

    public struct Point: Hashable, Sendable {
        public let at: Date
        public let value: Double
    }

    public let board: String
    public let range: String
    public let series: [Series]

    enum CodingKeys: String, CodingKey { case board, range, series }

    public init(from d: Decoder) throws {
        let c = try d.container(keyedBy: CodingKeys.self)
        board = try c.decodeIfPresent(String.self, forKey: .board) ?? ""
        range = try c.decodeIfPresent(String.self, forKey: .range) ?? ""
        series = try c.decodeIfPresent([Series].self, forKey: .series) ?? []
    }

    /// One panel per metric name, in the board's order.
    public var panels: [(name: String, series: [Series])] {
        var out: [(name: String, series: [Series])] = []
        for s in series {
            if let i = out.firstIndex(where: { $0.name == s.name }) { out[i].series.append(s) } else { out.append((s.name, [s])) }
        }
        return out
    }
}

// MARK: problem reports (53.15)

public enum ReportCategory: String, CaseIterable, Hashable, Sendable {
    case playback, liveTV = "live_tv", search, signingIn = "signing_in", other
}

public enum ReportStatus: String, CaseIterable, Hashable, Sendable {
    case new, looking, fixed, closed
}

/// A report in a list (the admin's inbox, or a person's own).
public struct ReportSummary: Decodable, Identifiable, Hashable, Sendable {
    public let id: Int
    public let at: Date?
    public let name: String?
    public let app: String
    public let category: String
    public let status: String
    public let sharedWithMaktaba: Bool
    public let reply: String?

    enum CodingKeys: String, CodingKey {
        case id, at, name, app, category, status, reply
        case sharedWithMaktaba = "shared_with_maktaba"
    }

    public init(from d: Decoder) throws {
        let c = try d.container(keyedBy: CodingKeys.self)
        id = try c.decode(Int.self, forKey: .id)
        at = (try? c.decodeIfPresent(String.self, forKey: .at)).flatMap { $0.flatMap(homeTime) }
        name = try c.decodeIfPresent(String.self, forKey: .name)
        app = try c.decodeIfPresent(String.self, forKey: .app) ?? ""
        category = try c.decodeIfPresent(String.self, forKey: .category) ?? "other"
        status = try c.decodeIfPresent(String.self, forKey: .status) ?? "new"
        sharedWithMaktaba = try c.decodeIfPresent(Bool.self, forKey: .sharedWithMaktaba) ?? false
        reply = try c.decodeIfPresent(String.self, forKey: .reply)
    }
}

/// One report as an admin reads it: the device, the text, the app's lines and the home's.
public struct ReportDoc: Decodable, Sendable {
    public let summary: ReportSummary
    public let when: Date?
    public let version: String
    public let platform: String
    public let osVersion: String?
    public let device: String?
    public let text: String?
    public let screenshot: Bool
    public let requestIDs: [String]
    public let lines: [LogLine]
    public let server: [LogLine]
    public let crashes: Int

    enum CodingKeys: String, CodingKey {
        case when, version, platform, text, screenshot, lines, server, crashes
        case osVersion = "os_version", device = "device_model", requestIDs = "request_ids"
    }

    public init(from d: Decoder) throws {
        summary = try ReportSummary(from: d)
        let c = try d.container(keyedBy: CodingKeys.self)
        when = (try? c.decodeIfPresent(String.self, forKey: .when)).flatMap { $0.flatMap(homeTime) }
        version = try c.decodeIfPresent(String.self, forKey: .version) ?? ""
        platform = try c.decodeIfPresent(String.self, forKey: .platform) ?? ""
        osVersion = try c.decodeIfPresent(String.self, forKey: .osVersion)
        device = try c.decodeIfPresent(String.self, forKey: .device)
        text = try c.decodeIfPresent(String.self, forKey: .text)
        screenshot = try c.decodeIfPresent(Bool.self, forKey: .screenshot) ?? false
        requestIDs = try c.decodeIfPresent([String].self, forKey: .requestIDs) ?? []
        lines = (try? c.decodeIfPresent([LogLine].self, forKey: .lines)) ?? []
        server = (try? c.decodeIfPresent([LogLine].self, forKey: .server)) ?? []
        crashes = ((try? c.decodeIfPresent([JSONValue].self, forKey: .crashes)) ?? nil)?.count ?? 0
    }

    /// The app's lines and the home's merged by time, oldest first.
    public var timeline: [(fromHome: Bool, line: LogLine)] {
        (lines.map { (false, $0) } + server.map { (true, $0) })
            .sorted { ($0.1.ts ?? .distantPast) < ($1.1.ts ?? .distantPast) }
            .map { (fromHome: $0.0, line: $0.1) }
    }
}

struct ErrorGroups: Decodable { let groups: [ErrorGroup]? }
struct ReportList: Decodable { let reports: [ReportSummary]? }
struct UserNames: Decodable { let users: [String: String]? }
struct ReportNumber: Decodable { let id: Int }

extension APIClient {
    // MARK: errors

    public func errorGroups(_ filter: ErrorFilter = .open, sort: ErrorSort = .lastSeen) async throws -> [ErrorGroup] {
        var q = ["sort": sort.rawValue]
        if let s = filter.query { q["status"] = s }
        let r: ErrorGroups = try await get("/api/admin/errors", query: q)
        return r.groups ?? []
    }

    public func errorGroup(_ fp: String) async throws -> ErrorGroupDetail {
        try await get("/api/admin/errors/\(Self.segment(fp))")
    }

    public func setErrorStatus(_ fp: String, _ action: ErrorGroupAction) async throws {
        _ = try await sendRaw(request("POST", "/api/admin/errors/\(Self.segment(fp))/status", body: action.body))
    }

    // MARK: logs

    /// A search of the log index (503 `log_index_unavailable` while the logs role is off or
    /// restarting).
    public func searchLogs(_ q: LogQuery, cursor: String? = nil, now: Date = Date()) async throws -> LogSearch {
        try await get("/api/admin/logs/search", query: q.items(now: now, cursor: cursor))
    }

    /// Every service's lines around one line.
    public func linesAround(_ id: Int64) async throws -> LogSearch {
        try await get("/api/admin/logs/around", query: ["id": String(id), "before": "25", "after": "25"])
    }

    /// The people the lines name, by `user_h`.
    public func logUserNames() async throws -> [String: String] {
        let r: UserNames = try await get("/api/admin/logs/users")
        return r.users ?? [:]
    }

    // MARK: dashboards

    public func board(_ board: Board, range: BoardRange) async throws -> BoardDoc {
        try await get("/api/admin/dashboards/\(board.rawValue)", query: ["range": range.rawValue])
    }

    // MARK: problem reports

    /// The admin's inbox, or a viewer's own reports.
    public func problemReports() async throws -> [ReportSummary] {
        let r: ReportList = try await get("/api/reports")
        return r.reports ?? []
    }

    public func problemReport(_ id: Int) async throws -> ReportDoc {
        try await get("/api/reports/\(id)")
    }

    public func setReportStatus(_ id: Int, _ status: ReportStatus) async throws {
        _ = try await sendRaw(request("POST", "/api/reports/\(id)/status", body: ["status": status.rawValue]))
    }

    public func replyToReport(_ id: Int, _ reply: String) async throws {
        _ = try await sendRaw(request("POST", "/api/reports/\(id)/reply", body: ["reply": reply]))
    }

    /// Sends a problem report (`ReportProblem.multipart`); answers its number.
    public func sendProblemReport(_ body: Data, boundary: String) async throws -> Int {
        var r = request("POST", "/api/reports")
        r.httpBody = body
        r.setValue("multipart/form-data; boundary=\(boundary)", forHTTPHeaderField: "Content-Type")
        let n: ReportNumber = try await send(r)
        return n.id
    }

    // MARK: crash reports (53.14)

    /// Sends one crash envelope. The home answers 200, 403 `crash_reports_off`, or 429 with
    /// `Retry-After`.
    public func sendCrashEnvelope(_ envelope: Data) async throws {
        var r = request("POST", "/api/diagnostics/envelope")
        r.httpBody = envelope
        r.setValue("application/x-sentry-envelope", forHTTPHeaderField: "Content-Type")
        _ = try await sendRaw(r)
    }
}
