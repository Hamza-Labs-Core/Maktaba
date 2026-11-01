import Foundation
import MaktabaKit

// What Home shows, as data (FR-10, FR-11): the hero, the rows in their order, each row's cards
// and what a card does. Built by pure functions from the home's answers, so the order and the
// hiding of empty rows do not depend on the screen. Everything here but Live TV on now is
// `Codable`: it is what `HomeCache` keeps on the device for the next launch (FR-12).

/// A row's title: one of Home's own (translated on screen), or a pinned library's name.
enum HomeRowTitle: Codable, Hashable, Sendable {
    case continueWatching, onDeck, favorites, recentlyAdded, recentlyWatched, forYou, fromLibrary
    case named(String)
}

/// A card of a row.
/// - `title` is nil for a recommendation whose video Home has not read yet: the home's
///   recommendation items carry no title, so the screen asks for the video (`GET /api/videos/{id}`).
/// - `resumeAt`: a Continue Watching card's context menu has Resume, the player at that second.
/// - `recommended`: a recommendation's context menu has Not interested.
/// - `newLessons`: a series of Recently Added with that many new lessons (it opens the series).
struct HomeCard: Codable, Hashable, Identifiable, Sendable {
    var videoID: String
    var title: String?
    var open: PlayerRoute
    var subtitle: String?
    var progress: Double?
    var resumeAt: Double?
    var recommended = false
    var newLessons: Int?

    /// The list key: a series group and its newest lesson's video card never collide.
    var id: String {
        if newLessons != nil, case let .series(series) = open { return "series:\(series)" }
        return videoID
    }
}

/// One row of video (or series) cards under the hero. `key` is stable: the list's id and the
/// row's accessibility identifier (`home.row.<key>`).
struct HomeRow: Codable, Hashable, Identifiable, Sendable {
    let key: String
    let title: HomeRowTitle
    var cards: [HomeCard]
    /// Opens the whole list (a pinned library's page).
    var seeAll: PlayerRoute?

    var id: String { key }

    static let continueWatching = "continue"
    static let onDeck = "ondeck"
    static let live = "live"
    static let favorites = "favorites"
    static let recentlyAdded = "added"
    static let recentlyWatched = "watched"
    static let pinnedPrefix = "pinned-"
    static let forYou = "rec-for_you"
    static let fromLibrary = "rec-from_library"
}

/// Home's hero (FR-10): the featured series (from its first lesson) or the video an admin picked.
struct HomeHero: Codable, Hashable, Sendable {
    let videoID: String
    let videoTitle: String
    let seriesID: String?
    let seriesName: String?
    let episodes: Int?
    let durationSec: Double?
    let picked: Bool
    let progress: Double?

    /// A series' hero opens the series; a video's opens its page.
    var isSeries: Bool { seriesID != nil }

    var details: PlayerRoute { seriesID.map { .series(id: $0) } ?? .video(id: videoID) }

    init(_ f: Featured) {
        videoID = f.video.id
        videoTitle = f.video.displayTitle
        let series = f.kind == "series" ? f.series : nil
        seriesID = series?.id
        seriesName = (series?.name).flatMap { $0.trimmingCharacters(in: .whitespaces).isEmpty ? nil : $0 }
        episodes = series?.episodes
        durationSec = f.video.durationSec
        picked = f.picked
        progress = f.video.progress > 0 ? f.video.progress : nil
    }
}

/// What a video is called, for a recommendation's card.
struct KnownVideo: Codable, Hashable, Sendable {
    let title: String
    let series: String?
}

/// A recommendation rail as Home keeps it: its kind and its videos (no titles: see `HomeCard`).
struct RecRail: Codable, Hashable, Sendable {
    struct Item: Codable, Hashable, Sendable {
        let videoID: String
        let progress: Double?
    }

    let kind: String
    var items: [Item]

    init(kind: String, items: [Item]) {
        self.kind = kind
        self.items = items
    }

    init(_ rail: MaktabaKit.Rail) {
        kind = rail.reasonKind
        items = rail.items.map { Item(videoID: $0.videoID, progress: $0.progress > 0 ? $0.progress : nil) }
    }
}

/// Home's own part of `GET /api/home`, in its order: the hero, then Continue Watching, On
/// Deck, Favorites, Recently Added, Recently Watched and one row per pinned library (Live TV
/// on now and the recommendations are put among them by `HomeLayout`). `names` holds what
/// these rows say about each video, which names a recommendation's card too.
struct HomeContent: Codable, Hashable, Sendable {
    var hero: HomeHero?
    var rows: [HomeRow]
    var names: [String: KnownVideo]

    init(hero: HomeHero?, rows: [HomeRow], names: [String: KnownVideo] = [:]) {
        self.hero = hero
        self.rows = rows
        self.names = names
    }

    init(_ home: HomeRows) {
        var rows: [HomeRow] = []
        func add(_ key: String, _ title: HomeRowTitle, _ cards: [HomeCard], seeAll: PlayerRoute? = nil) {
            if !cards.isEmpty { rows.append(HomeRow(key: key, title: title, cards: cards, seeAll: seeAll)) }
        }
        add(HomeRow.continueWatching, .continueWatching, home.continueWatching.map { Self.card($0, resume: true) })
        add(HomeRow.onDeck, .onDeck, home.onDeck.map { Self.card($0) })
        add(HomeRow.favorites, .favorites, home.favorites.map { Self.card($0) })
        // grouped by series when the home sends it (an older home: the plain list)
        let added = home.recentlyAddedGrouped.isEmpty
            ? home.recentlyAdded.map { Self.card($0) }
            : home.recentlyAddedGrouped.map(Self.card)
        add(HomeRow.recentlyAdded, .recentlyAdded, added)
        add(HomeRow.recentlyWatched, .recentlyWatched, home.recentlyWatched.map { Self.card($0) })
        for p in home.pinned {
            // what the person is in the middle of first, then what is new (the web's order)
            var seen = Set<String>()
            let videos = (p.continueWatching + p.recentlyAdded).filter { seen.insert($0.id).inserted }
            add(HomeRow.pinnedPrefix + p.library.id, .named(p.library.name), videos.map { Self.card($0) },
                seeAll: .library(id: p.library.id))
        }
        var names: [String: KnownVideo] = [:]
        let all = home.continueWatching + home.onDeck + home.favorites + home.recentlyAdded
            + home.recentlyAddedGrouped.map(\.video) + home.recentlyWatched
            + home.pinned.flatMap { $0.continueWatching + $0.recentlyAdded }
        for v in all { names[v.id] = KnownVideo(title: v.displayTitle, series: v.series?.name) }
        self.init(hero: home.featured.map(HomeHero.init), rows: rows, names: names)
    }

    /// A video's card; `resume` gives it Resume at the person's position (Continue Watching).
    static func card(_ v: BrowseVideo, resume: Bool = false) -> HomeCard {
        HomeCard(videoID: v.id, title: v.displayTitle, open: .video(id: v.id), subtitle: v.series?.name,
                 progress: v.progress > 0 ? v.progress : nil, resumeAt: resume ? (v.positionSec ?? 0) : nil)
    }

    /// A Recently Added entry: a series with its new lessons (opens the series), or a video.
    static func card(_ e: AddedEntry) -> HomeCard {
        if e.kind == "series", let s = e.series {
            return HomeCard(videoID: e.video.id, title: s.name.trimmingCharacters(in: .whitespaces).isEmpty ? nil : s.name,
                            open: .series(id: s.id), newLessons: e.count ?? 1)
        }
        var c = card(e.video)
        if c.subtitle == nil { c.subtitle = e.series?.name }
        return c
    }
}

/// One block of Home under the hero: a row of cards, or Live TV on now (one card per channel).
enum HomeSection: Identifiable {
    case cards(HomeRow)
    case live([ChannelNow])

    var id: String {
        switch self {
        case let .cards(row): row.key
        case .live: HomeRow.live
        }
    }
}

enum HomeLayout {
    /// The recommendation rails Home shows (FR-10), in their order; the others (Continue
    /// Watching's twin) are Home's own rows already.
    static let railKinds: [(kind: String, key: String, title: HomeRowTitle)] = [
        ("for_you", HomeRow.forYou, .forYou),
        ("from_library", HomeRow.fromLibrary, .fromLibrary),
    ]

    /// Home's blocks under the hero, top to bottom (FR-10): Continue Watching, On Deck, Live
    /// TV on now, Favorites, Recently Added, Recently Watched, one row per pinned library, For
    /// You, From your library. A row with nothing in it is left out. `known` names the videos
    /// read for recommendation cards.
    static func sections(_ content: HomeContent, recs: [RecRail], now: [ChannelNow],
                         known: [String: KnownVideo] = [:]) -> [HomeSection] {
        var out = content.rows.map(HomeSection.cards)
        if !now.isEmpty {
            // after Continue Watching and On Deck, before the rest
            let at = content.rows.prefix { [HomeRow.continueWatching, HomeRow.onDeck].contains($0.key) }.count
            out.insert(.live(now), at: at)
        }
        let names = content.names.merging(known) { _, read in read }
        for (kind, key, title) in railKinds {
            var seen = Set<String>()
            let items = recs.filter { $0.kind == kind }.flatMap(\.items).filter { seen.insert($0.videoID).inserted }
            let cards = items.map { item in
                HomeCard(videoID: item.videoID, title: names[item.videoID]?.title, open: .video(id: item.videoID),
                         subtitle: names[item.videoID]?.series, progress: item.progress, recommended: true)
            }
            if !cards.isEmpty { out.append(.cards(HomeRow(key: key, title: title, cards: cards))) }
        }
        return out
    }

    /// FR-13: nothing at all to show (no library the person may see, or nothing in them).
    static func isEmpty(_ content: HomeContent, sections: [HomeSection]) -> Bool {
        content.hero == nil && !sections.contains { if case .cards = $0 { true } else { false } }
    }
}
