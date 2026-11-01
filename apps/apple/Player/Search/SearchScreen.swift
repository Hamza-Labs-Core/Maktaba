import MaktabaKit
import SwiftUI

/// Search (Epic 48, FR-19 and FR-20): the system's search field (`.searchable`) with the
/// person's own past searches as suggestions while typing, and their recent searches while
/// the field is empty. Submit asks the home (`POST /api/search`, hybrid) and shows **Titles**
/// and **In the transcript**: a title opens the video's page, a transcript passage opens the
/// player at its time. The query goes as typed, Arabic with its diacritics too.
struct SearchScreen: View {
    let context: PlayerContext

    @State private var model: SearchModel
    @FocusState private var typing: Bool

    init(context: PlayerContext) {
        self.context = context
        let session = context.session
        _model = State(initialValue: SearchModel(client: context.client) { session.signedOut() })
    }

    var body: some View {
        @Bindable var model = model
        content
            .accessibilityElement(children: .contain)
            .accessibilityIdentifier("screen.search")
            .maktabaSurface()
            .navigationTitle(Text(PlayerSection.search.title))
            .searchable(text: $model.query, placement: Self.placement,
                        prompt: Text("Search titles and transcripts", tableName: "Search", comment: "The search field's placeholder"))
            .searchFocused($typing)
            .searchSuggestions {
                ForEach(model.suggestions, id: \.self) { s in
                    Button {
                        submit(s)
                    } label: {
                        Label { Text(verbatim: s) } icon: { Image(systemName: "magnifyingglass") }
                    }
                    .accessibilityIdentifier("search.suggestion")
                }
            }
            .onSubmit(of: .search) { submit(model.query) }
            // suggestions follow the typing once it rests
            .task(id: model.query) {
                try? await Task.sleep(for: SearchModel.suggestDelay)
                guard !Task.isCancelled else { return }
                await model.suggest(model.query)
            }
            .task { await model.loadRecent() }
    }

    /// Where the field is: always showing under the title on an iPhone and an iPad (Search is
    /// the field, not a button to find it), the toolbar's on the Mac.
    private static var placement: SearchFieldPlacement {
        #if os(iOS)
        .navigationBarDrawer(displayMode: .always)
        #else
        .automatic
        #endif
    }

    @ViewBuilder private var content: some View {
        if model.query.isEmpty {
            recentList
        } else if model.searching, model.results?.query != model.submitted {
            ProgressView().frame(maxWidth: .infinity, maxHeight: .infinity)
                .accessibilityIdentifier("search.loading")
        } else if let problem = model.problem {
            HomeErrorView(problem, video: false) { _ in Task { await model.retry() } }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
        } else if let results = model.results {
            if results.isEmpty {
                ContentUnavailableView {
                    Label { Text("Nothing matches “\(results.query)”.", tableName: "Search", comment: "A search found nothing; the query")
                    } icon: { Image(systemName: "magnifyingglass") }
                }
                .accessibilityIdentifier("search.none")
            } else {
                ResultsList(results: results, home: context.client.home, paging: model.paging,
                            moreTitles: { Task { await model.moreTitles() } },
                            moreTranscripts: { Task { await model.moreTranscripts() } })
            }
        } else {
            recentList
        }
    }

    /// The recent searches under a header with Clear all: a tap runs one again, ✕ forgets it.
    private var recentList: some View {
        List {
            if model.recent.isEmpty {
                Text("Search for a title, or for words said in a video.", tableName: "Search",
                     comment: "Search before anything is typed, with no recent searches")
                    .font(.maktabaBody(15, relativeTo: .body))
                    .foregroundStyle(Color.mktMuted)
                    .frame(maxWidth: .infinity)
                    .multilineTextAlignment(.center)
                    .padding(.top, MaktabaTokens.Spacing.s6)
                    .listRowBackground(Color.clear)
                    .listRowSeparator(.hidden)
            } else {
                Section {
                    ForEach(model.recent) { r in
                        HStack {
                            Button {
                                submit(r.query)
                            } label: {
                                HStack(spacing: MaktabaTokens.Spacing.s3) {
                                    Image(systemName: "clock.arrow.circlepath")
                                        .foregroundStyle(Color.mktMuted)
                                        .accessibilityHidden(true)
                                    Text(verbatim: r.query)
                                        .font(.maktabaBody(17, relativeTo: .body))
                                        .foregroundStyle(Color.mktText)
                                        .fixedSize(horizontal: false, vertical: true)
                                }
                                .frame(maxWidth: .infinity, minHeight: 44, alignment: .leading)
                                .contentShape(Rectangle())
                            }
                            .accessibilityIdentifier("search.recent")
                            Button {
                                Task { await model.forget(r) }
                            } label: {
                                Image(systemName: "xmark").foregroundStyle(Color.mktMuted)
                                    .frame(minWidth: 44, minHeight: 44)
                                    .contentShape(Rectangle())
                            }
                            .accessibilityLabel(Text("Remove \(r.query)", tableName: "Search",
                                                     comment: "Forgets one recent search; the query"))
                            .accessibilityIdentifier("search.recent.remove")
                        }
                        .buttonStyle(.borderless)
                        .listRowBackground(Color.clear)
                    }
                } header: {
                    HStack {
                        SearchHeading(text: Text("Recent searches", tableName: "Search", comment: "The recent searches' heading"))
                        Spacer()
                        Button {
                            Task { await model.clearRecent() }
                        } label: {
                            Text("Clear all", tableName: "Search", comment: "Forgets every recent search")
                        }
                        .buttonStyle(.borderless)
                        .accessibilityIdentifier("search.recent.clear")
                    }
                }
            }
        }
        .listStyle(.plain)
    }

    private func submit(_ q: String) {
        guard !SearchModel.sendable(q).isEmpty else { return }
        // the field holds the chosen text at once: the field's own submit, which can follow
        // a suggestion's tap, then sends that too rather than what was typed so far
        model.query = q
        typing = false
        Task { await model.submit(q) }
    }
}

/// A group's heading: brass, a heading for VoiceOver.
private struct SearchHeading: View {
    let text: Text

    var body: some View {
        text
            .font(.maktabaBody(15, weight: .semibold, relativeTo: .headline))
            .foregroundStyle(Color.mktBrassText)
            .textCase(nil)
            .accessibilityAddTraits(.isHeader)
    }
}

/// Titles, then In the transcript, each with Show more while the home kept more.
private struct ResultsList: View {
    let results: SearchModel.Results
    let home: URL
    let paging: Bool
    let moreTitles: () -> Void
    let moreTranscripts: () -> Void

    var body: some View {
        List {
            if results.degraded {
                Text("Text matches only: the meaning search did not answer.", tableName: "Search",
                     comment: "The home answered a search with its text half only")
                    .font(.maktabaBody(13, relativeTo: .footnote))
                    .foregroundStyle(Color.mktMuted)
                    .listRowBackground(Color.clear)
                    .accessibilityIdentifier("search.degraded")
            }
            if !results.titles.isEmpty {
                Section {
                    ForEach(results.titles) { hit in
                        NavigationLink(value: PlayerRoute.video(id: hit.videoID)) {
                            TitleRow(hit: hit, poster: poster(hit.videoID))
                        }
                        .accessibilityIdentifier("search.title.\(hit.videoID)")
                        .listRowBackground(Color.clear)
                    }
                    if results.titlesNext != nil {
                        MoreRow(paging: paging, id: "search.titles.more", action: moreTitles)
                    }
                } header: {
                    SearchHeading(text: Text("Titles", tableName: "Search", comment: "Search results whose title matches"))
                        .accessibilityIdentifier("search.titles")
                }
            }
            if !results.transcripts.isEmpty {
                Section {
                    ForEach(results.transcripts) { hit in
                        NavigationLink(value: PlayerRoute.video(id: hit.videoID)) {
                            TranscriptHeader(hit: hit, poster: poster(hit.videoID))
                        }
                        .accessibilityIdentifier("search.transcript.\(hit.videoID)")
                        .listRowBackground(Color.clear)
                        ForEach(Array(hit.passages.enumerated()), id: \.element.id) { i, p in
                            NavigationLink(value: PlayerRoute.watch(videoID: hit.videoID, at: p.startSec)) {
                                PassageRow(passage: p)
                            }
                            .accessibilityIdentifier("search.passage.\(hit.videoID).\(i)")
                            .listRowBackground(Color.clear)
                        }
                    }
                    if results.transcriptsNext != nil {
                        MoreRow(paging: paging, id: "search.transcripts.more", action: moreTranscripts)
                    }
                } header: {
                    SearchHeading(text: Text("In the transcript", tableName: "Search",
                                             comment: "Search results whose transcript matches"))
                        .accessibilityIdentifier("search.transcripts")
                }
            }
        }
        .listStyle(.plain)
        .accessibilityIdentifier("search.results")
    }

    /// `/api/videos/{id}/poster?size=small` on this home (the image source adds the token).
    private func poster(_ id: String) -> URL {
        home.appending(path: "api/videos/\(id)/poster").appending(queryItems: [URLQueryItem(name: "size", value: "small")])
    }
}

private struct MoreRow: View {
    let paging: Bool
    let id: String
    let action: () -> Void

    var body: some View {
        HStack {
            Spacer()
            if paging {
                ProgressView()
            } else {
                Button(action: action) {
                    Text("Show more", tableName: "Search", comment: "Loads the next page of a group of search results")
                }
                .buttonStyle(.borderless)
                .accessibilityIdentifier(id)
            }
            Spacer()
        }
        .listRowBackground(Color.clear)
    }
}

/// A result's poster, the brand's colour with the title when it has none.
private struct Thumb: View {
    let url: URL
    let title: String
    @ScaledMetric(relativeTo: .body) private var width: CGFloat = 112

    var body: some View {
        PosterArt(url: url, title: title)
            .frame(width: min(width, 168))
            .aspectRatio(16 / 9, contentMode: .fit)
            .accessibilityHidden(true)
    }
}

/// "Lectures · 55:05": the library and the length, what there is of them.
private func details(_ hit: SearchVideo) -> String {
    var parts: [String] = []
    if let l = hit.libraryName, !l.isEmpty { parts.append(l) }
    if let d = hit.durationSec, d > 0 { parts.append(clock(d)) }
    return parts.joined(separator: " · ")
}

/// A time as a clock: 27:02, 1:02:03.
private func clock(_ seconds: Double) -> String {
    Duration.seconds(seconds).formatted(.time(pattern: seconds >= 3600 ? .hourMinuteSecond : .minuteSecond))
}

/// The home's snippet as one text, the query's words marked: one run for the whole snippet,
/// so an Arabic passage keeps the space between a plain word and a marked one.
private func marked(_ snippet: String) -> AttributedString {
    var out = AttributedString()
    for run in SearchSnippet.runs(snippet) {
        var part = AttributedString(run.text)
        if run.marked {
            part.foregroundColor = Color.mktBrassText
            part.inlinePresentationIntent = .stronglyEmphasized
        }
        out += part
    }
    return out
}

/// A video of Titles: poster, the title with the query's words marked, its library and length.
/// One VoiceOver element.
private struct TitleRow: View {
    let hit: SearchVideo
    let poster: URL

    var body: some View {
        let sub = details(hit)
        let title = hit.titleSnippet.flatMap { $0.isEmpty ? nil : $0 } ?? hit.title
        HStack(spacing: MaktabaTokens.Spacing.s3) {
            Thumb(url: poster, title: hit.title)
            VStack(alignment: .leading, spacing: 2) {
                Text(marked(title))
                    .font(.maktabaBody(15, weight: .medium, relativeTo: .subheadline))
                    .foregroundStyle(Color.mktText)
                if !sub.isEmpty {
                    Text(verbatim: sub)
                        .font(.maktabaBody(13, relativeTo: .caption))
                        .foregroundStyle(Color.mktMuted)
                }
            }
            .fixedSize(horizontal: false, vertical: true)
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(Text(verbatim: [hit.title.isEmpty ? SearchSnippet.plain(title) : hit.title, sub]
                .filter { !$0.isEmpty }.joined(separator: ", ")))
    }
}

/// A video of In the transcript: poster, title, its library, length and how many lines matched.
private struct TranscriptHeader: View {
    let hit: SearchVideo
    let poster: URL

    var body: some View {
        let sub = details(hit)
        let matches = Text("Matches: \(hit.matches)", tableName: "Search", comment: "How many transcript lines matched; a number")
        HStack(spacing: MaktabaTokens.Spacing.s3) {
            Thumb(url: poster, title: hit.title)
            VStack(alignment: .leading, spacing: 2) {
                Text(verbatim: hit.title)
                    .font(.maktabaBody(15, weight: .medium, relativeTo: .subheadline))
                    .foregroundStyle(Color.mktText)
                Group {
                    if sub.isEmpty { matches } else { Text(verbatim: sub + " · ") + matches }
                }
                .font(.maktabaBody(13, relativeTo: .caption))
                .foregroundStyle(Color.mktMuted)
            }
            .fixedSize(horizontal: false, vertical: true)
        }
        .accessibilityElement(children: .combine)
    }
}

/// A matching line: its time and the passage, the query's words marked. It plays from the time.
private struct PassageRow: View {
    let passage: Passage

    var body: some View {
        let time = clock(passage.startSec)
        HStack(alignment: .firstTextBaseline, spacing: MaktabaTokens.Spacing.s2) {
            Label { Text(verbatim: time) } icon: { Image(systemName: "play.fill") }
                .labelStyle(.titleAndIcon)
                .font(.maktabaBody(13, weight: .medium, relativeTo: .caption))
                .foregroundStyle(Color.mktBrassText)
                .padding(.horizontal, 6)
                .padding(.vertical, 2)
                .background(Color.mktRaised, in: RoundedRectangle(cornerRadius: MaktabaTokens.Radius.sm))
                // a time reads left to right in Arabic too
                .environment(\.layoutDirection, .leftToRight)
            Text(marked(passage.snippet ?? ""))
                .font(.maktabaBody(15, relativeTo: .body))
                .foregroundStyle(Color.mktText)
                .fixedSize(horizontal: false, vertical: true)
        }
        .padding(.leading, MaktabaTokens.Spacing.s6)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(Text("Play from \(time)", tableName: "Search", comment: "A transcript passage plays from this time")
            + Text(verbatim: ": " + SearchSnippet.plain(passage.snippet ?? "")))
    }
}
