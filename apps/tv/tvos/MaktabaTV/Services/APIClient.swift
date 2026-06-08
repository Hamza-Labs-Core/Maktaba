import Foundation

/// Thin async REST client against the Maktaba API — the same server
/// the web SPA and mobile apps talk to. All endpoints are documented
/// in `api/internal/handlers/…`.
///
/// Responsibilities:
///  - prefix paths with the configured base URL,
///  - inject the `Authorization: Bearer <jwt>` header,
///  - on a 401, transparently refresh the token once and retry,
///  - decode JSON into the `Codable` models.
actor APIClient {
    /// Base URL of the API, e.g. `https://media.example.com`. Persisted
    /// by `SettingsView` so a household can point the TV at self-hosted
    /// or cloud instances without a rebuild.
    private(set) var baseURL: URL

    /// Supplies the current access token. Wired to `AuthManager` so the
    /// client never owns token storage (avoids a retain cycle).
    private let tokenProvider: () -> String?
    /// Called on 401 to mint a fresh token. Returns the new access token.
    private let refresher: () async throws -> String

    private let session: URLSession
    private let decoder = JSONDecoder()
    private let encoder = JSONEncoder()
    /// Guards against a refresh stampede when many requests 401 at once.
    private var refreshing = false

    init(
        baseURL: URL,
        tokenProvider: @escaping () -> String?,
        refresher: @escaping () async throws -> String,
        session: URLSession = .shared
    ) {
        self.baseURL = baseURL
        self.tokenProvider = tokenProvider
        self.refresher = refresher
        self.session = session
    }

    func setBaseURL(_ url: URL) { baseURL = url }

    // MARK: - Verbs

    func get<T: Decodable>(_ path: String, query: [String: String] = [:]) async throws -> T {
        try await send(makeRequest(path, method: "GET", query: query))
    }

    func post<T: Decodable>(_ path: String, body: [String: Any]) async throws -> T {
        var req = makeRequest(path, method: "POST")
        req.httpBody = try JSONSerialization.data(withJSONObject: body)
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        return try await send(req)
    }

    /// Builds the absolute HLS manifest URL for a video id. The streaming
    /// service serves signed `.m3u8` manifests under `/api/stream`.
    func hlsURL(for videoID: String) -> URL {
        baseURL.appendingPathComponent("api/stream/\(videoID)/index.m3u8")
    }

    // MARK: - Plumbing

    private func makeRequest(_ path: String, method: String, query: [String: String] = [:]) -> URLRequest {
        var components = URLComponents(
            url: baseURL.appendingPathComponent(path.hasPrefix("/") ? String(path.dropFirst()) : path),
            resolvingAgainstBaseURL: false
        )!
        if !query.isEmpty {
            components.queryItems = query.map { URLQueryItem(name: $0.key, value: $0.value) }
        }
        var req = URLRequest(url: components.url!)
        req.httpMethod = method
        req.setValue("application/json", forHTTPHeaderField: "Accept")
        if let token = tokenProvider() {
            req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        }
        return req
    }

    private func send<T: Decodable>(_ request: URLRequest, isRetry: Bool = false) async throws -> T {
        let (data, response) = try await session.data(for: request)
        guard let http = response as? HTTPURLResponse else {
            throw APIError.transport
        }

        if http.statusCode == 401 && !isRetry {
            // Single-flight refresh: only the first 401 rotates the token.
            let newToken = try await refreshOnce()
            var retry = request
            retry.setValue("Bearer \(newToken)", forHTTPHeaderField: "Authorization")
            return try await send(retry, isRetry: true)
        }

        guard (200..<300).contains(http.statusCode) else {
            throw APIError.status(http.statusCode, String(decoding: data, as: UTF8.self))
        }
        do {
            return try decoder.decode(T.self, from: data)
        } catch {
            throw APIError.decoding(error)
        }
    }

    private func refreshOnce() async throws -> String {
        if refreshing {
            // Another task is already refreshing; give it a beat and
            // reuse whatever token it lands.
            try await Task.sleep(nanoseconds: 300_000_000)
            if let token = tokenProvider() { return token }
        }
        refreshing = true
        defer { refreshing = false }
        return try await refresher()
    }
}

enum APIError: LocalizedError {
    case transport
    case status(Int, String)
    case decoding(Error)

    var errorDescription: String? {
        switch self {
        case .transport: return "Could not reach the server."
        case let .status(code, body): return "Server returned \(code): \(body)"
        case let .decoding(err): return "Unexpected response: \(err.localizedDescription)"
        }
    }
}
