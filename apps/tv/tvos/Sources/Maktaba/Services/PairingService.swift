import Foundation

// Networking layer for the Apple TV app. These talk to the real
// Maktaba backbone:
//
//   * Pairing is REST: POST /api/pairing/request returns a short code;
//     GET /api/pairing/status?code=… long-polls until the phone
//     redeems it and yields a device token.
//   * Rows and search are GraphQL: POST /graphql with the
//     MaktabaSchema query bodies. The server resolves
//     continueWatching / recommendations / search against the same DB
//     logic the REST API uses (Epic 14 GraphQL backbone).
//
// All canned data has been removed; every call performs a real
// request and decodes the live response.

/// Errors surfaced to the UI so it can show a banner instead of
/// silently swallowing failures.
public enum APIError: Error, Equatable {
    case server(Int)
    case decoding
    case transport
    case graphql(String)
}

/// HTTPClient is a thin URLSession wrapper, injected so tests can use
/// a stub transport without a live server.
public protocol HTTPClient {
    func data(for request: URLRequest) async throws -> (Data, URLResponse)
}

extension URLSession: HTTPClient {}

/// APIConfig carries the paired server origin + device token.
public struct APIConfig {
    public var baseURL: URL
    public var deviceToken: String?
    public init(baseURL: URL, deviceToken: String? = nil) {
        self.baseURL = baseURL
        self.deviceToken = deviceToken
    }
}

public struct PairingService {
    let config: APIConfig
    let http: HTTPClient

    public init(config: APIConfig, http: HTTPClient = URLSession.shared) {
        self.config = config
        self.http = http
    }

    private struct RequestResponse: Decodable {
        let code: String
        let expiresAt: Date
    }

    /// POST /api/pairing/request — returns the short pairing code the
    /// user types/scans on their phone.
    public func requestCode() async throws -> PairingCode {
        var req = URLRequest(url: config.baseURL.appendingPathComponent("api/pairing/request"))
        req.httpMethod = "POST"
        let (data, resp) = try await http.data(for: req)
        try Self.check(resp)
        let dec = JSONDecoder()
        dec.dateDecodingStrategy = .iso8601
        guard let body = try? dec.decode(RequestResponse.self, from: data) else {
            throw APIError.decoding
        }
        return PairingCode(code: body.code, expiresAt: body.expiresAt)
    }

    private struct StatusResponse: Decodable { let deviceId: String }

    /// GET /api/pairing/status?code=… — long-poll until the code is
    /// redeemed; returns the issued device id.
    public func waitForApproval(code: String) async throws -> String {
        var comps = URLComponents(
            url: config.baseURL.appendingPathComponent("api/pairing/status"),
            resolvingAgainstBaseURL: false)!
        comps.queryItems = [URLQueryItem(name: "code", value: code)]
        var req = URLRequest(url: comps.url!)
        req.httpMethod = "GET"
        let (data, resp) = try await http.data(for: req)
        try Self.check(resp)
        guard let body = try? JSONDecoder().decode(StatusResponse.self, from: data) else {
            throw APIError.decoding
        }
        return body.deviceId
    }

    static func check(_ resp: URLResponse) throws {
        guard let h = resp as? HTTPURLResponse else { throw APIError.transport }
        guard (200..<300).contains(h.statusCode) else { throw APIError.server(h.statusCode) }
    }
}

/// GraphQLClient executes the MaktabaSchema operations against
/// POST /graphql and decodes the `data.<field>` envelope.
public struct GraphQLClient {
    let config: APIConfig
    let http: HTTPClient

    public init(config: APIConfig, http: HTTPClient = URLSession.shared) {
        self.config = config
        self.http = http
    }

    struct Envelope<T: Decodable>: Decodable {
        let data: [String: T]?
        let errors: [GQLError]?
    }
    struct GQLError: Decodable { let message: String }

    /// execute posts `query` with `variables` and returns the value at
    /// `data.<field>` decoded as `[Card]`.
    public func cards(query: String, field: String, variables: [String: Any]) async throws -> [VideoSummary] {
        let body: [String: Any] = ["query": query, "variables": variables]
        var req = URLRequest(url: config.baseURL.appendingPathComponent("graphql"))
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        if let tok = config.deviceToken {
            req.setValue("Bearer \(tok)", forHTTPHeaderField: "Authorization")
        }
        req.httpBody = try JSONSerialization.data(withJSONObject: body)
        let (data, resp) = try await http.data(for: req)
        try PairingService.check(resp)
        let env: Envelope<[RailCardDTO]>
        do {
            env = try JSONDecoder().decode(Envelope<[RailCardDTO]>.self, from: data)
        } catch {
            throw APIError.decoding
        }
        if let e = env.errors?.first { throw APIError.graphql(e.message) }
        let cards = env.data?[field] ?? []
        return cards.map { $0.asSummary }
    }
}

/// RailCardDTO matches the GraphQL RailCard type.
struct RailCardDTO: Decodable {
    let id: String
    let title: String
    let durationSec: Double
    let positionSec: Double?
    let posterUrl: String?

    var asSummary: VideoSummary {
        VideoSummary(
            id: id,
            title: title,
            durationSec: durationSec,
            positionSec: positionSec,
            posterURL: posterUrl.flatMap(URL.init(string:)))
    }
}

public struct LibraryService {
    let gql: GraphQLClient
    public init(gql: GraphQLClient) { self.gql = gql }

    public func continueWatching() async throws -> [VideoSummary] {
        try await gql.cards(
            query: MaktabaSchema.ContinueWatchingQuery.body,
            field: "continueWatching",
            variables: ["limit": 12])
    }

    public func recommendations() async throws -> [VideoSummary] {
        try await gql.cards(
            query: MaktabaSchema.RecommendationsQuery.body,
            field: "recommendations",
            variables: ["limit": 12])
    }

    public func listVideos() async throws -> [VideoSummary] {
        try await gql.cards(
            query: MaktabaSchema.RecommendationsQuery.body,
            field: "recommendations",
            variables: ["limit": 20])
    }
}

public struct SearchService {
    let gql: GraphQLClient
    public init(gql: GraphQLClient) { self.gql = gql }

    public func query(_ q: String) async throws -> [VideoSummary] {
        guard !q.isEmpty else { return [] }
        return try await gql.cards(
            query: MaktabaSchema.SearchQuery.body,
            field: "search",
            variables: ["q": q, "limit": 24])
    }
}
