import MaktabaKit
import SwiftUI

/// Search on Apple TV (story 49.11): the home's search (`POST /api/search`, story 49.8), with
/// the TV's keyboard or the Siri remote's dictation.
///
/// - While typing, the suggestions show under the keyboard, each with its kind (an earlier
///   search, a word, a video, a speaker); a video's title opens its page, any other suggestion
///   is searched as its words.
/// - What is typed is searched by **Search for “…”**, the first thing under the keyboard (or
///   the keyboard's own search key): a search is kept on the home as a recent one, so the TV
///   does not search each word as it is typed.
/// - Over the results: "Did you mean …?" (runs the corrected words, the order and filters
///   kept), the order (Relevance, Newest, Most matches), **Filters** (a sheet of the facets
///   with their counts, several combinable) and the filters in use, each removable, with
///   Clear all filters.
/// - Titles, then In the transcript, as cards that open the video's page.
///
/// The model is MaktabaKit's `SearchModel`, the Player's.
struct SearchView: View {
    @Environment(\.client) private var client
    @Environment(\.appSession) private var session

    var body: some View {
        if let client {
            // another home (or account) is another search: a new model
            TVSearch(client: client, session: session)
                .id(ObjectIdentifier(client))
        }
    }
}

private struct TVSearch: View {
    @State private var model: SearchModel
    /// A title suggestion's video, its page pushed.
    @State private var opened: String?
    /// The facets' sheet is open.
    @State private var filtering = false
    /// A title suggestion was chosen: a search-field submit that comes with it is not a search.
    @State private var titleChosen = false
    private let session: AppSession?

    private let columns = Array(repeating: GridItem(.fixed(VideoCard.width), spacing: 56), count: 4)

    init(client: APIClient, session: AppSession?) {
        self.session = session
        _model = State(initialValue: SearchModel(client: client,
                                                 byMeaning: { session?.capabilities.current.isOn(.semanticSearch) ?? true }) {
            session?.signedOut()
        })
    }

    /// What is typed and not yet searched.
    private var pending: String? {
        let q = SearchModel.sendable(model.query)
        return q.isEmpty || q == model.submitted ? nil : q
    }

    var body: some View {
        @Bindable var model = model
        ScrollView {
            VStack(alignment: .leading, spacing: 40) {
                // the TV searches by words alone while search by meaning is off on the home;
                // an admin reads so under the field (51.11)
                if session?.capabilities.current.wordsOnlyLine == true {
                    Text(verbatim: FeatureWords.wordsOnly)
                        .font(Theme.body(26))
                        .foregroundStyle(Theme.muted)
                        .accessibilityIdentifier("search.wordsOnly")
                }
                if let pending {
                    Button {
                        Task { await model.submit(pending) }
                    } label: {
                        Label { Text(verbatim: SearchWords.searchFor(pending)) } icon: { Image(systemName: "magnifyingglass") }
                            .font(Theme.body(30, weight: .medium))
                    }
                    .accessibilityIdentifier("search.run")
                }
                content
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(.horizontal, 80)
            .padding(.vertical, 48)
        }
        .searchable(text: $model.query, prompt: Text("Search your libraries"))
        .searchSuggestions {
            ForEach(model.suggestions) { s in
                Button {
                    choose(s)
                } label: {
                    Label {
                        if let kind = s.kind {
                            Text(verbatim: s.text) + Text(verbatim: "  ·  " + SearchWords.kind(kind)).foregroundStyle(Theme.muted)
                        } else {
                            Text(verbatim: s.text)
                        }
                    } icon: {
                        Image(systemName: Self.icon(s.kind))
                    }
                }
                .accessibilityIdentifier("search.suggestion.\(s.kind?.rawValue ?? "plain")")
            }
        }
        .onSubmit(of: .search) {
            let typed = model.query
            // a moment first: tvOS can send the submit just before a title suggestion's own
            // action, which then says it was not a search
            Task {
                try? await Task.sleep(for: .milliseconds(300))
                if titleChosen {
                    titleChosen = false
                    return
                }
                await model.submit(typed)
            }
        }
        // suggestions follow the typing once it rests
        .task(id: model.query) {
            do { try await Task.sleep(for: SearchModel.suggestDelay) } catch { return }
            await model.suggest(model.query)
        }
        .navigationDestination(item: $opened) { VideoPage(id: $0).parentalFresh() }
        .sheet(isPresented: $filtering) { TVFilterSheet(model: model) }
    }

    @ViewBuilder private var content: some View {
        if let problem = model.problem {
            ErrorBanner(failure: problem) { Task { await model.retry() } }
        } else if model.searching, model.results?.sent != model.submitted {
            ProgressView().frame(maxWidth: .infinity).padding(.top, 80)
                .accessibilityIdentifier("search.loading")
        } else if let results = model.results {
            help(results)
            if results.isEmpty {
                ContentUnavailableView.search(text: results.query)
                    .padding(.top, 40)
                    .accessibilityIdentifier("search.none")
            } else {
                group(SectionTitle(String(localized: "Titles")), id: "search.titles", results.titles,
                      more: results.titlesNext == nil ? nil : { Task { await model.moreTitles() } }, moreID: "search.titles.more")
                group(SectionTitle(String(localized: "In the transcript")), id: "search.transcripts", results.transcripts,
                      more: results.transcriptsNext == nil ? nil : { Task { await model.moreTranscripts() } },
                      moreID: "search.transcripts.more")
            }
        }
    }

    /// Did you mean, the order, Filters, and the filters in use.
    @ViewBuilder private func help(_ results: SearchModel.Results) -> some View {
        if let dym = results.didYouMean {
            Button {
                let ask = model.ask(didYouMean: dym)
                model.query = ask.query
                Task { await model.run(ask) }
            } label: {
                Text(verbatim: SearchWords.didYouMean(dym))
                    .font(Theme.body(30, weight: .medium))
            }
            .accessibilityIdentifier("search.didYouMean")
        }
        HStack(spacing: 32) {
            Menu {
                ForEach(SearchSort.allCases, id: \.self) { s in
                    Button {
                        if let ask = model.ask(sort: s) { Task { await model.run(ask) } }
                    } label: {
                        if s == model.sort {
                            Label { Text(verbatim: SearchWords.sort(s)) } icon: { Image(systemName: "checkmark") }
                        } else {
                            Text(verbatim: SearchWords.sort(s))
                        }
                    }
                    .accessibilityIdentifier("search.sort.\(s.rawValue)")
                }
            } label: {
                Label { Text(verbatim: SearchWords.sortedBy(model.sort)) } icon: { Image(systemName: "arrow.up.arrow.down") }
                    .font(Theme.body(28, weight: .medium))
            }
            .accessibilityIdentifier("search.sort")
            if !model.shownFacets.isEmpty {
                let n = model.filters.count
                Button {
                    filtering = true
                } label: {
                    Label {
                        Text(verbatim: n > 0 ? "\(SearchWords.filters()) (\(SearchWords.number(n)))" : SearchWords.filters())
                    } icon: {
                        Image(systemName: n > 0 ? "line.3.horizontal.decrease.circle.fill" : "line.3.horizontal.decrease.circle")
                    }
                    .font(Theme.body(28, weight: .medium))
                }
                .accessibilityIdentifier("search.filters")
            }
            if model.refining {
                ProgressView().accessibilityIdentifier("search.refining")
            }
        }
        .focusSection()
        if !model.filters.isEmpty {
            ScrollView(.horizontal) {
                HStack(spacing: 24) {
                    ForEach(Array(model.filters.inUse.enumerated()), id: \.offset) { _, f in
                        let name = SearchWords.inUse(f.facet, f.value, label: model.label(f.facet, f.value))
                        Button {
                            if let ask = model.ask(removing: f.facet, f.value) { Task { await model.run(ask) } }
                        } label: {
                            Label { Text(verbatim: name) } icon: { Image(systemName: "xmark") }
                                .font(Theme.body(24, weight: .medium))
                        }
                        .accessibilityLabel(Text(verbatim: SearchWords.remove(name)))
                        .accessibilityIdentifier("search.filter.\(f.facet.rawValue).\(f.value)")
                    }
                    Button {
                        if let ask = model.askClearingFilters() { Task { await model.run(ask) } }
                    } label: {
                        Text(verbatim: SearchWords.clearFilters()).font(Theme.body(24, weight: .medium))
                    }
                    .accessibilityIdentifier("search.filters.clear")
                }
                .padding(.vertical, 20)
            }
            .scrollClipDisabled()
            .focusSection()
            .accessibilityElement(children: .contain)
            .accessibilityLabel(Text(verbatim: SearchWords.filtersInUse()))
        }
    }

    /// A group of results: its heading, its cards (each opens the video's page), Show more.
    @ViewBuilder private func group(_ title: SectionTitle, id: String, _ videos: [SearchVideo],
                                    more: (() -> Void)?, moreID: String) -> some View {
        if !videos.isEmpty {
            VStack(alignment: .leading, spacing: 32) {
                title.accessibilityIdentifier(id)
                LazyVGrid(columns: columns, alignment: .leading, spacing: 64) {
                    ForEach(videos) { v in
                        NavigationLink {
                            VideoPage(id: v.videoID).parentalFresh()
                        } label: {
                            VideoCard(id: v.videoID, title: v.title.isEmpty ? nil : v.title)
                        }
                        .buttonStyle(.borderless)
                        .accessibilityIdentifier("\(id).\(v.videoID)")
                    }
                }
                if let more {
                    Button(action: more) {
                        Text("Show more").font(Theme.body(26, weight: .medium))
                    }
                    .disabled(model.paging)
                    .accessibilityIdentifier(moreID)
                }
            }
            .focusSection()
        }
    }

    private func choose(_ s: SearchSuggestion) {
        if s.kind == .title, let id = s.videoID {
            titleChosen = true
            opened = id
            Task {
                try? await Task.sleep(for: .seconds(1))
                titleChosen = false
            }
        } else {
            model.query = s.text
            Task { await model.submit(s.text) }
        }
    }

    static func icon(_ kind: SuggestionKind?) -> String {
        switch kind {
        case .history: "clock.arrow.circlepath"
        case .title: "play.rectangle"
        case .speaker: "person"
        case .term, nil: "magnifyingglass"
        }
    }
}

/// Narrow the results: a row of buckets per facet, each with its count, a chosen one ticked.
/// Select takes it on or off and searches again; the sheet stays open on the new counts.
/// Done or Menu closes it.
private struct TVFilterSheet: View {
    let model: SearchModel
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 48) {
                HStack(spacing: 32) {
                    Text(verbatim: SearchWords.narrow())
                        .font(Theme.heading(52))
                        .foregroundStyle(Theme.text)
                    Spacer()
                    if !model.filters.isEmpty {
                        Button {
                            if let ask = model.askClearingFilters() { Task { await model.run(ask) } }
                        } label: {
                            Text(verbatim: SearchWords.clearFilters()).font(Theme.body(26, weight: .medium))
                        }
                        .accessibilityIdentifier("search.facets.clear")
                    }
                    Button {
                        dismiss()
                    } label: {
                        Text(verbatim: SearchWords.done()).font(Theme.body(26, weight: .medium))
                    }
                    .accessibilityIdentifier("search.facets.done")
                }
                .focusSection()
                ForEach(model.shownFacets) { group in
                    VStack(alignment: .leading, spacing: 16) {
                        Text(verbatim: SearchWords.facet(group.facet))
                            .font(Theme.heading(36))
                            .foregroundStyle(Theme.goldText)
                            .accessibilityAddTraits(.isHeader)
                        ScrollView(.horizontal) {
                            HStack(spacing: 24) {
                                ForEach(group.buckets) { b in
                                    bucket(b)
                                }
                            }
                            .padding(.vertical, 24)
                        }
                        .scrollClipDisabled()
                    }
                    .focusSection()
                }
                if model.refining {
                    ProgressView()
                }
            }
            .padding(80)
        }
        .background(Theme.background)
        .accessibilityIdentifier("search.facets")
    }

    private func bucket(_ b: ShownBucket) -> some View {
        let name = SearchWords.bucket(b.facet, b.value, label: b.label)
        return Button {
            if let ask = model.ask(toggling: b.facet, b.value) { Task { await model.run(ask) } }
        } label: {
            HStack(spacing: 16) {
                Image(systemName: b.chosen ? "checkmark.circle.fill" : "circle")
                Text(verbatim: name)
                if let n = b.count {
                    Text(verbatim: SearchWords.number(n)).foregroundStyle(.secondary)
                }
            }
            .font(Theme.body(26, weight: .medium))
        }
        .accessibilityLabel(Text(verbatim: b.count.map { "\(name), \(SearchWords.videos($0))" } ?? name))
        .accessibilityAddTraits(b.chosen ? .isSelected : [])
        .accessibilityIdentifier("search.facet.\(b.facet.rawValue).\(b.value)")
    }
}
