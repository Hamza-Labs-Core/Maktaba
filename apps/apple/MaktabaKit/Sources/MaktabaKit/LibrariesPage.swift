import Foundation
import Observation

// The Player's Libraries root (Epic 48, FR-14) and its Series tab (FR-16): plain state over
// the browse client, so the screens only draw it. Every route already scopes to the caller's
// libraries on the home; nothing here filters by role.

@MainActor @Observable
public final class LibrariesModel {
    private let client: APIClient
    private let signedOut: () -> Void

    /// The cards: the pinned first in the person's order, then the rest as the home sent them.
    public private(set) var libraries: [LibrarySummary] = []
    public private(set) var series: [SeriesSummary] = []
    public private(set) var librariesLoaded = false
    public private(set) var seriesLoaded = false
    /// The Series tab's next page, nil once the home sent a short one.
    private var seriesNext: Int?
    private var seriesBusy = false
    public var problem: LoadProblem?
    public var seriesProblem: LoadProblem?
    /// The pins the person sees, by id (the home's answer replaces them).
    public private(set) var pins: [String] = []
    /// The libraries in the home's order: an unpinned one goes back to its place.
    private var order: [String] = []
    /// The libraries' reads against a pin: a read that began before a pin was saved has the
    /// old pins, so it is dropped (else the old order comes back while the home has the new one).
    @ObservationIgnored private let pinOrder = ReadOrder()

    public init(client: APIClient, signedOut: @escaping () -> Void) {
        self.client = client
        self.signedOut = signedOut
    }

    public func loadLibraries() async {
        let ticket = pinOrder.begin()
        await loading("player libraries", signedOut: signedOut, problem: { if pinOrder.fresh(ticket) { problem = $0 } }) {
            let all = try await client.librarySummaries()
            guard pinOrder.fresh(ticket) else { return }
            order = all.map(\.id)
            pins = LibrarySummary.ordered(all).filter(\.pinned).map(\.id)
            libraries = arranged(all)
            problem = nil
            librariesLoaded = true
        }
    }

    public var moreSeries: Bool { seriesNext != nil }

    /// The Series tab from its first page (the first look, a refresh, Try again).
    public func loadSeries() async {
        await seriesPage(0)
    }

    /// The Series tab's next page, asked for as the grid nears its end: a home with
    /// thousands of series shows the first at once and the rest as the person scrolls.
    public func loadMoreSeries() async {
        guard let page = seriesNext, !seriesBusy else { return }
        await seriesPage(page)
    }

    private func seriesPage(_ page: Int) async {
        seriesBusy = true
        defer { seriesBusy = false }
        await loading("player series", signedOut: signedOut, problem: { seriesProblem = $0 }) {
            let r = try await client.seriesListPage(page: page)
            let have = page == 0 ? [] : Set(series.map(\.id))
            series = (page == 0 ? [] : series) + r.items.filter { !have.contains($0.id) }
            seriesNext = r.more ? page + 1 : nil
            seriesProblem = nil
            seriesLoaded = true
        }
    }

    public func isPinned(_ id: String) -> Bool { pins.contains(id) }

    /// Pins `id` (at the end, as the web's sidebar does) or unpins it
    /// (`PUT /api/me/pinned-libraries`): the grid moves at once and the home's answer wins;
    /// when the home refuses, the old order comes back.
    public func togglePin(_ id: String) async {
        let before = pins
        pins = before.contains(id) ? before.filter { $0 != id } : before + [id]
        libraries = arranged(libraries)
        await loading("player pin", signedOut: signedOut, problem: { p in
            pins = before
            libraries = arranged(libraries)
            problem = p
        }) {
            let saved = try await pinOrder.saving { try await client.setPinnedLibraries(pins) }
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
