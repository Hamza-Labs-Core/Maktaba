import Foundation

/// A media library the user can browse. Mirrors the `libraryView`
/// projection in `api/internal/handlers/libraries/libraries.go`.
///
/// Only `id` and `name` are guaranteed by the list endpoint; the
/// richer fields come from `GET /api/libraries/{id}/stats` and are
/// optional so the same struct decodes both payloads.
struct Library: Codable, Identifiable, Hashable {
    let id: String
    let name: String
    var contentType: String?
    var totalVideos: Int?

    enum CodingKeys: String, CodingKey {
        case id
        case name
        case contentType = "content_type"
        case totalVideos = "total_videos"
    }
}

/// Envelope for `GET /api/libraries` — the handler wraps the slice in
/// an `items` object, matching the web client's expectations.
struct LibraryList: Codable {
    let items: [Library]
}
