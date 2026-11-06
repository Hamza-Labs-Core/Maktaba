import Foundation

// Epic 56: what a library's five tabs read (the Apple TV; the Players' Collections and
// Playlists tabs, story 56.12): its collections, the user's playlists with videos of it,
// its categories, the collections promoted to rows, the grid grouped by series.

/// A card of a library's Collections tab.
public struct LibraryCollection: Decodable, Identifiable, Hashable, Sendable {
    public let kind: String
    public let subkind: String?
    public let id: String
    public let name: String
    public let videos: Int?
    public let posterVideoIDs: [String]
    public let sourceKind: String?

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        func k(_ s: String) -> AnyKey { AnyKey(stringValue: s) }
        kind = c.lenient(k("kind")) ?? "curated"
        subkind = c.lenient(k("subkind"))
        id = try c.decode(String.self, forKey: k("id"))
        name = c.lenient(k("name")) ?? ""
        videos = c.lenient(k("videos"))
        posterVideoIDs = c.lenientList(k("poster_video_ids"))
        sourceKind = c.lenient(k("source_kind"))
    }

    /// The play queue's collection source: the pipeline's groups as topic:/clip:.
    public var queueID: String { kind == "topic" || kind == "clip" ? "\(kind):\(id)" : id }
    public var queueSource: QueueSource { QueueSource(kind: "collection", id: queueID) }
}

/// A user's playlist, manual or smart (story 56.09).
public struct LibraryPlaylist: Decodable, Identifiable, Hashable, Sendable {
    public let id: String
    public let name: String
    public let videos: Int
    public let posterVideoIDs: [String]
    public let kind: String
    /// A smart playlist's rules as words the TV shows ("speaker: X", …).
    public let rules: [String]

    public var smart: Bool { kind == "smart" }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        func k(_ s: String) -> AnyKey { AnyKey(stringValue: s) }
        id = try c.decode(String.self, forKey: k("id"))
        name = c.lenient(k("name")) ?? ""
        videos = c.lenient(k("videos")) ?? 0
        posterVideoIDs = c.lenientList(k("poster_video_ids"))
        kind = c.lenient(k("kind")) ?? "manual"
        if let q = try? c.nestedContainer(keyedBy: AnyKey.self, forKey: k("smart_query")) {
            let f = q.lenientFields(k("filters"))
            var r = f.sorted { $0.key < $1.key }.map { "\($0.key): \($0.value)" }
            if let sort: String = q.lenient(k("sort")) { r.append(sort) }
            if let limit: Int = q.lenient(k("limit")) { r.append("≤ \(limit)") }
            rules = r
        } else {
            rules = []
        }
    }
}

public struct LibraryPlaylistPage: Decodable, Sendable {
    public let playlist: LibraryPlaylist
    public let items: [BrowseVideo]

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        playlist = try c.decode(LibraryPlaylist.self, forKey: AnyKey(stringValue: "playlist"))
        items = c.lenientList(AnyKey(stringValue: "items"))
    }
}

/// A tile of the Categories tab: `value` is what the grid filters by.
public struct Category: Decodable, Identifiable, Hashable, Sendable {
    public var id: String { value }
    public let value: String
    public let label: String
    public let count: Int

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        func k(_ s: String) -> AnyKey { AnyKey(stringValue: s) }
        value = try c.decode(String.self, forKey: k("value"))
        label = c.lenient(k("label")) ?? value
        count = c.lenient(k("count")) ?? 0
    }
}

/// `GET /api/libraries/{id}/categories`.
public struct LibraryCategories: Decodable, Sendable {
    public let speakers: [Category]
    public let topics: [Category]
    public let languages: [Category]
    public let years: [Category]

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        func k(_ s: String) -> AnyKey { AnyKey(stringValue: s) }
        speakers = c.lenientList(k("speakers"))
        topics = c.lenientList(k("topics"))
        languages = c.lenientList(k("languages"))
        years = c.lenientList(k("years"))
    }
}

/// A collection promoted to a row (a library's Recommended tab, Home).
public struct CollectionRow: Decodable, Identifiable, Hashable, Sendable {
    public var id: String { collectionID }
    public let collectionID: String
    public let name: String
    public let items: [BrowseVideo]

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        func k(_ s: String) -> AnyKey { AnyKey(stringValue: s) }
        let head = try c.nestedContainer(keyedBy: AnyKey.self, forKey: k("collection"))
        collectionID = try head.decode(String.self, forKey: k("id"))
        name = head.lenient(k("name")) ?? ""
        items = c.lenientList(k("items"))
    }
}

private struct CollectionRows: Decodable {
    let rows: [CollectionRow]
    init(from decoder: Decoder) throws {
        rows = try decoder.container(keyedBy: AnyKey.self).lenientList(AnyKey(stringValue: "collection_rows"))
    }
}

/// A card of the grid grouped by series: a series or a loose video.
public struct GroupedCard: Decodable, Identifiable, Hashable, Sendable {
    public let kind: String
    public let id: String
    public let label: String
    public let posterVideoID: String?
    public let episodeCount: Int

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        func k(_ s: String) -> AnyKey { AnyKey(stringValue: s) }
        kind = c.lenient(k("kind")) ?? "video"
        id = try c.decode(String.self, forKey: k("id"))
        let name: String? = c.lenient(k("name"))
        let title: String? = c.lenient(k("title"))
        label = name?.nilIfBlank ?? title?.nilIfBlank ?? (c.lenient(k("filename")) ?? id)
        posterVideoID = c.lenient(k("poster_video_id"))
        episodeCount = c.lenient(k("episode_count")) ?? 0
    }

    public var isSeries: Bool { kind == "series" }
    public var picture: String { posterVideoID ?? id }
}

public struct GroupedPage: Decodable, Sendable {
    public let items: [GroupedCard]
    public let next: String?

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        items = c.lenientList(AnyKey(stringValue: "items"))
        next = (c.lenient(AnyKey(stringValue: "next")) as String?)?.nilIfBlank
    }
}

public extension VideoQuery {
    /// The grid's filters, sort and order as a library queue's source filters.
    var queueFilters: [String: String] {
        items.filter { !["library", "cursor", "limit", "favorite"].contains($0.key) }
    }

    /// The library queue of this view (Play and Shuffle play it).
    var queueSource: QueueSource? { library.map { QueueSource(kind: "library", id: $0, filters: queueFilters) } }
}

public extension APIClient {
    /// A library's Collections tab, all kinds or one (`youtube`, `plex`, `curated`, `topic`, `filler`).
    func libraryCollections(_ library: String, kind: String? = nil) async throws -> [LibraryCollection] {
        var q = ["limit": "200"]
        if let kind { q["kind"] = kind }
        let r: Items<LibraryCollection> = try await browse(request("GET", "/api/libraries/\(Self.segment(library))/collections", query: q))
        return r.items
    }

    /// A collection's videos in its order (an admin's or a sourced one, or a topic or clip group).
    func collectionVideos(_ library: String, _ c: LibraryCollection) async throws -> [BrowseVideo] {
        if c.kind == "topic" || c.kind == "clip" {
            let r: Items<BrowseVideo> = try await browse(request("GET", "/api/libraries/\(Self.segment(library))/collections/\(c.kind)/\(Self.segment(c.id))", query: ["limit": "200"]))
            return r.items
        }
        let r: Items<CollectionItemCard> = try await browse(request("GET", "/api/collections/\(Self.segment(c.id))/items", query: ["limit": "200"]))
        return r.items.compactMap(\.video)
    }

    /// The user's playlists; with `library`, those with videos of it (story 56.10).
    func libraryPlaylists(library: String? = nil) async throws -> [LibraryPlaylist] {
        let r: Items<LibraryPlaylist> = try await browse(request("GET", "/api/me/playlists", query: library.map { ["library": $0] } ?? [:]))
        return r.items
    }

    func libraryPlaylist(_ id: String) async throws -> LibraryPlaylistPage {
        try await browse(request("GET", "/api/me/playlists/\(Self.segment(id))"))
    }

    /// Adds videos to a manual playlist.
    func addToPlaylist(_ playlist: String, videos: [String]) async throws {
        _ = try await browseRaw(request("POST", "/api/me/playlists/\(Self.segment(playlist))/items", body: ["video_ids": videos]))
    }

    /// A new manual playlist holding `videos`.
    func createPlaylist(name: String, videos: [String]) async throws {
        _ = try await browseRaw(request("POST", "/api/me/playlists", body: ["name": name, "video_ids": videos]))
    }

    func removeFromPlaylist(_ playlist: String, video: String) async throws {
        _ = try await browseRaw(request("DELETE", "/api/me/playlists/\(Self.segment(playlist))/items/\(Self.segment(video))"))
    }

    /// A smart playlist from the grid's filters (the filter sheet's Save as smart playlist).
    func createSmartPlaylist(name: String, query: VideoQuery, limit: Int = 200) async throws {
        var filters = query.queueFilters
        let sort = filters.removeValue(forKey: "sort")
        let order = filters.removeValue(forKey: "order")
        var rules: [String: Any] = ["filters": filters, "limit": limit, "libraries": query.library.map { [$0] } as Any]
        if let sort { rules["sort"] = sort }
        if let order { rules["order"] = order }
        _ = try await browseRaw(request("POST", "/api/me/playlists", body: ["name": name, "kind": "smart", "smart_query": rules]))
    }

    /// The Categories tab.
    func libraryCategories(_ library: String) async throws -> LibraryCategories {
        try await browse(request("GET", "/api/libraries/\(Self.segment(library))/categories"))
    }

    /// The collections promoted to rows of a library's Recommended tab.
    func libraryCollectionRows(_ library: String) async throws -> [CollectionRow] {
        let r: CollectionRows = try await browse(request("GET", "/api/libraries/\(Self.segment(library))/hub"))
        return r.rows
    }

    /// The collections an admin shows on Home.
    func homeCollectionRows() async throws -> [CollectionRow] {
        let r: CollectionRows = try await browse(request("GET", "/api/home"))
        return r.rows
    }

    /// A library's grid with a card per series.
    func videosBySeries(_ query: VideoQuery) async throws -> GroupedPage {
        var q = query.items
        q["group"] = "series"
        q["sort"] = "name"
        q.removeValue(forKey: "order")
        return try await browse(request("GET", "/api/videos", query: q))
    }
}

/// An item of a collection: a manual one's `{video_id, video}`, a saved filter's video.
private struct CollectionItemCard: Decodable {
    let video: BrowseVideo?
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        if let v: BrowseVideo = c.lenient(AnyKey(stringValue: "video")) {
            video = v
        } else {
            video = try? BrowseVideo(from: decoder)
        }
    }
}
