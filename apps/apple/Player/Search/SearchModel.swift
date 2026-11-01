import Foundation
import MaktabaKit
import Observation

/// Search's state (FR-19, FR-20): what is typed, the suggestions for it, the recent searches,
/// and the last submitted search's two groups, each paged from the search the home keeps.
/// Every answer is the home's (`/api/search*`), which decides what this person may see.
/// The screen keeps one for as long as its tab lives, so another section and back finds the
/// same query and results.
@MainActor @Observable
final class SearchModel {
    /// How long typing rests before the field asks for suggestions.
    static let suggestDelay: Duration = .milliseconds(250)

    /// What a submitted search found.
    struct Results {
        /// The query as it was sent.
        let query: String
        /// The kept result set ("" when the home kept none: no more pages).
        let searchID: String
        /// How many of each group a page holds.
        let pageSize: Int
        var titles: [SearchVideo]
        var titlesTotal: Int
        /// The next page's offset of Titles; nil at the end.
        var titlesNext: Int?
        var transcripts: [SearchVideo]
        var transcriptsTotal: Int
        var transcriptsNext: Int?
        /// The meaning half did not answer: text matches only.
        let degraded: Bool

        var isEmpty: Bool { titles.isEmpty && transcripts.isEmpty }

        init(_ a: SearchAnswer, sent: String) {
            query = a.query.isEmpty ? sent : a.query
            searchID = a.searchID
            pageSize = max(a.pageSize, 1)
            titles = a.titles
            titlesTotal = max(a.titlesTotal, a.titles.count)
            transcripts = a.transcripts
            transcriptsTotal = max(a.transcriptsTotal, a.transcripts.count)
            degraded = a.degraded
            let kept = !a.searchID.isEmpty
            titlesNext = kept && a.titles.count < titlesTotal ? max(a.pageSize, a.titles.count) : nil
            transcriptsNext = kept && a.transcripts.count < transcriptsTotal ? max(a.pageSize, a.transcripts.count) : nil
        }
    }

    /// What is typed in the field.
    var query = ""
    /// The person's own past searches starting with what is typed (`GET /api/search/suggest`).
    private(set) var suggestions: [String] = []
    /// Recent searches, newest first (`GET /api/search/recent`).
    private(set) var recent: [RecentSearch] = []
    /// The last submitted search's answer; nil before the first.
    private(set) var results: Results?
    /// A search is on its way.
    private(set) var searching = false
    /// A page after the first is on its way.
    private(set) var paging = false
    /// Why the last search failed; nil when it did not.
    private(set) var problem: HomeFailure?
    /// The query of the last submit (what Try again sends again).
    private(set) var submitted: String?

    private let client: APIClient
    private let signedOut: @MainActor () -> Void
    private var generation = 0

    init(client: APIClient, signedOut: @escaping @MainActor () -> Void) {
        self.client = client
        self.signedOut = signedOut
    }

    /// The query as it is sent: only the spaces around it go. Nothing else changes (no
    /// normalising, no removing an Arabic word's diacritics): the home ranks it.
    static func sendable(_ q: String) -> String { q.trimmingCharacters(in: .whitespacesAndNewlines) }

    /// Asks for the suggestions for `q`; blank, or the query just submitted, asks nothing.
    /// A failure keeps the list empty.
    func suggest(_ q: String) async {
        let q = Self.sendable(q)
        guard !q.isEmpty, q != submitted.map(Self.sendable) else {
            suggestions = []
            return
        }
        await quietly("search suggest") {
            let found = try await client.searchSuggestions(q)
            // typing went on meanwhile: this list is for an older query
            if Self.sendable(query) == q { suggestions = found }
        }
    }

    func loadRecent() async {
        await quietly("search recent") { recent = try await client.recentSearches() }
    }

    /// Runs `q` in hybrid mode. A newer submit wins over an older one still on its way.
    func submit(_ q: String) async {
        let sent = Self.sendable(q)
        guard !sent.isEmpty else { return }
        query = q
        submitted = sent
        suggestions = []
        generation += 1
        let mine = generation
        searching = true
        problem = nil
        defer { if mine == generation { searching = false } }
        do {
            let answer = try await client.search(sent, mode: .hybrid)
            guard mine == generation else { return }
            results = Results(answer, sent: sent)
        } catch {
            guard mine == generation else { return }
            if case APIError.signedOut = error { return signedOut() }
            guard !Log.isCancellation(error) else { return }
            Log.failure("player search", error)
            problem = HomeFailure(error)
            return
        }
        // the search is now the newest recent one
        await loadRecent()
    }

    /// Submits the last query again (the error's Try again).
    func retry() async {
        if let submitted { await submit(submitted) }
    }

    /// The next page of Titles.
    func moreTitles() async {
        await page(.titles)
    }

    /// The next page of In the transcript.
    func moreTranscripts() async {
        await page(.transcripts)
    }

    /// Forgets one recent search on the home, then here.
    func forget(_ r: RecentSearch) async {
        await quietly("search forget") {
            try await client.forgetRecentSearch(r.searchID)
            recent.removeAll { $0.searchID == r.searchID }
        }
    }

    /// Forgets every recent search on the home, then here.
    func clearRecent() async {
        await quietly("search clear") {
            try await client.forgetRecentSearch()
            recent = []
        }
    }

    private func page(_ list: SearchList) async {
        guard let r = results, !paging, !r.searchID.isEmpty,
              let offset = list == .titles ? r.titlesNext : r.transcriptsNext else { return }
        let mine = generation
        paging = true
        defer { paging = false }
        do {
            let p = try await client.searchResults(r.searchID, list: list, offset: offset, limit: r.pageSize)
            guard mine == generation, var now = results else { return }
            switch list {
            case .titles:
                let have = Set(now.titles.map(\.videoID))
                now.titles += p.items.filter { !have.contains($0.videoID) }
                now.titlesTotal = p.items.isEmpty ? now.titles.count : max(p.total, now.titles.count)
                now.titlesNext = p.items.isEmpty ? nil : p.nextOffset
            case .transcripts:
                let have = Set(now.transcripts.map(\.videoID))
                now.transcripts += p.items.filter { !have.contains($0.videoID) }
                now.transcriptsTotal = p.items.isEmpty ? now.transcripts.count : max(p.total, now.transcripts.count)
                now.transcriptsNext = p.items.isEmpty ? nil : p.nextOffset
            }
            results = now
        } catch BrowseError.searchExpired {
            // the home let the kept results go: run the search again
            guard mine == generation else { return }
            paging = false
            await retry()
        } catch {
            if case APIError.signedOut = error { return signedOut() }
            guard !Log.isCancellation(error) else { return }
            // the list so far stays; Show more asks again
            Log.failure("search page", error)
        }
    }

    private func quietly(_ what: StaticString, _ body: () async throws -> Void) async {
        do {
            try await body()
        } catch {
            if case APIError.signedOut = error { return signedOut() }
            guard !Log.isCancellation(error) else { return }
            Log.failure(what, error)
        }
    }
}
