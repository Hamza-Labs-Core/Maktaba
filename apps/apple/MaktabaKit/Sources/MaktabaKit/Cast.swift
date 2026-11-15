import Foundation

// Google Cast senders (Epic 58 story 58.06, docs/developers/contributing/casting.md): the
// Player casts to Maktaba's receiver (58.07) with a cast grant the home mints for the
// person, never with the person's own token. Everything here is plain logic, the same rules
// as the Kotlin and TypeScript senders (`shared/cast/load-vectors.json`); the Google Cast SDK
// itself stays in the Player (iOS and iPadOS only) behind a protocol.

/// The receiver this build casts to: `shared/cast/app.json`'s `receiver_app_id`, carried by
/// the build (`MAKTABA_CAST_APP_ID`, Info.plist `MaktabaCastReceiverAppID`). Public, never a
/// secret. Empty: no receiver is registered, so Cast is hidden and the SDK never starts.
public struct CastConfig: Equatable, Sendable {
    /// The Info.plist key the Player's build writes the id under.
    public static let infoKey = "MaktabaCastReceiverAppID"
    /// The custom namespace sender and receiver talk on.
    public static let namespace = "urn:x-cast:tv.maktaba.cast"

    public let receiverAppID: String

    public init(receiverAppID: String) {
        // an unexpanded build setting is no id
        let id = receiverAppID.trimmingCharacters(in: .whitespacesAndNewlines)
        self.receiverAppID = id.hasPrefix("$(") ? "" : id
    }

    /// The id in `bundle`'s Info.plist (none: empty).
    public init(bundle: Bundle = .main) {
        self.init(receiverAppID: bundle.object(forInfoDictionaryKey: Self.infoKey) as? String ?? "")
    }

    /// Whether a Cast button may show at all.
    public var isEnabled: Bool { !receiverAppID.isEmpty }

    /// The Bonjour services the Cast SDK browses: what Info.plist's `NSBonjourServices` must
    /// list for it (none without a receiver).
    public var bonjourServices: [String] {
        isEnabled ? ["_googlecast._tcp", "_\(receiverAppID)._googlecast._tcp"] : []
    }
}

/// What a cast plays: one video, or a server play queue (Play all, Shuffle).
public enum CastItem: Equatable, Sendable {
    case video(String)
    case queue(String, position: Int?)

    var body: [String: Any] {
        switch self {
        case let .video(id): ["video_id": id]
        case let .queue(id, _): ["queue_id": id]
        }
    }
}

/// `MediaInfo.customData` of a cast load: one pure function, run against the shared vectors.
public enum CastCustomData {
    /// The custom data for a load, or nil when no address the TV can reach is known (then
    /// Cast is hidden for this home). `home` is the first HTTPS address that is not
    /// localhost, a `.local` name or a bare IP, from `discoveryURLs` then `senderBase`;
    /// `urls` keeps those addresses in order, each once, without a trailing slash. The grant
    /// and the address go here only, never in a header.
    public static func build(senderBase: URL?, discoveryURLs: [String], grant: String, item: CastItem,
                             startSec: Double, lang: String, apiLevel: Int) -> [String: Any]? {
        let urls = reachable(discoveryURLs + [senderBase?.absoluteString].compactMap { $0 })
        guard let home = urls.first else { return nil }
        var out: [String: Any] = [
            "home": home,
            "urls": urls,
            "grant": grant,
            "start_sec": startSec.isFinite ? max(0, Int(startSec.rounded(.down))) : 0,
            "lang": language(lang),
            "api_level": apiLevel,
        ]
        switch item {
        case let .video(id):
            out["video_id"] = id
        case let .queue(id, position):
            out["queue_id"] = id
            if let position, position >= 0 { out["queue_pos"] = position }
        }
        return out
    }

    /// Whether a TV can reach this home at all (a castable address is known).
    public static func castable(senderBase: URL?, discoveryURLs: [String]) -> Bool {
        !reachable(discoveryURLs + [senderBase?.absoluteString].compactMap { $0 }).isEmpty
    }

    /// The receiver speaks English or Arabic: anything else is English.
    public static func language(_ lang: String) -> String {
        let l = lang.lowercased()
        return l == "ar" || l.hasPrefix("ar-") || l.hasPrefix("ar_") ? "ar" : "en"
    }

    /// The addresses a receiver page (HTTPS, on the TV) can use, normalized, each once.
    static func reachable(_ candidates: [String]) -> [String] {
        var seen = Set<String>(), out: [String] = []
        for c in candidates {
            guard let n = normalized(c), seen.insert(n).inserted else { continue }
            out.append(n)
        }
        return out
    }

    /// `scheme://host[:port][/path]` without a trailing slash, query or fragment; nil when the
    /// address is not HTTPS, or is localhost, a `.local` name or a bare IP.
    static func normalized(_ s: String) -> String? {
        guard let c = URLComponents(string: s.trimmingCharacters(in: .whitespaces)),
              c.scheme?.lowercased() == "https",
              var host = c.host?.lowercased(), !host.isEmpty else { return nil }
        if host.hasPrefix("["), host.hasSuffix("]") { host = String(host.dropFirst().dropLast()) }
        if host.hasSuffix(".") { host.removeLast() }
        if host == "localhost" || host.hasSuffix(".localhost") || host == "local" || host.hasSuffix(".local") { return nil }
        if host.contains(":") || isIPv4(host) { return nil }
        var path = c.percentEncodedPath
        while path.hasSuffix("/") { path.removeLast() }
        let port = c.port.map { ":\($0)" } ?? ""
        return "https://\(host)\(port)\(path)"
    }

    static func isIPv4(_ host: String) -> Bool {
        let parts = host.split(separator: ".", omittingEmptySubsequences: false)
        return parts.count == 4 && parts.allSatisfy { !$0.isEmpty && $0.allSatisfy(\.isNumber) }
    }
}

/// `POST /api/cast/grants`'s answer.
public struct CastGrant: Decodable, Equatable, Sendable {
    public let grant: String
    public let scope: String
    public let expiresAt: Date?
    public let videoID: String?
    public let queueID: String?

    enum CodingKeys: String, CodingKey {
        case grant, scope
        case expiresAt = "expires_at", videoID = "video_id", queueID = "queue_id"
    }

    public init(grant: String, scope: String = "cast", expiresAt: Date?, videoID: String? = nil, queueID: String? = nil) {
        self.grant = grant
        self.scope = scope
        self.expiresAt = expiresAt
        self.videoID = videoID
        self.queueID = queueID
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        grant = try c.decode(String.self, forKey: .grant)
        scope = try c.decodeIfPresent(String.self, forKey: .scope) ?? "cast"
        expiresAt = try c.decodeIfPresent(String.self, forKey: .expiresAt).flatMap(homeTime)
        videoID = try c.decodeIfPresent(String.self, forKey: .videoID)
        queueID = try c.decodeIfPresent(String.self, forKey: .queueID)
    }

    /// When the sender mints the next grant while the cast plays: 30 minutes before this one
    /// ends (at once when that is already past; never, when the home gave no end).
    public func refreshAt(lead: TimeInterval = 30 * 60) -> Date? {
        expiresAt.map { $0.addingTimeInterval(-lead) }
    }
}

/// Why the home would not let this cast happen. Each is shown; none casts.
public enum CastRefusal: Equatable, Sendable {
    /// 403 `cast_via_cloud`: a sign-in through Maktaba Cloud cannot mint a grant.
    case viaCloud
    /// 403 `cast_needs_person`: the admin token, no person.
    case needsPerson
    /// 404 (a problem): no such video or queue, or above a restricted account's limit.
    case notFound
    /// 403 `access-denied` or any other refusal.
    case denied
}

/// What minting a grant came to.
public enum CastMint: Equatable, Sendable {
    case granted(CastGrant)
    /// The home has no cast grant route (it predates it): cast the plain signed URL.
    case oldHome
    case refused(CastRefusal)

    /// Reads the home's refusal of `POST /api/cast/grants`: nil for an answer that is no
    /// refusal of the cast itself (a 5xx, a 422), which the caller shows as an error.
    public static func refusal(status: Int, body: String) -> CastMint? {
        let problem = Problem.parse(body)
        let isProblem = (try? JSONSerialization.jsonObject(with: Data(body.utf8))) is [String: Any]
        switch status {
        // a router's plain 404 (or 405) for the route itself: a home older than the grant
        case 404 where !isProblem, 405, 501: return .oldHome
        case 404: return .refused(.notFound)
        case 403:
            switch problem?.code {
            case "cast_via_cloud": return .refused(.viaCloud)
            case "cast_needs_person": return .refused(.needsPerson)
            default: return .refused(.denied)
            }
        default: return nil
        }
    }
}

public extension APIClient {
    /// `GET /api/discovery/home`: the home's addresses and level, read again at cast time.
    func discovery() async throws -> HomeDiscovery {
        try await send(request("GET", "/api/discovery/home"))
    }

    /// `POST /api/cast/grants` with the person's own token: a grant for `item`, or why not.
    /// Other failures (no answer, a 5xx) throw.
    func mintCastGrant(_ item: CastItem) async throws -> CastMint {
        do {
            let g: CastGrant = try await send(request("POST", "/api/cast/grants", body: item.body),
                                              redirects: SameOriginOnly.shared)
            return .granted(g)
        } catch let APIError.status(code, body, retryAfter) {
            if let m = CastMint.refusal(status: code, body: body) { return m }
            throw APIError.status(code, body, retryAfter: retryAfter)
        }
    }
}

/// The custom namespace's messages (`urn:x-cast:tv.maktaba.cast`).
public enum CastMessage: Equatable, Sendable {
    /// sender → receiver: a fresh grant for the cast that plays.
    case grant(String)
    /// receiver → senders: what the TV is doing (`loading`, `ready`, `playing`, `paused`,
    /// `idle`), with the item when it said.
    case state(String, videoID: String?, queuePos: Int?)
    /// receiver → senders: the screen the TV shows (`home`, `format`, `grant`, `update`).
    case error(String)

    /// The message as the namespace's text.
    public var text: String {
        var o: [String: Any]
        switch self {
        case let .grant(g): o = ["type": "grant", "grant": g]
        case let .state(s, v, p):
            o = ["type": "state", "state": s]
            if let v { o["video_id"] = v }
            if let p { o["queue_pos"] = p }
        case let .error(e): o = ["type": "error", "error": e]
        }
        let data = (try? JSONSerialization.data(withJSONObject: o, options: [.sortedKeys])) ?? Data()
        return String(decoding: data, as: UTF8.self)
    }

    /// A message the receiver sent (nil: not one of ours).
    public init?(text: String) {
        guard let o = (try? JSONSerialization.jsonObject(with: Data(text.utf8))) as? [String: Any],
              let type = o["type"] as? String else { return nil }
        switch type {
        case "grant":
            guard let g = o["grant"] as? String, !g.isEmpty else { return nil }
            self = .grant(g)
        case "state":
            guard let s = o["state"] as? String else { return nil }
            self = .state(s, videoID: o["video_id"] as? String, queuePos: o["queue_pos"] as? Int)
        case "error":
            guard let e = o["error"] as? String else { return nil }
            self = .error(e)
        default: return nil
        }
    }
}

/// What the TV plays, from the receiver's media status (`customData`: `video_id`,
/// `queue_id`, `queue_pos`, `lang`; never the grant). The phone follows it: after a lock or
/// a return to the app, the item and position shown are the TV's.
public struct CastReceiverStatus: Equatable, Sendable {
    public enum PlayerState: String, Sendable { case idle, loading, buffering, playing, paused }

    public var state: PlayerState
    public var position: Double
    public var duration: Double
    public var videoID: String?
    public var queueID: String?
    public var queuePos: Int?

    public init(state: PlayerState, position: Double = 0, duration: Double = 0,
                videoID: String? = nil, queueID: String? = nil, queuePos: Int? = nil) {
        self.state = state
        self.position = position
        self.duration = duration
        self.videoID = videoID
        self.queueID = queueID
        self.queuePos = queuePos
    }

    /// The status from the receiver's media status values.
    public init(state: PlayerState, position: Double, duration: Double, customData: [String: Any]?) {
        self.init(state: state, position: position.isFinite ? max(0, position) : 0,
                  duration: duration.isFinite ? max(0, duration) : 0,
                  videoID: customData?["video_id"] as? String, queueID: customData?["queue_id"] as? String,
                  queuePos: (customData?["queue_pos"] as? NSNumber)?.intValue)
    }

    public var isPlaying: Bool { state == .playing || state == .buffering }
}

/// Who reports progress for a video: while it is cast the receiver does, with the grant, so
/// the phone's own progress posting stops (two reporters would double or move it back).
public enum CastProgress {
    public static func phoneReports(casting: Bool) -> Bool { !casting }
}

/// The receiver's error screens, as the sender says them.
public enum CastReceiverError: String, Sendable {
    case home, format, grant, update
}
