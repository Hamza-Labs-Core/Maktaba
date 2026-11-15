import Foundation
import os
import Security

// Epic 50 story 50.27 §2: Watch a broadcast. A guest watches a home's published channels with
// no Maktaba or home account: an invite (a link, or the publisher's handle and an `MK-` short
// code) or a phone's pairing gives the device a guest pass, kept in the Keychain; the edge
// (`watch.maktaba.tv`) answers the lineup and a play with the viewer token, and the channel
// plays from `live.maktaba.tv` through `StreamProxy(live:)`. The edge enforces every right
// (the credential, its channels, the limits); the app only shows what it answers.

/// The edge the guest space talks to.
public enum GuestEdge {
    public static let production = URL(string: "https://watch.maktaba.tv")!

    /// `watch.maktaba.tv`; a Debug build launched by the UI tests with
    /// `-maktaba-guest-edge http://127.0.0.1:<port>` talks to their mock edge instead.
    public static var watch: URL {
        #if DEBUG
        let args = CommandLine.arguments
        if let i = args.firstIndex(of: "-maktaba-guest-edge"), i + 1 < args.count,
           let u = URL(string: args[i + 1]), u.host() == "127.0.0.1" {
            return u
        }
        #endif
        return production
    }
}

// MARK: - the pass

/// A saved broadcast: one publisher's credential on this device.
public struct GuestPass: Codable, Sendable, Hashable, Identifiable {
    public enum Kind: String, Codable, Sendable {
        /// A guest pass from an invite (`mkbg1.`, 30 days, renewed while its invite lasts).
        case pass
        /// An open session (`mkbo1.`, 12 hours), from a phone's pairing.
        case open
    }

    /// The publisher's id (the home's server id, as in its watch address).
    public let publisher: String
    /// "{publisher}" as the lineup names it ("" until the lineup is read).
    public var name: String
    public var credential: String
    public var kind: Kind
    /// The channels the credential opens (an invite's; empty: the publisher's open ones).
    public var channels: [String]
    public var expiresAt: Date
    public var savedAt: Date

    public var id: String { publisher }

    public init(publisher: String, name: String = "", credential: String, kind: Kind, channels: [String],
                expiresAt: Date, savedAt: Date = Date()) {
        self.publisher = publisher
        self.name = name
        self.credential = credential
        self.kind = kind
        self.channels = channels
        self.expiresAt = expiresAt
        self.savedAt = savedAt
    }

    /// The expiry a credential says it has (its payload's `exp`), unverified: the edge checks
    /// the signature; the app only needs to know when to forget it.
    public static func claimedExpiry(_ credential: String) -> Date? {
        let parts = credential.split(separator: ".")
        guard parts.count == 3 else { return nil }
        var b = parts[1].replacingOccurrences(of: "-", with: "+").replacingOccurrences(of: "_", with: "/")
        b += String(repeating: "=", count: (4 - b.count % 4) % 4)
        guard let data = Data(base64Encoded: b),
              let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              let exp = json["exp"] as? Double else { return nil }
        return Date(timeIntervalSince1970: exp)
    }
}

/// The saved broadcasts, in the Keychain: one item (`<app>.guest`, account `broadcasts`) holding
/// the list, newest first. An expired pass is dropped the next time the list is read.
public final class GuestPassStore: @unchecked Sendable {
    static let account = "broadcasts"
    private let secrets: SecretStore
    private let now: @Sendable () -> Date
    private let lock = NSLock()

    public convenience init(app: String) {
        let service = "\(app).guest"
        #if DEBUG
        if UITestSecrets.isOn {
            self.init(secrets: UITestSecrets(service: service))
            return
        }
        #endif
        #if os(macOS)
        self.init(secrets: KeychainSecrets(service: service, dataProtection: true))
        #else
        self.init(secrets: KeychainSecrets(service: service, dataProtection: false))
        #endif
    }

    init(secrets: SecretStore, now: @escaping @Sendable () -> Date = { Date() }) {
        self.secrets = secrets
        self.now = now
    }

    /// The saved broadcasts still valid, the most recently saved first.
    public func list() -> [GuestPass] {
        lock.withLock {
            let all = read()
            let live = all.filter { $0.expiresAt > now() }
            if live.count != all.count { write(live) }
            return live
        }
    }

    public func pass(for publisher: String) -> GuestPass? {
        list().first { $0.publisher == publisher }
    }

    /// Saves `pass`, replacing the publisher's earlier one, at the top of the list. A name the
    /// earlier one had is kept when the new one has none yet.
    public func save(_ pass: GuestPass) {
        lock.withLock {
            var all = read().filter { $0.expiresAt > now() }
            var p = pass
            if p.name.isEmpty, let old = all.first(where: { $0.publisher == pass.publisher }) { p.name = old.name }
            all.removeAll { $0.publisher == pass.publisher }
            p.savedAt = now()
            all.insert(p, at: 0)
            write(all)
        }
    }

    /// The edge renewed the pass (`X-Maktaba-Pass` on a play): kept with its new expiry.
    public func renew(_ publisher: String, credential: String) {
        lock.withLock {
            var all = read()
            guard let i = all.firstIndex(where: { $0.publisher == publisher }) else { return }
            all[i].credential = credential
            if let exp = GuestPass.claimedExpiry(credential) { all[i].expiresAt = exp }
            write(all)
        }
    }

    /// The lineup's name for the publisher.
    public func name(_ publisher: String, _ name: String) {
        lock.withLock {
            var all = read()
            guard let i = all.firstIndex(where: { $0.publisher == publisher }), !name.isEmpty, all[i].name != name else { return }
            all[i].name = name
            write(all)
        }
    }

    /// Remove: the pass leaves the device (the edge keeps counting the invite's device use).
    public func remove(_ publisher: String) {
        lock.withLock { write(read().filter { $0.publisher != publisher }) }
    }

    private func read() -> [GuestPass] {
        guard let text = secrets.read(Self.account).value else { return [] }
        let d = JSONDecoder()
        d.dateDecodingStrategy = .secondsSince1970
        return (try? d.decode([GuestPass].self, from: Data(text.utf8))) ?? []
    }

    private func write(_ all: [GuestPass]) {
        if all.isEmpty {
            secrets.delete(Self.account)
            return
        }
        let e = JSONEncoder()
        e.dateEncodingStrategy = .secondsSince1970
        e.outputFormatting = .sortedKeys
        guard let data = try? e.encode(all) else { return }
        secrets.write(Self.account, String(decoding: data, as: UTF8.self))
    }
}

// MARK: - what the guest sees

/// The states a guest can see (story 50.27 §3), from an edge answer: the same mapping as the
/// watch pages' (`site/states.ts`), both tested against `shared/broadcast/viewer-states.json`.
public struct GuestViewerState: Sendable, Equatable {
    public enum Name: String, Sendable {
        case live, tuning, offline, off_air, full, busy, geoBlocked, ended, inviteUsed, inviteExpired
        case inviteRequired, revoked, verify, dailyLimit, error
    }

    public let state: Name
    /// The message's key (`broadcast.tuning`), "" for a channel playing with nothing to say.
    public let key: String
    /// The player plays (live, tuning while the channel wakes, offline on the slate).
    public let plays: Bool
    /// A Try again action helps.
    public let retry: Bool

    public init(status: Int, channelState: String? = nil, error: String? = nil) {
        func s(_ n: Name, _ k: String, _ p: Bool, _ r: Bool) -> (Name, String, Bool, Bool) { (n, k, p, r) }
        let v: (Name, String, Bool, Bool)
        if (200..<300).contains(status) {
            switch channelState {
            case "waiting": v = s(.tuning, "broadcast.tuning", true, false)
            case "offline_slate": v = s(.offline, "broadcast.homeOffline", true, false)
            case "off_air": v = s(.off_air, "broadcast.off", false, true)
            case "revoked": v = s(.ended, "broadcast.ended", false, false)
            default: v = s(.live, "", true, false)
            }
        } else if status == 451 || error == "geo_blocked" {
            v = s(.geoBlocked, "broadcast.geoBlocked", false, false)
        } else {
            switch error ?? "" {
            case "broadcast_full": v = s(.full, "broadcast.full", false, true)
            case "broadcast_wait": v = s(.full, "broadcast.waiting", false, true)
            case "broadcast_ip_limit": v = s(.full, "broadcast.ipLimit", false, true)
            case "rate_limited": v = s(.busy, "broadcast.tooManyTries", false, true)
            case "broadcast_daily_limit": v = s(.dailyLimit, "broadcast.dailyLimit", false, false)
            case "invite_used": v = s(.inviteUsed, "broadcast.invite.used", false, false)
            case "invite_expired": v = s(.inviteExpired, "broadcast.invite.expired", false, false)
            case "invite_revoked", "pass_revoked", "revoked", "session_ended": v = s(.revoked, "broadcast.revoked", false, true)
            case "invite_required", "not_invited": v = s(.inviteRequired, "broadcast.inviteRequired", false, false)
            case "turnstile_required": v = s(.verify, "site.verify", false, false)
            case "broadcast_off", "channel_unknown": v = s(.ended, "broadcast.ended", false, false)
            default: v = s(.error, "site.error", false, true)
            }
        }
        (state, key, plays, retry) = v
    }

    public static let live = GuestViewerState(status: 200)

    /// The words for this state ("" while live).
    public var message: String { key.isEmpty ? "" : GuestWords.message(key) }
}

/// A refusal from the edge: its status and `error` code.
public struct GuestError: Error, Sendable, Equatable {
    public let status: Int
    public let code: String
    public var state: GuestViewerState { GuestViewerState(status: status, error: code) }
    /// No answer (no network, a timeout).
    public static let offline = GuestError(status: 0, code: "network")

    /// The message the guest reads: the state's, or for a code the states do not name (a
    /// wrong short code, a bad invite link) the invite's own.
    public var message: String {
        switch code {
        case "invite_used", "invite_expired", "invite_revoked", "rate_limited", "broadcast_full",
             "broadcast_wait", "broadcast_ip_limit", "broadcast_daily_limit", "geo_blocked", "pass_revoked",
             "invite_required", "not_invited", "broadcast_off", "session_ended", "revoked", "channel_unknown":
            return state.message
        case "code_unknown", "bad_token", "bad_signature", "unknown_key", "bad_request":
            return GuestWords.message("broadcast.invite.invalid")
        case "network":
            return GuestWords.message("guest.network")
        default:
            return state.message
        }
    }
}

// MARK: - the edge's answers

/// A programme on a channel's guide; `attribution` for a CC BY one (50.23).
public struct GuestProgramme: Decodable, Sendable, Hashable, Identifiable {
    public struct Attribution: Decodable, Sendable, Hashable {
        public let title: String?
        public let by: String?
        public let licence: String?
        public let url: String?

        /// "Title, by Name (CC BY 4.0)".
        public var line: String {
            [title, by.map { GuestWords.by($0) }].compactMap { $0?.nilIfBlank }.joined(separator: " · ")
                + (licence?.nilIfBlank.map { " (\($0))" } ?? "")
        }
    }

    public let start: Date?
    public let end: Date?
    public let title: String
    public let attribution: Attribution?
    public var id: String { "\(start?.timeIntervalSince1970 ?? 0)-\(title)" }

    enum CodingKeys: String, CodingKey { case start, end, title, attribution }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        start = (try? c.decode(String.self, forKey: .start)).flatMap(homeTime)
        end = (try? c.decode(String.self, forKey: .end)).flatMap(homeTime)
        title = (try? c.decode(String.self, forKey: .title)) ?? ""
        attribution = try? c.decode(Attribution.self, forKey: .attribution)
    }
}

/// `GET /api/v1/lineup`: the channels the credential opens, with now and next.
public struct GuestLineup: Decodable, Sendable, Equatable {
    public struct Publisher: Decodable, Sendable, Equatable {
        public let id: String
        public let name: String
    }

    public struct Channel: Decodable, Sendable, Hashable, Identifiable {
        public let id: String
        public let name: String
        public let number: Int
        /// live, waiting, offline_slate, off_air, revoked.
        public let state: String
        public let now: GuestProgramme?
        public let next: GuestProgramme?
    }

    public let publisher: Publisher
    public let channels: [Channel]
}

/// `POST /api/v1/play`: a session and its master playlist on the edge.
public struct GuestPlay: Sendable, Equatable {
    public let session: String
    public let beatKey: String
    public let masterURL: URL
    public let refreshAfter: Int
    public let state: GuestViewerState
    /// The pass renewed on the way (`X-Maktaba-Pass`), to keep.
    public let renewedPass: String?
}

/// `POST /api/v1/play/<session>/beat`: the channel's state, and a new playlist address when the
/// viewer token was refreshed.
public struct GuestBeat: Sendable, Equatable {
    public let state: GuestViewerState
    public let masterURL: URL?
    public let refreshAfter: Int
}

/// `POST /api/v1/pair`: a TV's pairing (10 minutes). The QR code carries `url` (the nonce
/// only); `pollKey` stays on the TV.
public struct GuestPairing: Decodable, Sendable, Equatable {
    public let nonce: String
    public let pollKey: String
    /// Shown beside the QR code; the phone's page shows it too.
    public let code: String
    public let url: String
    public let expiresAt: Date?

    enum CodingKeys: String, CodingKey { case nonce, poll_key, code, url, expires_at }

    public init(nonce: String, pollKey: String, code: String, url: String, expiresAt: Date?) {
        self.nonce = nonce
        self.pollKey = pollKey
        self.code = code
        self.url = url
        self.expiresAt = expiresAt
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        nonce = try c.decode(String.self, forKey: .nonce)
        pollKey = try c.decode(String.self, forKey: .poll_key)
        code = try c.decode(String.self, forKey: .code)
        url = try c.decode(String.self, forKey: .url)
        expiresAt = (try? c.decode(String.self, forKey: .expires_at)).flatMap(homeTime)
    }
}

/// One answer of the TV's long-poll.
public enum GuestPairPoll: Sendable, Equatable {
    /// Nothing yet: ask again.
    case waiting
    /// The phone sent a credential.
    case paired(GuestPass)
    /// The pairing ended (10 minutes, or taken): show a new code.
    case gone
}

/// What a guest typed or opened: an invite link (`…/i/<token>`), a bare invite, or nothing.
public enum GuestInput {
    /// The invite token in a link or as typed; nil when it is not one.
    public static func invite(_ text: String) -> String? {
        let v = text.trimmingCharacters(in: .whitespacesAndNewlines)
        if let r = v.range(of: "/i/") {
            let rest = v[r.upperBound...].prefix { !"/?#".contains($0) && !$0.isWhitespace }
            return String(rest).removingPercentEncoding.flatMap { $0.hasPrefix("mkbn1.") ? $0 : nil }
        }
        return v.hasPrefix("mkbn1.") && !v.contains(" ") ? v : nil
    }

    /// The invite in a link the app was opened with (`https://watch.maktaba.tv/i/<token>`, or
    /// `maktaba://watch/i/<token>`), nil for any other link.
    public static func invite(in url: URL) -> String? {
        let isWatch = (url.scheme == "https" && url.host() == GuestEdge.production.host())
            || (url.scheme == "maktaba" && url.host() == "watch")
        return isWatch ? invite(url.absoluteString) : nil
    }

    /// A short code as the edge wants it (`MK-` and four letters or digits), from what was
    /// typed ("mk 7q2x", "7Q2X"); nil when it cannot be one.
    public static func code(_ text: String) -> String? {
        var s = text.uppercased().filter { $0.isLetter || $0.isNumber }
        if s.hasPrefix("MK"), s.count == 6 { s.removeFirst(2) }
        guard s.count == 4, s.allSatisfy({ ($0.isASCII && $0.isLetter) || ($0.isASCII && $0.isNumber) }) else { return nil }
        return "MK-" + s
    }

    /// The publisher's handle as typed (its watch address works too: the part after the language).
    public static func publisher(_ text: String) -> String? {
        var v = text.trimmingCharacters(in: .whitespacesAndNewlines)
        if let u = URL(string: v), u.host() != nil {
            let parts = u.path().split(separator: "/").map(String.init)
            let rest = parts.first.map { ["ar", "en"].contains($0) } == true ? Array(parts.dropFirst()) : parts
            v = rest.first ?? ""
        }
        guard (1...64).contains(v.count), v.allSatisfy({ $0.isASCII && ($0.isLetter || $0.isNumber || $0 == "-") }) else { return nil }
        return v
    }
}

// MARK: - the client

/// The edge's guest API (`docs/broadcast-edge.md`, "Viewer access" and "Guest viewers").
public struct GuestClient: Sendable {
    public let watch: URL
    let session: URLSession
    private static let log = Logger(subsystem: "com.hamzalabs.maktaba", category: "guest")

    public init(watch: URL = GuestEdge.watch, session: URLSession = .shared) {
        self.watch = watch
        self.session = session
    }

    /// Redeems an invite (link or token): one device use; the pass this device keeps.
    public func redeem(invite: String) async throws -> GuestPass {
        try pass(try await call("POST", "/api/v1/redeem", body: ["invite": invite]))
    }

    /// Redeems the publisher's short code (`MK-XXXX`).
    public func redeem(publisher: String, code: String) async throws -> GuestPass {
        try pass(try await call("POST", "/api/v1/redeem", body: ["publisher": publisher, "code": code]))
    }

    public func lineup(_ pass: GuestPass) async throws -> GuestLineup {
        let (data, _) = try await call("GET", "/api/v1/lineup", query: ["publisher": pass.publisher], bearer: pass.credential)
        return try decode(GuestLineup.self, data)
    }

    public func guide(_ pass: GuestPass, channel: String) async throws -> [GuestProgramme] {
        struct Doc: Decodable { let programmes: [GuestProgramme] }
        let (data, _) = try await call("GET", "/api/v1/guide", query: ["publisher": pass.publisher, "channel": channel],
                                       bearer: pass.credential)
        return try decode(Doc.self, data).programmes
    }

    public func play(_ pass: GuestPass, channel: String) async throws -> GuestPlay {
        let (data, http) = try await call("POST", "/api/v1/play", body: ["publisher": pass.publisher, "channel": channel],
                                          bearer: pass.credential)
        let j = try object(data)
        guard let session = j["session_id"] as? String, let key = j["beat_key"] as? String,
              let master = (j["master_url"] as? String).flatMap(URL.init(string:)) else { throw GuestError(status: 502, code: "bad_answer") }
        return GuestPlay(session: session, beatKey: key, masterURL: master, refreshAfter: j["refresh_after"] as? Int ?? 300,
                         state: GuestViewerState(status: 200, channelState: j["channel_state"] as? String),
                         renewedPass: http.value(forHTTPHeaderField: "X-Maktaba-Pass")?.nilIfBlank)
    }

    public func beat(_ pass: GuestPass, session: String, beatKey: String) async throws -> GuestBeat {
        let (data, _) = try await call("POST", "/api/v1/play/\(session)/beat",
                                       body: ["publisher": pass.publisher, "beat_key": beatKey], bearer: pass.credential)
        let j = try object(data)
        return GuestBeat(state: GuestViewerState(status: 200, channelState: j["channel_state"] as? String),
                         masterURL: (j["master_url"] as? String).flatMap(URL.init(string:)),
                         refreshAfter: j["refresh_after"] as? Int ?? 300)
    }

    /// A TV's new pairing: the QR code and the code to show.
    public func pair() async throws -> GuestPairing {
        let (data, _) = try await call("POST", "/api/v1/pair", body: [:])
        return try decode(GuestPairing.self, data)
    }

    /// One long-poll of a pairing (the edge holds it up to `wait` seconds).
    public func poll(_ pairing: GuestPairing, wait: Int = 25) async throws -> GuestPairPoll {
        do {
            let (data, http) = try await call("GET", "/api/v1/pair/\(pairing.nonce)", query: ["wait": String(wait)],
                                              headers: ["X-Maktaba-Pair-Key": pairing.pollKey], timeout: TimeInterval(wait + 15))
            if http.statusCode == 204 || data.isEmpty { return .waiting }
            let j = try object(data)
            guard let pub = j["publisher"] as? String, let cred = j["credential"] as? String else { return .waiting }
            let kind = GuestPass.Kind(rawValue: j["kind"] as? String ?? "") ?? (cred.hasPrefix("mkbo1.") ? .open : .pass)
            let exp = (j["expires_at"] as? String).flatMap(homeTime) ?? GuestPass.claimedExpiry(cred) ?? Date().addingTimeInterval(3600)
            return .paired(GuestPass(publisher: pub, credential: cred, kind: kind, channels: j["channels"] as? [String] ?? [],
                                     expiresAt: exp))
        } catch let e as GuestError where e.status == 410 {
            return .gone
        }
    }

    // MARK: plumbing

    private func pass(_ answer: (Data, HTTPURLResponse)) throws -> GuestPass {
        let j = try object(answer.0)
        guard let pub = j["publisher"] as? String, let cred = j["pass"] as? String else { throw GuestError(status: 502, code: "bad_answer") }
        let exp = (j["expires_at"] as? String).flatMap(homeTime) ?? GuestPass.claimedExpiry(cred) ?? Date().addingTimeInterval(86400)
        return GuestPass(publisher: pub, credential: cred, kind: .pass, channels: j["channels"] as? [String] ?? [], expiresAt: exp)
    }

    private func object(_ data: Data) throws -> [String: Any] {
        guard let j = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else { throw GuestError(status: 502, code: "bad_answer") }
        return j
    }

    private func decode<T: Decodable>(_ type: T.Type, _ data: Data) throws -> T {
        do { return try JSONDecoder().decode(type, from: data) } catch { throw GuestError(status: 502, code: "bad_answer") }
    }

    private func call(_ method: String, _ path: String, query: [String: String] = [:], body: [String: String]? = nil,
                      bearer: String? = nil, headers: [String: String] = [:], timeout: TimeInterval = 20) async throws -> (Data, HTTPURLResponse) {
        var c = URLComponents(url: watch, resolvingAgainstBaseURL: false)!
        c.path = path
        if !query.isEmpty { c.queryItems = query.sorted { $0.key < $1.key }.map { URLQueryItem(name: $0.key, value: $0.value) } }
        var r = URLRequest(url: c.url!, timeoutInterval: timeout)
        r.httpMethod = method
        r.setValue("application/json", forHTTPHeaderField: "Accept")
        // the edge answers its messages in the app's language
        r.setValue(AppLanguage.isRightToLeft ? "ar" : "en", forHTTPHeaderField: "Accept-Language")
        if let bearer { r.setValue("Bearer \(bearer)", forHTTPHeaderField: "Authorization") }
        for (k, v) in headers { r.setValue(v, forHTTPHeaderField: k) }
        if let body {
            r.setValue("application/json", forHTTPHeaderField: "Content-Type")
            r.httpBody = try JSONSerialization.data(withJSONObject: body)
        }
        let data: Data, response: URLResponse
        do {
            (data, response) = try await session.data(for: r)
        } catch {
            if error is CancellationError || (error as? URLError)?.code == .cancelled { throw CancellationError() }
            Self.log.notice("guest \(method, privacy: .public) \(path, privacy: .public): \(error.localizedDescription, privacy: .public)")
            throw GuestError.offline
        }
        guard let http = response as? HTTPURLResponse else { throw GuestError.offline }
        guard (200..<300).contains(http.statusCode) else {
            let code = (try? JSONSerialization.jsonObject(with: data) as? [String: Any])?["error"] as? String ?? ""
            Self.log.notice("guest \(method, privacy: .public) \(path, privacy: .public): \(http.statusCode) \(code, privacy: .public)")
            throw GuestError(status: http.statusCode, code: code)
        }
        return (data, http)
    }
}

// MARK: - the token in the path

/// The edge address a guest's channel plays from, holding the newest viewer token
/// (`StreamProxy(live:)` asks it for each request). Made from a play's `master_url`
/// (`https://live.maktaba.tv/<token>/<channel>/master.m3u8`).
public final class GuestLive: @unchecked Sendable {
    public let channel: String
    private let origin: String
    private let lock = NSLock()
    private var token: String

    public init?(master: URL) {
        let parts = master.path().split(separator: "/").map(String.init)
        guard parts.count == 3, parts[2] == "master.m3u8", let scheme = master.scheme, let host = master.host() else { return nil }
        token = parts[0]
        channel = parts[1]
        origin = "\(scheme)://\(host)" + (master.port.map { ":\($0)" } ?? "")
    }

    /// A new viewer token from the heartbeat (the same channel's master playlist); false for
    /// an address of another channel or edge.
    @discardableResult
    public func update(_ master: URL) -> Bool {
        guard let next = GuestLive(master: master), next.channel == channel, next.origin == origin else { return false }
        lock.withLock { token = next.currentToken }
        return true
    }

    var currentToken: String { lock.withLock { token } }

    /// The edge's address for a file under the channel (`master.m3u8`, `480p/index.m3u8`).
    func remote(_ rest: String) -> URL? {
        URL(string: "\(origin)/\(currentToken)/\(channel)/\(rest)")
    }
}
