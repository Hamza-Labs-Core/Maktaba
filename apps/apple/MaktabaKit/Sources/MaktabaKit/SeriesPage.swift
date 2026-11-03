import Foundation
import Observation

// How the Player's Libraries pages load (Epic 48, FR-14 to FR-17), and a series' page.

/// Why a page has nothing to show.
public enum LoadProblem: Equatable, Sendable {
    /// 404 (or the video routes' 403): gone, or not shared with this person.
    case notAvailable
    /// Anything else: the list's error screen, with Try again.
    case failed(HomeFailure)
}

/// Runs `block` the way every screen of the section loads: signed out goes back to signing
/// in, a cancel (the screen went away, the query changed) is nothing, anything else is kept
/// as the problem to show (never the raw answer).
@MainActor
public func loading(_ what: StaticString, signedOut: () -> Void, problem: (LoadProblem) -> Void,
             _ block: () async throws -> Void) async {
    do {
        try await block()
    } catch {
        if case APIError.signedOut = error {
            signedOut()
            return
        }
        guard !Log.isCancellation(error) else { return }
        Log.failure(what, error)
        if case BrowseError.notAvailable = error {
            problem(.notAvailable)
        } else {
            problem(.failed(HomeFailure(error)))
        }
    }
}

// MARK: a series (FR-17): the Player's SeriesScreen shows it on iPhone, iPad and Mac

@MainActor @Observable
public final class SeriesModel {
    private let client: APIClient
    private let signedOut: () -> Void
    public let id: String

    public private(set) var series: SeriesSummary?
    public private(set) var seasons: [SeriesSeason] = []
    /// Continue: its next episode.
    public private(set) var next: Episode?
    /// The chosen season.
    public var season: Int?
    public private(set) var loaded = false
    public var problem: LoadProblem?
    /// A watched mark the home did not take (the page says so; the marks stay as they were).
    public var markFailed = false
    /// Episodes whose watched mark was changed here (the home's answer is read on the next load).
    private var marks: [String: Bool] = [:]
    /// Episodes marked since the read under way began: that read was answered before the
    /// save, so their marks stay over its answer (else the old marks come back on screen while
    /// the home has the new ones). As `VideoPage`.
    private var marksSinceRead: Set<String> = []

    public init(client: APIClient, id: String, signedOut: @escaping () -> Void) {
        self.client = client
        self.id = id
        self.signedOut = signedOut
    }

    public func load() async {
        marksSinceRead = []
        await loading("player series page", signedOut: signedOut, problem: { problem = $0; loaded = true }) {
            async let summary = client.series(id)
            async let doc = client.episodes(series: id)
            let (s, d) = try await (summary, doc)
            series = s
            seasons = d.seasons
            next = d.nextEpisode
            marks = marks.filter { marksSinceRead.contains($0.key) }
            // the season of Continue's episode, else the first
            if season == nil || !d.seasons.contains(where: { $0.season == season }) {
                season = d.seasons.first { $0.episodes.contains { $0.videoID == d.nextEpisode?.videoID } }?.season
                    ?? d.seasons.first?.season
            }
            problem = nil
            loaded = true
        }
    }

    /// The chosen season's episodes.
    public var episodes: [Episode] {
        (seasons.first { $0.season == season } ?? seasons.first)?.episodes ?? []
    }

    public func watched(_ e: Episode) -> Bool { marks[e.videoID] ?? e.watched }

    public func progress(_ e: Episode) -> Double {
        if let m = marks[e.videoID] { return m ? 1 : 0 }
        return e.progress
    }

    /// Every episode of the chosen season is watched.
    public var seasonWatched: Bool { !episodes.isEmpty && episodes.allSatisfy(watched) }

    /// How many episodes of the series are watched, with this page's marks.
    public var watchedCount: Int { seasons.flatMap(\.episodes).filter(watched).count }
    public var episodeCount: Int { seasons.reduce(0) { $0 + $1.episodes.count } }

    /// Marks `ids` watched or not (`POST /api/me/watched`), then shows them so.
    public func mark(_ ids: [String], watched: Bool) async {
        guard !ids.isEmpty else { return }
        await loading("player mark watched", signedOut: signedOut, problem: { _ in markFailed = true }) {
            _ = try await client.markWatched(ids, watched)
            for id in ids { marks[id] = watched }
            marksSinceRead.formUnion(ids)
        }
    }

    /// Marks every episode of the chosen season watched (or not, when all already are).
    public func markSeason() async {
        await mark(episodes.map(\.videoID), watched: !seasonWatched)
    }
}
