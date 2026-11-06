import MaktabaKit
import SwiftUI

// What the Libraries section's screens hold (Epic 48, FR-14 to FR-17): plain state over the
// browse client (48.6), so the screens only draw it. Every route already scopes to the
// caller's libraries on the home; nothing here filters by role.

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

// MARK: a library (FR-15)

/// A library's tabs.
enum LibraryTab: String, CaseIterable, Identifiable {
    case videos, series, collections, playlists
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

    /// The grid's view as a library queue's filters (Epic 56: Play and Shuffle play it).
    var queueFilters: [String: String] {
        var q = VideoQuery(library: id, sort: sort, language: language, speaker: speaker)
        q.limit = 60
        return q.queueFilters
    }

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
