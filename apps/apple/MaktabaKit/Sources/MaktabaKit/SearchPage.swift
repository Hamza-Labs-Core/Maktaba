import Foundation
import MaktabaKit
import Observation

/// Search's state (FR-19, FR-20): what is typed, the suggestions for it, the recent searches,
/// and the last submitted search's two groups, each paged from the search the home keeps.
/// Story 49.11 adds the search help of story 49.8: each suggestion's kind, the did-you-mean,
/// the facets to narrow the results by, and the order.
/// Every answer is the home's (`/api/search*`), which decides what this person may see.
/// The screen keeps one for as long as its tab lives, so another section and back finds the
/// same query and results.
@MainActor @Observable
public final class SearchModel {
    /// How long typing rests before the field asks for suggestions.
    public static let suggestDelay: Duration = .milliseconds(250)

    /// What is sent: the query, its order and its filters. A refinement (an order, a facet, the
    /// did-you-mean) is a new ask, run as a search is.
    public struct Ask: Hashable, Sendable {
        public var query: String
        public var sort: SearchSort
        public var filters: SearchFilterChoice

        public init(query: String, sort: SearchSort = .relevance, filters: SearchFilterChoice = SearchFilterChoice()) {
            self.query = query
            self.sort = sort
            self.filters = filters
        }
    }

    /// What a submitted search found.
    public struct Results {
        /// The query as it was sent.
        public let query: String
        /// The kept result set ("" when the home kept none: no more pages).
        public let searchID: String
        /// How many of each group a page holds.
        public let pageSize: Int
        public internal(set) var titles: [SearchVideo]
        public internal(set) var titlesTotal: Int
        /// The next page's offset of Titles; nil at the end.
        public internal(set) var titlesNext: Int?
        public internal(set) var transcripts: [SearchVideo]
        public internal(set) var transcriptsTotal: Int
        public internal(set) var transcriptsNext: Int?
        /// The meaning half did not answer: text matches only.
        public let degraded: Bool
        /// The order the lists are in: the home's answer, else what was asked.
        public let sort: SearchSort
        /// The query corrected, when few videos were found and it differs from the query.
        public let didYouMean: String?
        /// The counts of the videos found (nil when none were, or from an older home). The
        /// pages after the first keep them.
        public let facets: SearchFacets?

        public var isEmpty: Bool { titles.isEmpty && transcripts.isEmpty }

        public init(_ a: SearchAnswer, sent: String, sort asked: SearchSort = .relevance) {
            query = a.query.isEmpty ? sent : a.query
            searchID = a.searchID
            pageSize = max(a.pageSize, 1)
            titles = a.titles
            titlesTotal = max(a.titlesTotal, a.titles.count)
            transcripts = a.transcripts
            transcriptsTotal = max(a.transcriptsTotal, a.transcripts.count)
            degraded = a.degraded
            sort = a.sort ?? asked
            didYouMean = a.didYouMean.flatMap { SearchModel.sendable($0) == sent ? nil : $0 }
            facets = a.facets.flatMap { $0.isEmpty ? nil : $0 }
            let kept = !a.searchID.isEmpty
            titlesNext = kept && a.titles.count < titlesTotal ? max(a.pageSize, a.titles.count) : nil
            transcriptsNext = kept && a.transcripts.count < transcriptsTotal ? max(a.pageSize, a.transcripts.count) : nil
        }
    }

    /// What is typed in the field.
    public var query = ""
    /// The suggestions for what is typed, each with its kind (`GET /api/search/suggest`).
    public private(set) var suggestions: [SearchSuggestion] = []
    /// Recent searches, newest first (`GET /api/search/recent`).
    public private(set) var recent: [RecentSearch] = []
    /// The last submitted search's answer; nil before the first.
    public private(set) var results: Results?
    /// A search is on its way.
    public private(set) var searching = false
    /// A page after the first is on its way.
    public private(set) var paging = false
    /// Why the last search failed; nil when it did not.
    public private(set) var problem: HomeFailure?
    /// The query of the last submit (what Try again sends again).
    public private(set) var submitted: String?
    /// The last search asked: its query, order and filters (what Try again asks again).
    public private(set) var asked: Ask?
    /// The names seen of buckets ("library:<id>"), for a chosen bucket the narrowed answer no
    /// longer lists.
    public private(set) var labels: [String: String] = [:]

    /// The order asked (relevance before the first search).
    public var sort: SearchSort { asked?.sort ?? .relevance }
    /// The filters in use.
    public var filters: SearchFilterChoice { asked?.filters ?? SearchFilterChoice() }
    /// The facets to show: the answer's buckets with the chosen ones kept.
    public var shownFacets: [ShownFacet] { filters.shown(results?.facets, labels: labels) }
    /// A bucket's name from the home: the answer's, else the one seen before (a library).
    public func label(_ facet: SearchFacet, _ value: String) -> String? {
        results?.facets?.buckets(facet).first { $0.value == value }?.label ?? labels["\(facet.rawValue):\(value)"]
    }

    /// A refinement of the search on screen is on its way (its results stay meanwhile).
    public var refining: Bool { searching && results != nil && results?.query == submitted }

    private let client: APIClient
    private let signedOut: @MainActor () -> Void
    /// Search by meaning is on on the home (51.11); off, a search asks for words only.
    private let byMeaning: @MainActor () -> Bool
    private var generation = 0
    /// The recent searches' reads against forgetting them: a read that began before one was
    /// forgotten still has it, so it is dropped (else it comes back while the home has let it go).
    @ObservationIgnored private let recentOrder = ReadOrder()

    public init(client: APIClient, byMeaning: @escaping @MainActor () -> Bool = { true },
                signedOut: @escaping @MainActor () -> Void) {
        self.client = client
        self.byMeaning = byMeaning
        self.signedOut = signedOut
    }

    /// How a search is sent: hybrid, or words only (`fts`) while search by meaning is off.
    static func mode(byMeaning: Bool) -> SearchMode { byMeaning ? .hybrid : .fts }

    /// The query as it is sent: only the spaces around it go. Nothing else changes (no
    /// normalising, no removing an Arabic word's diacritics): the home ranks it.
    nonisolated public static func sendable(_ q: String) -> String { q.trimmingCharacters(in: .whitespacesAndNewlines) }

    /// Asks for the suggestions for `q`; blank, or the query just submitted, asks nothing.
    /// A failure keeps the list empty.
    public func suggest(_ q: String) async {
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

    public func loadRecent() async {
        let ticket = recentOrder.begin()
        await quietly("search recent") {
            let found = try await client.recentSearches()
            if recentOrder.fresh(ticket) { recent = found }
        }
    }

    // MARK: Asks

    /// New words, as typed or chosen: the order stays; the filters stay only for the same
    /// words (they were the old results' counts).
    public func ask(words q: String) -> Ask {
        let sent = Self.sendable(q)
        let same = asked.map { $0.query == sent } ?? false
        return Ask(query: q, sort: sort, filters: same ? filters : SearchFilterChoice())
    }

    /// The did-you-mean's words, with the order and the filters kept.
    public func ask(didYouMean q: String) -> Ask {
        Ask(query: q, sort: sort, filters: filters)
    }

    /// The search on screen in another order; nil before the first search.
    public func ask(sort s: SearchSort) -> Ask? {
        guard var a = asked else { return nil }
        a.sort = s
        return a
    }

    /// The search on screen with a bucket taken on or off; nil before the first search.
    public func ask(toggling facet: SearchFacet, _ value: String) -> Ask? {
        guard var a = asked else { return nil }
        a.filters = a.filters.toggled(facet, value)
        return a
    }

    /// The search on screen with a filter taken off.
    public func ask(removing facet: SearchFacet, _ value: String) -> Ask? {
        guard var a = asked else { return nil }
        a.filters = a.filters.removing(facet, value)
        return a
    }

    /// The search on screen with every filter taken off.
    public func askClearingFilters() -> Ask? {
        guard var a = asked else { return nil }
        a.filters = SearchFilterChoice()
        return a
    }

    /// Runs `q` as new words (`ask(words:)`).
    public func submit(_ q: String) async {
        await run(ask(words: q))
    }

    /// Runs `ask` in hybrid mode (words only while search by meaning is off). A newer search
    /// wins over an older one still on its way.
    public func run(_ ask: Ask) async {
        let q = ask.query
        let sent = Self.sendable(q)
        guard !sent.isEmpty else { return }
        let ask = Ask(query: sent, sort: ask.sort, filters: ask.filters)
        // the same search already on its way (a suggestion's tap and the field's submit)
        guard !(searching && ask == asked) else { return }
        // a year or a band goes as its bucket's own filter, from the answer on screen
        let facets = results?.facets
        query = q
        submitted = sent
        asked = ask
        suggestions = []
        generation += 1
        let mine = generation
        searching = true
        problem = nil
        defer { if mine == generation { searching = false } }
        do {
            let answer = try await client.search(sent, mode: Self.mode(byMeaning: byMeaning()), sort: ask.sort,
                                                 filters: ask.filters, facets: facets)
            guard mine == generation else { return }
            results = Results(answer, sent: sent, sort: ask.sort)
            for b in answer.facets?.library ?? [] { if let l = b.label { labels["library:\(b.value)"] = l } }
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

    /// Asks the last search again, its order and filters too (the error's Try again).
    public func retry() async {
        if let asked { await run(asked) }
    }

    /// The next page of Titles.
    public func moreTitles() async {
        await page(.titles)
    }

    /// The next page of In the transcript.
    public func moreTranscripts() async {
        await page(.transcripts)
    }

    /// Forgets one recent search on the home, then here.
    public func forget(_ r: RecentSearch) async {
        await quietly("search forget") {
            try await recentOrder.saving { try await client.forgetRecentSearch(r.searchID) }
            recent.removeAll { $0.searchID == r.searchID }
        }
    }

    /// Forgets every recent search on the home, then here.
    public func clearRecent() async {
        await quietly("search clear") {
            try await recentOrder.saving { try await client.forgetRecentSearch() }
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
