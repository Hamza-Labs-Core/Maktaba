import MaktabaKit
import SwiftUI

/// The five tabs of a library on Apple TV (Epic 56 story 56.03), in Plex's order and names.
enum LibraryTab: String, CaseIterable, Identifiable {
    case recommended, library, collections, playlists, categories
    var id: String { rawValue }

    var title: LocalizedStringKey {
        switch self {
        case .recommended: "Recommended"
        case .library: "Library"
        case .collections: "Collections"
        case .playlists: "Playlists"
        case .categories: "Categories"
        }
    }

    /// The tab a library reopens on: the last one chosen there, kept on the device.
    static func remembered(_ library: String) -> LibraryTab {
        UserDefaults.standard.string(forKey: "libraryTab.\(library)").flatMap(LibraryTab.init) ?? .recommended
    }

    static func remember(_ tab: LibraryTab, for library: String) {
        UserDefaults.standard.set(tab.rawValue, forKey: "libraryTab.\(library)")
    }
}

/// A library: its name with Play and Shuffle (the Library tab's view), the tab bar (a
/// focus section above the content, a tab chosen when it takes focus) and the tab's content.
struct LibraryTabsView: View {
    let library: String
    let name: String
    @State private var tab: LibraryTab
    @State private var query: VideoQuery
    @State private var bySeries = false
    @Environment(\.queuePlayer) private var queue

    init(library: String, name: String) {
        self.library = library
        self.name = name
        _tab = State(initialValue: LibraryTab.remembered(library))
        _query = State(initialValue: VideoQuery(library: library))
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 28) {
            HStack(spacing: 32) {
                Text(name).font(Theme.heading(52)).foregroundStyle(Theme.text).lineLimit(1)
                Spacer()
                if tab == .recommended || tab == .library, let source = query.queueSource {
                    PlayShuffleButtons(source: source)
                }
            }
            if let f = queue?.failure {
                Text(f).font(Theme.body(26)).foregroundStyle(Theme.gold).accessibilityIdentifier("library.failure")
            }
            HStack(spacing: 24) {
                ForEach(LibraryTab.allCases) { t in
                    TabButton(title: t.title, selected: t == tab) {
                        tab = t
                        LibraryTab.remember(t, for: library)
                    }
                    .accessibilityIdentifier("library.tab.\(t.rawValue)")
                }
            }
            .focusSection()
            Group {
                switch tab {
                case .recommended: RecommendedTab(library: library)
                case .library: LibraryGridTab(query: $query, bySeries: $bySeries)
                case .collections: CollectionsTab(library: library)
                case .playlists: PlaylistsGrid(library: library)
                case .categories:
                    CategoriesTab(library: library) { key, value in
                        query = VideoQuery(library: library)
                        switch key {
                        case "speaker": query.speaker = value
                        case "tag": query.tag = value
                        case "language": query.language = value
                        case "year": query.year = Int(value)
                        default: break
                        }
                        tab = .library
                    }
                }
            }
            .focusSection()
            .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        }
        .padding(.horizontal, 80)
        .padding(.vertical, 40)
        .background(Theme.background.ignoresSafeArea())
    }
}

/// A tab of the row: chosen on focus (as Plex's), gold while chosen.
struct TabButton: View {
    let title: LocalizedStringKey
    let selected: Bool
    let choose: () -> Void
    @FocusState private var focused: Bool

    var body: some View {
        Button(action: choose) {
            Text(title)
                .font(Theme.body(30, weight: selected ? .semibold : .medium))
                .foregroundStyle(selected ? Theme.gold : Theme.muted)
        }
        .buttonStyle(.plain)
        .focused($focused)
        .onChange(of: focused) { _, f in if f { choose() } }
    }
}

/// A horizontal row of video cards with its title.
struct CardShelf: View {
    let title: String
    let videos: [BrowseVideo]

    var body: some View {
        VStack(alignment: .leading, spacing: 20) {
            Text(title).font(Theme.heading(34)).foregroundStyle(Theme.text)
            ScrollView(.horizontal) {
                LazyHStack(spacing: 48) {
                    ForEach(videos) { v in
                        NavigationLink { VideoPage(id: v.id) } label: {
                            VideoCard(id: v.id, title: v.title ?? v.filename, progress: v.positionSec.map { p in
                                (v.durationSec ?? 0) > 0 ? p / v.durationSec! : 0 } ?? 0)
                        }
                        .buttonStyle(.borderless)
                    }
                }
                .padding(.vertical, 24)
            }
        }
    }
}

struct RecommendedTab: View {
    let library: String
    @Environment(\.client) private var client
    @State private var hub: LibraryHub?
    @State private var promoted: [CollectionRow] = []
    @State private var failed = false

    var body: some View {
        ScrollView {
            if let hub {
                let rows: [(String, [BrowseVideo])] = [
                    (String(localized: "On Deck"), hub.onDeck), (String(localized: "Continue Watching"), hub.continueWatching),
                    (String(localized: "Recently Added"), hub.recentlyAdded), (String(localized: "Recently Watched"), hub.recentlyWatched),
                ] + promoted.map { ($0.name, $0.items) }
                let shown = rows.filter { !$0.1.isEmpty }
                if shown.isEmpty {
                    Text("Nothing to recommend yet. Browse the Library tab.").foregroundStyle(Theme.muted)
                }
                VStack(alignment: .leading, spacing: 36) {
                    ForEach(shown, id: \.0) { CardShelf(title: $0.0, videos: $0.1) }
                }
            } else if failed {
                Text("The queue could not be loaded.").foregroundStyle(Theme.muted)
            } else {
                ProgressView()
            }
        }
        .accessibilityIdentifier("library.recommended")
        .task(id: library) {
            guard let client else { return }
            do {
                hub = try await client.libraryHub(library)
                promoted = (try? await client.libraryCollectionRows(library)) ?? []
            } catch { failed = true }
        }
    }
}

/// The Library tab: Show (videos or series), Sort, Filter (unwatched and the facets) and the grid.
struct LibraryGridTab: View {
    @Binding var query: VideoQuery
    @Binding var bySeries: Bool
    @Environment(\.client) private var client
    @State private var videos: [BrowseVideo] = []
    @State private var series: [GroupedCard] = []
    @State private var next: String?
    @State private var facets: VideoFacets?
    @State private var filtering = false

    private let columns = Array(repeating: GridItem(.fixed(VideoCard.width), spacing: 56), count: 4)

    var body: some View {
        VStack(alignment: .leading, spacing: 24) {
            HStack(spacing: 24) {
                Button(bySeries ? "Show: Series" : "Show: Videos") { bySeries.toggle() }
                    .accessibilityIdentifier("library.show")
                Button("Sort") {
                    let all = VideoSort.allCases
                    query.sort = all[(all.firstIndex(of: query.sort)! + 1) % all.count]
                }
                .accessibilityIdentifier("library.sort")
                Button(activeFilters == 0 ? "Filter" : "Filter (\(activeFilters))") { filtering.toggle() }
                    .accessibilityIdentifier("library.filter")
                if activeFilters > 0 {
                    Button("Clear filters") { query = VideoQuery(library: query.library) }
                }
            }
            .buttonStyle(.gold)
            .focusSection()
            if filtering { filterRow }
            ScrollView {
                LazyVGrid(columns: columns, spacing: 64) {
                    if bySeries {
                        ForEach(series) { c in
                            NavigationLink {
                                if c.isSeries { SeriesView(id: c.id) } else { VideoPage(id: c.id) }
                            } label: { VideoCard(id: c.picture, title: c.label) }
                            .buttonStyle(.borderless)
                            .onAppear { if c.id == series.last?.id, next != nil { Task { await load(more: true) } } }
                        }
                    } else {
                        ForEach(videos) { v in
                            NavigationLink { VideoPage(id: v.id) } label: { VideoCard(id: v.id, title: v.title ?? v.filename) }
                                .buttonStyle(.borderless)
                                .onAppear { if v.id == videos.last?.id, next != nil { Task { await load(more: true) } } }
                        }
                    }
                }
                .padding(.vertical, 24)
                if !bySeries && videos.isEmpty || bySeries && series.isEmpty {
                    Text("Nothing matches these filters.").foregroundStyle(Theme.muted)
                }
            }
            .accessibilityIdentifier("library.grid")
        }
        .task(id: "\(query.queueFilters.sorted { $0.key < $1.key })\(bySeries)") { await load(more: false) }
        .task(id: query.library) {
            if let client, let lib = query.library { facets = try? await client.videoFacets(library: lib) }
        }
    }

    private var activeFilters: Int {
        [query.watched, query.speaker, query.language, query.tag, query.year.map(String.init)].compactMap { $0 }.count
    }

    private var filterRow: some View {
        ScrollView(.horizontal) {
            HStack(spacing: 20) {
                ForEach([("unwatched", "Unwatched"), ("in_progress", "In progress"), ("watched", "Watched")], id: \.0) { value, label in
                    Button((query.watched == value ? "✓ " : "") + String(localized: String.LocalizationValue(label))) {
                        query.watched = query.watched == value ? nil : value
                    }
                }
                ForEach(facets?.speakers.prefix(20).map { $0 } ?? []) { f in
                    Button((query.speaker == f.value ? "✓ " : "") + (f.name ?? f.value)) { query.speaker = query.speaker == f.value ? nil : f.value }
                }
            }
            .buttonStyle(.gold)
            .padding(.vertical, 12)
        }
        .focusSection()
    }

    private func load(more: Bool) async {
        guard let client else { return }
        let q = more ? (next.map(query.next) ?? query) : query
        if bySeries {
            guard let page = try? await client.videosBySeries(q) else { return }
            series = more ? series + page.items : page.items
            next = page.next
        } else {
            guard let page = try? await client.videos(q) else { return }
            videos = more ? videos + page.items : page.items
            next = page.next
        }
    }
}

/// The Collections tab: a card per collection with its badge; Kind keeps one kind (story 56.08).
struct CollectionsTab: View {
    let library: String
    @Environment(\.client) private var client
    @State private var kind: String?
    @State private var items: [LibraryCollection] = []
    private let kinds: [(String?, LocalizedStringKey)] = [(nil, "All"), ("youtube", "From YouTube"), ("plex", "From Plex"),
                                                         ("curated", "Curated"), ("topic", "Topics"), ("filler", "Clips")]
    private let columns = Array(repeating: GridItem(.fixed(VideoCard.width), spacing: 56), count: 4)

    var body: some View {
        VStack(alignment: .leading, spacing: 24) {
            Button {
                let i = kinds.firstIndex { $0.0 == kind } ?? 0
                kind = kinds[(i + 1) % kinds.count].0
            } label: {
                HStack { Text("Kind"); Text(kinds.first { $0.0 == kind }?.1 ?? "All") }
            }
            .buttonStyle(.gold)
            .accessibilityIdentifier("collections.kind")
            ScrollView {
                if items.isEmpty {
                    Text("No collections in this library yet.").foregroundStyle(Theme.muted)
                }
                LazyVGrid(columns: columns, spacing: 64) {
                    ForEach(items) { c in
                        NavigationLink { CollectionPageView(library: library, collection: c) } label: {
                            VStack(alignment: .leading) {
                                VideoCard(id: c.posterVideoIDs.first ?? "", title: c.name)
                                if let b = Self.badge(c) { Text(b).font(Theme.body(22)).foregroundStyle(Theme.gold) }
                            }
                        }
                        .buttonStyle(.borderless)
                        .accessibilityIdentifier("collection.\(c.id)")
                    }
                }
            }
        }
        .task(id: kind) { items = (try? await client?.libraryCollections(library, kind: kind)) ?? [] }
    }

    static func badge(_ c: LibraryCollection) -> String? {
        switch (c.sourceKind, c.kind) {
        case ("youtube_playlist", _): String(localized: "YouTube playlist")
        case ("youtube_channel", _): String(localized: "YouTube channel")
        case ("plex", _): String(localized: "Plex collection")
        case (_, "smart"): String(localized: "Smart")
        case (_, "topic"): String(localized: "Topics")
        case (_, "clip"): String(localized: "Clips")
        default: nil
        }
    }
}

/// The user's playlists (a library's, or every one from Settings), Smart ones badged (story 56.10).
struct PlaylistsGrid: View {
    let library: String?
    @Environment(\.client) private var client
    @State private var lists: [LibraryPlaylist]?
    private let columns = Array(repeating: GridItem(.fixed(VideoCard.width), spacing: 56), count: 4)

    var body: some View {
        ScrollView {
            if let lists, lists.isEmpty {
                Text(library == nil ? "No playlists yet" : "None of your playlists has videos from this library.")
                    .foregroundStyle(Theme.muted)
            }
            LazyVGrid(columns: columns, spacing: 64) {
                ForEach(lists ?? []) { p in
                    NavigationLink { PlaylistPageView(id: p.id) } label: {
                        VStack(alignment: .leading) {
                            VideoCard(id: p.posterVideoIDs.first ?? "", title: p.name)
                            if p.smart { Text("Smart").font(Theme.body(22)).foregroundStyle(Theme.gold) }
                        }
                    }
                    .buttonStyle(.borderless)
                    .accessibilityIdentifier("playlist.\(p.id)")
                }
            }
        }
        .task { lists = (try? await client?.libraryPlaylists(library: library)) ?? [] }
    }
}

/// The Categories tab: Speakers, Topics, Languages, Years; a tile opens the Library tab filtered.
struct CategoriesTab: View {
    let library: String
    let pick: (String, String) -> Void
    @Environment(\.client) private var client
    @State private var c: LibraryCategories?

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 36) {
                if let c {
                    row("Speakers", "speaker", c.speakers)
                    row("Topics", "tag", c.topics)
                    row("Languages", "language", c.languages)
                    row("Years", "year", c.years)
                }
            }
        }
        .task { c = try? await client?.libraryCategories(library) }
    }

    @ViewBuilder
    private func row(_ title: LocalizedStringKey, _ key: String, _ tiles: [MaktabaKit.Category]) -> some View {
        if !tiles.isEmpty {
            VStack(alignment: .leading, spacing: 16) {
                Text(title).font(Theme.heading(34)).foregroundStyle(Theme.text)
                ScrollView(.horizontal) {
                    HStack(spacing: 32) {
                        ForEach(tiles) { t in
                            Button { pick(key, t.value) } label: {
                                VStack(alignment: .leading) { Text(t.label).lineLimit(1); Text("\(t.count)").foregroundStyle(Theme.muted) }
                            }
                            .buttonStyle(.gold)
                            .accessibilityIdentifier("category.\(key).\(t.value)")
                        }
                    }
                    .padding(.vertical, 16)
                }
            }
        }
    }
}
