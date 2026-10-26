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

/// `GET /api/auth/me`: who is signed in, and the home's name for people (an admin renames
/// it in the web Settings; a home that does not say keeps the name the app has).
public struct Me: Decodable, Sendable {
    public let user: User
    public let homeName: String?

    enum CodingKeys: String, CodingKey {
        case homeName = "home_name"
    }

    public init(from decoder: Decoder) throws {
        user = try User(from: decoder)
        let name = try decoder.container(keyedBy: CodingKeys.self)
            .decodeIfPresent(String.self, forKey: .homeName)?
            .trimmingCharacters(in: .whitespacesAndNewlines)
        homeName = name?.isEmpty == false ? name : nil
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
    /// The folders it reads (an admin's view; nil when the home does not say).
    public let roots: [String]?
}

/// A video in `GET /api/videos` and `GET /api/videos/{id}`.
public struct Video: Codable, Identifiable, Hashable, Sendable {
    public let id: String
    public let libraryID: String
    public let filename: String
    public let title: String?
    public let description: String?
    public let durationSec: Double?
    /// Where this person stopped (`GET /api/videos/{id}` only).
    public let playbackState: PlaybackState?

    enum CodingKeys: String, CodingKey {
        case id, filename, title, description
        case libraryID = "library_id"
        case durationSec = "duration_sec"
        case playbackState = "playback_state"
    }

    /// What a person reads: the title, else the file's name.
    public var displayTitle: String { title?.isEmpty == false ? title! : filename }
}

/// Where a person stopped watching a video.
public struct PlaybackState: Codable, Hashable, Sendable {
    public let positionSec: Double
    public let completed: Bool

    enum CodingKeys: String, CodingKey {
        case positionSec = "position_sec"
        case completed
    }
}

/// `GET /api/recommendations`: the home screen's rails.
struct Recommendations: Decodable, Sendable {
    let rails: [Rail]
}

/// One rail of the home screen. Its title is the app's to say, from `reasonKind`
/// (`continue_watching`, `for_you`, `from_library`, `newly_added`, `editor_picks`).
public struct Rail: Codable, Identifiable, Hashable, Sendable {
    public let id: String
    public let reasonKind: String
    public let items: [RailItem]

    enum CodingKeys: String, CodingKey {
        case id, items
        case reasonKind = "reason_kind"
    }
}

/// A video on a rail: its id, and where the person stopped (continue watching).
public struct RailItem: Codable, Identifiable, Hashable, Sendable {
    public var id: String { videoID }
    public let videoID: String
    public let positionSec: Double?
    public let durationSec: Double?

    enum CodingKeys: String, CodingKey {
        case videoID = "video_id"
        case positionSec = "position_sec"
        case durationSec = "duration_sec"
    }

    /// How much was watched, 0...1.
    public var progress: Double {
        guard let positionSec, let durationSec, durationSec > 0 else { return 0 }
        return min(max(positionSec / durationSec, 0), 1)
    }
}

/// `POST /api/pairing/device`: the code a TV shows until a signed-in person approves it
/// (on the web at `verifyPath`, or a phone), and the TV's own secret it asks with.
public struct DevicePairing: Codable, Sendable, Equatable {
    public let code: String
    public let deviceCode: String
    public let expiresAt: Date
    public let verifyPath: String
    public let interval: Int

    public enum State: Sendable, Equatable {
        case waiting, approved, expired
    }

    enum CodingKeys: String, CodingKey {
        case code, interval
        case deviceCode = "device_code"
        case expiresAt = "expires_at"
        case verifyPath = "verify_path"
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        code = try c.decode(String.self, forKey: .code)
        deviceCode = try c.decode(String.self, forKey: .deviceCode)
        verifyPath = try c.decode(String.self, forKey: .verifyPath)
        interval = max(1, try c.decodeIfPresent(Int.self, forKey: .interval) ?? 2)
        let exp = try c.decode(String.self, forKey: .expiresAt)
        expiresAt = (try? Date(exp, strategy: .iso8601)) ?? .now.addingTimeInterval(600)
    }

    /// The page that approves it, on the address this device reached the home at.
    public func verifyURL(home: URL) -> URL? {
        URL(string: verifyPath, relativeTo: home)?.absoluteURL
    }
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
        expiresAt.flatMap(homeTime)
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
