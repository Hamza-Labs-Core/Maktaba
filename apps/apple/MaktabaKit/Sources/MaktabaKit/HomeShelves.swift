import Foundation

/// A TV home's shelves, top to bottom, in the iPhone/iPad Player's Home order (FR-10):
/// Continue watching, Up next (On Deck), Live TV on now, Favorites, Recently added, Recently
/// watched, one shelf per pinned library, For you, From your libraries. Built from the same
/// three answers the Player reads (`GET /api/home`, `GET /api/recommendations`,
/// `GET /api/channels/now`), so the platforms show the same rows.
///
/// A shelf with nothing in it is left out. A source that fails is not: its shelf stays, as a
/// retry tile (`.failed`), so a row never just vanishes. Live TV is the exception for a person
/// (or a home) without it: a 403 or 404 there means no Live TV, and no shelf.
public struct HomeShelf: Identifiable, Equatable, Sendable {
    /// What the shelf is; the screen says its title (translated), a library by its name.
    public enum Kind: Hashable, Sendable {
        case continueWatching, upNext, liveNow, favorites, recentlyAdded, recentlyWatched
        case library(id: String, name: String)
        case forYou, fromLibrary
        /// `GET /api/home` did not answer: its rows, as one retry tile.
        case home
        /// `GET /api/recommendations` did not answer: For you and From your libraries.
        case recommendations
    }

    public enum Content: Equatable, Sendable {
        case cards([HomeShelfCard])
        case channels([ChannelNow])
        /// Could not be read; the tile's Select reads again.
        case failed(HomeFailure)
    }

    /// Stable: the list's id and the accessibility identifier (`home.row.<key>`).
    public let key: String
    public let kind: Kind
    public var content: Content

    public var id: String { key }

    public static let continueWatching = "continue"
    public static let upNext = "ondeck"
    public static let liveNow = "live"
    public static let favorites = "favorites"
    public static let recentlyAdded = "added"
    public static let recentlyWatched = "watched"
    public static let libraryPrefix = "pinned-"
    public static let forYou = "rec-for_you"
    public static let fromLibrary = "rec-from_library"
    public static let home = "home"
    public static let recommendations = "rec"
}

/// A card on a shelf: a video (its poster from `GET /api/videos/{id}/poster`), or a series of
/// Recently added with its new lessons (opened at the series' newest lesson).
public struct HomeShelfCard: Identifiable, Hashable, Sendable {
    public let videoID: String
    /// Nil for a recommendation the home sent without a title (the card asks for the video).
    public let title: String?
    public let subtitle: String?
    /// 0 to 1; 0 when nothing was watched.
    public let progress: Double
    /// A series' id when this card stands for its new lessons.
    public let seriesID: String?
    public let newLessons: Int?

    public var id: String { seriesID.map { "series:\($0)" } ?? videoID }

    public init(videoID: String, title: String?, subtitle: String? = nil, progress: Double = 0,
                seriesID: String? = nil, newLessons: Int? = nil) {
        self.videoID = videoID
        self.title = title
        self.subtitle = subtitle
        self.progress = progress
        self.seriesID = seriesID
        self.newLessons = newLessons
    }
}

public enum HomeShelves {
    /// The recommendation kinds a home shelf shows (Continue watching's twin is Home's own).
    static let railKinds: [(kind: String, key: String, shelf: HomeShelf.Kind)] = [
        ("for_you", HomeShelf.forYou, .forYou),
        ("from_library", HomeShelf.fromLibrary, .fromLibrary),
    ]

    /// Reads the three answers together and builds the shelves (the calls are the client's;
    /// tests pass their own).
    public static func load(
        home: @escaping @Sendable () async throws -> HomeRows,
        rails: @escaping @Sendable () async throws -> [Rail],
        now: @escaping @Sendable () async throws -> ChannelsNow
    ) async -> [HomeShelf] {
        async let h = attempt(home)
        async let r = attempt(rails)
        async let n = attempt { try await now().channels }
        return await build(home: h, rails: r, now: n)
    }

    /// The shelves from what each source answered (or why it did not).
    public static func build(home: Result<HomeRows, Error>, rails: Result<[Rail], Error>,
                             now: Result<[ChannelNow], Error>) -> [HomeShelf] {
        var out: [HomeShelf] = []
        func add(_ key: String, _ kind: HomeShelf.Kind, _ cards: [HomeShelfCard]) {
            if !cards.isEmpty { out.append(HomeShelf(key: key, kind: kind, content: .cards(cards))) }
        }
        var names: [String: (title: String, series: String?)] = [:]
        switch home {
        case let .success(h):
            add(HomeShelf.continueWatching, .continueWatching, h.continueWatching.map(card))
            add(HomeShelf.upNext, .upNext, h.onDeck.map(card))
            add(HomeShelf.favorites, .favorites, h.favorites.map(card))
            // grouped by series when the home sends it (an older home: the plain list)
            add(HomeShelf.recentlyAdded, .recentlyAdded,
                h.recentlyAddedGrouped.isEmpty ? h.recentlyAdded.map(card) : h.recentlyAddedGrouped.map(card))
            add(HomeShelf.recentlyWatched, .recentlyWatched, h.recentlyWatched.map(card))
            for p in h.pinned {
                // what the person is in the middle of first, then what is new (the web's order)
                var seen = Set<String>()
                let videos = (p.continueWatching + p.recentlyAdded).filter { seen.insert($0.id).inserted }
                add(HomeShelf.libraryPrefix + p.library.id, .library(id: p.library.id, name: p.library.name),
                    videos.map(card))
            }
            let all = h.continueWatching + h.onDeck + h.favorites + h.recentlyAdded
                + h.recentlyAddedGrouped.map(\.video) + h.recentlyWatched
                + h.pinned.flatMap { $0.continueWatching + $0.recentlyAdded }
            for v in all { names[v.id] = (v.displayTitle, v.series?.name) }
        case let .failure(e):
            out.append(HomeShelf(key: HomeShelf.home, kind: .home, content: .failed(HomeFailure(e))))
        }

        switch now {
        case let .success(channels) where !channels.isEmpty:
            // after Continue watching and Up next, before the rest
            let at = out.prefix { [HomeShelf.continueWatching, HomeShelf.upNext].contains($0.key) }.count
            out.insert(HomeShelf(key: HomeShelf.liveNow, kind: .liveNow, content: .channels(channels)), at: at)
        case let .failure(e) where !noLiveTV(e):
            let at = out.prefix { [HomeShelf.continueWatching, HomeShelf.upNext].contains($0.key) }.count
            out.insert(HomeShelf(key: HomeShelf.liveNow, kind: .liveNow, content: .failed(HomeFailure(e))), at: at)
        default:
            break
        }

        switch rails {
        case let .success(rails):
            for (kind, key, shelf) in railKinds {
                var seen = Set<String>()
                let items = rails.filter { $0.reasonKind == kind }.flatMap(\.items)
                    .filter { seen.insert($0.videoID).inserted }
                add(key, shelf, items.map { item in
                    HomeShelfCard(videoID: item.videoID, title: names[item.videoID]?.title,
                                  subtitle: names[item.videoID]?.series, progress: item.progress)
                })
            }
        case let .failure(e):
            out.append(HomeShelf(key: HomeShelf.recommendations, kind: .recommendations, content: .failed(HomeFailure(e))))
        }
        return out
    }

    static func card(_ v: BrowseVideo) -> HomeShelfCard {
        HomeShelfCard(videoID: v.id, title: v.displayTitle, subtitle: v.series?.name, progress: max(0, v.progress))
    }

    static func card(_ e: AddedEntry) -> HomeShelfCard {
        if e.kind == "series", let s = e.series {
            let name = s.name.trimmingCharacters(in: .whitespaces)
            return HomeShelfCard(videoID: e.video.id, title: name.isEmpty ? e.video.displayTitle : name,
                                 seriesID: s.id, newLessons: e.count ?? 1)
        }
        let c = card(e.video)
        return HomeShelfCard(videoID: c.videoID, title: c.title, subtitle: c.subtitle ?? e.series?.name, progress: c.progress)
    }

    /// Live TV is not this person's (or this home's): no shelf rather than a retry.
    static func noLiveTV(_ e: Error) -> Bool {
        if case let .status(code, _, _) = e as? APIError { return code == 403 || code == 404 }
        return false
    }

    private static func attempt<T: Sendable>(_ call: @Sendable () async throws -> T) async -> Result<T, Error> {
        do { return .success(try await call()) } catch { return .failure(error) }
    }
}
