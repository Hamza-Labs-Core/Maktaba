import MaktabaKit
import SwiftUI

// What the Libraries section's screens hold (Epic 48, FR-14 to FR-17): plain state over the
// browse client (48.6), so the screens only draw it. Every route already scopes to the
// caller's libraries on the home; nothing here filters by role.

/// Why a page has nothing to show.
enum LoadProblem: Equatable {
    /// 404 (or the video routes' 403): gone, or not shared with this person.
    case notAvailable
    /// Anything else: the list's error screen, with Try again.
    case failed(HomeFailure)
}

/// Runs `block` the way every screen of the section loads: signed out goes back to signing
/// in, a cancel (the screen went away, the query changed) is nothing, anything else is kept
/// as the problem to show (never the raw answer).
@MainActor
func loading(_ what: StaticString, signedOut: () -> Void, problem: (LoadProblem) -> Void,
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

/// The pictures of the section: a video's poster on the home (`?size=small` for a card; the
/// cache adds the token, to the home's own `/api/` only).
enum LibraryArt {
    static func poster(_ client: APIClient, video id: String?, small: Bool = true) -> URL? {
        guard let id, !id.isEmpty else { return nil }
        let url = client.home.appending(path: "api/videos/\(id)/poster")
        return small ? url.appending(queryItems: [URLQueryItem(name: "size", value: "small")]) : url
    }
}

// MARK: Libraries (FR-14) and Series (FR-16)

/// The Libraries root's two tabs.
enum LibrariesTab: String, CaseIterable, Identifiable {
    case libraries, series
    var id: String { rawValue }
}

@MainActor @Observable
final class LibrariesModel {
    private let client: APIClient
    private let signedOut: () -> Void

    /// The cards: the pinned first in the person's order, then the rest as the home sent them.
    private(set) var libraries: [LibrarySummary] = []
    private(set) var series: [SeriesSummary] = []
    private(set) var librariesLoaded = false
    private(set) var seriesLoaded = false
    var problem: LoadProblem?
    var seriesProblem: LoadProblem?
    /// The pins the person sees, by id (the home's answer replaces them).
    private(set) var pins: [String] = []
    /// The libraries in the home's order: an unpinned one goes back to its place.
    private var order: [String] = []

    init(client: APIClient, signedOut: @escaping () -> Void) {
        self.client = client
        self.signedOut = signedOut
    }

    func loadLibraries() async {
        await loading("player libraries", signedOut: signedOut, problem: { problem = $0 }) {
            let all = try await client.librarySummaries()
            order = all.map(\.id)
            pins = LibrarySummary.ordered(all).filter(\.pinned).map(\.id)
            libraries = arranged(all)
            problem = nil
            librariesLoaded = true
        }
    }

    func loadSeries() async {
        await loading("player series", signedOut: signedOut, problem: { seriesProblem = $0 }) {
            series = try await client.seriesList()
            seriesProblem = nil
            seriesLoaded = true
        }
    }

    func isPinned(_ id: String) -> Bool { pins.contains(id) }

    /// Pins `id` (at the end, as the web's sidebar does) or unpins it
    /// (`PUT /api/me/pinned-libraries`): the grid moves at once and the home's answer wins;
    /// when the home refuses, the old order comes back.
    func togglePin(_ id: String) async {
        let before = pins
        pins = before.contains(id) ? before.filter { $0 != id } : before + [id]
        libraries = arranged(libraries)
        await loading("player pin", signedOut: signedOut, problem: { p in
            pins = before
            libraries = arranged(libraries)
            problem = p
        }) {
            let saved = try await client.setPinnedLibraries(pins)
            pins = saved.map(\.id)
            libraries = arranged(libraries)
        }
    }

    /// The pinned first, in `pins`' order, then the rest in the home's order.
    private func arranged(_ list: [LibrarySummary]) -> [LibrarySummary] {
        let byID = Dictionary(list.map { ($0.id, $0) }, uniquingKeysWith: { a, _ in a })
        let pinned = pins.compactMap { byID[$0] }
        let rest = order.compactMap { byID[$0] }.filter { !pins.contains($0.id) }
        let unknown = list.filter { !order.contains($0.id) && !pins.contains($0.id) }
        return pinned + rest + unknown
    }
}

// MARK: a library (FR-15)

/// A library's tabs.
enum LibraryTab: String, CaseIterable, Identifiable {
    case videos, series
    var id: String { rawValue }
}

@MainActor @Observable
final class LibraryModel {
    private let client: APIClient
    private let signedOut: () -> Void
    let id: String

    private(set) var name = ""
    private(set) var hub: LibraryHub?
    private(set) var facets: VideoFacets?
    var tab = LibraryTab.videos
    /// The grid's order: newest first or A to Z.
    var sort = VideoSort.newest
    /// A language code, nil for any.
    var language: String?
    /// A speaker's name, nil for any.
    var speaker: String?

    private(set) var videos: [BrowseVideo] = []
    private(set) var next: String?
    private(set) var loaded = false
    var problem: LoadProblem?

    private(set) var series: [LibrarySeries] = []
    private var seriesNext: Int? = 0
    private(set) var seriesLoaded = false

    init(client: APIClient, id: String, signedOut: @escaping () -> Void) {
        self.client = client
        self.id = id
        self.signedOut = signedOut
    }

    /// What the grid asks `GET /api/videos` for: this library, the sort and the facets.
    func query(cursor: String? = nil) -> VideoQuery {
        VideoQuery(library: id, sort: sort, language: language, speaker: speaker, cursor: cursor)
    }

    /// Changes whenever the grid must be read again from its first page.
    var gridKey: String { "\(sort.rawValue)|\(language ?? "")|\(speaker ?? "")" }

    /// The hub rows and the facets: a row or a facet that does not answer is left out, never
    /// the page (a library gone is the grid's "not available").
    func loadHub() async {
        await loading("player library hub", signedOut: signedOut, problem: { if $0 == .notAvailable { problem = .notAvailable } }) {
            let h = try await client.libraryHub(id)
            if !h.library.name.isEmpty { name = h.library.name }
            hub = h
        }
        await loading("player facets", signedOut: signedOut, problem: { _ in }) {
            facets = try await client.videoFacets(library: id)
        }
    }

    /// The grid's first page, for the chosen facets and sort.
    func reload() async {
        await loading("player library videos", signedOut: signedOut, problem: { problem = $0; loaded = true }) {
            let page = try await client.videos(query())
            videos = page.items
            next = page.next
            if problem != .notAvailable { problem = nil }
            loaded = true
        }
    }

    /// The grid's next page, by cursor; nothing at the end.
    func loadMore() async {
        guard let cursor = next else { return }
        next = nil
        await loading("player library more", signedOut: signedOut, problem: { _ in next = cursor }) {
            let page = try await client.videos(query(cursor: cursor))
            let have = Set(videos.map(\.id))
            videos += page.items.filter { !have.contains($0.id) }
            next = page.next
        }
    }

    var moreSeries: Bool { seriesNext != nil }

    /// The Series tab's next page (by offset).
    func loadSeries() async {
        guard let offset = seriesNext else { return }
        seriesNext = nil
        await loading("player library series", signedOut: signedOut, problem: { p in seriesNext = offset; problem = p }) {
            let page = try await client.librarySeries(id, offset: offset)
            let have = Set(series.map(\.id))
            series = (offset == 0 ? [] : series) + page.items.filter { !have.contains($0.id) }
            seriesNext = page.next
            seriesLoaded = true
        }
    }
}

// MARK: a series (FR-17)

@MainActor @Observable
final class SeriesModel {
    private let client: APIClient
    private let signedOut: () -> Void
    let id: String

    private(set) var series: SeriesSummary?
    private(set) var seasons: [SeriesSeason] = []
    /// Continue: its next episode.
    private(set) var next: Episode?
    /// The chosen season.
    var season: Int?
    private(set) var loaded = false
    var problem: LoadProblem?
    /// A watched mark the home did not take (the page says so; the marks stay as they were).
    var markFailed = false
    /// Episodes whose watched mark was changed here (the home's answer is read on the next load).
    private var marks: [String: Bool] = [:]

    init(client: APIClient, id: String, signedOut: @escaping () -> Void) {
        self.client = client
        self.id = id
        self.signedOut = signedOut
    }

    func load() async {
        await loading("player series page", signedOut: signedOut, problem: { problem = $0; loaded = true }) {
            async let summary = client.series(id)
            async let doc = client.episodes(series: id)
            let (s, d) = try await (summary, doc)
            series = s
            seasons = d.seasons
            next = d.nextEpisode
            marks = [:]
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
    var episodes: [Episode] {
        (seasons.first { $0.season == season } ?? seasons.first)?.episodes ?? []
    }

    func watched(_ e: Episode) -> Bool { marks[e.videoID] ?? e.watched }

    func progress(_ e: Episode) -> Double {
        if let m = marks[e.videoID] { return m ? 1 : 0 }
        return e.progress
    }

    /// Every episode of the chosen season is watched.
    var seasonWatched: Bool { !episodes.isEmpty && episodes.allSatisfy(watched) }

    /// How many episodes of the series are watched, with this page's marks.
    var watchedCount: Int { seasons.flatMap(\.episodes).filter(watched).count }
    var episodeCount: Int { seasons.reduce(0) { $0 + $1.episodes.count } }

    /// Marks `ids` watched or not (`POST /api/me/watched`), then shows them so.
    func mark(_ ids: [String], watched: Bool) async {
        guard !ids.isEmpty else { return }
        await loading("player mark watched", signedOut: signedOut, problem: { _ in markFailed = true }) {
            _ = try await client.markWatched(ids, watched)
            for id in ids { marks[id] = watched }
        }
    }

    /// Marks every episode of the chosen season watched (or not, when all already are).
    func markSeason() async {
        await mark(episodes.map(\.videoID), watched: !seasonWatched)
    }
}
