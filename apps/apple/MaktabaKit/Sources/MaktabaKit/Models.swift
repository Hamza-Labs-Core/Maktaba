import Foundation

/// A signed-in user (`user` in the sign-in answer, `GET /api/auth/me`).
public struct User: Codable, Hashable, Sendable {
    public let id: String
    public let username: String
    public let isAdmin: Bool

    enum CodingKeys: String, CodingKey {
        case id, username
        case isAdmin = "is_admin"
    }
}

/// `POST /api/auth/login` and `/api/auth/refresh` for a native client (`X-Maktaba-Client:
/// native`): the tokens in the body, not cookies.
public struct AuthTokens: Codable, Sendable {
    public let accessToken: String
    public let refreshToken: String
    public let user: User?

    enum CodingKeys: String, CodingKey {
        case accessToken = "access_token"
        case refreshToken = "refresh_token"
        case user
    }
}

/// `GET /api/libraries`.
public struct Library: Codable, Identifiable, Hashable, Sendable {
    public let id: String
    public let name: String
}

/// A video in `GET /api/videos` and `GET /api/videos/{id}`.
public struct Video: Codable, Identifiable, Hashable, Sendable {
    public let id: String
    public let libraryID: String
    public let filename: String
    public let title: String?
    public let description: String?
    public let durationSec: Double?

    enum CodingKeys: String, CodingKey {
        case id, filename, title, description
        case libraryID = "library_id"
        case durationSec = "duration_sec"
    }

    /// What a person reads: the title, else the file's name.
    public var displayTitle: String { title?.isEmpty == false ? title! : filename }
}

/// A page of a list (`{"items": [...], "next": cursor}`).
public struct Page<Item: Decodable & Sendable>: Decodable, Sendable {
    public let items: [Item]
    public let next: String?
}

/// `POST /api/stream/sessions`: where the video plays from. `direct_url` is the file
/// itself; `manifest_url` an HLS playlist. Both are signed (`?sig=`, good until
/// `expires_at`) and relative to the home. The home's web port also wants a signed-in
/// caller on `/stream/` (`APIClient.playbackHeaders`).
public struct StreamSession: Codable, Sendable {
    public let sessionID: String
    public let mode: String
    public let manifestURL: String?
    public let directURL: String?
    public let expiresAt: String?

    enum CodingKeys: String, CodingKey {
        case mode
        case sessionID = "session_id"
        case manifestURL = "manifest_url"
        case directURL = "direct_url"
        case expiresAt = "expires_at"
    }

    /// When the signed URL stops working (nil: the home did not say).
    public var expires: Date? {
        guard let expiresAt else { return nil }
        return (try? Date(expiresAt, strategy: .iso8601.year().month().day().time(includingFractionalSeconds: true)))
            ?? (try? Date(expiresAt, strategy: .iso8601))
    }

    /// The URL the player opens, against the home's address.
    public func playbackURL(home: URL) -> URL? {
        guard let path = directURL ?? manifestURL else { return nil }
        return URL(string: path, relativeTo: home)?.absoluteURL
    }
}

/// A group of failed jobs in "Needs attention" (`GET /api/issues`).
public struct FailureGroup: Codable, Identifiable, Hashable, Sendable {
    public var id: String { "\(stage):\(cause)" }
    public let stage: String
    public let cause: String
    public let label: String
    public let count: Int
    /// "retry", "apply", "open_settings" or "show_workers".
    public let action: String
    public let sampleMessage: String?

    enum CodingKeys: String, CodingKey {
        case stage, cause, label, count, action
        case sampleMessage = "sample_message"
    }
}

public struct FailureGroups: Codable, Sendable {
    public let groups: [FailureGroup]
}
