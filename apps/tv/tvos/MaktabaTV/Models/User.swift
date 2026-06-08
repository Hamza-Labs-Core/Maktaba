import Foundation

/// The authenticated principal. Mirrors `userResponse` in the API
/// (`api/internal/handlers/auth/auth.go`) — never carries secrets.
struct User: Codable, Identifiable, Hashable {
    let id: String
    let username: String
    let isAdmin: Bool

    enum CodingKeys: String, CodingKey {
        case id
        case username
        case isAdmin = "is_admin"
    }
}

/// Token bundle returned by `POST /api/auth/login` and
/// `POST /api/auth/refresh` (the "native" client response shape).
struct AuthTokens: Codable, Hashable {
    let accessToken: String
    let accessExpiresIn: Int
    let refreshToken: String
    let refreshExpiresIn: Int
    let user: User?

    enum CodingKeys: String, CodingKey {
        case accessToken = "access_token"
        case accessExpiresIn = "access_expires_in"
        case refreshToken = "refresh_token"
        case refreshExpiresIn = "refresh_expires_in"
        case user
    }
}
