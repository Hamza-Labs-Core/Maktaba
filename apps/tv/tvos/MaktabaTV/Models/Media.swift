import Foundation

/// A playable item (movie, episode, lecture …). The TV grid only
/// needs enough to render a poster + title and to start playback, so
/// this is intentionally a lean projection of the server's video row.
struct Media: Codable, Identifiable, Hashable {
    let id: String
    let title: String
    var posterURL: String?
    var durationSec: Double?
    /// Absolute HLS manifest URL (`.m3u8`). When `nil`, the client
    /// derives it from `id` via `APIClient.hlsURL(for:)`.
    var hlsURL: String?

    enum CodingKeys: String, CodingKey {
        case id
        case title
        case posterURL = "poster_url"
        case durationSec = "duration_sec"
        case hlsURL = "hls_url"
    }
}

/// One "rail" on the Home screen — Continue Watching, Recently Added,
/// or a recommendation row. Maps to `Rail` in
/// `api/internal/handlers/recommendations/recommendations.go`.
struct MediaRail: Codable, Identifiable, Hashable {
    let id: String
    let title: String
    var reasonKind: String?
    var items: [RailItem]

    enum CodingKeys: String, CodingKey {
        case id
        case title
        case reasonKind = "reason_kind"
        case items
    }
}

/// A single entry inside a rail. Continue-Watching entries carry
/// resume position; freshly-added entries do not.
struct RailItem: Codable, Identifiable, Hashable {
    var id: String { videoID }
    let videoID: String
    var posterURL: String?
    var positionSec: Double?
    var durationSec: Double?
    var remainingSec: Double?

    enum CodingKeys: String, CodingKey {
        case videoID = "video_id"
        case posterURL = "poster_url"
        case positionSec = "position_sec"
        case durationSec = "duration_sec"
        case remainingSec = "remaining_sec"
    }

    /// Fraction watched in `0...1`, for the progress bar under a card.
    var progress: Double {
        guard let pos = positionSec, let dur = durationSec, dur > 0 else { return 0 }
        return min(max(pos / dur, 0), 1)
    }
}

/// Top-level payload of `GET /api/recommendations`.
struct Recommendations: Codable {
    let rails: [MediaRail]
}
