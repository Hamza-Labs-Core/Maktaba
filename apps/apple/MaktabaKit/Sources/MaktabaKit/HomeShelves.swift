import Foundation

/// Home's shelves, top to bottom, for every Apple app that has a Home: the Player (iPhone,
/// iPad, Mac) and the Apple TV app build their rows here, the same way, in FR-10's order:
/// Continue watching, Up next (On Deck), Live TV on now, Favorites, Recently added, Recently
/// watched, one shelf per pinned library, For you, From your libraries. Built from the same
/// three answers (`GET /api/home`, `GET /api/recommendations`, `GET /api/channels/now`), so the
/// platforms show the same rows.
///
/// A shelf with nothing in it is left out. A source that fails is not: its shelf stays, as a
/// retry tile (`.failed`), so a row never just vanishes, unless the rows it showed before are
/// passed as `keeping`: a refresh that fails then keeps them. Live TV is the exception for a
/// person (or a home) without it: a 403 or 404 there means no Live TV, and no shelf.
public struct HomeShelf: Identifiable, Equatable, Sendable {
    /// What the shelf is; the screen says its title (translated), a library by its name.
    public enum Kind: Hashable, Codable, Sendable {
        case continueWatching, upNext, liveNow, favorites, recentlyAdded, recentlyWatched
        case library(id: String, name: String)
        case forYou, fromLibrary
        /// `GET /api/home` did not answer: its rows, as one retry tile.
        case home
        /// `GET /api/recommendations` did not answer: For you and From your libraries.
        case recommendations
    }

    /// Which of the three answers a shelf comes from (a failed one is one retry tile).
    public enum Source: Hashable, Sendable {
        case home, live, recommendations
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

    public init(key: String, kind: Kind, content: Content) {
        self.key = key
        self.kind = kind
        self.content = content
    }

    public var id: String { key }

    public var source: Source {
        switch kind {
        case .liveNow: .live
        case .forYou, .fromLibrary, .recommendations: .recommendations
        default: .home
        }
    }

    /// A recommendation's card has Not interested.
    public var isRecommendation: Bool { kind == .forYou || kind == .fromLibrary }

    /// The cards (empty for Live TV and a retry tile).
    public var cards: [HomeShelfCard] {
        if case let .cards(cards) = content { return cards }
        return []
    }

    public var failure: HomeFailure? {
        if case let .failed(f) = content { return f }
        return nil
    }

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
/// Recently added with its new lessons (opened at the series' newest lesson). `Codable`: the
/// Player keeps its last shelves on the device for the next launch (FR-12).
public struct HomeShelfCard: Identifiable, Hashable, Codable, Sendable {
    public let videoID: String
    /// Nil for a recommendation the home sent without a title (the card asks for the video).
    public let title: String?
    public let subtitle: String?
    /// 0 to 1; 0 when nothing was watched.
    public let progress: Double
    /// A series' id when this card stands for its new lessons.
    public let seriesID: String?
    public let newLessons: Int?
    /// Continue watching: where the person stopped, in seconds (Resume plays from there).
    public let resumeAt: Double?

    public var id: String { seriesID.map { "series:\($0)" } ?? videoID }

    public init(videoID: String, title: String?, subtitle: String? = nil, progress: Double = 0,
                seriesID: String? = nil, newLessons: Int? = nil, resumeAt: Double? = nil) {
        self.videoID = videoID
        self.title = title
        self.subtitle = subtitle
        self.progress = progress
        self.seriesID = seriesID
        self.newLessons = newLessons
        self.resumeAt = resumeAt
    }
}

public enum HomeShelves {
    /// The recommendation kinds a home shelf shows (Continue watching's twin is Home's own).
    static let railKinds: [(kind: String, key: String, shelf: HomeShelf.Kind)] = [
        ("for_you", HomeShelf.forYou, .forYou),
        ("from_library", HomeShelf.fromLibrary, .fromLibrary),
    ]

    /// Each source's answer, or why it did not come.
    public struct Answers: Sendable {
        public let home: Result<HomeRows, Error>
        public let rails: Result<[Rail], Error>
        public let now: Result<[ChannelNow], Error>
    }

    /// Asks the three sources together (the calls are the client's; tests pass their own).
    public static func read(
        home: @escaping @Sendable () async throws -> HomeRows,
        rails: @escaping @Sendable () async throws -> [Rail],
        now: @escaping @Sendable () async throws -> ChannelsNow
    ) async -> Answers {
        async let h = attempt(home)
        async let r = attempt(rails)
        async let n = attempt { try await now().channels }
        return await Answers(home: h, rails: r, now: n)
    }

    /// Reads the three answers together and builds the shelves; a source that fails keeps
    /// its shelves in `keeping` (what showed before), else becomes a retry tile.
    public static func load(
        home: @escaping @Sendable () async throws -> HomeRows,
        rails: @escaping @Sendable () async throws -> [Rail],
        now: @escaping @Sendable () async throws -> ChannelsNow,
        keeping: [HomeShelf] = []
    ) async -> [HomeShelf] {
        let a = await read(home: home, rails: rails, now: now)
        return build(home: a.home, rails: a.rails, now: a.now, keeping: keeping)
    }

    /// The shelves from what each source answered (or why it did not). A source that failed
    /// keeps its shelves of `keeping` when there are any (a refresh that fails changes nothing
    /// it showed); otherwise it is a retry tile where its rows would be.
    public static func build(home: Result<HomeRows, Error>, rails: Result<[Rail], Error>,
                             now: Result<[ChannelNow], Error>, keeping: [HomeShelf] = []) -> [HomeShelf] {
        var out: [HomeShelf] = []
        func add(_ key: String, _ kind: HomeShelf.Kind, _ cards: [HomeShelfCard]) {
            if !cards.isEmpty { out.append(HomeShelf(key: key, kind: kind, content: .cards(cards))) }
        }
        func kept(_ source: HomeShelf.Source) -> [HomeShelf] {
            keeping.filter { $0.source == source && $0.failure == nil }
        }
        var names: [String: (title: String, series: String?)] = [:]
        switch home {
        case let .success(h):
            add(HomeShelf.continueWatching, .continueWatching, h.continueWatching.map { card($0, resume: true) })
            add(HomeShelf.upNext, .upNext, h.onDeck.map { card($0) })
            add(HomeShelf.favorites, .favorites, h.favorites.map { card($0) })
            // grouped by series when the home sends it (an older home: the plain list)
            add(HomeShelf.recentlyAdded, .recentlyAdded,
                h.recentlyAddedGrouped.isEmpty ? h.recentlyAdded.map { card($0) } : h.recentlyAddedGrouped.map(card))
            add(HomeShelf.recentlyWatched, .recentlyWatched, h.recentlyWatched.map { card($0) })
            for p in h.pinned {
                // what the person is in the middle of first, then what is new (the web's order)
                var seen = Set<String>()
                let videos = (p.continueWatching + p.recentlyAdded).filter { seen.insert($0.id).inserted }
                add(HomeShelf.libraryPrefix + p.library.id, .library(id: p.library.id, name: p.library.name),
                    videos.map { card($0) })
            }
            let all = h.continueWatching + h.onDeck + h.favorites + h.recentlyAdded
                + h.recentlyAddedGrouped.map(\.video) + h.recentlyWatched
                + h.pinned.flatMap { $0.continueWatching + $0.recentlyAdded }
            for v in all { names[v.id] = (v.displayTitle, v.series?.name) }
        case let .failure(e):
            let old = kept(.home)
            if old.isEmpty {
                out.append(HomeShelf(key: HomeShelf.home, kind: .home, content: .failed(HomeFailure(e))))
            } else {
                out += old
                // the kept cards still name the videos a recommendation shows
                for c in old.flatMap(\.cards) where c.seriesID == nil {
                    if let t = c.title { names[c.videoID] = (t, c.subtitle) }
                }
            }
        }

        // after Continue watching and Up next, before the rest
        var liveAt: Int { out.prefix { [HomeShelf.continueWatching, HomeShelf.upNext].contains($0.key) }.count }
        switch now {
        case let .success(channels) where !channels.isEmpty:
            out.insert(HomeShelf(key: HomeShelf.liveNow, kind: .liveNow, content: .channels(channels)), at: liveAt)
        case let .failure(e) where !noLiveTV(e):
            let shelf = kept(.live).first
                ?? HomeShelf(key: HomeShelf.liveNow, kind: .liveNow, content: .failed(HomeFailure(e)))
            out.insert(shelf, at: liveAt)
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
                                  subtitle: names[item.videoID]?.series, progress: max(0, item.progress))
                })
            }
        case let .failure(e):
            let old = kept(.recommendations)
            if old.isEmpty {
                out.append(HomeShelf(key: HomeShelf.recommendations, kind: .recommendations,
                                     content: .failed(HomeFailure(e))))
            } else {
                out += old
            }
        }
        return out
    }

    /// The shelves a Home shows: with Live TV off on the home (story 51.11) there is no On now
    /// shelf, its channels or its failure (the channels' call answers 409 `component_off`
    /// then), as the Player's Home. Read at each draw, so the row goes or comes back with the
    /// capabilities' answer.
    public static func shown(_ shelves: [HomeShelf], liveTV: Bool) -> [HomeShelf] {
        liveTV ? shelves : shelves.filter { $0.key != HomeShelf.liveNow }
    }

    /// Not interested: `videoID` leaves the recommendation shelves at once (a shelf it
    /// empties goes too); the other shelves keep it.
    public static func dropping(_ videoID: String, from shelves: [HomeShelf]) -> [HomeShelf] {
        shelves.compactMap { shelf in
            guard shelf.isRecommendation, case let .cards(cards) = shelf.content else { return shelf }
            let left = cards.filter { $0.videoID != videoID }
            if left.isEmpty { return nil }
            var s = shelf
            s.content = .cards(left)
            return s
        }
    }

    /// A video's card; `resume` gives it where the person stopped (Continue watching).
    static func card(_ v: BrowseVideo, resume: Bool = false) -> HomeShelfCard {
        HomeShelfCard(videoID: v.id, title: v.displayTitle, subtitle: v.series?.name, progress: max(0, v.progress),
                      resumeAt: resume ? (v.positionSec ?? 0) : nil)
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
