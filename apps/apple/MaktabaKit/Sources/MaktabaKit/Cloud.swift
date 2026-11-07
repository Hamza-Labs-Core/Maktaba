import Foundation

// Epic 42 story 42.7: signing in to a home through Maktaba Cloud. The person signs in to the
// cloud (email, password), the cloud lists the homes that account reaches, and for a home that
// linked the account the cloud signs a one-use assertion (a minute) that the home trades for
// its own tokens (`POST /api/auth/cloud`). The cloud account grants nothing by itself: the home
// decides, from the linked home user, what the app may do.
//
// The cloud's access token lives in this process only: never the Keychain, never the defaults,
// and no cookie jar (the cloud's refresh cookie is not kept). When it is gone the person signs
// in to the cloud again; the homes' own tokens are unaffected.

// MARK: - Where the cloud is

/// The cloud's API address: `https://api.maktaba.tv`, or the one the person set (a test or
/// self-hosted cloud), remembered in the defaults (an address, nothing secret).
public enum CloudAddress {
    public static let standard = URL(string: "https://api.maktaba.tv")!
    static let key = "cloud.api"

    public static func current(_ defaults: UserDefaults = .standard) -> URL {
        defaults.string(forKey: key).flatMap(Home.normalizeBase) ?? standard
    }

    /// Remembers `text` (nil, empty or the standard address: back to the standard). false:
    /// not an address.
    @discardableResult
    public static func set(_ text: String?, in defaults: UserDefaults = .standard) -> Bool {
        let s = text?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        guard !s.isEmpty else {
            defaults.removeObject(forKey: key)
            return true
        }
        guard let url = Home.normalizeBase(s) else { return false }
        if url == standard { defaults.removeObject(forKey: key) } else { defaults.set(url.absoluteString, forKey: key) }
        return true
    }
}

// MARK: - What the cloud says

/// The cloud account signed in (`user` of `POST /v1/auth/login`).
public struct CloudUser: Decodable, Equatable, Sendable {
    public let id: String
    public let email: String
}

/// One way to reach a home (story 50.13): `local`, `direct`, `relay_hint` or `relay`.
public struct CloudConnection: Codable, Equatable, Sendable {
    public let url: URL
    public let kind: String
    public init(url: URL, kind: String) { self.url = url; self.kind = kind }

    /// The order apps try (story 50.13 §2): local, direct, relay hint, relay.
    public static let order = ["local", "direct", "relay_hint", "relay"]

    /// `cs` in that order; a kind this build doesn't know is left out.
    public static func ordered(_ cs: [CloudConnection]) -> [CloudConnection] {
        cs.filter { order.contains($0.kind) }
            .enumerated()
            .sorted { (order.firstIndex(of: $0.element.kind)!, $0.offset) < (order.firstIndex(of: $1.element.kind)!, $1.offset) }
            .map(\.element)
    }
}

/// A home the cloud account reaches (`GET /v1/homes`).
public struct CloudHome: Codable, Identifiable, Equatable, Sendable {
    public var id: String { serverID }
    /// The home's id in the cloud (not the home's own instance id).
    public let serverID: String
    public let name: String
    public let slug: String
    /// The home through the relay (`https://<slug>.<relay host>`).
    public let relayURL: URL?
    /// The home's direct address, when it reported one (tried first).
    public let directURL: URL?
    public let online: Bool
    /// The home linked this account to one of its users: signing in works.
    public let linked: Bool
    /// This account holds the home's cloud registration, which is no account in the home.
    public let owned: Bool
    /// Every way to reach it, in the order apps try (story 50.13); from `direct_url` and
    /// `relay_url` when the cloud is older than the list.
    public let connections: [CloudConnection]

    enum CodingKeys: String, CodingKey {
        case name, slug, online, linked, owned, connections
        case serverID = "server_id"
        case relayURL = "relay_url"
        case directURL = "direct_url"
    }

    public init(serverID: String, name: String, slug: String = "", relayURL: URL?, directURL: URL? = nil,
                online: Bool = true, linked: Bool, owned: Bool = false, connections: [CloudConnection]? = nil) {
        self.serverID = serverID
        self.name = name
        self.slug = slug
        self.relayURL = relayURL
        self.directURL = directURL
        self.online = online
        self.linked = linked
        self.owned = owned
        self.connections = connections.map(CloudConnection.ordered) ?? Self.fromOldFields(directURL, relayURL)
    }

    static func fromOldFields(_ direct: URL?, _ relay: URL?) -> [CloudConnection] {
        [direct.map { CloudConnection(url: $0, kind: "direct") }, relay.map { CloudConnection(url: $0, kind: "relay") }]
            .compactMap { $0 }
    }

    public func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(serverID, forKey: .serverID)
        try c.encode(name, forKey: .name)
        try c.encode(slug, forKey: .slug)
        try c.encodeIfPresent(relayURL?.absoluteString, forKey: .relayURL)
        try c.encodeIfPresent(directURL?.absoluteString, forKey: .directURL)
        try c.encode(online, forKey: .online)
        try c.encode(linked, forKey: .linked)
        try c.encode(owned, forKey: .owned)
        try c.encode(connections, forKey: .connections)
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        serverID = try c.decode(String.self, forKey: .serverID)
        slug = try c.decodeIfPresent(String.self, forKey: .slug) ?? ""
        let n = try c.decodeIfPresent(String.self, forKey: .name)?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        name = n.isEmpty ? slug : n
        relayURL = try c.decodeIfPresent(String.self, forKey: .relayURL).flatMap(Home.normalizeBase)
        directURL = try c.decodeIfPresent(String.self, forKey: .directURL).flatMap(Home.normalizeBase)
        online = try c.decodeIfPresent(Bool.self, forKey: .online) ?? false
        linked = try c.decodeIfPresent(Bool.self, forKey: .linked) ?? false
        owned = try c.decodeIfPresent(Bool.self, forKey: .owned) ?? false
        // each address normalised as the old fields are; one that isn't an address is dropped
        let given = (try? c.decodeIfPresent([RawConnection].self, forKey: .connections)) ?? nil
        connections = given.map { list in
            CloudConnection.ordered(list.compactMap { r in Home.normalizeBase(r.url).map { CloudConnection(url: $0, kind: r.kind) } })
        } ?? Self.fromOldFields(directURL, relayURL)
    }

    private struct RawConnection: Decodable {
        let url: String
        let kind: String
    }

    /// The addresses the cloud gives for this home, in the order apps try them (local,
    /// direct, relay hint, relay). Only these are handed an assertion for it: the cloud
    /// vouches for them, the home's own list does not.
    public var addresses: [URL] {
        var out: [URL] = []
        for u in connections.map(\.url) where !out.contains(u) { out.append(u) }
        return out
    }

    /// Whether a sign-in through the cloud is offered (`linked`; owning it is not enough).
    public var canSignIn: Bool { linked && !addresses.isEmpty }
}

struct CloudHomes: Decodable {
    let homes: [CloudHome]
}

/// `POST /v1/homes/{server_id}/assertion`: one use, a minute.
public struct CloudAssertion: Decodable, Sendable {
    public let assertion: String
    public let expiresAt: String?
    public let serverID: String?
    public let relayURL: String?

    enum CodingKeys: String, CodingKey {
        case assertion
        case expiresAt = "expires_at"
        case serverID = "server_id"
        case relayURL = "relay_url"
    }
}

struct CloudLogin: Decodable {
    let accessToken: String
    let expiresIn: Int?
    let user: CloudUser

    enum CodingKeys: String, CodingKey {
        case user
        case accessToken = "access_token"
        case expiresIn = "expires_in"
    }
}

/// Why the cloud said no.
public enum CloudError: LocalizedError, Equatable {
    /// The email or password is wrong (401 at sign-in).
    case wrongCredentials
    /// Too many tries: wait (seconds, when the cloud said).
    case tooManyAttempts(retryAfter: Int?)
    /// Not signed in to the cloud (never, expired, or the cloud ended it): sign in again.
    case signedOut
    /// The account may not do this (403: email not confirmed, suspended), with the cloud's words.
    case refused(code: String, message: String)
    /// The cloud does not give this account that home (404 for an assertion).
    case notLinked
    case transport(String)
    case status(Int, String)
    case decoding(String)
    /// Maktaba Cloud can't be reached for a new sign-in (story 50.13): the home's own sign-in
    /// works without it.
    case cloudDown

    public var errorDescription: String? {
        switch self {
        case .wrongCredentials: "Wrong email or password."
        case .tooManyAttempts: "Too many attempts. Try again later."
        case .signedOut: "Signed out of Maktaba Cloud. Sign in again."
        case let .refused(code, message): message.isEmpty ? code : message
        case .notLinked: "This home is not linked to your Maktaba Cloud account."
        case let .transport(why): "Could not reach Maktaba Cloud: \(why)"
        case let .status(code, message): "Maktaba Cloud answered \(code): \(message.prefix(300))"
        case let .decoding(why): "Unexpected answer from Maktaba Cloud: \(why)"
        case .cloudDown: "Signing in with Maktaba Cloud isn't working right now. Sign in to your home with its username and password, or with a code."
        }
    }
}

// MARK: - The client

/// Maktaba Cloud for this app: sign in, the homes, an assertion for one home. The access
/// token is kept in memory only; a request never follows a redirect to another origin
/// (the token and the password stay with the cloud's own address).
public actor CloudClient {
    public nonisolated let base: URL
    private let session: URLSession
    private let now: @Sendable () -> Date
    private var token: String?
    private var expires: Date?
    public private(set) var user: CloudUser?

    public init(base: URL, session: URLSession? = nil, now: @escaping @Sendable () -> Date = { .now }) {
        self.base = base
        self.session = session ?? Self.ephemeral()
        self.now = now
    }

    /// A session that keeps nothing: no cookies (the cloud's refresh cookie is dropped), no cache.
    static func ephemeral() -> URLSession {
        let c = URLSessionConfiguration.ephemeral
        c.httpCookieStorage = nil
        c.httpShouldSetCookies = false
        c.httpCookieAcceptPolicy = .never
        c.urlCache = nil
        c.requestCachePolicy = .reloadIgnoringLocalCacheData
        return URLSession(configuration: c)
    }

    /// Signed in, with a token that has not ended.
    public var isSignedIn: Bool { liveToken != nil }

    private var liveToken: String? {
        guard let token else { return nil }
        if let expires, expires <= now() { return nil }
        return token
    }

    /// `POST /v1/auth/login`.
    @discardableResult
    public func signIn(email: String, password: String) async throws -> CloudUser {
        signOut()
        let (code, data, response) = try await send("POST", "/v1/auth/login", body: ["email": email, "password": password], bearer: nil)
        switch code {
        case 200:
            let l: CloudLogin = try decode(data)
            token = l.accessToken
            // a little early, so a request does not leave with a token about to end
            expires = l.expiresIn.map { now().addingTimeInterval(TimeInterval(max(0, $0 - 15))) }
            user = l.user
            return l.user
        case 401:
            throw CloudError.wrongCredentials
        default:
            throw Self.failure(code, data, response)
        }
    }

    /// Forgets the cloud token (the homes' tokens stay).
    public func signOut() {
        token = nil
        expires = nil
        user = nil
    }

    /// `GET /v1/homes`: the homes this account reaches, linked or owned.
    public func homes() async throws -> [CloudHome] {
        let data = try await authorized("GET", "/v1/homes", body: nil)
        let h: CloudHomes = try decode(data)
        return h.homes
    }

    /// `POST /v1/homes/{server_id}/assertion` for signing in: one use, a minute.
    public func signInAssertion(serverID: String) async throws -> CloudAssertion {
        do {
            let data = try await authorized("POST", "/v1/homes/\(APIClient.segment(serverID))/assertion", body: ["purpose": "signin"])
            return try decode(data)
        } catch let CloudError.status(code, _) where code == 404 {
            throw CloudError.notLinked
        }
    }

    // MARK: plumbing

    private func authorized(_ method: String, _ path: String, body: [String: String]?) async throws -> Data {
        guard let bearer = liveToken else {
            signOut()
            throw CloudError.signedOut
        }
        let (code, data, response) = try await send(method, path, body: body, bearer: bearer)
        if code == 401 {
            signOut()
            throw CloudError.signedOut
        }
        guard (200..<300).contains(code) else { throw Self.failure(code, data, response) }
        return data
    }

    private func send(_ method: String, _ path: String, body: [String: String]?, bearer: String?) async throws -> (Int, Data, HTTPURLResponse?) {
        var r = URLRequest(url: base.appending(path: path))
        r.httpMethod = method
        r.timeoutInterval = 20
        r.setValue("application/json", forHTTPHeaderField: "Accept")
        r.setValue(ClientHeader.value, forHTTPHeaderField: ClientHeader.name)
        if let bearer { r.setValue("Bearer \(bearer)", forHTTPHeaderField: "Authorization") }
        if let body {
            r.httpBody = try? JSONSerialization.data(withJSONObject: body)
            r.setValue("application/json", forHTTPHeaderField: "Content-Type")
        }
        do {
            let (data, response) = try await session.data(for: r, delegate: SameOriginOnly.shared)
            let http = response as? HTTPURLResponse
            return (http?.statusCode ?? 0, data, http)
        } catch {
            throw CloudError.transport(error.localizedDescription)
        }
    }

    private func decode<T: Decodable>(_ data: Data) throws -> T {
        do {
            return try JSONDecoder().decode(T.self, from: data)
        } catch {
            throw CloudError.decoding(String(describing: error))
        }
    }

    /// The cloud's `{"error","message"}` as a `CloudError`.
    static func failure(_ code: Int, _ data: Data, _ response: HTTPURLResponse?) -> CloudError {
        let body = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any]
        let kind = body?["error"] as? String ?? ""
        let message = body?["message"] as? String ?? ""
        switch code {
        case 429:
            let after = response?.value(forHTTPHeaderField: "Retry-After").flatMap { Int($0.trimmingCharacters(in: .whitespaces)) }
            return .tooManyAttempts(retryAfter: after)
        case 403:
            return .refused(code: kind, message: message)
        default:
            return .status(code, message.isEmpty ? (kind.isEmpty ? String(decoding: data.prefix(300), as: UTF8.self) : kind) : message)
        }
    }
}

extension CloudError {
    /// The cloud couldn't be reached (story 50.13): no answer, a server error, or an answer
    /// that isn't the cloud's JSON (a captive portal). Not a refusal, which is the cloud's word.
    public var isOutage: Bool {
        switch self {
        case .transport, .decoding, .cloudDown: true
        case let .status(code, _): code >= 500 || code == 0
        default: false
        }
    }
}

/// The cloud's home list as last answered, kept per account (story 50.13): names and addresses,
/// never a token. Shown while the cloud can't be reached; 30 days, then marked old; removed at
/// sign-out.
public struct SavedCloudHomes: Codable, Equatable, Sendable {
    public let user: CloudSavedUser
    public let savedAt: Date
    public let homes: [CloudHome]

    public static let oldAfter: TimeInterval = 30 * 24 * 3600
    static let prefix = "cloud.homes."

    public func isOld(now: Date = .now) -> Bool { now.timeIntervalSince(savedAt) > Self.oldAfter }

    static func key(_ email: String) -> String { prefix + email.trimmingCharacters(in: .whitespacesAndNewlines).lowercased() }

    public static func save(_ homes: [CloudHome], for user: CloudUser, in defaults: UserDefaults, now: Date = .now) {
        let list = SavedCloudHomes(user: CloudSavedUser(id: user.id, email: user.email), savedAt: now, homes: homes)
        if let data = try? JSONEncoder().encode(list) { defaults.set(data, forKey: key(user.email)) }
    }

    public static func load(email: String, from defaults: UserDefaults) -> SavedCloudHomes? {
        defaults.data(forKey: key(email)).flatMap { try? JSONDecoder().decode(SavedCloudHomes.self, from: $0) }
    }

    /// Every account's list (sign-out).
    public static func forgetAll(in defaults: UserDefaults) {
        for k in defaults.dictionaryRepresentation().keys where k.hasPrefix(prefix) { defaults.removeObject(forKey: k) }
    }
}

/// Who a saved list belongs to.
public struct CloudSavedUser: Codable, Equatable, Sendable {
    public let id: String
    public let email: String
}

