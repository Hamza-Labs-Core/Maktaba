import Foundation
import Observation

/// The home's status (Epic 53 story 53.06): `/status.json`, answered by the supervisor even
/// when the API or the database is down (docs/api/status.md). The apps read it at start,
/// every 10 s while in front, and at once when a request fails with a server error; a failed
/// read changes nothing (an older home has no `/status.json`: no strip).
public struct HomeStatusDoc: Decodable, Equatable, Sendable {
    public var v: Int
    public var mode: String
    public var overall: String
    public var components: [String: String]
    public var featuresDown: [String]

    enum CodingKeys: String, CodingKey {
        case v, mode, overall, components
        case featuresDown = "features_down"
    }

    public init(v: Int = 1, mode: String = "normal", overall: String = "ok", components: [String: String] = [:],
                featuresDown: [String] = []) {
        self.v = v
        self.mode = mode
        self.overall = overall
        self.components = components
        self.featuresDown = featuresDown
    }

    public init(from d: Decoder) throws {
        let c = try d.container(keyedBy: CodingKeys.self)
        v = try c.decode(Int.self, forKey: .v)
        mode = try c.decodeIfPresent(String.self, forKey: .mode) ?? "normal"
        overall = try c.decodeIfPresent(String.self, forKey: .overall) ?? "ok"
        components = try c.decodeIfPresent([String: String].self, forKey: .components) ?? [:]
        featuresDown = try c.decodeIfPresent([String].self, forKey: .featuresDown) ?? []
    }
}

/// One line of the strip: a key (what it is about), its words (English: the catalog's key,
/// with its Arabic) and, for admins, where Details goes.
public struct StripLine: Equatable, Sendable, Identifiable {
    public let key: String
    public let text: String
    public let details: String?
    /// the words' values (the cloud's maintenance window), in order
    public var args: [String] = []
    /// an outside Details link for everyone (the cloud's status page, story 50.11)
    public var link: URL? = nil
    public var id: String { key }
}

/// The strip's lines for a status, highest priority first (story 53.06 §1): offline; the
/// home restarting or its database down; safe mode; a part a viewer feels (playback,
/// search); admin-only parts (processing, log search). A feature that is off is no outage:
/// an off group has no line, and Live TV is left out of the playback sentence when it is off.
public enum StatusStripRules {
    /// `cloud` is Maktaba Cloud's line (story 50.11, `CloudStatusRules.line`) when the app
    /// reaches the home through the cloud: after the home's own lines, before admin-only ones.
    public static func lines(_ s: HomeStatusDoc?, online: Bool, admin: Bool, liveTVOff: Bool = false,
                             cloud: StripLine? = nil) -> [StripLine] {
        var out: [StripLine] = []
        if !online { out.append(StripLine(key: "offline", text: StripWords.offline, details: nil)) }
        guard let s, s.v == 1 else { return out + (cloud.map { [$0] } ?? []) }
        func bad(_ g: String) -> Bool { ["degraded", "restarting", "down"].contains(s.components[g] ?? "ok") }
        func adminLink(_ link: String) -> String? { admin ? link : nil }
        if bad("database") {
            out.append(StripLine(key: "database", text: StripWords.database, details: adminLink("/recovery")))
        } else if s.overall == "down" || bad("api") {
            out.append(StripLine(key: "restarting", text: StripWords.restarting, details: adminLink("/recovery")))
        }
        if s.mode == "safe" { out.append(StripLine(key: "safe", text: StripWords.safe, details: adminLink("/recovery"))) }
        if bad("streaming") {
            out.append(StripLine(key: "streaming", text: liveTVOff ? StripWords.streamingNoTV : StripWords.streaming,
                                 details: adminLink("/admin/health")))
        }
        if bad("search") { out.append(StripLine(key: "search", text: StripWords.search, details: adminLink("/admin/health"))) }
        if let cloud { out.append(cloud) }
        if admin && bad("processing") {
            out.append(StripLine(key: "processing", text: StripWords.processing, details: "/admin/health"))
        }
        if admin && bad("logs") { out.append(StripLine(key: "logs", text: StripWords.logs, details: "/admin/health")) }
        return out
    }

    /// What the strip shows, so a closed strip comes back when it changes.
    public static func signature(_ lines: [StripLine]) -> String {
        lines.map { "\($0.key):\($0.text)\($0.args.isEmpty ? "" : $0.args.joined(separator: ","))" }.joined(separator: "|")
    }

    /// Whether playback is down on the home: the player's error screen says so instead of a
    /// generic error.
    public static func playbackDown(_ s: HomeStatusDoc?) -> Bool {
        ["degraded", "restarting", "down"].contains(s?.components["streaming"] ?? "ok")
    }
}

/// The strip's words: the web's English (web/src/i18n/en.json `status.*`), the catalog's
/// keys; their Arabic is the web's ar.json, in Localizable.xcstrings.
public enum StripWords {
    public static let offline = "You're offline"
    public static let restarting = "Maktaba is restarting. Some things won't work for a moment."
    public static let database = "Maktaba is having trouble with its database. Playback and browsing may not work."
    public static let safe = "Maktaba is in safe mode. Playback, processing and Live TV are off until it restarts."
    public static let streaming = "Playback and Live TV are unavailable right now."
    public static let streamingNoTV = "Playback is unavailable right now."
    public static let search = "Search is unavailable right now."
    public static let processing = "Processing is paused: new videos won't be added for now."
    public static let logs = "Log search is restarting. Recent logs can still be read from the recovery page."
    public static let details = "Details"
    public static let more = "%lld more"
    public static let close = "Close"
    public static let player = "Playback is unavailable right now. Try again in a few minutes."
    // Maktaba Cloud's lines (Epic 50 story 50.11), the web's `banner.*`
    public static let relayDegraded = "Remote access is slower than usual. Playback at home is not affected."
    public static let relayOutage = "Remote access is down. We're working on it. Playback at home still works."
    public static let apiDegraded = "Signing in with Maktaba Cloud is slower than usual."
    public static let apiOutage =
        "Signing in with Maktaba Cloud isn't working right now. You can sign in to your home with its password."
    public static let maintenance = "Planned maintenance on %1$@, %2$@–%3$@. Remote play may pause for a few seconds."

    /// `english` in the person's language (MaktabaKit's catalog), for an app's own view.
    public static func localized(_ english: String) -> String {
        String(localized: String.LocalizationValue(english), bundle: .module)
    }

    /// Every line's words (the catalog test checks each has its Arabic).
    public static let all = [offline, restarting, database, safe, streaming, streamingNoTV, search, processing, logs,
                             details, more, close, player, relayDegraded, relayOutage, apiDegraded, apiOutage,
                             maintenance]
}

extension APIClient {
    /// The home's `/status.json`; nil when it does not answer it (an older home, the home
    /// unreachable): the strip then shows nothing new.
    public func homeStatus() async -> HomeStatusDoc? {
        var r = request("GET", "/status.json")
        r.timeoutInterval = 4
        r.cachePolicy = .reloadIgnoringLocalCacheData
        guard let (data, resp) = try? await session.data(for: r),
              let http = resp as? HTTPURLResponse, http.statusCode == 200 else { return nil }
        return try? JSONDecoder().decode(HomeStatusDoc.self, from: data)
    }
}

/// Keeps the home's status fresh while the app is in front: every 10 s, and at once when
/// asked (a request failed with a server error).
@MainActor
@Observable
public final class HomeStatusWatcher {
    public private(set) var status: HomeStatusDoc?
    /// The strip state closed by the person ("" when none): it shows again once it changes.
    public var closed = ""

    public init(status: HomeStatusDoc? = nil) { self.status = status }

    /// Reads now; a failed read keeps what was known.
    public func refresh(_ client: APIClient) async {
        if let s = await client.homeStatus() { status = s }
    }

    /// Reads every `every` until the task is cancelled (the app left the front).
    public func run(_ client: APIClient, every: Duration = .seconds(10)) async {
        while !Task.isCancelled {
            await refresh(client)
            try? await Task.sleep(for: every)
        }
    }
}
