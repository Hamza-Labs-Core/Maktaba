import Foundation

// Live TV for the native players (Epic 48, story 48.7): what is on now, the guide, one
// channel's guide, a channel's logo, where the TV streams (`GET /api/tv/info`), and playing a
// channel at home (the LAN playlist through the web port) or away (a remote session: tune,
// heartbeat, end). The Player (iPhone, iPad, Mac) and the Apple TV app both use it, so
// nothing here touches UIKit or AppKit.
//
// Roles are the home's: `now` and the guides list only the channels the caller may read, and
// one channel's guide (and its stream) answers 404 for any other (NFR-12, FR-24).

// MARK: models

/// A channel as the guide routes describe it.
public struct LiveChannel: Decodable, Hashable, Sendable, Identifiable {
    public let id: String
    public let number: Int
    public let name: String
    public let slug: String
    /// An external logo URL the home was given (a file logo is never named by its path).
    public let logo: String?
    /// The home's own logo route (`/api/channels/{id}/logo`), present when the channel has a logo.
    public let logoPath: String?

    public init(id: String, number: Int, name: String, slug: String = "", logo: String? = nil, logoPath: String? = nil) {
        self.id = id
        self.number = number
        self.name = name
        self.slug = slug
        self.logo = logo
        self.logoPath = logoPath
    }

    enum CodingKeys: String, CodingKey {
        case id, number, name, slug, logo
        case logoPath = "logo_url"
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        number = c.lenient(.number) ?? 0
        name = c.lenient(.name) ?? ""
        slug = c.lenient(.slug) ?? ""
        logo = c.lenient(.logo)
        logoPath = c.lenient(.logoPath)
    }

    /// Whether the home has a logo for this channel.
    public var hasLogo: Bool { !(logoPath ?? "").isEmpty || !(logo ?? "").isEmpty }
}

/// One programme (or a block of filler) on a channel.
public struct GuideBlock: Decodable, Hashable, Sendable, Identifiable {
    public let channelID: String
    /// `programme`, `episode`, `movie`, `filler`, ... (the home's own word).
    public let kind: String
    public let start: Date
    public let stop: Date
    public let title: String
    public let subTitle: String?
    public let desc: String?
    public let poster: String?
    public let genre: String?
    public let rating: String?
    public let series: String?
    public let season: Int?
    public let episode: Int?
    /// The channel strip's line (what the web shows under the channel's name).
    public let onAir: String?
    /// On now, by the home's clock when it answered.
    public let isLive: Bool
    /// How far in, 0...1, by the home's clock when it answered (`progress(at:)` for now).
    public let progress: Double

    public var id: String { "\(channelID)@\(start.timeIntervalSince1970)" }

    enum CodingKeys: String, CodingKey {
        case kind, start, stop, title, desc, poster, genre, rating, series, season, episode, progress
        case channelID = "channel_id"
        case subTitle = "sub_title"
        case onAir = "on_air"
        case isLive = "is_live"
    }

    public init(channelID: String, kind: String = "programme", start: Date, stop: Date, title: String,
                subTitle: String? = nil, desc: String? = nil, isLive: Bool = false, progress: Double = 0) {
        self.channelID = channelID
        self.kind = kind
        self.start = start
        self.stop = stop
        self.title = title
        self.subTitle = subTitle
        self.desc = desc
        poster = nil
        genre = nil
        rating = nil
        series = nil
        season = nil
        episode = nil
        onAir = nil
        self.isLive = isLive
        self.progress = progress
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        channelID = c.lenient(.channelID) ?? ""
        kind = c.lenient(.kind) ?? ""
        start = try c.decode(Date.self, forKey: .start)
        stop = try c.decode(Date.self, forKey: .stop)
        title = c.lenient(.title) ?? ""
        subTitle = c.lenient(.subTitle)
        desc = c.lenient(.desc)
        poster = c.lenient(.poster)
        genre = c.lenient(.genre)
        rating = c.lenient(.rating)
        series = c.lenient(.series)
        season = c.lenient(.season)
        episode = c.lenient(.episode)
        onAir = c.lenient(.onAir)
        isLive = c.lenient(.isLive) ?? false
        progress = c.lenient(.progress) ?? 0
    }

    public var duration: TimeInterval { stop.timeIntervalSince(start) }

    /// On at `date` (its start included, its stop not).
    public func isOn(at date: Date) -> Bool { start <= date && date < stop }

    /// How far in at `date`, 0...1.
    public func progress(at date: Date) -> Double {
        guard duration > 0 else { return 0 }
        return min(1, max(0, date.timeIntervalSince(start) / duration))
    }
}

/// `GET /api/channels/now`: every channel the caller may read, with what is on and what is next.
public struct ChannelsNow: Decodable, Sendable {
    public let serverTime: Date?
    public let channels: [ChannelNow]

    enum CodingKeys: String, CodingKey {
        case channels
        case serverTime = "server_time"
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        serverTime = c.lenient(.serverTime)
        channels = c.lenientList(.channels)
    }
}

public struct ChannelNow: Decodable, Hashable, Sendable, Identifiable {
    public let channel: LiveChannel
    public let current: GuideBlock?
    public let next: GuideBlock?
    /// Encoding now on a streaming host (its playlist answers at once).
    public let onAir: Bool

    public var id: String { channel.id }

    enum CodingKeys: String, CodingKey {
        case channel, current, next
        case onAir = "on_air"
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        channel = try c.decode(LiveChannel.self, forKey: .channel)
        current = c.lenient(.current)
        next = c.lenient(.next)
        onAir = c.lenient(.onAir) ?? false
    }
}

/// `GET /api/channels/guide?start=&end=`: the channels × time grid.
public struct GuideWindow: Decodable, Sendable {
    public let serverTime: Date?
    public let start: Date
    public let end: Date
    public let channels: [ChannelGuide]

    enum CodingKeys: String, CodingKey {
        case start, end, channels
        case serverTime = "server_time"
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        serverTime = c.lenient(.serverTime)
        start = try c.decode(Date.self, forKey: .start)
        end = try c.decode(Date.self, forKey: .end)
        channels = c.lenientList(.channels)
    }
}

/// One channel's row in the guide, or its own page (`GET /api/channels/{id}/guide`).
public struct ChannelGuide: Decodable, Hashable, Sendable, Identifiable {
    public let channel: LiveChannel
    public let blocks: [GuideBlock]
    /// How far ahead the home has planned this channel (its own page only).
    public let horizonUntil: Date?

    public var id: String { channel.id }

    enum CodingKeys: String, CodingKey {
        case channel, blocks
        case horizonUntil = "horizon_until"
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        channel = try c.decode(LiveChannel.self, forKey: .channel)
        blocks = c.lenientList(.blocks)
        horizonUntil = c.lenient(.horizonUntil)
    }

    /// What is on at `date`.
    public func block(at date: Date) -> GuideBlock? { blocks.first { $0.isOn(at: date) } }
}

/// `GET /api/tv/info`: where the TV streams, and whether this caller is away from home.
public struct TVInfo: Decodable, Sendable, Equatable {
    /// The channel playlist's path on the web port, `{id}` for the channel.
    public let hlsPathTemplate: String
    public let remote: Remote

    public struct Remote: Decodable, Sendable, Equatable {
        /// The home can serve a viewer away from home.
        public let available: Bool
        /// Why not, when it cannot (the home's words).
        public let reason: String?
        /// This request came from away from home: play through a remote session.
        public let viaRelay: Bool
        public let maxStreams: Int
        public let tokenTTLMinutes: Int

        enum CodingKeys: String, CodingKey {
            case available, reason
            case viaRelay = "via_relay"
            case maxStreams = "max_streams"
            case tokenTTLMinutes = "token_ttl_minutes"
        }

        public init(available: Bool = false, reason: String? = nil, viaRelay: Bool = false,
                    maxStreams: Int = 0, tokenTTLMinutes: Int = 0) {
            self.available = available
            self.reason = reason
            self.viaRelay = viaRelay
            self.maxStreams = maxStreams
            self.tokenTTLMinutes = tokenTTLMinutes
        }

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            available = c.lenient(.available) ?? false
            reason = c.lenient(.reason)
            viaRelay = c.lenient(.viaRelay) ?? false
            maxStreams = c.lenient(.maxStreams) ?? 0
            tokenTTLMinutes = c.lenient(.tokenTTLMinutes) ?? 0
        }
    }

    enum CodingKeys: String, CodingKey {
        case remote
        case hlsPathTemplate = "hls_path_template"
    }

    public static let defaultHLSPathTemplate = "/stream/channel/{id}/index.m3u8"

    public init(hlsPathTemplate: String = TVInfo.defaultHLSPathTemplate, remote: Remote = Remote()) {
        self.hlsPathTemplate = hlsPathTemplate
        self.remote = remote
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        let t: String? = c.lenient(.hlsPathTemplate)
        hlsPathTemplate = (t?.contains("{id}") == true) ? t! : Self.defaultHLSPathTemplate
        remote = c.lenient(.remote) ?? Remote()
    }
}

/// `POST /api/tv/remote/tune`: 200 live with the URL, or 202 starting (ask again).
public struct RemoteTune: Decodable, Sendable, Equatable {
    public let sessionID: String
    /// `live` or `starting`.
    public let state: String
    public let hlsURL: URL?
    public let expiresAt: Date?
    public let refreshAfter: Date?
    /// Seconds to wait before asking again while starting.
    public let retryAfter: Int?

    enum CodingKeys: String, CodingKey {
        case state
        case sessionID = "session_id"
        case hlsURL = "hls_url"
        case expiresAt = "expires_at"
        case refreshAfter = "refresh_after"
        case retryAfter = "retry_after"
    }

    public init(sessionID: String, state: String, hlsURL: URL? = nil, expiresAt: Date? = nil,
                refreshAfter: Date? = nil, retryAfter: Int? = nil) {
        self.sessionID = sessionID
        self.state = state
        self.hlsURL = hlsURL
        self.expiresAt = expiresAt
        self.refreshAfter = refreshAfter
        self.retryAfter = retryAfter
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        sessionID = try c.decode(String.self, forKey: .sessionID)
        state = c.lenient(.state) ?? ""
        hlsURL = (c.lenient(.hlsURL) as String?).flatMap { $0.isEmpty ? nil : URL(string: $0) }
        expiresAt = c.lenient(.expiresAt)
        refreshAfter = c.lenient(.refreshAfter)
        retryAfter = c.lenient(.retryAfter)
    }

    public var isLive: Bool { state == "live" && hlsURL != nil }
}

/// `POST /api/tv/remote/sessions/{id}/heartbeat`: a new token only when one is due.
public struct RemoteBeat: Decodable, Sendable, Equatable {
    /// `live`, `starting` or `ended`.
    public let state: String
    public let expiresAt: Date?
    public let token: String?
    public let hlsURL: URL?

    enum CodingKeys: String, CodingKey {
        case state, token
        case expiresAt = "expires_at"
        case hlsURL = "hls_url"
    }

    public init(state: String, expiresAt: Date? = nil, token: String? = nil, hlsURL: URL? = nil) {
        self.state = state
        self.expiresAt = expiresAt
        self.token = token
        self.hlsURL = hlsURL
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        state = c.lenient(.state) ?? ""
        expiresAt = c.lenient(.expiresAt)
        token = (c.lenient(.token) as String?).flatMap { $0.isEmpty ? nil : $0 }
        hlsURL = (c.lenient(.hlsURL) as String?).flatMap { $0.isEmpty ? nil : URL(string: $0) }
    }
}

// MARK: the client

/// The calls `LiveSource` makes (`APIClient` in the apps, a fake in tests).
public protocol LiveAPI: Sendable {
    func tvInfo() async throws -> TVInfo
    func tune(channel: String, device: String) async throws -> RemoteTune
    func heartbeat(session: String) async throws -> RemoteBeat
    func endRemoteSession(_ id: String) async
    /// The channel's playlist on the web port (played with the Bearer token, or through `StreamProxy`).
    func channelStreamURL(_ channel: String, template: String) -> URL
}

extension APIClient: LiveAPI {
    /// What is on now on every channel the caller may read (`category`: one category only).
    public func channelsNow(category: String? = nil) async throws -> ChannelsNow {
        try await get("/api/channels/now", query: category.map { ["category": $0] } ?? [:])
    }

    /// The guide from `start` to `end` (the web asks three hours on a phone, six on a tablet).
    public func guide(from start: Date, to end: Date, category: String? = nil) async throws -> GuideWindow {
        var q = ["start": Self.rfc3339(start), "end": Self.rfc3339(end)]
        if let category { q["category"] = category }
        return try await get("/api/channels/guide", query: q)
    }

    /// One channel's guide (the home's default: three hours from now). 404 when the caller
    /// may not read the channel, as for an unknown one.
    public func channelGuide(_ channel: String, from start: Date? = nil, to end: Date? = nil) async throws -> ChannelGuide {
        var q: [String: String] = [:]
        if let start { q["start"] = Self.rfc3339(start) }
        if let end { q["end"] = Self.rfc3339(end) }
        return try await get("/api/channels/\(Self.segment(channel))/guide", query: q)
    }

    /// The channel's logo on the home (`nil`: it has none). The route needs the Bearer token
    /// (`channelLogo(_:)`, or an image loader given `playbackHeaders()`).
    public nonisolated func channelLogoURL(_ channel: LiveChannel) -> URL? {
        guard !(channel.logoPath ?? "").isEmpty else { return nil }
        return home.appending(path: "/api/channels/\(Self.segment(channel.id))/logo")
    }

    /// The channel's logo, fetched with the Bearer token.
    public func channelLogo(_ channel: String) async throws -> Data {
        try await sendRaw(request("GET", "/api/channels/\(Self.segment(channel))/logo"))
    }

    /// Where the TV streams and whether this caller is away from home.
    public func tvInfo() async throws -> TVInfo {
        try await get("/api/tv/info")
    }

    public nonisolated func channelStreamURL(_ channel: String, template: String = TVInfo.defaultHLSPathTemplate) -> URL {
        let path = template.contains("{id}") ? template : TVInfo.defaultHLSPathTemplate
        return home.appending(path: path.replacingOccurrences(of: "{id}", with: Self.segment(channel)))
    }

    /// Opens (or resumes) a remote session for a channel: 200 live, 202 starting (ask again
    /// with the same channel after `retryAfter`). A refusal is an `APIError.status` whose
    /// problem `code` says why (`budget_exhausted`, `remote_not_allowed`, `no_such_channel`,
    /// `user_streams_full`, `remote_channels_full`, `remote_unavailable`).
    public func tune(channel: String, device: String) async throws -> RemoteTune {
        try await send(request("POST", "/api/tv/remote/tune", body: ["channel_id": channel, "device": device]))
    }

    /// Keeps a remote session alive (every 60 s; the home allows 150 s) and collects a new token.
    public func heartbeat(session: String) async throws -> RemoteBeat {
        try await send(request("POST", "/api/tv/remote/sessions/\(Self.segment(session))/heartbeat"))
    }

    /// Ends a remote session. Best effort (the home reaps a silent one): a failure is logged.
    public func endRemoteSession(_ id: String) async {
        _ = try? await sendRaw(request("DELETE", "/api/tv/remote/sessions/\(Self.segment(id))"))
    }

    /// A time as the guide routes read it (RFC 3339, whole seconds, UTC).
    nonisolated static func rfc3339(_ d: Date) -> String {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime]
        f.timeZone = TimeZone(identifier: "UTC")
        return f.string(from: Date(timeIntervalSince1970: d.timeIntervalSince1970.rounded(.down)))
    }
}

// MARK: playing a channel

/// How a channel plays.
public enum LivePath: String, Sendable, Equatable {
    /// At home: the channel's playlist through the web port, with the Bearer token.
    case lan
    /// Away: a remote session's signed URL, kept alive by heartbeats.
    case relay
}

public enum LiveError: Error, Equatable, Sendable {
    /// Away from home, and the home cannot serve this viewer remotely (its reason).
    case remoteUnavailable(String?)
    /// The remote stream did not start in time.
    case startTimeout
}

/// What a `LiveSource` tells the player after it opened.
public enum LiveEvent: Sendable, Equatable {
    /// A new signed token (and maybe URL): the same session, so the player keeps its item and
    /// applies `LiveSource.rewrite(_:)` to each request from now on.
    case token(URL)
    /// The home ended the session (or took the viewer's access away): leave the player.
    case ended
}

/// Plays one channel: picks the LAN path or a remote session from `GET /api/tv/info`, keeps a
/// remote session alive with a heartbeat every 60 s, swaps in each new token without a new
/// session (the player is not reopened), and ends the session on `close()`.
public actor LiveSource {
    public typealias Sleep = @Sendable (Duration) async throws -> Void

    public nonisolated let channel: String
    private let api: any LiveAPI
    private let device: String
    private let sleep: Sleep
    private let heartbeatEvery: Duration
    private let startTimeout: Duration

    public private(set) var path: LivePath?
    public private(set) var sessionID: String?
    /// The URL the player opened (stays the same for a session's life).
    public private(set) var openedURL: URL?
    /// The latest URL and token from the home.
    public private(set) var currentURL: URL?
    public private(set) var token: String?
    public private(set) var ended = false

    private var beat: Task<Void, Never>?
    private let stream: AsyncStream<LiveEvent>
    private let continuation: AsyncStream<LiveEvent>.Continuation

    /// `sleep`: the clock (a fake one in tests). `device`: how the home's admin page names this device.
    public init(api: any LiveAPI, channel: String, device: String = LiveSource.defaultDevice,
                heartbeat: Duration = .seconds(60), startTimeout: Duration = .seconds(90),
                sleep: @escaping Sleep = { try await Task.sleep(for: $0) }) {
        self.api = api
        self.channel = channel
        self.device = device
        self.sleep = sleep
        heartbeatEvery = heartbeat
        self.startTimeout = startTimeout
        (stream, continuation) = AsyncStream.makeStream(of: LiveEvent.self, bufferingPolicy: .unbounded)
    }

    /// The device's name for the home's remote sessions page.
    public static var defaultDevice: String {
        #if os(tvOS)
        "Maktaba on Apple TV"
        #elseif os(macOS)
        "Maktaba on Mac"
        #elseif os(visionOS)
        "Maktaba on Vision Pro"
        #else
        "Maktaba on iOS"
        #endif
    }

    /// New tokens and the end of the session, for the player.
    public nonisolated var events: AsyncStream<LiveEvent> { stream }

    /// Finds the path and the URL to play. Away, it tunes (asking again while the stream
    /// starts, up to `startTimeout`) and starts the heartbeat.
    public func open() async throws -> (url: URL, path: LivePath) {
        let info = try await api.tvInfo()
        guard info.remote.viaRelay else {
            let url = api.channelStreamURL(channel, template: info.hlsPathTemplate)
            path = .lan
            openedURL = url
            currentURL = url
            return (url, .lan)
        }
        guard info.remote.available else { throw LiveError.remoteUnavailable(info.remote.reason) }
        var waited = Duration.zero
        while true {
            let t: RemoteTune
            do {
                t = try await api.tune(channel: channel, device: device)
            } catch {
                await giveBack()
                throw error
            }
            sessionID = t.sessionID
            if t.isLive, let url = t.hlsURL {
                path = .relay
                openedURL = url
                currentURL = url
                token = Self.token(of: url)
                startHeartbeat()
                return (url, .relay)
            }
            let delay = Duration.seconds(max(1, t.retryAfter ?? 3))
            guard waited + delay <= startTimeout else {
                await giveBack()
                throw LiveError.startTimeout
            }
            do {
                try await sleep(delay)
            } catch {
                await giveBack()
                throw error
            }
            waited += delay
        }
    }

    /// A request's URL with the newest token (a Cloudflare Stream URL's first path segment);
    /// any other URL as it is.
    public nonisolated func rewrite(_ url: URL, token: String?) -> URL {
        guard let token else { return url }
        return Self.rewrite(url, token: token)
    }

    public func rewrite(_ url: URL) -> URL { rewrite(url, token: token) }

    /// Stops the heartbeat and ends the remote session.
    public func close() async {
        guard !ended else { return }
        stop()
        await giveBack()
    }

    private func giveBack() async {
        guard let id = sessionID else { return }
        sessionID = nil
        await api.endRemoteSession(id)
    }

    private func stop() {
        ended = true
        beat?.cancel()
        beat = nil
        continuation.finish()
    }

    private func startHeartbeat() {
        beat = Task { [sleep, heartbeatEvery] in
            while !Task.isCancelled {
                do { try await sleep(heartbeatEvery) } catch { return }
                if Task.isCancelled { return }
                await self.beatOnce()
            }
        }
    }

    /// One heartbeat: a new token is passed on; a session the home ended ends here too;
    /// anything else waits for the next beat.
    func beatOnce() async {
        guard !ended, let id = sessionID else { return }
        do {
            let b = try await api.heartbeat(session: id)
            guard !ended else { return }
            if b.state == "ended" {
                sessionID = nil
                continuation.yield(.ended)
                stop()
                return
            }
            if b.token != nil || b.hlsURL != nil {
                if let u = b.hlsURL { currentURL = u }
                token = b.token ?? currentURL.flatMap(Self.token(of:)) ?? token
                if let u = currentURL { continuation.yield(.token(rewrite(u, token: token))) }
            }
        } catch {
            if Self.endsSession(error) {
                sessionID = nil
                continuation.yield(.ended)
                stop()
            }
        }
    }

    /// The refusals that mean the session is over (not a blip worth another beat).
    static func endsSession(_ error: Error) -> Bool {
        guard let e = error as? APIError else { return false }
        if case .signedOut = e { return true }
        return ["session_ended", "no_such_session", "remote_not_allowed"].contains(e.problem?.code ?? "")
    }

    static let streamHostSuffix = ".cloudflarestream.com"

    /// The signed token of a Cloudflare Stream URL (its first path segment).
    static func token(of url: URL) -> String? {
        guard url.host()?.hasSuffix(streamHostSuffix) == true else { return nil }
        let seg = url.path().split(separator: "/", omittingEmptySubsequences: false)
        return seg.count > 1 && !seg[1].isEmpty ? String(seg[1]) : nil
    }

    static func rewrite(_ url: URL, token: String) -> URL {
        guard !token.isEmpty, url.host()?.hasSuffix(streamHostSuffix) == true,
              var c = URLComponents(url: url, resolvingAgainstBaseURL: false) else { return url }
        var parts = c.percentEncodedPath.split(separator: "/", omittingEmptySubsequences: false).map(String.init)
        guard parts.count >= 3, !parts[1].isEmpty else { return url }
        parts[1] = token
        c.percentEncodedPath = parts.joined(separator: "/")
        return c.url ?? url
    }
}
