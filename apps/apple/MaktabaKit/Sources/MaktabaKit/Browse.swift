import Foundation

// The Player's browse client (Epic 48, story 48.6): the calls and models of Home, Libraries,
// a library, Series, a video's page, Search and Profile, on the routes the web already uses
// (specs/epics/48-native-players/research.md §4). Every model reads the home's answer
// leniently (as `Lenient.swift`): a field an older home leaves out or a newer one adds, a
// row that is missing, a field of another shape, a list element that cannot be read, none
// of them hides the screen. Only an id is required (a card without one cannot open).
//
// The home answers 404 for what the caller may not see, as for what does not exist (and the
// video routes answer 403 for a video of a library the caller has no access to): both are
// `BrowseError.notAvailable`. A 401 takes the client's signed-out path (one refresh, then
// `APIError.signedOut`). Any other failure stays an `APIError`.

/// Why a browse call has nothing to show, besides an `APIError`.
public enum BrowseError: LocalizedError, Equatable, Sendable {
    /// It does not exist, or this person may not see it (the home does not say which).
    case notAvailable
    /// A saved search's results are gone (410 `search-expired`): run the search again.
    case searchExpired

    public var errorDescription: String? {
        ErrorStrings.forPreferredLanguages().sentence(.notFound)
    }
}

// MARK: cards and names

/// A video as a card (the home's rows, a library's grid, a playlist): its picture is
/// `/api/videos/{id}/poster`.
public struct BrowseVideo: Decodable, Identifiable, Hashable, Sendable {
    public let id: String
    public let title: String?
    public let filename: String
    public let libraryID: String?
    public let durationSec: Double?
    public let state: String?
    public let createdAt: Date?
    public let publishedAt: Date?
    /// The caller's own playback, on the rows that read it.
    public let positionSec: Double?
    public let completed: Bool?
    public let lastWatchedAt: Date?
    /// Everyone's plays in the window (Most Watched).
    public let plays: Int?
    /// Its series (On Deck).
    public let series: SeriesRef?

    enum CodingKeys: String, CodingKey {
        case id, title, filename, state, completed, plays, series
        case libraryID = "library_id"
        case durationSec = "duration_sec"
        case createdAt = "created_at"
        case publishedAt = "published_at"
        case positionSec = "position_sec"
        case lastWatchedAt = "last_watched_at"
        case playbackState = "playback_state"
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        title = c.lenient(.title)
        filename = c.lenient(.filename) ?? ""
        libraryID = c.lenient(.libraryID)
        durationSec = c.lenient(.durationSec)
        state = c.lenient(.state)
        createdAt = c.lenient(.createdAt)
        publishedAt = c.lenient(.publishedAt)
        // Home's rows put the playback on the card; `GET /api/videos` nests it under
        // `playback_state` (its time as `updated_at`)
        let nested = try? c.nestedContainer(keyedBy: AnyKey.self, forKey: .playbackState)
        positionSec = c.lenient(.positionSec) ?? nested?.lenient(AnyKey(stringValue: "position_sec"))
        completed = c.lenient(.completed) ?? nested?.lenient(AnyKey(stringValue: "completed"))
        lastWatchedAt = c.lenient(.lastWatchedAt) ?? nested?.lenient(AnyKey(stringValue: "updated_at"))
        plays = c.lenient(.plays)
        series = c.lenient(.series)
    }

    /// What a person reads: the title, else the file's name.
    public var displayTitle: String { title?.nilIfBlank ?? filename }

    /// How much was watched, 0...1 (a finished video is 1).
    public var progress: Double {
        if completed == true { return 1 }
        return fraction(positionSec, of: durationSec)
    }
}

/// A series' id and name.
public struct SeriesRef: Decodable, Identifiable, Hashable, Sendable {
    public let id: String
    public let name: String

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        id = try c.decode(String.self, forKey: AnyKey(stringValue: "id"))
        name = c.lenient(AnyKey(stringValue: "name")) ?? ""
    }
}

/// A library's id and name (and its count, on a hub).
public struct LibraryRef: Decodable, Identifiable, Hashable, Sendable {
    public let id: String
    public let name: String
    public let videos: Int?

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        id = try c.decode(String.self, forKey: AnyKey(stringValue: "id"))
        name = c.lenient(AnyKey(stringValue: "name")) ?? ""
        videos = c.lenient(AnyKey(stringValue: "videos"))
    }
}

// MARK: Home

/// `GET /api/home`: the rows of Home, each empty when the home has nothing for it (or does
/// not send it).
public struct HomeRows: Decodable, Sendable {
    /// The hero: the admin's pick, else the newest series or video.
    public let featured: Featured?
    public let continueWatching: [BrowseVideo]
    public let onDeck: [BrowseVideo]
    public let favorites: [BrowseVideo]
    public let recentlyAdded: [BrowseVideo]
    /// Recently Added with a series' new episodes as one entry.
    public let recentlyAddedGrouped: [AddedEntry]
    public let recentlyWatched: [BrowseVideo]
    /// One row pair per pinned library, in the person's order.
    public let pinned: [PinnedRows]

    enum CodingKeys: String, CodingKey {
        case featured, favorites, pinned
        case continueWatching = "continue_watching"
        case onDeck = "on_deck"
        case recentlyAdded = "recently_added"
        case recentlyAddedGrouped = "recently_added_grouped"
        case recentlyWatched = "recently_watched"
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        featured = c.lenient(.featured)
        continueWatching = c.lenientList(.continueWatching)
        onDeck = c.lenientList(.onDeck)
        favorites = c.lenientList(.favorites)
        recentlyAdded = c.lenientList(.recentlyAdded)
        recentlyAddedGrouped = c.lenientList(.recentlyAddedGrouped)
        recentlyWatched = c.lenientList(.recentlyWatched)
        pinned = c.lenientList(.pinned)
    }

    /// Nothing at all to show (no library visible, or nothing in them).
    public var isEmpty: Bool {
        featured == nil && continueWatching.isEmpty && onDeck.isEmpty && favorites.isEmpty
            && recentlyAdded.isEmpty && recentlyAddedGrouped.isEmpty && recentlyWatched.isEmpty
            && pinned.allSatisfy { $0.recentlyAdded.isEmpty && $0.continueWatching.isEmpty }
    }
}

/// Home's hero.
public struct Featured: Decodable, Hashable, Sendable {
    /// `series` or `video` (a newer home's kind is kept as sent).
    public let kind: String
    public let video: BrowseVideo
    public let series: FeaturedSeries?
    /// The admin picked it (else the home chose the newest).
    public let picked: Bool

    enum CodingKeys: String, CodingKey { case kind, video, series, picked }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        video = try c.decode(BrowseVideo.self, forKey: .video)
        series = c.lenient(.series)
        kind = c.lenient(.kind) ?? (series == nil ? "video" : "series")
        picked = c.lenient(.picked) ?? false
    }
}

public struct FeaturedSeries: Decodable, Identifiable, Hashable, Sendable {
    public let id: String
    public let name: String
    public let episodes: Int?

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        id = try c.decode(String.self, forKey: AnyKey(stringValue: "id"))
        name = c.lenient(AnyKey(stringValue: "name")) ?? ""
        episodes = c.lenient(AnyKey(stringValue: "episodes"))
    }
}

/// An entry of Recently Added by series: a video, or a series' newest episode with how many
/// were added.
public struct AddedEntry: Decodable, Identifiable, Hashable, Sendable {
    public var id: String { series.map { "series:\($0.id)" } ?? video.id }
    /// `video` or `series`.
    public let kind: String
    public let video: BrowseVideo
    public let series: SeriesRef?
    public let count: Int?

    enum CodingKeys: String, CodingKey { case kind, video, series, count }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        video = try c.decode(BrowseVideo.self, forKey: .video)
        series = c.lenient(.series)
        kind = c.lenient(.kind) ?? (series == nil ? "video" : "series")
        count = c.lenient(.count)
    }
}

/// A pinned library's rows on Home.
public struct PinnedRows: Decodable, Identifiable, Hashable, Sendable {
    public var id: String { library.id }
    public let library: LibraryRef
    public let recentlyAdded: [BrowseVideo]
    public let continueWatching: [BrowseVideo]

    enum CodingKeys: String, CodingKey {
        case library
        case recentlyAdded = "recently_added"
        case continueWatching = "continue_watching"
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        library = try c.decode(LibraryRef.self, forKey: .library)
        recentlyAdded = c.lenientList(.recentlyAdded)
        continueWatching = c.lenientList(.continueWatching)
    }
}

/// `GET /api/recommendations`, read leniently into the existing `Rail`s.
struct LenientRecommendations: Decodable {
    let rails: [Rail]

    private struct LenientRail: Decodable {
        let rail: Rail
        init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: AnyKey.self)
            let items: [LenientItem] = c.lenientList(AnyKey(stringValue: "items"))
            rail = Rail(id: try c.decode(String.self, forKey: AnyKey(stringValue: "id")),
                        reasonKind: c.lenient(AnyKey(stringValue: "reason_kind")) ?? "",
                        items: items.map(\.item))
        }
    }

    private struct LenientItem: Decodable {
        let item: RailItem
        init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: AnyKey.self)
            item = RailItem(videoID: try c.decode(String.self, forKey: AnyKey(stringValue: "video_id")),
                            positionSec: c.lenient(AnyKey(stringValue: "position_sec")),
                            durationSec: c.lenient(AnyKey(stringValue: "duration_sec")))
        }
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        let list: [LenientRail] = c.lenientList(AnyKey(stringValue: "rails"))
        rails = list.map(\.rail)
    }
}

// MARK: Libraries

/// A library card of `GET /api/libraries/summary`.
public struct LibrarySummary: Decodable, Identifiable, Hashable, Sendable {
    public let id: String
    public let name: String
    public let videos: Int
    public let durationSec: Double
    /// Its newest videos, for the card's art.
    public let posterVideoIDs: [String]
    public let latestAddedAt: Date?
    public let pinned: Bool
    public let pinPosition: Int?

    enum CodingKeys: String, CodingKey {
        case id, name, videos, pinned
        case durationSec = "duration_sec"
        case posterVideoIDs = "poster_video_ids"
        case latestAddedAt = "latest_added_at"
        case pinPosition = "pin_position"
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        name = c.lenient(.name) ?? ""
        videos = c.lenient(.videos) ?? 0
        durationSec = c.lenient(.durationSec) ?? 0
        posterVideoIDs = c.lenientList(.posterVideoIDs)
        latestAddedAt = c.lenient(.latestAddedAt)
        pinPosition = c.lenient(.pinPosition)
        pinned = c.lenient(.pinned) ?? (pinPosition != nil)
    }

    /// The cards as Libraries shows them: the pinned first in the person's order, then the
    /// rest by name (as the home sends them).
    public static func ordered(_ all: [LibrarySummary]) -> [LibrarySummary] {
        let pinned = all.enumerated().filter(\.element.pinned)
            .sorted { ($0.element.pinPosition ?? .max, $0.offset) < ($1.element.pinPosition ?? .max, $1.offset) }
            .map(\.element)
        return pinned + all.filter { !$0.pinned }
    }
}

/// One of the person's pinned libraries (`GET`/`PUT /api/me/pinned-libraries`).
public struct PinnedLibrary: Decodable, Identifiable, Hashable, Sendable {
    public let id: String
    public let name: String
    public let position: Int

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        id = try c.decode(String.self, forKey: AnyKey(stringValue: "id"))
        name = c.lenient(AnyKey(stringValue: "name")) ?? ""
        position = c.lenient(AnyKey(stringValue: "position")) ?? 0
    }
}

struct PinnedLibraries: Decodable {
    let items: [PinnedLibrary]
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        items = c.lenientList(AnyKey(stringValue: "items")).sorted { $0.position < $1.position }
    }
}

// MARK: a library

/// `GET /api/libraries/{id}/hub`: a library's rows.
public struct LibraryHub: Decodable, Sendable {
    public let library: LibraryRef
    public let onDeck: [BrowseVideo]
    public let continueWatching: [BrowseVideo]
    public let recentlyAdded: [BrowseVideo]
    public let recentlyAddedGrouped: [AddedEntry]
    public let recentlyWatched: [BrowseVideo]
    public let mostWatched: [BrowseVideo]
    public let series: [HubSeries]
    public let years: [YearCount]

    enum CodingKeys: String, CodingKey {
        case library, rows
    }

    enum RowKeys: String, CodingKey {
        case series, years
        case onDeck = "on_deck"
        case continueWatching = "continue_watching"
        case recentlyAdded = "recently_added"
        case recentlyAddedGrouped = "recently_added_grouped"
        case recentlyWatched = "recently_watched"
        case mostWatched = "most_watched"
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        library = try c.decode(LibraryRef.self, forKey: .library)
        let rows = try? c.nestedContainer(keyedBy: RowKeys.self, forKey: .rows)
        onDeck = rows?.lenientList(.onDeck) ?? []
        continueWatching = rows?.lenientList(.continueWatching) ?? []
        recentlyAdded = rows?.lenientList(.recentlyAdded) ?? []
        recentlyAddedGrouped = rows?.lenientList(.recentlyAddedGrouped) ?? []
        recentlyWatched = rows?.lenientList(.recentlyWatched) ?? []
        mostWatched = rows?.lenientList(.mostWatched) ?? []
        series = rows?.lenientList(.series) ?? []
        years = rows?.lenientList(.years) ?? []
    }
}

/// A series on a library's hub.
public struct HubSeries: Decodable, Identifiable, Hashable, Sendable {
    public let id: String
    public let name: String
    public let episodes: Int
    public let posterVideoID: String?

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        id = try c.decode(String.self, forKey: AnyKey(stringValue: "id"))
        name = c.lenient(AnyKey(stringValue: "name")) ?? ""
        episodes = c.lenient(AnyKey(stringValue: "episodes")) ?? 0
        posterVideoID = c.lenient(AnyKey(stringValue: "poster_video_id"))
    }
}

/// A year of a library and how many videos were published in it.
public struct YearCount: Decodable, Hashable, Sendable {
    public let year: Int
    public let videos: Int

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        year = try c.decode(Int.self, forKey: AnyKey(stringValue: "year"))
        videos = c.lenient(AnyKey(stringValue: "videos")) ?? 0
    }
}

/// The order of a library's grid.
public enum VideoSort: String, Sendable, CaseIterable {
    /// The newest added first.
    case newest
    /// A to Z by title.
    case title
    /// The newest published first.
    case published

    var query: (sort: String, order: String) {
        switch self {
        case .newest: ("created_at", "desc")
        case .title: ("title", "asc")
        case .published: ("published_at", "desc")
        }
    }
}

/// What a library's grid asks `GET /api/videos` for: its library, sort, facets and page.
public struct VideoQuery: Sendable, Equatable {
    public var library: String?
    public var sort: VideoSort
    /// A facet's value (`VideoFacets`), each nil for any.
    public var language: String?
    public var speaker: String?
    public var tag: String?
    public var year: Int?
    public var duration: String?
    public var added: String?
    public var resolution: String?
    public var letter: String?
    public var series: String?
    /// `unwatched`, `in_progress` or `watched`.
    public var watched: String?
    /// Only the person's favorites (true); false or nil: every video (the home accepts only
    /// `favorite=true`).
    public var favorite: Bool?
    public var cursor: String?
    public var limit: Int

    public init(library: String? = nil, sort: VideoSort = .newest, language: String? = nil, speaker: String? = nil,
                tag: String? = nil, year: Int? = nil, duration: String? = nil, added: String? = nil,
                resolution: String? = nil, letter: String? = nil, series: String? = nil, watched: String? = nil,
                favorite: Bool? = nil, cursor: String? = nil, limit: Int = 60) {
        self.library = library
        self.sort = sort
        self.language = language
        self.speaker = speaker
        self.tag = tag
        self.year = year
        self.duration = duration
        self.added = added
        self.resolution = resolution
        self.letter = letter
        self.series = series
        self.watched = watched
        self.favorite = favorite
        self.cursor = cursor
        self.limit = limit
    }

    /// The same query's next page.
    public func next(_ cursor: String) -> VideoQuery {
        var q = self
        q.cursor = cursor
        return q
    }

    var items: [String: String] {
        var q = ["limit": String(max(1, min(limit, 200))), "sort": sort.query.sort, "order": sort.query.order]
        let optional: [(String, String?)] = [
            ("library", library), ("language", language), ("speaker", speaker), ("tag", tag),
            ("year", year.map(String.init)), ("duration", duration), ("added", added),
            ("resolution", resolution), ("letter", letter), ("series", series), ("watched", watched),
            ("favorite", favorite == true ? "true" : nil), ("cursor", cursor),
        ]
        for case let (k, v?) in optional where !v.isEmpty { q[k] = v }
        return q
    }
}

/// A page of a library's grid; `next` asks for the following one (nil: the end).
public struct VideoListPage: Decodable, Sendable {
    public let items: [BrowseVideo]
    public let next: String?

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        items = c.lenientList(AnyKey(stringValue: "items"))
        next = (c.lenient(AnyKey(stringValue: "next")) as String?)?.nilIfBlank
    }
}

/// `GET /api/videos/facets`: what a library's grid can be narrowed by, with counts.
public struct VideoFacets: Decodable, Sendable {
    public let languages: [Facet]
    public let speakers: [Facet]
    public let tags: [Facet]
    public let letters: [Facet]
    public let durations: [Facet]
    public let added: [Facet]
    public let resolutions: [Facet]

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        func list(_ k: String) -> [Facet] { c.lenientList(AnyKey(stringValue: k)) }
        languages = list("languages")
        speakers = list("speakers")
        tags = list("tags")
        letters = list("letters")
        durations = list("durations")
        added = list("added")
        resolutions = list("resolutions")
    }
}

/// One facet: `value` is what `VideoQuery` sends (a language code, a speaker's name, a tag's
/// id, a band, a letter); `name` is a tag's name.
public struct Facet: Decodable, Identifiable, Hashable, Sendable {
    public var id: String { value }
    public let value: String
    public let name: String?
    public let videos: Int

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        func s(_ k: String) -> String? { (c.lenient(AnyKey(stringValue: k)) as String?)?.nilIfBlank }
        guard let v = s("code") ?? s("id") ?? s("value") ?? s("letter") ?? s("name") else {
            throw DecodingError.dataCorrupted(.init(codingPath: decoder.codingPath, debugDescription: "a facet without a value"))
        }
        value = v
        name = s("name")
        videos = c.lenient(AnyKey(stringValue: "videos")) ?? 0
    }
}

/// The order of a library's Series tab.
public enum LibrarySeriesSort: String, Sendable, CaseIterable {
    case name, newest, recent
}

/// `GET /api/libraries/{id}/series`: a page of a library's series.
public struct LibrarySeriesPage: Decodable, Sendable {
    public let library: LibraryRef?
    public let items: [LibrarySeries]
    public let total: Int
    /// The next page's offset (nil: the end).
    public let next: Int?

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        library = c.lenient(AnyKey(stringValue: "library"))
        items = c.lenientList(AnyKey(stringValue: "items"))
        total = c.lenient(AnyKey(stringValue: "total")) ?? items.count
        next = c.lenient(AnyKey(stringValue: "next"))
    }
}

/// A series on a library's Series tab, with the person's progress through it.
public struct LibrarySeries: Decodable, Identifiable, Hashable, Sendable {
    public let id: String
    public let name: String
    public let posterVideoID: String?
    public let episodes: Int
    public let watched: Int
    public let inProgress: Int
    public let latestAddedAt: Date?
    public let lastWatchedAt: Date?

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        func k(_ s: String) -> AnyKey { AnyKey(stringValue: s) }
        id = try c.decode(String.self, forKey: k("id"))
        name = c.lenient(k("name")) ?? ""
        posterVideoID = c.lenient(k("poster_video_id"))
        episodes = c.lenient(k("episodes")) ?? 0
        watched = c.lenient(k("watched")) ?? 0
        inProgress = c.lenient(k("in_progress")) ?? 0
        latestAddedAt = c.lenient(k("latest_added_at"))
        lastWatchedAt = c.lenient(k("last_watched_at"))
    }

    /// How much of it was watched, 0...1.
    public var progress: Double { fraction(Double(watched), of: Double(episodes)) }
}

// MARK: Series

/// A series of `GET /api/series` and `GET /api/series/{id}`. The one-series answer leaves
/// the counts 0 (the home does not count there): a series' page counts its episodes.
public struct SeriesSummary: Decodable, Identifiable, Hashable, Sendable {
    public let id: String
    public let name: String
    public let libraryID: String?
    public let year: Int?
    /// `season` or `absolute`.
    public let numbering: String
    public let seasonCount: Int
    public let episodeCount: Int
    public let watchedCount: Int
    public let inProgress: Int
    public let posterVideoID: String?

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        func k(_ s: String) -> AnyKey { AnyKey(stringValue: s) }
        id = try c.decode(String.self, forKey: k("id"))
        name = c.lenient(k("name")) ?? ""
        libraryID = c.lenient(k("library_id"))
        year = c.lenient(k("year"))
        numbering = c.lenient(k("numbering")) ?? "season"
        seasonCount = c.lenient(k("season_count")) ?? 0
        episodeCount = c.lenient(k("episode_count")) ?? 0
        watchedCount = c.lenient(k("watched_count")) ?? 0
        inProgress = c.lenient(k("in_progress")) ?? 0
        posterVideoID = c.lenient(k("poster_video_id"))
    }

    /// How much of it was watched, 0...1.
    public var progress: Double { fraction(Double(watchedCount), of: Double(episodeCount)) }
}

struct SeriesList: Decodable {
    let items: [SeriesSummary]
    /// How many the home sent (an item that cannot be read still counts toward a full page).
    let sent: Int
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        items = c.lenientList(AnyKey(stringValue: "items"))
        sent = (try? c.nestedUnkeyedContainer(forKey: AnyKey(stringValue: "items")))?.count ?? items.count
    }
}

/// The order of a series' episodes.
public enum EpisodeSort: String, Sendable {
    /// By season and episode.
    case episode
    /// The newest added first.
    case added
}

/// `GET /api/series/{id}/episodes`: the seasons and their episodes, and what to watch next.
public struct SeriesEpisodes: Decodable, Sendable {
    public let numbering: String
    public let seasons: [SeriesSeason]
    /// The series' Continue: the first episode not finished after the last one watched.
    public let nextEpisode: Episode?

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        numbering = c.lenient(AnyKey(stringValue: "numbering")) ?? "season"
        seasons = c.lenientList(AnyKey(stringValue: "seasons"))
        nextEpisode = c.lenient(AnyKey(stringValue: "next_episode"))
    }

    /// Every episode, season by season.
    public var episodes: [Episode] { seasons.flatMap(\.episodes) }

    /// The episode after `video` (Up next), nil after the last.
    public func episode(after video: String) -> Episode? {
        let all = episodes
        guard let i = all.firstIndex(where: { $0.videoID == video }), i + 1 < all.count else { return nil }
        return all[i + 1]
    }
}

public struct SeriesSeason: Decodable, Identifiable, Hashable, Sendable {
    public var id: Int { season }
    /// 0: the episodes with no season.
    public let season: Int
    public let episodes: [Episode]

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        season = c.lenient(AnyKey(stringValue: "season")) ?? 0
        episodes = c.lenientList(AnyKey(stringValue: "episodes"))
    }
}

/// An episode of a series, with the person's progress.
public struct Episode: Decodable, Identifiable, Hashable, Sendable {
    public var id: String { videoID }
    public let videoID: String
    public let season: Int?
    public let episode: Int?
    public let absoluteNumber: Int?
    public let title: String
    /// Its picture's path on the home (`/api/videos/{id}/poster`).
    public let thumbnail: String?
    public let durationSec: Double?
    /// 0...100.
    public let progressPct: Double
    public let watched: Bool

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        func k(_ s: String) -> AnyKey { AnyKey(stringValue: s) }
        videoID = try c.decode(String.self, forKey: k("video_id"))
        season = c.lenient(k("season"))
        episode = c.lenient(k("episode"))
        absoluteNumber = c.lenient(k("absolute_number"))
        title = c.lenient(k("title")) ?? ""
        thumbnail = c.lenient(k("thumbnail"))
        durationSec = c.lenient(k("duration_sec"))
        progressPct = c.lenient(k("progress_pct")) ?? 0
        watched = c.lenient(k("watched")) ?? false
    }

    /// How much was watched, 0...1.
    public var progress: Double { watched ? 1 : min(max(progressPct / 100, 0), 1) }
}

// MARK: a video

/// `GET /api/videos/{id}`: a video's page.
public struct VideoInfo: Decodable, Identifiable, Hashable, Sendable {
    public let id: String
    public let libraryID: String?
    public let filename: String
    public let title: String?
    public let description: String?
    public let durationSec: Double?
    public let state: String?
    public let detectedLanguage: String?
    public let createdAt: Date?
    public let publishedAt: Date?
    /// The file is there to play (false: its drive is not mounted).
    public let playable: Bool
    /// Where this person stopped.
    public let playbackState: PlaybackState?

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        func k(_ s: String) -> AnyKey { AnyKey(stringValue: s) }
        id = try c.decode(String.self, forKey: k("id"))
        libraryID = c.lenient(k("library_id"))
        filename = c.lenient(k("filename")) ?? ""
        title = c.lenient(k("title"))
        description = (c.lenient(k("description")) as String?)?.nilIfBlank
        durationSec = c.lenient(k("duration_sec"))
        state = c.lenient(k("state"))
        detectedLanguage = c.lenient(k("detected_language"))
        createdAt = c.lenient(k("created_at"))
        publishedAt = c.lenient(k("published_at"))
        playable = c.lenient(k("playable")) ?? true
        playbackState = (c.lenient(k("playback_state")) as LenientPlayback?)?.state
    }

    public var displayTitle: String { title?.nilIfBlank ?? filename }

    /// Where Resume opens (nil: Play from the start, the video is new or was finished).
    public var resumeAt: Double? {
        guard let p = playbackState, !p.completed, p.positionSec > 0 else { return nil }
        return p.positionSec
    }
}

private struct LenientPlayback: Decodable {
    let state: PlaybackState
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        state = PlaybackState(positionSec: c.lenient(AnyKey(stringValue: "position_sec")) ?? 0,
                              completed: c.lenient(AnyKey(stringValue: "completed")) ?? false)
    }
}

/// `GET /api/me/videos/{id}`: the person's own marks on a video.
public struct MyVideo: Decodable, Sendable {
    public let videoID: String
    public let positionSec: Double
    public let completed: Bool
    public let favorite: Bool
    public let playlists: [PlaylistRef]
    public let series: SeriesRef?

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        func k(_ s: String) -> AnyKey { AnyKey(stringValue: s) }
        videoID = try c.decode(String.self, forKey: k("video_id"))
        positionSec = c.lenient(k("position_sec")) ?? 0
        completed = c.lenient(k("completed")) ?? false
        favorite = c.lenient(k("favorite")) ?? false
        playlists = c.lenientList(k("playlists"))
        series = c.lenient(k("series"))
    }
}

public struct PlaylistRef: Decodable, Identifiable, Hashable, Sendable {
    public let id: String
    public let name: String

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        id = try c.decode(String.self, forKey: AnyKey(stringValue: "id"))
        name = c.lenient(AnyKey(stringValue: "name")) ?? ""
    }
}

/// A chapter of a video (`GET /api/videos/{id}/chapters`).
public struct Chapter: Decodable, Identifiable, Hashable, Sendable {
    public let id: Int
    public let seq: Int
    public let startSec: Double
    /// nil: to the end.
    public let endSec: Double?
    public let title: String

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        func k(_ s: String) -> AnyKey { AnyKey(stringValue: s) }
        startSec = try c.decode(Double.self, forKey: k("start_sec"))
        seq = c.lenient(k("seq")) ?? 0
        id = c.lenient(k("id")) ?? seq
        endSec = c.lenient(k("end_sec"))
        title = c.lenient(k("title")) ?? ""
    }
}

/// A subtitle of a video (`GET /api/videos/{id}/subtitles`); `url` is on the home.
public struct SubtitleTrack: Decodable, Identifiable, Hashable, Sendable {
    public let id: Int
    public let language: String
    /// `vtt` or `srt`.
    public let format: String
    /// `embedded`, `generated` or `external`.
    public let source: String?
    public let isDefault: Bool
    public let url: String

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        func k(_ s: String) -> AnyKey { AnyKey(stringValue: s) }
        id = try c.decode(Int.self, forKey: k("id"))
        url = try c.decode(String.self, forKey: k("url"))
        language = c.lenient(k("language")) ?? ""
        format = c.lenient(k("format")) ?? "vtt"
        source = c.lenient(k("source"))
        isDefault = c.lenient(k("is_default")) ?? false
    }
}

/// `GET /api/videos/{id}/segments`: the transcript (a window of it).
public struct Transcript: Decodable, Sendable {
    public let segments: [TranscriptSegment]
    /// The transcript is still being written.
    public let partial: Bool

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        segments = c.lenientList(AnyKey(stringValue: "items"))
        partial = c.lenient(AnyKey(stringValue: "partial")) ?? false
    }
}

public struct TranscriptSegment: Decodable, Identifiable, Hashable, Sendable {
    public let id: Int
    public let seq: Int
    public let startSec: Double
    public let endSec: Double
    public let text: String
    public let speaker: String?

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        func k(_ s: String) -> AnyKey { AnyKey(stringValue: s) }
        startSec = try c.decode(Double.self, forKey: k("start_sec"))
        seq = c.lenient(k("seq")) ?? 0
        id = c.lenient(k("id")) ?? seq
        endSec = c.lenient(k("end_sec")) ?? startSec
        text = c.lenient(k("text")) ?? ""
        speaker = c.lenient(k("speaker"))
    }
}

/// `GET /api/videos/{id}/trickplay`: the scrubber's thumbnails, one sprite sheet of
/// `columns` × `rows` tiles, one every `intervalSec`.
public struct TrickplayIndex: Decodable, Hashable, Sendable {
    public let spriteURL: String
    public let columns: Int
    public let rows: Int
    public let count: Int
    public let intervalSec: Double
    public let tileWidth: Int
    public let tileHeight: Int

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        func k(_ s: String) -> AnyKey { AnyKey(stringValue: s) }
        spriteURL = try c.decode(String.self, forKey: k("sprite_url"))
        columns = max(1, c.lenient(k("columns")) ?? 1)
        rows = max(1, c.lenient(k("rows")) ?? 1)
        count = c.lenient(k("count")) ?? 0
        intervalSec = c.lenient(k("interval_sec")) ?? 10
        tileWidth = c.lenient(k("tile_width")) ?? 0
        tileHeight = c.lenient(k("tile_height")) ?? 0
    }

    /// The tile for `second`: its column and row on the sheet (nil: no tiles).
    public func tile(at second: Double) -> (column: Int, row: Int)? {
        guard count > 0, intervalSec > 0 else { return nil }
        let i = min(max(Int(second / intervalSec), 0), count - 1)
        return (i % columns, i / columns)
    }
}

// MARK: Search

public enum SearchMode: String, Sendable, CaseIterable {
    case hybrid, fts, semantic
}

/// A video a search found, in titles or in transcripts.
public struct SearchVideo: Decodable, Identifiable, Hashable, Sendable {
    public var id: String { videoID }
    public let videoID: String
    public let title: String
    public let durationSec: Double?
    public let libraryID: String?
    public let libraryName: String?
    public let score: Double?
    /// The title with the matches in `<mark>` (`SearchSnippet`).
    public let titleSnippet: String?
    /// Titles: it also matched in its transcript. Transcripts: it also matched in its title.
    public let alsoElsewhere: Bool
    /// Transcripts: how many passages matched, and the first of them.
    public let matches: Int
    public let passages: [Passage]

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        func k(_ s: String) -> AnyKey { AnyKey(stringValue: s) }
        videoID = try c.decode(String.self, forKey: k("video_id"))
        title = c.lenient(k("title")) ?? ""
        durationSec = c.lenient(k("duration_sec"))
        libraryID = c.lenient(k("library_id"))
        libraryName = c.lenient(k("library_name"))
        score = c.lenient(k("score")) ?? c.lenient(k("best_score"))
        titleSnippet = c.lenient(k("title_snippet"))
        alsoElsewhere = (c.lenient(k("in_transcripts")) ?? c.lenient(k("in_title"))) ?? false
        passages = c.lenientList(k("passages"))
        matches = c.lenient(k("matches")) ?? passages.count
    }
}

/// A transcript passage a search found: it plays from `startSec`.
public struct Passage: Decodable, Identifiable, Hashable, Sendable {
    public var id: Int { segmentID }
    public let segmentID: Int
    public let startSec: Double
    public let endSec: Double
    /// The text with the matches in `<mark>` (`SearchSnippet`).
    public let snippet: String?
    public let score: Double?

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        func k(_ s: String) -> AnyKey { AnyKey(stringValue: s) }
        segmentID = try c.decode(Int.self, forKey: k("segment_id"))
        startSec = c.lenient(k("start_sec")) ?? 0
        endSec = c.lenient(k("end_sec")) ?? startSec
        snippet = c.lenient(k("snippet"))
        score = c.lenient(k("score"))
    }
}

/// `POST /api/search`: the first page of both groups, and the saved search's id to page on.
public struct SearchAnswer: Decodable, Sendable {
    public let searchID: String
    public let query: String
    public let mode: String?
    public let pageSize: Int
    public let passagesPageSize: Int
    /// Titles (page 1).
    public let titles: [SearchVideo]
    public let titlesTotal: Int
    /// In the transcript, one row per video (page 1).
    public let transcripts: [SearchVideo]
    public let transcriptsTotal: Int
    /// The home answered without its semantic half (keywords only).
    public let degraded: Bool

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        func k(_ s: String) -> AnyKey { AnyKey(stringValue: s) }
        searchID = c.lenient(k("search_id")) ?? ""
        query = c.lenient(k("q")) ?? ""
        mode = c.lenient(k("mode"))
        pageSize = c.lenient(k("page_size")) ?? 24
        passagesPageSize = c.lenient(k("passages_page_size")) ?? 20
        titles = c.lenientList(k("videos"))
        titlesTotal = c.lenient(k("videos_total")) ?? titles.count
        transcripts = c.lenientList(k("transcript_videos"))
        transcriptsTotal = c.lenient(k("transcript_total")) ?? transcripts.count
        degraded = c.lenient(k("degraded")) ?? false
    }
}

/// Which group of a saved search to page.
public enum SearchList: String, Sendable {
    case titles, transcripts
}

/// `GET /api/search/results/{sid}` and `/api/search/passages`: one page.
public struct SearchPage<Item: Decodable & Sendable>: Decodable, Sendable {
    public let searchID: String
    public let offset: Int
    public let limit: Int
    public let total: Int
    public let items: [Item]

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        func k(_ s: String) -> AnyKey { AnyKey(stringValue: s) }
        searchID = c.lenient(k("search_id")) ?? ""
        items = c.lenientList(k("items"))
        offset = c.lenient(k("offset")) ?? 0
        limit = c.lenient(k("limit")) ?? items.count
        total = c.lenient(k("total")) ?? items.count
    }

    /// The next page's offset (nil: the end).
    /// It steps by the page's `limit`, not by the items read: the home leaves out a video the
    /// person may no longer see, and a row that cannot be read is dropped here, so a short
    /// page is not the last.
    public var nextOffset: Int? {
        let step = limit > 0 ? limit : items.count
        guard step > 0, offset + step < total else { return nil }
        return offset + step
    }
}

/// A search the person ran (`GET /api/search/recent`).
public struct RecentSearch: Decodable, Identifiable, Hashable, Sendable {
    public var id: String { searchID }
    public let searchID: String
    public let query: String
    public let mode: String?
    public let lastUsedAt: Date?
    /// Its saved results are still there (else it runs again).
    public let live: Bool

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        func k(_ s: String) -> AnyKey { AnyKey(stringValue: s) }
        searchID = try c.decode(String.self, forKey: k("search_id"))
        query = c.lenient(k("q")) ?? ""
        mode = c.lenient(k("mode"))
        lastUsedAt = c.lenient(k("last_used_at")) ?? c.lenient(k("created_at"))
        live = c.lenient(k("live")) ?? false
    }
}

/// A snippet's text in runs, the matches marked: the home marks them with `<mark>` (and
/// escapes nothing else), so the text is never read as HTML.
public enum SearchSnippet {
    public struct Run: Hashable, Sendable {
        public let text: String
        public let marked: Bool
    }

    public static func runs(_ snippet: String) -> [Run] {
        var out: [Run] = []
        var rest = Substring(snippet)
        var marked = false
        while !rest.isEmpty {
            let tag = marked ? "</mark>" : "<mark>"
            if let r = rest.range(of: tag) {
                if r.lowerBound > rest.startIndex { out.append(Run(text: String(rest[..<r.lowerBound]), marked: marked)) }
                rest = rest[r.upperBound...]
                marked.toggle()
            } else {
                out.append(Run(text: String(rest), marked: marked))
                break
            }
        }
        return out
    }

    /// The snippet without its marks.
    public static func plain(_ snippet: String) -> String { runs(snippet).map(\.text).joined() }
}

// MARK: Profile

/// `GET /api/me`: the person's profile.
public struct Profile: Decodable, Sendable {
    public let id: String
    public let username: String
    public let displayName: String?
    public let email: String?
    public let isAdmin: Bool

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        func k(_ s: String) -> AnyKey { AnyKey(stringValue: s) }
        id = try c.decode(String.self, forKey: k("id"))
        username = c.lenient(k("username")) ?? ""
        displayName = (c.lenient(k("display_name")) as String?)?.nilIfBlank
        email = (c.lenient(k("email")) as String?)?.nilIfBlank
        isAdmin = c.lenient(k("is_admin")) ?? false
    }

    /// The name to show: the display name, else the username.
    public var name: String { displayName ?? username }
}

/// A watched video in the person's history (`GET /api/me/history`).
public struct HistoryEntry: Decodable, Identifiable, Hashable, Sendable {
    public var id: String { videoID }
    public let videoID: String
    public let title: String
    public let durationSec: Double?
    public let timesWatched: Int
    public let lastWatchedAt: Date?
    public let positionSec: Double
    public let completed: Bool

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        func k(_ s: String) -> AnyKey { AnyKey(stringValue: s) }
        videoID = try c.decode(String.self, forKey: k("video_id"))
        title = c.lenient(k("title")) ?? ""
        durationSec = c.lenient(k("duration_sec"))
        timesWatched = c.lenient(k("times_watched")) ?? 0
        lastWatchedAt = c.lenient(k("last_watched_at"))
        positionSec = c.lenient(k("position_sec")) ?? 0
        completed = c.lenient(k("completed")) ?? false
    }

    public var progress: Double { completed ? 1 : fraction(positionSec, of: durationSec) }
}

/// A signed-in web session of the person (`GET /api/me/sessions`).
public struct SignInSession: Decodable, Identifiable, Hashable, Sendable {
    public let id: String
    public let createdAt: Date?
    public let lastSeenAt: Date?
    public let expiresAt: Date?
    public let ip: String?
    public let userAgent: String?
    /// The session this request came with.
    public let current: Bool

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        func k(_ s: String) -> AnyKey { AnyKey(stringValue: s) }
        id = try c.decode(String.self, forKey: k("id"))
        createdAt = c.lenient(k("created_at"))
        lastSeenAt = c.lenient(k("last_seen_at"))
        expiresAt = c.lenient(k("expires_at"))
        ip = c.lenient(k("ip"))
        userAgent = c.lenient(k("user_agent"))
        current = c.lenient(k("current")) ?? false
    }
}

/// `GET /api/me/cloud-link`: whether this home is in Maktaba Cloud, and the person's link.
public struct CloudLinkState: Decodable, Sendable {
    public let available: Bool
    /// The linked cloud account's label (nil: not linked).
    public let linkedLabel: String?
    public let linked: Bool

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        available = c.lenient(AnyKey(stringValue: "available")) ?? false
        let link = c.lenientFields(AnyKey(stringValue: "link"))
        linked = !link.isEmpty
        linkedLabel = link["cloud_label"]?.nilIfBlank
    }
}

/// A playlist of the person (`GET /api/me/playlists`).
public struct PlaylistSummary: Decodable, Identifiable, Hashable, Sendable {
    public let id: String
    public let name: String
    public let description: String?
    public let videos: Int
    public let durationSec: Double
    public let posterVideoIDs: [String]
    public let updatedAt: Date?

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        func k(_ s: String) -> AnyKey { AnyKey(stringValue: s) }
        id = try c.decode(String.self, forKey: k("id"))
        name = c.lenient(k("name")) ?? ""
        description = (c.lenient(k("description")) as String?)?.nilIfBlank
        videos = c.lenient(k("videos")) ?? 0
        durationSec = c.lenient(k("duration_sec")) ?? 0
        posterVideoIDs = c.lenientList(k("poster_video_ids"))
        updatedAt = c.lenient(k("updated_at"))
    }
}

/// `GET /api/me/playlists/{id}`: a playlist and its videos in order.
public struct Playlist: Decodable, Sendable {
    public let playlist: PlaylistSummary
    public let items: [BrowseVideo]

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyKey.self)
        playlist = try c.decode(PlaylistSummary.self, forKey: AnyKey(stringValue: "playlist"))
        items = c.lenientList(AnyKey(stringValue: "items"))
    }
}

/// `{"items": [...]}`, read leniently.
struct Items<Item: Decodable>: Decodable {
    let items: [Item]
    init(from decoder: Decoder) throws {
        items = try decoder.container(keyedBy: AnyKey.self).lenientList(AnyKey(stringValue: "items"))
    }
}

private struct Suggestions: Decodable {
    let suggestions: [String]
    init(from decoder: Decoder) throws {
        suggestions = try decoder.container(keyedBy: AnyKey.self).lenientList(AnyKey(stringValue: "suggestions"))
    }
}

private struct Updated: Decodable {
    let updated: Int
    init(from decoder: Decoder) throws {
        updated = try decoder.container(keyedBy: AnyKey.self).lenient(AnyKey(stringValue: "updated")) ?? 0
    }
}

private struct TrackingSetting: Decodable {
    let on: Bool
    init(from decoder: Decoder) throws {
        on = try decoder.container(keyedBy: AnyKey.self).lenient(AnyKey(stringValue: "track_enabled")) ?? true
    }
}

// MARK: the calls

public extension APIClient {
    // Home

    /// Home's rows (`GET /api/home`).
    func home() async throws -> HomeRows {
        try await browse(request("GET", "/api/home"))
    }

    /// Home's For You and From your library rails (`GET /api/recommendations`).
    func homeRails(surface: String = "mobile-home", limit: Int = 20) async throws -> [Rail] {
        let r: LenientRecommendations = try await browse(
            request("GET", "/api/recommendations", query: ["surface": surface, "limit": String(limit)]))
        return r.rails
    }

    /// "Not interested": the video leaves the person's recommendations.
    func dismissRecommendation(video: String) async throws {
        _ = try await browseRaw(request("DELETE", "/api/recommendations/items/\(Self.segment(video))"))
    }

    /// Hides a whole rail (`reason_kind`) from the person's recommendations.
    func dismissRecommendations(reasonKind: String) async throws {
        _ = try await browseRaw(request("DELETE", "/api/recommendations/rows/\(Self.segment(reasonKind))"))
    }

    // Libraries

    /// The library cards, by name (`LibrarySummary.ordered` puts the pinned first).
    func librarySummaries() async throws -> [LibrarySummary] {
        let r: Items<LibrarySummary> = try await browse(request("GET", "/api/libraries/summary"))
        return r.items
    }

    /// The person's pinned libraries, in their order.
    func pinnedLibraries() async throws -> [PinnedLibrary] {
        let r: PinnedLibraries = try await browse(request("GET", "/api/me/pinned-libraries"))
        return r.items
    }

    /// Sets the pinned libraries to `ids`, in that order (an empty list unpins all).
    func setPinnedLibraries(_ ids: [String]) async throws -> [PinnedLibrary] {
        let r: PinnedLibraries = try await browse(request("PUT", "/api/me/pinned-libraries", body: ["library_ids": ids]))
        return r.items
    }

    // a library

    func libraryHub(_ library: String) async throws -> LibraryHub {
        try await browse(request("GET", "/api/libraries/\(Self.segment(library))/hub"), missing: Self.oneThing)
    }

    /// A page of a library's grid (`GET /api/videos`). The favorites filter is read on
    /// `GET /api/me/videos` (the same list, from the home itself): a joined machine serving
    /// `/api/videos` has no favorites and refuses it (the web's `videosUrl` does the same).
    func videos(_ query: VideoQuery) async throws -> VideoListPage {
        try await browse(request("GET", query.favorite == true ? "/api/me/videos" : "/api/videos", query: query.items))
    }

    /// What the grid can be narrowed by (`GET /api/videos/facets`), for one library or all.
    func videoFacets(library: String? = nil) async throws -> VideoFacets {
        try await browse(request("GET", "/api/videos/facets", query: library.map { ["library": $0] } ?? [:]))
    }

    /// A page of a library's Series tab.
    func librarySeries(_ library: String, sort: LibrarySeriesSort = .name, offset: Int = 0,
                       limit: Int = 60) async throws -> LibrarySeriesPage {
        var q = ["sort": sort.rawValue, "limit": String(limit)]
        if offset > 0 { q["offset"] = String(offset) }
        return try await browse(request("GET", "/api/libraries/\(Self.segment(library))/series", query: q),
                                missing: Self.oneThing)
    }

    // Series

    /// The home's page size for `GET /api/series`.
    static let seriesPageSize = 60

    /// One page (from 0) of the series the person may see, or of one library's.
    func seriesList(library: String? = nil, page: Int) async throws -> [SeriesSummary] {
        try await seriesPage(library: library, page: page).items
    }

    /// One page (from 0) of the series, and whether the home has another: a grid asks for
    /// the next as it nears its end instead of waiting for all of them.
    func seriesListPage(library: String? = nil, page: Int) async throws -> (items: [SeriesSummary], more: Bool) {
        let r = try await seriesPage(library: library, page: page)
        return (r.items, r.sent >= Self.seriesPageSize)
    }

    /// Every series the person may see, or one library's: page after page until a short one.
    func seriesList(library: String? = nil) async throws -> [SeriesSummary] {
        var all: [SeriesSummary] = []
        var page = 0
        while true {
            let r = try await seriesPage(library: library, page: page)
            all += r.items
            // the home's count, not the items read (one this client could not read is dropped)
            if r.sent < Self.seriesPageSize { return all }
            page += 1
        }
    }

    private func seriesPage(library: String?, page: Int) async throws -> SeriesList {
        var q = ["page": String(max(0, page))]
        if let library { q["library_id"] = library }
        return try await browse(request("GET", "/api/series", query: q))
    }

    func series(_ id: String) async throws -> SeriesSummary {
        try await browse(request("GET", "/api/series/\(Self.segment(id))"), missing: Self.oneThing)
    }

    func episodes(series id: String, sort: EpisodeSort = .episode) async throws -> SeriesEpisodes {
        try await browse(request("GET", "/api/series/\(Self.segment(id))/episodes", query: ["sort": sort.rawValue]),
                         missing: Self.oneThing)
    }

    // a video

    func videoDetail(_ id: String) async throws -> VideoInfo {
        try await browse(request("GET", "/api/videos/\(Self.segment(id))"), missing: Self.oneThing)
    }

    /// The person's marks on a video: favorite, watched, playlists.
    func myVideo(_ id: String) async throws -> MyVideo {
        try await browse(request("GET", "/api/me/videos/\(Self.segment(id))"), missing: Self.oneThing)
    }

    func chapters(video: String) async throws -> [Chapter] {
        let r: Items<Chapter> = try await browse(request("GET", "/api/videos/\(Self.segment(video))/chapters"),
                                                 missing: Self.oneThing)
        return r.items.sorted { $0.startSec < $1.startSec }
    }

    func subtitles(video: String) async throws -> [SubtitleTrack] {
        let r: Items<SubtitleTrack> = try await browse(request("GET", "/api/videos/\(Self.segment(video))/subtitles"),
                                                       missing: Self.oneThing)
        return r.items
    }

    /// The transcript from `from` to `to` seconds (nil: to the end), at most `limit` segments.
    func transcript(video: String, from: Double = 0, to: Double? = nil, limit: Int = 200) async throws -> Transcript {
        var q = ["limit": String(limit)]
        if from > 0 { q["from"] = Self.seconds(from) }
        if let to { q["to"] = Self.seconds(to) }
        return try await browse(request("GET", "/api/videos/\(Self.segment(video))/segments", query: q),
                                missing: Self.oneThing)
    }

    /// The scrubber's thumbnails; nil when the video has none yet (the home answers 404). A
    /// video the person may not see (the route's 403) is `BrowseError.notAvailable`.
    func trickplay(video: String) async throws -> TrickplayIndex? {
        do {
            return try await send(request("GET", "/api/videos/\(Self.segment(video))/trickplay"))
        } catch let e as APIError {
            if case .status(404, _, _) = e, e.kind == .notFound { return nil }
            throw Self.browseError(e, missing: Self.oneThing)
        }
    }

    /// The video's picture; `small` asks for the pipeline's small copy (an older home sends
    /// the full one).
    func poster(video: String, small: Bool) async throws -> Data {
        try await browseRaw(request("GET", "/api/videos/\(Self.segment(video))/poster",
                                    query: small ? ["size": "small"] : [:]), missing: Self.oneThing)
    }

    // marks

    /// Marks videos watched (from the start next time) or not. Answers how many changed.
    @discardableResult
    func markWatched(_ videos: [String], _ watched: Bool = true) async throws -> Int {
        let r: Updated = try await browse(request("POST", "/api/me/watched", body: ["video_ids": videos, "watched": watched]))
        return r.updated
    }

    @discardableResult
    func markFavorite(_ videos: [String], _ favorite: Bool = true) async throws -> Int {
        let r: Updated = try await browse(request("POST", "/api/me/favorites", body: ["video_ids": videos, "favorite": favorite]))
        return r.updated
    }

    // Search

    /// Runs a search; its results stay on the home under `searchID` for paging. The query is
    /// sent as typed (Arabic or English: the home ranks both).
    func search(_ query: String, mode: SearchMode = .hybrid) async throws -> SearchAnswer {
        try await browse(request("POST", "/api/search", body: ["q": query, "mode": mode.rawValue, "view": "grid"]))
    }

    /// A page of a saved search's group (`BrowseError.searchExpired`: run it again).
    func searchResults(_ searchID: String, list: SearchList, offset: Int, limit: Int) async throws -> SearchPage<SearchVideo> {
        try await browse(request("GET", "/api/search/results/\(Self.segment(searchID))",
                                 query: ["list": list.rawValue, "offset": String(offset), "limit": String(limit)]))
    }

    /// A page of one video's passages in a saved search, best first.
    func searchPassages(_ searchID: String, video: String, offset: Int, limit: Int) async throws -> SearchPage<Passage> {
        try await browse(request("GET", "/api/search/passages",
                                 query: ["sid": searchID, "video_id": video, "order": "score",
                                         "offset": String(offset), "limit": String(limit)]))
    }

    func searchSuggestions(_ prefix: String) async throws -> [String] {
        let r: Suggestions = try await browse(request("GET", "/api/search/suggest", query: ["q": prefix]))
        return r.suggestions
    }

    func recentSearches() async throws -> [RecentSearch] {
        let r: Items<RecentSearch> = try await browse(request("GET", "/api/search/recent"))
        return r.items
    }

    /// Forgets one recent search, or all of them (nil).
    func forgetRecentSearch(_ searchID: String? = nil) async throws {
        let path = searchID.map { "/api/search/recent/\(Self.segment($0))" } ?? "/api/search/recent"
        _ = try await browseRaw(request("DELETE", path))
    }

    // Profile

    func profile() async throws -> Profile {
        try await browse(request("GET", "/api/me"))
    }

    /// Changes the display name (and the email, when given).
    func updateProfile(displayName: String? = nil, email: String? = nil) async throws -> Profile {
        var body: [String: Any] = [:]
        if let displayName { body["display_name"] = displayName }
        if let email { body["email"] = email }
        return try await browse(request("PATCH", "/api/me", body: body))
    }

    func watchHistory(offset: Int = 0, limit: Int = 50) async throws -> [HistoryEntry] {
        let r: Items<HistoryEntry> = try await browse(
            request("GET", "/api/me/history", query: ["offset": String(offset), "limit": String(limit)]))
        return r.items
    }

    /// Removes a video from the history.
    func forgetHistory(video: String) async throws {
        _ = try await browseRaw(request("DELETE", "/api/me/history/\(Self.segment(video))"))
    }

    /// Whether the home keeps the person's watch history.
    func historyTracking() async throws -> Bool {
        let r: TrackingSetting = try await browse(request("GET", "/api/me/activity/settings"))
        return r.on
    }

    func setHistoryTracking(_ on: Bool) async throws {
        _ = try await browseRaw(request("PUT", "/api/me/activity/settings", body: ["track_enabled": on]))
    }

    func signInSessions() async throws -> [SignInSession] {
        let r: Items<SignInSession> = try await browse(request("GET", "/api/me/sessions"))
        return r.items
    }

    /// Signs one session out.
    func endSignInSession(_ id: String) async throws {
        _ = try await browseRaw(request("DELETE", "/api/me/sessions/\(Self.segment(id))"))
    }

    func cloudLink() async throws -> CloudLinkState {
        try await browse(request("GET", "/api/me/cloud-link"))
    }

    func playlists() async throws -> [PlaylistSummary] {
        let r: Items<PlaylistSummary> = try await browse(request("GET", "/api/me/playlists"))
        return r.items
    }

    func playlist(_ id: String) async throws -> Playlist {
        try await browse(request("GET", "/api/me/playlists/\(Self.segment(id))"), missing: Self.oneThing)
    }
}

// MARK: plumbing

extension APIClient {
    /// The answers that mean "not available": any route's 404 and 410, and also the 403 the
    /// video routes answer for a video of a library the caller may not see (`oneThing`).
    static let anyRoute: Set<Int> = [404, 410]
    static let oneThing: Set<Int> = [403, 404, 410]

    func browse<T: Decodable>(_ r: URLRequest, missing: Set<Int> = anyRoute) async throws -> T {
        do {
            return try await send(r)
        } catch let e as APIError {
            throw Self.browseError(e, missing: missing)
        }
    }

    func browseRaw(_ r: URLRequest, missing: Set<Int> = anyRoute) async throws -> Data {
        do {
            return try await sendRaw(r)
        } catch let e as APIError {
            throw Self.browseError(e, missing: missing)
        }
    }

    /// `e` as a browse screen tells it: a 404 (or the route's other "not yours to see") is
    /// `.notAvailable`, a saved search gone is `.searchExpired`; a 401 has already become
    /// `APIError.signedOut`, and the rest stay as they are (a front door's "home unreachable"
    /// 404 too).
    static func browseError(_ e: APIError, missing: Set<Int>) -> Error {
        guard case let .status(code, _, _) = e else { return e }
        if code == 410, e.problem?.slug == "search-expired" { return BrowseError.searchExpired }
        guard missing.contains(code), e.kind != .unreachable, e.kind != .notReady else { return e }
        return BrowseError.notAvailable
    }

    /// Seconds for a query: whole when whole.
    static func seconds(_ s: Double) -> String {
        s == s.rounded() ? String(Int(s)) : String(s)
    }
}

/// `part` of `whole`, 0...1 (0 when either is unknown).
func fraction(_ part: Double?, of whole: Double?) -> Double {
    guard let part, let whole, whole > 0 else { return 0 }
    return min(max(part / whole, 0), 1)
}

extension String {
    /// nil when only whitespace.
    var nilIfBlank: String? {
        trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? nil : self
    }
}
