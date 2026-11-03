import Foundation
import MaktabaKit

// What Home shows, as data (FR-10, FR-11): the hero, the rows in their order, each row's cards
// and what a card does. The rows are MaktabaKit's `HomeShelves`, the same ones the Apple TV
// builds from the same answers (their order, the hiding of empty rows, the retry tile of a
// row that could not be read); here they become the Player's cards (what a card opens, its
// Resume and Not interested). The hero and the kept rows are `Codable`: they are what
// `HomeCache` keeps on the device for the next launch (FR-12).

/// A card of a row.
/// - `title` is nil for a recommendation whose video Home has not read yet: the home's
///   recommendation items carry no title, so the screen asks for the video (`GET /api/videos/{id}`).
/// - `resumeAt`: a Continue Watching card's context menu has Resume, the player at that second.
/// - `recommended`: a recommendation's context menu has Not interested.
/// - `newLessons`: a series of Recently Added with that many new lessons (it opens the series).
struct HomeCard: Hashable, Identifiable, Sendable {
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

    /// A shelf's card as the Player shows it; `known` names a recommendation read since.
    init(_ c: HomeShelfCard, recommended: Bool, known: [String: KnownVideo] = [:]) {
        videoID = c.videoID
        open = c.seriesID.map { .series(id: $0) } ?? .video(id: c.videoID)
        title = c.title ?? known[c.videoID]?.title
        subtitle = c.subtitle ?? known[c.videoID]?.series
        progress = c.progress > 0 ? c.progress : nil
        resumeAt = c.resumeAt
        self.recommended = recommended
        newLessons = c.newLessons
    }
}

/// One row of video (or series) cards under the hero. `key` is the shelf's: the list's id and
/// the row's accessibility identifier (`home.row.<key>`).
struct HomeRow: Hashable, Identifiable, Sendable {
    let key: String
    let kind: HomeShelf.Kind
    var cards: [HomeCard]
    /// Opens the whole list (a pinned library's page).
    var seeAll: PlayerRoute?

    var id: String { key }
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

/// A shelf of cards as the device keeps it (Live TV on now and retry tiles are not kept: one
/// is stale within minutes, the other is no row).
struct KeptShelf: Codable, Hashable, Sendable {
    let key: String
    let kind: HomeShelf.Kind
    let cards: [HomeShelfCard]

    init?(_ shelf: HomeShelf) {
        guard case let .cards(cards) = shelf.content else { return nil }
        key = shelf.key
        kind = shelf.kind
        self.cards = cards
    }

    var shelf: HomeShelf { HomeShelf(key: key, kind: kind, content: .cards(cards)) }
}

/// One block of Home under the hero: a row of cards, Live TV on now (one card per channel),
/// or a row that could not be read (a tile that reads Home again).
enum HomeSection: Identifiable {
    case cards(HomeRow)
    case live([ChannelNow])
    case failed(key: String, kind: HomeShelf.Kind, HomeFailure)

    var id: String {
        switch self {
        case let .cards(row): row.key
        case .live: HomeShelf.liveNow
        case let .failed(key, _, _): key
        }
    }
}

enum HomeLayout {
    /// Home's blocks under the hero, top to bottom: the shelves as `HomeShelves` built them,
    /// as the Player shows them. Home's own answer failing is not a row here (the screen says
    /// so above the rows it keeps, or in place of Home). `known` names the videos read for
    /// recommendation cards.
    static func sections(_ shelves: [HomeShelf], known: [String: KnownVideo] = [:]) -> [HomeSection] {
        shelves.compactMap { shelf in
            switch shelf.content {
            case let .cards(cards):
                let seeAll: PlayerRoute? = if case let .library(id, _) = shelf.kind { .library(id: id) } else { nil }
                let row = HomeRow(key: shelf.key, kind: shelf.kind,
                                  cards: cards.map { HomeCard($0, recommended: shelf.isRecommendation, known: known) },
                                  seeAll: seeAll)
                return .cards(row)
            case let .channels(channels):
                return .live(channels)
            case let .failed(f):
                return shelf.kind == .home ? nil : .failed(key: shelf.key, kind: shelf.kind, f)
            }
        }
    }

    /// FR-13: nothing at all to show (no library the person may see, or nothing in them).
    static func isEmpty(hero: HomeHero?, sections: [HomeSection]) -> Bool {
        hero == nil && !sections.contains { if case .cards = $0 { true } else { false } }
    }
}
