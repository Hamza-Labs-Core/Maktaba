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

/// What a card's context menu offers, in its order. VoiceOver offers the same list as the
/// card's custom actions (its Actions rotor), so each is reachable without the long press;
/// the menu item and the custom action both call `run`, so they do the same thing.
enum HomeCardAction: Hashable, Sendable {
    /// A Continue Watching card: the player at the person's position.
    case resume(at: Double)
    /// A recommendation: show it no more.
    case notInterested

    /// The card's actions: Resume when it has a position, Not interested on a recommendation.
    static func of(_ card: HomeCard) -> [HomeCardAction] {
        var actions: [HomeCardAction] = []
        if let at = card.resumeAt { actions.append(.resume(at: at)) }
        if card.recommended { actions.append(.notInterested) }
        return actions
    }

    /// The menu item's words, which are also the custom action's name.
    var title: LocalizedStringResource {
        switch self {
        case .resume:
            LocalizedStringResource("Resume", table: "Components", comment: "The hero's button: go on from where it stopped")
        case .notInterested:
            LocalizedStringResource("Not interested", table: "Home", comment: "A recommendation's menu: show it no more")
        }
    }

    var systemImage: String {
        switch self {
        case .resume: "play.fill"
        case .notInterested: "hand.thumbsdown"
        }
    }

    /// The menu item's accessibility identifier.
    var identifier: String {
        switch self {
        case .resume: "home.menu.resume"
        case .notInterested: "home.menu.notInterested"
        }
    }

    var isDestructive: Bool { self == .notInterested }

    /// What Home does for an action: open a route, hide a recommendation (false: the home
    /// could not be reached), say that hiding failed.
    struct Handlers {
        var open: @MainActor (PlayerRoute) -> Void
        var dismiss: @MainActor (String) async -> Bool
        var dismissFailed: @MainActor () -> Void
    }

    /// Does the action for `card`. Resume opens the player at once; Not interested runs in
    /// the returned task (a test awaits it).
    @MainActor @discardableResult
    func run(on card: HomeCard, _ handlers: Handlers) -> Task<Void, Never>? {
        switch self {
        case let .resume(at):
            handlers.open(.watch(videoID: card.videoID, at: at))
            return nil
        case .notInterested:
            let video = card.videoID
            return Task { @MainActor in
                if !(await handlers.dismiss(video)) { handlers.dismissFailed() }
            }
        }
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

extension HomeHero {
    /// Opens the series, or the video's page.
    var details: PlayerRoute { seriesID.map { .series(id: $0) } ?? .video(id: videoID) }
}

/// Home's blocks, as the Player shows them.
extension HomeModel {
    /// The blocks under the hero, in FR-10's order, empty rows left out.
    var sections: [HomeSection] { HomeLayout.sections(shelves, known: known) }

    /// Home answered and has nothing at all (FR-13).
    var isEmpty: Bool { loaded && HomeLayout.isEmpty(hero: hero, sections: sections) }
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
