import Foundation
import Observation

/// Maktaba Cloud's status (Epic 50 story 50.11): `https://status.maktaba.tv/summary.json`,
/// read only when the app reaches its home through the cloud (the relay's address). Read at
/// start, every 5 minutes while in front, and at once when a request through the relay fails.
/// It never blocks: a failed read shows nothing. The status strip (53.06) draws its line after
/// the home's own.
public struct CloudSummary: Decodable, Equatable, Sendable {
    public struct Title: Decodable, Equatable, Sendable {
        public var en: String
        public var ar: String
        public init(en: String, ar: String) { self.en = en; self.ar = ar }
    }
    public struct Incident: Decodable, Equatable, Sendable {
        public var id: String
        public var state: String
        public var components: [String]?
        public var title: Title
        public var url: String
        public init(id: String, state: String, components: [String]?, title: Title, url: String) {
            self.id = id; self.state = state; self.components = components; self.title = title; self.url = url
        }
    }
    public struct Maintenance: Decodable, Equatable, Sendable {
        public var id: String?
        public var startsAt: String
        public var endsAt: String
        public var components: [String]
        public var title: Title
        public var url: String?
        enum CodingKeys: String, CodingKey {
            case id, components, title, url
            case startsAt = "starts_at"
            case endsAt = "ends_at"
        }
        public init(id: String?, startsAt: String, endsAt: String, components: [String], title: Title, url: String?) {
            self.id = id; self.startsAt = startsAt; self.endsAt = endsAt; self.components = components
            self.title = title; self.url = url
        }
    }

    public var components: [String: String]
    public var incident: Incident?
    public var maintenance: [Maintenance]

    enum CodingKeys: String, CodingKey { case components, incident, maintenance }

    public init(components: [String: String], incident: Incident? = nil, maintenance: [Maintenance] = []) {
        self.components = components
        self.incident = incident
        self.maintenance = maintenance
    }

    public init(from d: Decoder) throws {
        let c = try d.container(keyedBy: CodingKeys.self)
        components = try c.decode([String: String].self, forKey: .components)
        incident = try c.decodeIfPresent(Incident.self, forKey: .incident)
        maintenance = try c.decodeIfPresent([Maintenance].self, forKey: .maintenance) ?? []
    }
}

/// The cloud's line of the strip (story 50.11 §5).
public enum CloudStatusRules {
    public static let site = URL(string: "https://status.maktaba.tv")!
    /// The relay's public names: a home at `<slug>.relay.maktaba.tv` is reached through it.
    public static let relayDomain = "relay.maktaba.tv"
    public static let every: Duration = .seconds(300)

    /// The app reaches the home at `home` through Maktaba Cloud.
    public static func throughCloud(_ home: URL?) -> Bool {
        guard let host = home?.host?.lowercased() else { return false }
        return host == relayDomain || host.hasSuffix("." + relayDomain)
    }

    /// The feed's address, with the minute in it: GitHub Pages lets caches keep a copy for
    /// 10 minutes.
    public static func summaryURL(now: Date = Date()) -> URL {
        URL(string: "summary.json?t=\(Int(now.timeIntervalSince1970 / 60))", relativeTo: site)!.absoluteURL
    }

    /// The line, or nil: the relay or the API down or slow, or maintenance on either from 24
    /// hours before it starts until it ends; worst first. Notifications alone never show.
    /// `when` formats the maintenance window's date and times in the person's language.
    public static func line(_ s: CloudSummary?, now: Date = Date(),
                            when: (Date) -> (date: String, time: String) = CloudStatusRules.localWhen) -> StripLine? {
        guard let s else { return nil }
        func incident(_ comp: String) -> CloudSummary.Incident? {
            guard let i = s.incident, i.components?.contains(comp) ?? true else { return nil }
            return i
        }
        for state in ["outage", "degraded"] {
            for comp in ["relay", "api"] where s.components[comp] == state {
                let words: String
                switch (comp, state) {
                case ("relay", "outage"): words = StripWords.relayOutage
                case ("relay", _): words = StripWords.relayDegraded
                case ("api", "outage"): words = StripWords.apiOutage
                default: words = StripWords.apiDegraded
                }
                let inc = incident(comp)
                return StripLine(key: "cloud:\(inc?.id ?? "\(comp)-\(state)")", text: words, details: nil,
                                 args: [], link: inc.flatMap { URL(string: $0.url) } ?? site)
            }
        }
        let iso = ISO8601DateFormatter()
        let ahead: TimeInterval = 24 * 3600
        let next = s.maintenance
            .filter { $0.components.contains("relay") || $0.components.contains("api") }
            .compactMap { m -> (CloudSummary.Maintenance, Date, Date)? in
                guard let a = iso.date(from: m.startsAt), let b = iso.date(from: m.endsAt) else { return nil }
                return (m, a, b)
            }
            .filter { $0.1.addingTimeInterval(-ahead) <= now && now < $0.2 }
            .min { $0.1 < $1.1 }
        if let (m, start, end) = next {
            let a = when(start), b = when(end)
            return StripLine(key: "cloud:\(m.id ?? m.startsAt)", text: StripWords.maintenance, details: nil,
                             args: [a.date, a.time, b.time], link: m.url.flatMap { URL(string: $0) } ?? site)
        }
        return nil
    }

    /// The date and the time in the device's language and time zone.
    @Sendable public static func localWhen(_ d: Date) -> (date: String, time: String) {
        (d.formatted(date: .abbreviated, time: .omitted), d.formatted(date: .omitted, time: .shortened))
    }
}

/// Keeps the cloud's status fresh while the app is in front: every 5 minutes, and at once
/// when asked (a request through the relay failed).
@MainActor
@Observable
public final class CloudStatusWatcher {
    public private(set) var summary: CloudSummary?
    let session: URLSession

    public init(summary: CloudSummary? = nil, session: URLSession = .shared) {
        self.summary = summary
        self.session = session
    }

    /// Reads now; a failed read (no answer, a 404, a broken body) keeps what was known.
    public func refresh(now: Date = Date()) async {
        if let s = await Self.fetch(session, now: now) { summary = s }
    }

    nonisolated static func fetch(_ session: URLSession, now: Date) async -> CloudSummary? {
        var r = URLRequest(url: CloudStatusRules.summaryURL(now: now))
        r.timeoutInterval = 5
        r.cachePolicy = .reloadIgnoringLocalCacheData
        r.httpShouldHandleCookies = false
        guard let (data, resp) = try? await session.data(for: r),
              let http = resp as? HTTPURLResponse, http.statusCode == 200 else { return nil }
        return try? JSONDecoder().decode(CloudSummary.self, from: data)
    }

    /// Reads every 5 minutes until the task is cancelled (the app left the front).
    public func run(every: Duration = CloudStatusRules.every) async {
        while !Task.isCancelled {
            await refresh()
            try? await Task.sleep(for: every)
        }
    }
}
