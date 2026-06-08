import Foundation

/// A single hit from `GET /api/search?q=…`. The TV search screen
/// renders these as a poster grid identical to the library grid, so
/// the shape deliberately overlaps with `Media`.
struct SearchResult: Codable, Identifiable, Hashable {
    let id: String
    let title: String
    var kind: String?
    var posterURL: String?
    var libraryID: String?

    enum CodingKeys: String, CodingKey {
        case id
        case title
        case kind
        case posterURL = "poster_url"
        case libraryID = "library_id"
    }

    /// Adapts a search hit into the grid's `Media` model.
    var asMedia: Media {
        Media(id: id, title: title, posterURL: posterURL, durationSec: nil, hlsURL: nil)
    }
}

/// Envelope for the search endpoint.
struct SearchResponse: Codable {
    let items: [SearchResult]
}

/// A single autocomplete suggestion from `GET /api/search/suggest`.
struct SearchSuggestion: Codable, Identifiable, Hashable {
    var id: String { text }
    let text: String
}
