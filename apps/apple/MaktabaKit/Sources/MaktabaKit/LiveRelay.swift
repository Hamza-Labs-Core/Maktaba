import AVFoundation
import Foundation

/// Keeps a remote Live TV stream on the newest token while it plays (Epic 48, story 48.21).
///
/// Away from home a channel plays from a signed Cloudflare Stream URL whose first path segment
/// is the token (`https://customer-….cloudflarestream.com/<token>/manifest/video.m3u8`), and
/// the heartbeat hands a new token before the old one ends (`LiveSource`, `LiveEvent.token`).
/// AVPlayer keeps asking the URLs it was given, so a new token reached it only by opening the
/// stream again (a black gap, the live edge lost). Instead the player opens the stream through
/// this loader: every playlist (the master, and each variant reloaded every few seconds) is
/// asked under a private scheme that `AVAssetResourceLoader` hands here, fetched with the
/// newest token, and rewritten so that its variants come back here and its segments (which a
/// resource loader may not serve for HLS) are absolute URLs carrying the newest token. After a
/// heartbeat the next playlist reload already names the new token: the same item plays on.
///
/// Any other URL (the LAN path never comes here; a relay URL on another host) passes with its
/// token unchanged, so the loader is safe to use for any remote session.
public final class LiveRelayLoader: NSObject, AVAssetResourceLoaderDelegate, @unchecked Sendable {
    /// Fetches one URL: its body and HTTP status (`URLSession` in the apps, a fake in tests).
    public typealias Fetch = @Sendable (URL) async throws -> (Data, Int)

    /// The private scheme in front of the real one (`maktaba-relay+https://…`).
    static let schemePrefix = "maktaba-relay+"

    /// The URL the player opened (the session's first signed URL).
    public let opened: URL
    private let fetch: Fetch
    private let lock = NSLock()
    private var newest: URL
    private var newestToken: String?
    private var asked: [URL] = []
    /// The queue AVFoundation calls the delegate on.
    let queue = DispatchQueue(label: "com.hamzalabs.maktaba.live-relay")

    public init(url: URL, fetch: @escaping Fetch = LiveRelayLoader.fetch(with: .shared)) {
        opened = url
        newest = url
        newestToken = LiveSource.token(of: url)
        self.fetch = fetch
    }

    /// A plain GET with `session` (no token header: the signed URL is the credential).
    public static func fetch(with session: URLSession) -> Fetch {
        { url in
            let (data, response) = try await session.data(from: url)
            return (data, (response as? HTTPURLResponse)?.statusCode ?? 0)
        }
    }

    /// What the player opens: the stream under the private scheme, with this loader as its
    /// resource loader's delegate (the asset keeps a weak reference: keep the loader).
    public func makeAsset() -> AVURLAsset {
        let asset = AVURLAsset(url: assetURL)
        asset.resourceLoader.setDelegate(self, queue: queue)
        return asset
    }

    /// The opened URL under the private scheme.
    public var assetURL: URL { Self.wrap(opened) }

    /// The newest token, nil when the URL carries none.
    public var token: String? { lock.withLock { newestToken } }

    /// The newest signed URL and every real URL the player's playlists were fetched from, in order.
    public var current: URL { lock.withLock { newest } }
    public var fetched: [URL] { lock.withLock { asked } }

    /// A heartbeat's new URL (`LiveEvent.token`): from the next playlist reload on, every
    /// request carries its token. The item playing is kept.
    public func apply(_ url: URL) {
        lock.withLock {
            newest = url
            newestToken = LiveSource.token(of: url) ?? newestToken
        }
    }

    /// Follows a source's events while the stream plays: each new token is applied here.
    /// Returns when the events end: true when the home ended the session.
    /// `applied`: after each new token (an AirPlay receiver, which reads the signed URL itself,
    /// is handed the new one).
    public func follow(_ source: LiveSource, applied: @MainActor (URL) -> Void = { _ in }) async -> Bool {
        for await e in source.events {
            switch e {
            case let .token(url):
                apply(url)
                await applied(current)
            case .ended: return true
            }
        }
        return false
    }

    /// The real URL for one the player asked: the opened master is the newest signed URL as
    /// it is (a heartbeat may change more than the token); anything else gets the newest token.
    func real(for asked: URL) -> URL? {
        guard let url = Self.unwrap(asked) else { return nil }
        return lock.withLock {
            if Self.sameResource(url, opened) { return newest }
            guard let newestToken else { return url }
            return LiveSource.rewrite(url, token: newestToken)
        }
    }

    /// One playlist as the player gets it: fetched with the newest token and rewritten.
    public func playlist(for asked: URL) async throws -> Data {
        guard let url = real(for: asked) else { throw URLError(.unsupportedURL) }
        lock.withLock { self.asked.append(url) }
        let (data, status) = try await fetch(url)
        guard status == 200 else {
            throw APIError.status(status, String(decoding: data.prefix(2048), as: UTF8.self), retryAfter: nil)
        }
        return Data(Self.rewrite(playlist: String(decoding: data, as: UTF8.self), base: url, token: token).utf8)
    }

    // MARK: AVAssetResourceLoaderDelegate

    public func resourceLoader(_ resourceLoader: AVAssetResourceLoader,
                               shouldWaitForLoadingOfRequestedResource loadingRequest: AVAssetResourceLoadingRequest) -> Bool {
        guard let url = loadingRequest.request.url, Self.unwrap(url) != nil else { return false }
        nonisolated(unsafe) let request = loadingRequest
        Task { [queue] in
            do {
                let data = try await self.playlist(for: url)
                queue.async {
                    guard !request.isCancelled else { return }
                    if let info = request.contentInformationRequest {
                        info.contentType = "public.m3u-playlist"
                        info.contentLength = Int64(data.count)
                        info.isByteRangeAccessSupported = false
                    }
                    request.dataRequest?.respond(with: data)
                    request.finishLoading()
                }
            } catch {
                queue.async {
                    guard !request.isCancelled else { return }
                    request.finishLoading(with: error)
                }
            }
        }
        return true
    }

    // MARK: rewriting

    /// A playlist with every URI resolved against `base`: playlists (`.m3u8`) under the
    /// private scheme so their reloads come back here, anything else (segments, keys, init
    /// sections) absolute; both with `token` in place of the one they had.
    static func rewrite(playlist: String, base: URL, token: String?) -> String {
        // Foundation's split: Swift's own treats "\r\n" as one Character
        playlist.components(separatedBy: "\n").map { raw -> String in
            let cr = raw.hasSuffix("\r")
            let line = cr ? String(raw.dropLast()) : String(raw)
            let trimmed = line.trimmingCharacters(in: .whitespaces)
            let out: String
            if trimmed.isEmpty {
                out = line
            } else if trimmed.hasPrefix("#") {
                out = rewriteAttributes(line, base: base, token: token)
            } else {
                out = map(trimmed, base: base, token: token)
            }
            return cr ? out + "\r" : out
        }.joined(separator: "\n")
    }

    /// A tag's `URI="…"` attributes (`EXT-X-MEDIA`, `EXT-X-MAP`, `EXT-X-KEY`,
    /// `EXT-X-I-FRAME-STREAM-INF`), mapped as a URI line is.
    private static func rewriteAttributes(_ line: String, base: URL, token: String?) -> String {
        var out = ""
        var rest = Substring(line)
        while let open = rest.range(of: "URI=\"") {
            out += rest[..<open.upperBound]
            rest = rest[open.upperBound...]
            guard let close = rest.firstIndex(of: "\"") else { break }
            out += map(String(rest[..<close]), base: base, token: token)
            rest = rest[close...]
        }
        return out + rest
    }

    private static func map(_ uri: String, base: URL, token: String?) -> String {
        guard let resolved = URL(string: uri, relativeTo: base)?.absoluteURL else { return uri }
        let signed = token.map { LiveSource.rewrite(resolved, token: $0) } ?? resolved
        return signed.path().hasSuffix(".m3u8") ? wrap(signed).absoluteString : signed.absoluteString
    }

    static func wrap(_ url: URL) -> URL {
        guard var c = URLComponents(url: url, resolvingAgainstBaseURL: true), let scheme = c.scheme,
              !scheme.hasPrefix(schemePrefix) else { return url }
        c.scheme = schemePrefix + scheme
        return c.url ?? url
    }

    static func unwrap(_ url: URL) -> URL? {
        guard var c = URLComponents(url: url, resolvingAgainstBaseURL: true), let scheme = c.scheme,
              scheme.hasPrefix(schemePrefix) else { return nil }
        c.scheme = String(scheme.dropFirst(schemePrefix.count))
        return c.url
    }

    /// The same resource whatever its token: host and path, the token's segment left out.
    static func sameResource(_ a: URL, _ b: URL) -> Bool {
        func key(_ u: URL) -> String {
            var parts = u.path().split(separator: "/", omittingEmptySubsequences: false).map(String.init)
            if LiveSource.token(of: u) != nil, parts.count > 1 { parts[1] = "" }
            return "\(u.host() ?? "")|\(u.port ?? 0)|\(parts.joined(separator: "/"))"
        }
        return key(a) == key(b)
    }
}
