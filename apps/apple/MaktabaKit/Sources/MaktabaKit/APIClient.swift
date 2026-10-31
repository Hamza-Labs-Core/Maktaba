import Foundation
import os

/// What went wrong talking to the home. `errorDescription` is the one friendly sentence for
/// its `kind` (see `HomeErrors.swift`); what the home answered is kept for the Details and
/// the logs, never shown as the message.
public enum APIError: LocalizedError, Equatable {
    case transport(String)
    /// The home answered `code` with `body` (a problem+json, usually), and `retryAfter`
    /// seconds when it said when to ask again (a 429 or 503).
    case status(Int, String, retryAfter: Int? = nil)
    case decoding(String)
    case signedOut
    /// The stream's playlist was still not there when the wait ran out (`body`: the home's
    /// last answer).
    case notReady(String)

    public var errorDescription: String? {
        ErrorStrings.forPreferredLanguages().sentence(kind)
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
            authorized: false, redirects: SameOriginOnly.shared)
        tokens.save(t)
        return t.user
    }

    /// Signs in with a Maktaba Cloud sign-in assertion (`POST /api/auth/cloud`, story 42.7):
    /// the home finds the home user the cloud account is linked to and issues its own tokens,
    /// kept as after `signIn` (under this home's address only). The home refuses with 401 (no
    /// reason given), 423 (the user is locked) or 403 (the password must be changed first).
    public func signIn(assertion: String) async throws -> User? {
        let t: AuthTokens = try await send(
            request("POST", "/api/auth/cloud", body: ["assertion": assertion]),
            authorized: false, redirects: SameOriginOnly.shared)
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

    /// The video list's query: the most recently added first. A search sends no
    /// sort: the home orders it by best match, then the most recently added (an
    /// older home, by its own default). The home's sorts are updated_at,
    /// created_at or title (it answers 400 to anything else).
    static func videoListQuery(library: String?, query: String?, cursor: String?) -> [String: String] {
        var q = ["limit": "60"]
        if let query, !query.isEmpty {
            q["q"] = query
        } else {
            q["sort"] = "created_at"
        }
        if let library { q["library"] = library }
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

    /// Waits until an HLS stream can start: a transcode writes its playlists a moment after
    /// the session opens (the home answers 404 `manifest-not-found` / `rendition-not-found`
    /// until then). hls.js retries those; AVPlayer gives up, so the player waits here first:
    /// the master, then its first rendition, asked again after 0.5 s, 1 s, then every 2 s
    /// for `timeout` in all (then `APIError.notReady`). Any other answer fails at once.
    public func waitUntilPlayable(_ url: URL, headers: [String: String], timeout: Duration = .seconds(30),
                                  sleep: @Sendable (Duration) async throws -> Void = { try await Task.sleep(for: $0) }) async throws {
        guard url.path().hasSuffix(".m3u8") else { return }
        let started = ContinuousClock.now
        var waited = Duration.zero
        var delay = Duration.milliseconds(500)
        func fetch(_ u: URL) async throws -> (Int, Data, Int?) {
            var r = URLRequest(url: u)
            // the token goes only to the home it was issued by
            if SameOriginRedirects.sameOrigin(u, home) {
                headers.forEach { r.setValue($1, forHTTPHeaderField: $0) }
            }
            do {
                let (d, resp) = try await session.data(for: r, delegate: SameOriginRedirects.shared)
                let http = resp as? HTTPURLResponse
                return (http?.statusCode ?? 0, d, http.flatMap(Self.retryAfter))
            } catch {
                throw APIError.transport(error.localizedDescription)
            }
        }
        // the master first, then its first rendition
        var target = url
        var master = true
        while true {
            let (code, body, retryAfter) = try await fetch(target)
            if code == 200 {
                guard master, let first = Self.firstURI(inPlaylist: String(decoding: body, as: UTF8.self)),
                      let rendition = URL(string: first, relativeTo: url)?.absoluteURL else { return }
                target = rendition
                master = false
                continue
            }
            let text = String(decoding: body.prefix(4096), as: UTF8.self)
            let answer = APIError.status(code, text, retryAfter: retryAfter)
            // not written yet (or a plain 404 from something in between): ask again
            guard code == 404, answer.kind == .notReady || answer.problem == nil else { throw answer }
            guard waited + delay <= timeout, ContinuousClock.now - started < timeout else {
                throw APIError.notReady(text)
            }
            try await sleep(delay)
            waited += delay
            delay = min(delay * 2, .seconds(2))
        }
    }

    /// A Retry-After in seconds: a number, or an HTTP date from now.
    static func retryAfter(_ r: HTTPURLResponse) -> Int? {
        guard let v = r.value(forHTTPHeaderField: "Retry-After")?.trimmingCharacters(in: .whitespaces), !v.isEmpty else {
            return nil
        }
        if let s = Int(v) { return max(0, s) }
        let f = DateFormatter()
        f.locale = Locale(identifier: "en_US_POSIX")
        f.timeZone = TimeZone(identifier: "GMT")
        f.dateFormat = "EEE, dd MMM yyyy HH:mm:ss zzz"
        return f.date(from: v).map { max(0, Int($0.timeIntervalSinceNow.rounded(.up))) }
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
        } catch let APIError.status(code, _, _) where code == 401 || code == 404 || code == 405 {
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
        } catch let APIError.status(status, body, _) where status == 404 || status == 405 || status == 501 {
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
            throw APIError.transport(error.localizedDescription)
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

    // MARK: running work (read only)

    /// The home's updates: the plan, blocked releases, the run under way or the last, the
    /// machines' releases, a TV held.
    public func updates() async throws -> UpdatesOverview {
        try await get("/api/cluster/updates")
    }

    /// An update run with its steps and machines.
    public func updateRun(_ id: String) async throws -> UpdateRunView {
        try await get("/api/cluster/updates/runs/\(Self.segment(id))")
    }

    /// The processes the home's supervisor runs, and its recent events.
    public func processes() async throws -> ProcessesDoc {
        try await get("/api/system/processes")
    }

    /// The jobs running now, oldest first.
    public func runningJobs() async throws -> [RunningJob] {
        let r: RunningJobs = try await get("/api/queues/running")
        return r.jobs
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

    private func send<T: Decodable>(_ r: URLRequest, authorized: Bool = true,
                                    redirects: URLSessionTaskDelegate = SameOriginRedirects.shared) async throws -> T {
        let data = try await sendRaw(r, authorized: authorized, redirects: redirects)
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

    /// `redirects`: `SameOriginRedirects` (a token header dropped to another origin), or
    /// `SameOriginOnly` for a request with a credential in its body (not followed there at all).
    private func sendRaw(_ r: URLRequest, authorized: Bool = true, retried: Bool = false,
                         redirects: URLSessionTaskDelegate = SameOriginRedirects.shared) async throws -> Data {
        var r = r
        if authorized, let access = tokens.access {
            r.setValue("Bearer \(access)", forHTTPHeaderField: "Authorization")
        }
        let data: Data, response: URLResponse
        do {
            (data, response) = try await session.data(for: r, delegate: redirects)
        } catch {
            // the screen went away (its task was cancelled): nothing went wrong
            if Log.isCancellation(error) { throw CancellationError() }
            Log.network.error("\(Self.describe(r), privacy: .public): no answer (URLError \((error as? URLError)?.code.rawValue ?? 0, privacy: .public))")
            throw APIError.transport(error.localizedDescription)
        }
        let code = (response as? HTTPURLResponse)?.statusCode ?? 0
        if code == 401, authorized, !retried, tokens.refresh != nil {
            _ = try await refreshOnce()
            return try await sendRaw(r, authorized: true, retried: true, redirects: redirects)
        }
        if code == 401, authorized {
            Log.session.notice("\(Self.describe(r), privacy: .public): 401 after a refresh: signed out")
            tokens.clear()
            throw APIError.signedOut
        }
        guard (200..<300).contains(code) else {
            let e = APIError.status(code, String(decoding: data.prefix(4096), as: UTF8.self),
                                    retryAfter: (response as? HTTPURLResponse).flatMap(Self.retryAfter))
            let p = e.problem
            Log.network.error("\(Self.describe(r), privacy: .public) -> \(code, privacy: .public) code=\(p?.code ?? "-", privacy: .public) type=\(p?.slug ?? "-", privacy: .public) detail=\(p?.detail ?? "", privacy: .private)")
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
                request("POST", "/api/auth/refresh", body: ["refresh_token": refresh]), authorized: false,
                redirects: SameOriginOnly.shared)
            tokens.save(t)
            return t.accessToken
        }
        refreshing = task
        defer { refreshing = nil }
        do {
            return try await task.value
        } catch let APIError.status(code, _, _) where (400..<500).contains(code) && code != 408 && code != 429 {
            // the home turned the refresh token down: this sign-in is over
            Log.session.notice("refresh refused (\(code, privacy: .public)): signed out")
            tokens.clear()
            throw APIError.signedOut
        } catch APIError.signedOut {
            tokens.clear()
            throw APIError.signedOut
        } catch {
            // the home did not answer, or failed: the sign-in stays for the next try (a home
            // off for a moment must not sign every app out)
            Log.failure("token refresh", error, logger: Log.session)
            throw error
        }
    }
}
