import MaktabaKit
import SwiftUI

/// A library's page (`PlayerRoute.library`, FR-15): its hub rows (On Deck, Continue Watching,
/// Recently Added, Recently Watched; an empty row is left out), then tabs **Videos** (every
/// video, newest first or A to Z, narrowed by language and speaker, paged by cursor to the
/// end) and **Series** (the library's series, with progress). A video opens its page.
struct LibraryScreen: View {
    let context: PlayerContext
    let id: String

    @State private var model: LibraryModel

    init(context: PlayerContext, id: String) {
        self.context = context
        self.id = id
        _model = State(initialValue: LibraryModel(client: context.client, id: id, signedOut: { context.session.signedOut() }))
    }

    private var client: APIClient { context.client }

    var body: some View {
        ScrollView {
            LazyVStack(alignment: .leading, spacing: MaktabaTokens.Spacing.s5) {
                if model.problem == .notAvailable {
                    ProblemView(problem: .notAvailable) {}
                } else {
                    rows
                    tabs
                }
            }
            .padding(.vertical, MaktabaTokens.Spacing.s3)
        }
        .maktabaSurface()
        .refreshable {
            await model.loadHub()
            await model.reload()
        }
        .task { await model.loadHub() }
        .task(id: model.gridKey) { await model.reload() }
        .task(id: model.tab) { if model.tab == .series, !model.seriesLoaded { await model.loadSeries() } }
        .navigationTitle(model.name.isEmpty ? Text("Library", tableName: "Libraries", comment: "A library's page, before its name loads") : Text(verbatim: model.name))
        #if os(iOS)
        .navigationBarTitleDisplayMode(.inline)
        #endif
        .accessibilityIdentifier("screen.library")
    }

    // MARK: the hub rows

    @ViewBuilder private var rows: some View {
        if let hub = model.hub {
            row(Text("On Deck", tableName: "Libraries", comment: "A library's row: the next episode of each series"), hub.onDeck, id: "onDeck")
            row(Text("Continue Watching", tableName: "Libraries", comment: "A library's row: videos started"), hub.continueWatching, id: "continue")
            row(Text("Recently Added", tableName: "Libraries", comment: "A library's row: the newest videos"), hub.recentlyAdded, id: "recentlyAdded")
            row(Text("Recently Watched", tableName: "Libraries", comment: "A library's row: videos watched lately"), hub.recentlyWatched, id: "recentlyWatched")
        }
    }

    @ViewBuilder private func row(_ title: Text, _ videos: [BrowseVideo], id: String) -> some View {
        if !videos.isEmpty {
            Rail(title, items: videos) { v in
                NavigationLink(value: PlayerRoute.video(id: v.id)) { VideoCard(client: client, video: v) }
                    .buttonStyle(.plain)
            }
            .accessibilityIdentifier("library.row.\(id)")
        }
    }

    // MARK: Videos | Series

    private var tabs: some View {
        VStack(alignment: .leading, spacing: MaktabaTokens.Spacing.s4) {
            Picker(selection: $model.tab) {
                Text("Videos", tableName: "Libraries", comment: "A library's tab of every video").tag(LibraryTab.videos)
                Text("Series", tableName: "Libraries", comment: "The Libraries root's tab of every series").tag(LibraryTab.series)
            } label: {
                Text("Show", tableName: "Libraries", comment: "The Libraries root's tabs, spoken")
            }
            .pickerStyle(.segmented)
            .accessibilityIdentifier("library.tabs")
            switch model.tab {
            case .videos:
                filters
                grid
            case .series:
                seriesGrid
            }
        }
        .padding(.horizontal, MaktabaTokens.Spacing.s4)
    }

    private var columns: [GridItem] {
        [GridItem(.adaptive(minimum: 160), spacing: MaktabaTokens.Spacing.s3, alignment: .top)]
    }

    /// Sort, language and speaker: each a menu that says what is chosen.
    private var filters: some View {
        ScrollView(.horizontal) {
            HStack(spacing: MaktabaTokens.Spacing.s2) {
                Menu {
                    Picker(selection: $model.sort) {
                        Text("Newest", tableName: "Libraries", comment: "A library's sort: the newest added first").tag(VideoSort.newest)
                        Text("A–Z", tableName: "Libraries", comment: "A library's sort: by title, A to Z").tag(VideoSort.title)
                    } label: { EmptyView() }
                } label: {
                    chip(systemImage: "arrow.up.arrow.down", model.sort == .title
                        ? Text("A–Z", tableName: "Libraries", comment: "A library's sort: by title, A to Z")
                        : Text("Newest", tableName: "Libraries", comment: "A library's sort: the newest added first"))
                }
                .accessibilityLabel(Text("Sort", tableName: "Libraries", comment: "A library's sort menu, spoken"))
                .accessibilityIdentifier("library.sort")

                if let languages = model.facets?.languages, !languages.isEmpty {
                    Menu {
                        Picker(selection: $model.language) {
                            Text("Any language", tableName: "Libraries", comment: "A library's language filter: off").tag(String?.none)
                            ForEach(languages) { f in Text(verbatim: Self.language(f.value)).tag(Optional(f.value)) }
                        } label: { EmptyView() }
                    } label: {
                        chip(systemImage: "globe", model.language.map { Text(verbatim: Self.language($0)) }
                            ?? Text("Any language", tableName: "Libraries", comment: "A library's language filter: off"))
                    }
                    .accessibilityLabel(Text("Language", tableName: "Libraries", comment: "A library's language filter, spoken"))
                    .accessibilityIdentifier("library.language")
                }

                if let speakers = model.facets?.speakers, !speakers.isEmpty {
                    Menu {
                        Picker(selection: $model.speaker) {
                            Text("Any speaker", tableName: "Libraries", comment: "A library's speaker filter: off").tag(String?.none)
                            ForEach(speakers) { f in Text(verbatim: f.name ?? f.value).tag(Optional(f.value)) }
                        } label: { EmptyView() }
                    } label: {
                        chip(systemImage: "person.wave.2", model.speaker.map { Text(verbatim: $0) }
                            ?? Text("Any speaker", tableName: "Libraries", comment: "A library's speaker filter: off"))
                    }
                    .accessibilityLabel(Text("Speaker", tableName: "Libraries", comment: "A library's speaker filter, spoken"))
                    .accessibilityIdentifier("library.speaker")
                }
            }
        }
        .scrollIndicators(.hidden)
        .accessibilityIdentifier("library.filters")
    }

    private func chip(systemImage: String, _ text: Text) -> some View {
        Label { text } icon: { Image(systemName: systemImage) }
            .font(.maktabaBody(14, weight: .medium, relativeTo: .subheadline))
            .foregroundStyle(Color.mktText)
            .padding(.horizontal, MaktabaTokens.Spacing.s3)
            .frame(minHeight: 44)
            .background(Capsule().fill(Color.mktSurface))
            .overlay(Capsule().strokeBorder(Color.mktBorder, lineWidth: 0.5))
            .contentShape(Capsule())
    }

    /// A language code in the reader's language ("ar" → "Arabic", «العربية»).
    static func language(_ code: String) -> String {
        Locale.current.localizedString(forLanguageCode: code) ?? code
    }

    @ViewBuilder private var grid: some View {
        if case let .failed(failure) = model.problem, model.videos.isEmpty {
            ProblemView(problem: .failed(failure)) { Task { await model.reload() } }
        } else if !model.loaded {
            ProgressView().frame(maxWidth: .infinity, minHeight: 160)
        } else if model.videos.isEmpty {
            ContentUnavailableView {
                Label {
                    model.language == nil && model.speaker == nil
                        ? Text("No videos yet", tableName: "Libraries", comment: "A library with no video")
                        : Text("No videos match your filters.", tableName: "Libraries", comment: "A library's filters match no video")
                } icon: { Image(systemName: "film") }
            }
        } else {
            LazyVGrid(columns: columns, alignment: .leading, spacing: MaktabaTokens.Spacing.s4) {
                ForEach(model.videos) { v in
                    NavigationLink(value: PlayerRoute.video(id: v.id)) { VideoCard(client: client, video: v) }
                        .buttonStyle(.plain)
                        .onAppear { if v.id == model.videos.last?.id { Task { await model.loadMore() } } }
                }
            }
            .accessibilityIdentifier("library.grid")
            if model.next != nil {
                ProgressView().frame(maxWidth: .infinity, minHeight: 60)
            }
        }
    }

    @ViewBuilder private var seriesGrid: some View {
        if !model.seriesLoaded {
            ProgressView().frame(maxWidth: .infinity, minHeight: 160)
        } else if model.series.isEmpty {
            ContentUnavailableView {
                Label { Text("No series yet", tableName: "Libraries", comment: "The Series grid with no series") } icon: {
                    Image(systemName: "rectangle.stack")
                }
            }
        } else {
            LazyVGrid(columns: [GridItem(.adaptive(minimum: 120), spacing: MaktabaTokens.Spacing.s3, alignment: .top)],
                      alignment: .leading, spacing: MaktabaTokens.Spacing.s4) {
                ForEach(model.series) { s in
                    NavigationLink(value: PlayerRoute.series(id: s.id)) {
                        PosterCard(title: s.name.isEmpty ? String(localized: SeriesGrid.untitled) : s.name,
                                   detail: String(localized: SeriesGrid.episodes(s.episodes)),
                                   poster: LibraryArt.poster(client, video: s.posterVideoID),
                                   progress: s.progress, shape: .tall, width: 120)
                    }
                    .buttonStyle(.plain)
                    .accessibilityIdentifier("series.\(s.id)")
                    .onAppear { if s.id == model.series.last?.id, model.moreSeries { Task { await model.loadSeries() } } }
                }
            }
        }
    }
}

/// A video in a row or the grid: its poster, title, length and progress.
struct VideoCard: View {
    let client: APIClient
    let video: BrowseVideo

    var body: some View {
        PosterCard(title: video.displayTitle, detail: video.durationSec.map(Self.length),
                   poster: LibraryArt.poster(client, video: video.id), progress: video.progress)
    }

    /// 1:05:30 or 42:10.
    static func length(_ seconds: Double) -> String {
        Duration.seconds(seconds).formatted(.time(pattern: seconds >= 3600 ? .hourMinuteSecond : .minuteSecond))
    }
}
