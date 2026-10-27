import Foundation
import os

public enum APIError: LocalizedError, Equatable {
    /// No answer came (the URLSession failure's code).
    case transport(URLError.Code)
    /// The home answered this status, with this body (a problem+json, or a proxy's page):
    /// for `problem` and the logs, never for a screen.
    case status(Int, String)
    /// An answer this app could not read (the decoder's words, for the logs).
    case decoding(String)
    case signedOut

    /// What to tell a person: the kind's sentence (`AppError`), never the home's own body.
    public var errorDescription: String? {
        AppError(self).message()
    }

    /// The URLSession failure's code (`.unknown` for anything else).
    static func transport(_ error: Error) -> APIError {
        .transport((error as? URLError)?.code ?? .unknown)
    }
}

/// The REST client for one home: base URL, Bearer tokens, one refresh on a 401.
public actor APIClient {
    public nonisolated let home: URL
    private let tokens: TokenStore
    private let session: URLSession
    private var refreshing: Task<String, Error>?

    public init(home: URL, tokens: TokenStore, session: URLSession = .shared) {
        self.home = home
        self.tokens = tokens
        self.session = session
    }

    // MARK: sign-in

    /// Signs in with a username and password. `X-Maktaba-Client: native` asks the home
    /// for the tokens in the body (a browser gets cookies instead).
    public func signIn(username: String, password: String) async throws -> User? {
        let t: AuthTokens = try await send(
            request("POST", "/api/auth/login", body: ["username": username, "password": password]),
            authorized: false)
        tokens.save(t)
        return t.user
    }

    public func me() async throws -> User {
        try await whoami().user
    }

    /// `GET /api/auth/me` with the home's name.
    public func whoami() async throws -> Me {
        try await get("/api/auth/me")
    }

    // MARK: viewer

    public func libraries() async throws -> [Library] {
        let page: Page<Library> = try await get("/api/libraries")
        return page.items
    }

    public func videos(library: String?, query: String? = nil, cursor: String? = nil) async throws -> Page<Video> {
        try await get("/api/videos", query: Self.videoListQuery(library: library, query: query, cursor: cursor))
    }

    /// The video list's query: the newest first. The home's sorts are updated_at,
    /// created_at or title (it answers 400 to anything else).
    static func videoListQuery(library: String?, query: String?, cursor: String?) -> [String: String] {
        var q = ["limit": "60", "sort": "created_at"]
        if let library { q["library"] = library }
        if let query, !query.isEmpty { q["q"] = query }
        if let cursor { q["cursor"] = cursor }
        return q
    }

    public func video(_ id: String) async throws -> Video {
        try await get("/api/videos/\(id)")
    }

    /// Opens a stream session (the home picks direct, remux or transcode).
    public func openStream(video: String, startSec: Int = 0) async throws -> StreamSession {
        var body: [String: Any] = ["video_id": video]
        if startSec > 0 { body["start_sec"] = startSec }
        return try await send(request("POST", "/api/stream/sessions", body: body))
    }

    /// The header the player sends with every `/stream/` request (the web port lets only
    /// a signed-in caller through), with an access token good for at least `minLife`,
    /// and when that token ends. AVPlayer cannot refresh it: reopen before `until`.
    public func playbackHeaders(minLife: TimeInterval = 120) async throws -> (headers: [String: String], until: Date) {
        var access = tokens.access
        if access.flatMap(Self.expiry(of:)).map({ $0.timeIntervalSinceNow < minLife }) ?? true {
            access = try await refreshOnce()
        }
        guard let access, let until = Self.expiry(of: access) else { throw APIError.signedOut }
        return (["Authorization": "Bearer \(access)"], until)
    }

    /// Waits until an HLS stream can start: a transcode writes its rendition playlist a
    /// moment after the session opens, while the master playlist names it at once. hls.js
    /// retries the 404 in between; AVPlayer gives up, so the player waits here first.
    public func waitUntilPlayable(_ url: URL, headers: [String: String], timeout: Duration = .seconds(30)) async throws {
        guard url.path().hasSuffix(".m3u8") else { return }
        func fetch(_ u: URL) async throws -> (Int, Data) {
            var r = URLRequest(url: u)
            // the token goes only to the home it was issued by
            if SameOriginRedirects.sameOrigin(u, home) {
                headers.forEach { r.setValue($1, forHTTPHeaderField: $0) }
            }
            do {
                let (d, resp) = try await session.data(for: r, delegate: SameOriginRedirects.shared)
                return ((resp as? HTTPURLResponse)?.statusCode ?? 0, d)
            } catch {
                throw APIError.transport(error)
            }
        }
        let (code, master) = try await fetch(url)
        guard code == 200 else { throw APIError.status(code, String(decoding: master.prefix(300), as: UTF8.self)) }
        guard let first = Self.firstURI(inPlaylist: String(decoding: master, as: UTF8.self)),
              let rendition = URL(string: first, relativeTo: url)?.absoluteURL else { return }
        let deadline = ContinuousClock.now + timeout
        while true {
            let (code, body) = try await fetch(rendition)
            if code == 200 { return }
            guard code == 404, ContinuousClock.now < deadline else {
                throw APIError.status(code, String(decoding: body.prefix(300), as: UTF8.self))
            }
            try await Task.sleep(for: .milliseconds(500))
        }
    }

    /// The first URI line of an HLS playlist (its first variant or segment).
    static func firstURI(inPlaylist text: String) -> String? {
        text.split(whereSeparator: \.isNewline)
            .map { $0.trimmingCharacters(in: .whitespaces) }
            .first { !$0.isEmpty && !$0.hasPrefix("#") }
    }

    /// The `exp` of a JWT (nil when it is not one).
    static func expiry(of jwt: String) -> Date? {
        let parts = jwt.split(separator: ".")
        guard parts.count == 3 else { return nil }
        var b64 = parts[1].replacingOccurrences(of: "-", with: "+").replacingOccurrences(of: "_", with: "/")
        b64 += String(repeating: "=", count: (4 - b64.count % 4) % 4)
        guard let data = Data(base64Encoded: b64),
              let claims = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              let exp = claims["exp"] as? Double else { return nil }
        return Date(timeIntervalSince1970: exp)
    }

    /// Best effort (the home reaps an idle session itself): a failure is logged by `sendRaw`.
    public func closeStream(_ session: String) async {
        _ = try? await sendRaw(request("DELETE", "/api/stream/sessions/\(session)"))
    }

    /// Best effort (the next report carries the position): a failure is logged by `sendRaw`.
    public func reportProgress(session: String, positionSec: Int, completed: Bool = false) async {
        var body: [String: Any] = ["position_sec": positionSec]
        if completed { body["completed"] = true }
        _ = try? await sendRaw(request("POST", "/api/stream/sessions/\(session)/progress", body: body))
    }

    /// The video's picture, fetched with the Bearer token (the route needs one).
    public func poster(video: String) async throws -> Data {
        try await sendRaw(request("GET", "/api/videos/\(video)/poster"))
    }

    /// The home screen's rails (`GET /api/recommendations`): continue watching first.
    public func recommendations(surface: String = "tv-home") async throws -> [Rail] {
        let r: Recommendations = try await get("/api/recommendations", query: ["surface": surface, "limit": "20"])
        return r.rails
    }

    // MARK: sign-in by code (a TV, a phone, a computer)

    /// Asks the home for a code to show (`POST /api/pairing/device`), naming this device
    /// (`label`, shown to the person who approves it). nil: this home signs in by password
    /// only (one from before codes answers 401 or 404).
    public func startDevicePairing(label: String? = nil) async throws -> DevicePairing? {
        let body: [String: Any]? = label.flatMap { $0.isEmpty ? nil : ["device_label": $0] }
        do {
            return try await send(request("POST", "/api/pairing/device", body: body), authorized: false)
        } catch let APIError.status(code, _) where code == 401 || code == 404 || code == 405 {
            return nil
        }
    }

    /// What is asking to be signed in with `code` (`GET /api/pairing/device/{code}`, signed
    /// in), for the person to check before approving it.
    public func pendingDevice(code: String) async throws -> DeviceLookup {
        let compact = code.filter { $0 != "-" }
        do {
            let d: PendingDevice = try await get("/api/pairing/device/\(Self.segment(compact))")
            return .found(d)
        } catch let APIError.status(status, body) where status == 404 || status == 405 || status == 501 {
            // the home's own 404 is a problem+json; a home from before the lookup has no
            // such route (its router's plain 404 or 405)
            let problem = (try? JSONSerialization.jsonObject(with: Data(body.utf8))) is [String: Any]
            return status == 404 && problem ? .notFound : .unknownOnThisHome
        }
    }

    /// Signs the device showing `code` in as the person signed in here
    /// (`POST /api/pairing/approve`).
    public func approveDevice(code: String) async throws {
        _ = try await sendRaw(request("POST", "/api/pairing/approve", body: ["code": code.filter { $0 != "-" }]))
    }

    /// Asks whether the code was approved; once it is, this device is signed in as the
    /// person who approved it (the tokens are kept, as after `signIn`).
    public func collectDevicePairing(_ p: DevicePairing, label: String) async throws -> DevicePairing.State {
        var r = request("POST", "/api/pairing/device/token", body: ["device_code": p.deviceCode, "device_label": label])
        r.timeoutInterval = 15
        let data: Data, response: URLResponse
        do {
            (data, response) = try await session.data(for: r, delegate: SameOriginRedirects.shared)
        } catch {
            throw APIError.transport(error)
        }
        switch (response as? HTTPURLResponse)?.statusCode ?? 0 {
        case 200:
            do {
                tokens.save(try JSONDecoder().decode(AuthTokens.self, from: data))
            } catch {
                throw APIError.decoding(String(describing: error))
            }
            return .approved
        case 202, 429:
            return .waiting
        case 409, 410:
            return .expired
        case let code:
            throw APIError.status(code, String(decoding: data.prefix(4096), as: UTF8.self))
        }
    }

    // MARK: remote (home admins; the home refuses anyone else)

    public func needsAttention() async throws -> [FailureGroup] {
        let r: FailureGroups = try await get("/api/issues")
        return r.groups
    }

    public func retry(stage: String, cause: String) async throws {
        _ = try await sendRaw(request("POST", "/api/queues/\(Self.segment(stage))/retry", body: ["cause": cause]))
    }

    /// The processing queue: every stage, its counts, paused or not.
    public func queues() async throws -> QueueSnapshot {
        try await get("/api/queues")
    }

    /// One stage's failed jobs, grouped by cause.
    public func failures(stage: String) async throws -> [FailureGroup] {
        let r: FailureGroups = try await get("/api/queues/\(Self.segment(stage))/failures")
        return r.groups
    }

    /// Pauses or resumes one stage (its running jobs finish; nothing new starts).
    public func setPaused(stage: String, _ paused: Bool) async throws {
        _ = try await sendRaw(request("POST", "/api/queues/\(Self.segment(stage))/\(paused ? "pause" : "resume")"))
    }

    /// Pauses or resumes every stage at once.
    public func setPausedAll(_ paused: Bool) async throws {
        _ = try await sendRaw(request("POST", paused ? "/api/queues/pause-all" : "/api/queues/resume-all"))
    }

    /// Looks for new and changed files in a library (the home answers 202 and scans).
    public func scan(library: String) async throws {
        _ = try await sendRaw(request("POST", "/api/libraries/\(Self.segment(library))/scan"))
    }

    /// The home and its machines.
    public func cluster() async throws -> ClusterOverview {
        try await get("/api/cluster")
    }

    /// Lets machines ask to join for the next hour, or stops them.
    public func setAcceptJoins(_ open: Bool) async throws {
        _ = try await sendRaw(request("PUT", "/api/cluster/settings", body: ["accept_joins": open]))
    }

    /// Approves a machine's request with the code it shows (a wrong code answers 400
    /// `wrong_code` with `attempts_left`; see `APIError.problem`).
    public func approve(machine: String, code: String) async throws {
        _ = try await sendRaw(request("POST", "/api/cluster/nodes/\(Self.segment(machine))/approve", body: ["code": code]))
    }

    public func reject(machine: String) async throws {
        _ = try await sendRaw(request("POST", "/api/cluster/nodes/\(Self.segment(machine))/reject"))
    }

    /// Lets an approved machine go: it stops working for the home at once.
    public func revoke(machine: String) async throws {
        _ = try await sendRaw(request("POST", "/api/cluster/nodes/\(Self.segment(machine))/revoke"))
    }

    // MARK: plumbing

    /// An id or stage name as one path segment (a "/" in it cannot reach another route).
    static func segment(_ s: String) -> String {
        s.addingPercentEncoding(withAllowedCharacters: .alphanumerics.union(CharacterSet(charactersIn: "-_."))) ?? s
    }

    func get<T: Decodable>(_ path: String, query: [String: String] = [:]) async throws -> T {
        try await send(request("GET", path, query: query))
    }

    func request(_ method: String, _ path: String, query: [String: String] = [:], body: [String: Any]? = nil) -> URLRequest {
        let url = home.appending(path: path)
        var c = URLComponents(url: url, resolvingAgainstBaseURL: false)
        if !query.isEmpty {
            c?.queryItems = query.sorted { $0.key < $1.key }.map { URLQueryItem(name: $0.key, value: $0.value) }
        }
        var r = URLRequest(url: c?.url ?? url)
        r.httpMethod = method
        r.setValue("application/json", forHTTPHeaderField: "Accept")
        r.setValue(ClientHeader.value, forHTTPHeaderField: ClientHeader.name)
        if let body {
            do {
                r.httpBody = try JSONSerialization.data(withJSONObject: body)
            } catch {
                // a body made in this file from strings, numbers and bools: a bug if it fails
                Log.network.fault("\(method, privacy: .public) \(Log.route(path), privacy: .public): body not JSON")
            }
            r.setValue("application/json", forHTTPHeaderField: "Content-Type")
        }
        return r
    }

    private func send<T: Decodable>(_ r: URLRequest, authorized: Bool = true) async throws -> T {
        let data = try await sendRaw(r, authorized: authorized)
        do {
            return try JSONDecoder.home.decode(T.self, from: data)
        } catch {
            let what = String(describing: error)
            Log.network.error("\(Self.describe(r), privacy: .public): answer not \(String(describing: T.self), privacy: .public): \(what, privacy: .private)")
            throw APIError.decoding(what)
        }
    }

    /// A request for the logs: its method and route (ids taken out), never its query.
    nonisolated static func describe(_ r: URLRequest) -> String {
        "\(r.httpMethod ?? "GET") \(Log.route(r.url?.path() ?? ""))"
    }

    private func sendRaw(_ r: URLRequest, authorized: Bool = true, retried: Bool = false) async throws -> Data {
        var r = r
        if authorized, let access = tokens.access {
            r.setValue("Bearer \(access)", forHTTPHeaderField: "Authorization")
        }
        let data: Data, response: URLResponse
        do {
            (data, response) = try await session.data(for: r, delegate: SameOriginRedirects.shared)
        } catch {
            let e = APIError.transport(error)
            if case let .transport(c) = e, c != .cancelled {
                Log.network.error("\(Self.describe(r), privacy: .public): no answer (URLError \(c.rawValue, privacy: .public))")
            }
            throw e
        }
        let code = (response as? HTTPURLResponse)?.statusCode ?? 0
        if code == 401, authorized, !retried, tokens.refresh != nil {
            _ = try await refreshOnce()
            return try await sendRaw(r, authorized: true, retried: true)
        }
        if code == 401, authorized {
            Log.session.notice("\(Self.describe(r), privacy: .public): 401 after a refresh: signed out")
            tokens.clear()
            throw APIError.signedOut
        }
        guard (200..<300).contains(code) else {
            let e = APIError.status(code, String(decoding: data.prefix(4096), as: UTF8.self))
            let p = e.problem
            Log.network.error("\(Self.describe(r), privacy: .public) -> \(code, privacy: .public) code=\(p?.code ?? "-", privacy: .public) type=\(AppError.problemSlug(p?.type), privacy: .public) detail=\(p?.detail ?? "", privacy: .private)")
            throw e
        }
        return data
    }

    /// One refresh at a time: requests that 401 together wait for the same one.
    private func refreshOnce() async throws -> String {
        if let refreshing { return try await refreshing.value }
        let task = Task<String, Error> {
            guard let refresh = tokens.refresh else { throw APIError.signedOut }
            let t: AuthTokens = try await send(
                request("POST", "/api/auth/refresh", body: ["refresh_token": refresh]), authorized: false)
            tokens.save(t)
            return t.accessToken
        }
        refreshing = task
        defer { refreshing = nil }
        do {
            return try await task.value
        } catch let APIError.status(code, _) where (400..<500).contains(code) && code != 408 && code != 429 {
            // the home turned the refresh token down: this sign-in is over
            Log.session.notice("refresh refused (\(code, privacy: .public)): signed out")
            tokens.clear()
            throw APIError.signedOut
        } catch APIError.signedOut {
            tokens.clear()
            throw APIError.signedOut
        } catch {
            // the home could not be reached, or failed: the sign-in stays, for the next try
            Log.failure("token refresh", error, logger: Log.session)
            throw error
        }
    }
}
