import MaktabaKit
import SwiftUI

/// Search (Epic 48, FR-19 and FR-20): the system's search field (`.searchable`) with the
/// person's own past searches as suggestions while typing, and their recent searches while
/// the field is empty. Submit asks the home (`POST /api/search`, hybrid) and shows **Titles**
/// and **In the transcript**: a title opens the video's page, a transcript passage opens the
/// player at its time. The query goes as typed, Arabic with its diacritics too.
///
/// Story 49.11 brings the search help of story 49.8, as the web has it (49.10): each suggestion
/// shows its kind, and a title opens its video; "Did you mean …?" runs the corrected words;
/// **Filters** opens a sheet of the facets with their counts (library, language, speaker, year,
/// length), several combinable; the filters in use are listed, each removable, with Clear all;
/// and the order is Relevance, Newest or Most matches.
///
/// Both a search and a result's page change the screen under the field, so both wait for the
/// field to let go of the keyboard first (`LetGoFirst`, Trail #1419 and #1425); so does every
/// refinement.
///
/// Story 49.14 adds AI mode, a second tab beside **Results** (`AIModePane`): the home's language
/// model answers a question from the library's passages, each sentence citing the moment it
/// comes from. It runs only when the tab is opened or a question is asked from it; Results
/// never waits on it. Leaving the tab, asking again or leaving Search closes its stream.
struct SearchScreen: View {
    let context: PlayerContext

    @State private var model: SearchModel
    @FocusState private var typing: Bool
    /// A search, or a result's page, held until the field has let go of the keyboard (see
    /// `submit` and `open`).
    @State private var letGo = LetGoFirst<SearchChange>()
    /// The stack this screen is in, to push a result's page once the field has let go.
    @Environment(\.playerPush) private var push
    /// A result's page, where no stack takes a push (`PlayerPush` is nil: a linked page's sheet).
    @State private var pushed: PlayerRoute?
    /// The facets' sheet is open.
    @State private var filtering = false
    /// A title suggestion was chosen: the field's own submit that follows a suggestion's tap
    /// is not a search (it would replace the held page with a search of what was typed).
    @State private var titleChosen = false
    /// Results or AI mode.
    @State private var tab = SearchTab.results
    /// AI mode's conversation (story 49.14).
    @State private var ai: AIModeModel

    /// A query sent as the screen opens (a plugin panel's chip, `PlayerRoute.search`).
    private let initial: String?

    init(context: PlayerContext, query: String? = nil) {
        self.context = context
        self.initial = query
        let session = context.session
        _model = State(initialValue: SearchModel(client: context.client,
                                                 byMeaning: { session.capabilities.current.isOn(.semanticSearch) }) { session.signedOut() })
        _ai = State(initialValue: AIModeModel(client: context.client) { session.signedOut() })
    }

    private var caps: Capabilities { context.session.capabilities.current }

    var body: some View {
        @Bindable var model = model
        Group {
            if tab == .ai {
                AIModePane(ai: ai, typed: model.query, home: context.client.home, isAdmin: caps.isAdmin,
                           open: open, ask: { hold(.ask($0)) }, followUp: { hold(.followUp($0)) })
            } else {
                content
            }
        }
            // Results and AI mode (49.14), over the screen under the field
            .safeAreaInset(edge: .top, spacing: 0) {
                Picker(selection: $tab) {
                    Text(verbatim: AIWords.results()).tag(SearchTab.results)
                    Text(verbatim: AIWords.aiMode()).tag(SearchTab.ai)
                } label: {
                    Text(verbatim: AIWords.aiMode())
                }
                .pickerStyle(.segmented)
                .labelsHidden()
                .padding(.horizontal, MaktabaTokens.Spacing.s4)
                .padding(.vertical, MaktabaTokens.Spacing.s2)
                .background(Color.mktBackground)
                .accessibilityIdentifier("search.tabs")
            }
            // search by meaning off: words only, and an admin reads so under the field (51.11)
            .safeAreaInset(edge: .top, spacing: 0) {
                if caps.wordsOnlyLine, tab == .results {
                    Text(verbatim: FeatureWords.wordsOnly)
                        .font(.maktabaBody(13, relativeTo: .footnote))
                        .foregroundStyle(Color.mktMuted)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .padding(.horizontal, MaktabaTokens.Spacing.s4)
                        .padding(.vertical, MaktabaTokens.Spacing.s2)
                        .background(Color.mktBackground)
                        .accessibilityIdentifier("search.wordsOnly")
                }
            }
            .accessibilityElement(children: .contain)
            .accessibilityIdentifier("screen.search")
            .task {
                if let initial, model.submitted == nil { submit(initial) }
            }
            .maktabaSurface()
            .navigationTitle(Text(PlayerSection.search.title))
            .searchable(text: $model.query, placement: Self.placement,
                        prompt: Text("Search titles and transcripts", tableName: "Search", comment: "The search field's placeholder"))
            .searchFocused($typing)
            .searchSuggestions {
                ForEach(model.suggestions) { s in
                    Button {
                        choose(s)
                    } label: {
                        SuggestionRow(suggestion: s)
                    }
                    .accessibilityIdentifier("search.suggestion.\(s.kind?.rawValue ?? "plain")")
                }
            }
            .onSubmit(of: .search) {
                if titleChosen {
                    titleChosen = false
                    return
                }
                submit(model.query)
            }
            .onChange(of: tab) { _, now in tabChanged(now) }
            // leaving Search closes AI mode's stream
            .onDisappear { ai.cancel() }
            // the field let go: the search or the page held for it goes now
            .onChange(of: typing) { _, now in
                if let change = letGo.focusChanged(now) { perform(change) }
            }
            .navigationDestination(item: $pushed) { PlayerDestination(context: context, route: $0) }
            .sheet(isPresented: $filtering) {
                FilterSheet(model: model) { hold(.refine($0)) }
            }
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
        } else if model.searching, model.results?.sent != model.submitted {
            ProgressView().frame(maxWidth: .infinity, maxHeight: .infinity)
                .accessibilityIdentifier("search.loading")
        } else if let problem = model.problem {
            HomeErrorView(problem, video: false) { _ in Task { await model.retry() } }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
        } else if let results = model.results {
            if results.isEmpty, results.didYouMean == nil, model.filters.isEmpty {
                ContentUnavailableView {
                    Label { Text("Nothing matches “\(results.query)”.", tableName: "Search", comment: "A search found nothing; the query")
                    } icon: { Image(systemName: "magnifyingglass") }
                }
                .accessibilityIdentifier("search.none")
            } else {
                ResultsList(results: results, model: model, home: context.client.home, byMeaning: caps.isOn(.semanticSearch),
                            open: open, refine: { hold(.refine($0)) }, showFilters: { hold(.filters) },
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

    /// A suggestion chosen: a title opens its video, any other is searched as its words.
    private func choose(_ s: SearchSuggestion) {
        if s.kind == .title, let id = s.videoID {
            titleChosen = true
            open(.video(id: id))
            // the submit comes with the tap, if at all: a later one is a search again
            Task {
                try? await Task.sleep(for: .seconds(1))
                titleChosen = false
            }
        } else {
            submit(s.text)
        }
    }

    private func submit(_ q: String) {
        guard !SearchModel.sendable(q).isEmpty else { return }
        // the field holds the chosen text at once: the field's own submit, which can follow
        // a suggestion's tap, then sends that too rather than what was typed so far
        model.query = q
        // in AI mode, the field asks a new question
        if tab == .ai { return hold(.ask(q)) }
        // The keyboard is let go of first, and the search runs once the field has let go
        // (`onChange(of: typing)`). Searching swaps the screen under the field (the suggestions
        // go, then a spinner, then the results). In the same update as the field letting go,
        // the keyboard's hiding could be cut off and never finish, and the app was never idle
        // again: every later XCUITest wait ran its full 60 s.
        hold(.search(q))
    }

    /// A result's page (a title, a video of In the transcript, a passage's player). Pushed only
    /// once the field has let go: someone who went back to the field to refine, then tapped a
    /// result, would otherwise push the page with the keyboard still up, its hiding cut off. A
    /// `Button` and not a `NavigationLink(value:)`: nothing can come before a link's own push,
    /// and a tap gesture beside the link took the link's tap on an iPhone (Trail #1425).
    private func open(_ route: PlayerRoute) {
        hold(.open(route))
    }

    /// AI mode opened: what the field holds is asked, unless it is the question on screen (a
    /// question stopped by leaving the tab is asked again). Back on Results: AI mode's stream
    /// closes, and what the field holds is searched when it is not the search on screen.
    private func tabChanged(_ now: SearchTab) {
        switch now {
        case .ai:
            let q = SearchModel.sendable(model.query)
            if !q.isEmpty, q != ai.question {
                hold(.ask(q))
            } else if let last = ai.turns.last, last.state == .stopped(code: AIModeModel.left) {
                ai.askAgain()
            }
        case .results:
            ai.cancel()
            let q = SearchModel.sendable(model.query)
            if !q.isEmpty, q != model.submitted { hold(.search(q)) }
        }
    }

    /// Runs `change` now when the field holds no keyboard; otherwise drops the focus, and
    /// `onChange(of: typing)` runs it once the field has let go.
    private func hold(_ change: SearchChange) {
        if let now = letGo.begin(change, focused: typing) { perform(now) } else { typing = false }
    }

    private func perform(_ change: SearchChange) {
        switch change {
        case let .search(q): Task { await model.submit(q) }
        case let .refine(ask):
            // the did-you-mean's words show in the field
            if ask.query != model.query { model.query = ask.query }
            Task { await model.run(ask) }
        case .filters: filtering = true
        case let .ask(q): ai.ask(q)
        case let .followUp(q): ai.followUp(q)
        case let .open(route): if let push { push(route) } else { pushed = route }
        }
    }
}

/// What the search screen changes once the field has let go of the keyboard: a search to run,
/// a refinement of it (an order, a filter, the did-you-mean), the facets' sheet to open, or a
/// result's page to open. One held at a time, the latest (`LetGoFirst`).
enum SearchChange: Equatable {
    case search(String)
    case refine(SearchModel.Ask)
    case filters
    case open(PlayerRoute)
    /// AI mode: a new question, or a follow-up in the conversation (49.14).
    case ask(String)
    case followUp(String)
}

/// Search's two tabs (49.14): the results, and AI mode.
enum SearchTab: Hashable {
    case results, ai
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

/// The search help over the results (story 49.11), then Titles, then In the transcript, each
/// with Show more while the home kept more.
private struct ResultsList: View {
    let results: SearchModel.Results
    let model: SearchModel
    let home: URL
    /// Search by meaning is on: words-only results while it is off are no failure to report.
    let byMeaning: Bool
    /// Opens a result's page (`SearchScreen.open`: after the field lets go).
    let open: (PlayerRoute) -> Void
    /// Runs a refinement (after the field lets go).
    let refine: (SearchModel.Ask) -> Void
    /// Opens the facets' sheet (after the field lets go).
    let showFilters: () -> Void
    let moreTitles: () -> Void
    let moreTranscripts: () -> Void

    private var paging: Bool { model.paging }

    var body: some View {
        List {
            if let dym = results.didYouMean {
                DidYouMeanRow(words: dym) { refine(model.ask(didYouMean: dym)) }
                    .listRowBackground(Color.clear)
            }
            RefineBar(sort: model.sort, inUse: model.filters.count, canFilter: !model.shownFacets.isEmpty,
                      refining: model.refining, sortBy: { s in model.ask(sort: s).map(refine) }, showFilters: showFilters)
                .listRowBackground(Color.clear)
            if !model.filters.isEmpty {
                FiltersInUse(model: model, refine: refine)
                    .listRowBackground(Color.clear)
            }
            if results.isEmpty {
                Text("Nothing matches “\(results.query)”.", tableName: "Search", comment: "A search found nothing; the query")
                    .font(.maktabaBody(15, relativeTo: .body))
                    .foregroundStyle(Color.mktMuted)
                    .frame(maxWidth: .infinity)
                    .multilineTextAlignment(.center)
                    .padding(.vertical, MaktabaTokens.Spacing.s6)
                    .listRowBackground(Color.clear)
                    .listRowSeparator(.hidden)
                    .accessibilityIdentifier("search.none")
            }
            if results.degraded, byMeaning {
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
                        ResultLink(route: .video(id: hit.videoID), open: open) {
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
                        ResultLink(route: .video(id: hit.videoID), open: open) {
                            TranscriptHeader(hit: hit, poster: poster(hit.videoID))
                        }
                        .accessibilityIdentifier("search.transcript.\(hit.videoID)")
                        .listRowBackground(Color.clear)
                        ForEach(Array(hit.passages.enumerated()), id: \.element.id) { i, p in
                            ResultLink(route: .watch(videoID: hit.videoID, at: p.startSec), open: open) {
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

/// A result's row: a button that opens `route` through `open`, so the screen lets the field go
/// before the push. It looks as a link's row did: the whole row taps, with the chevron on an
/// iPhone and an iPad; VoiceOver reads the row's own label and activates it as a button.
private struct ResultLink<Label: View>: View {
    let route: PlayerRoute
    let open: (PlayerRoute) -> Void
    @ViewBuilder let label: Label

    var body: some View {
        Button {
            open(route)
        } label: {
            HStack(spacing: MaktabaTokens.Spacing.s2) {
                label.frame(maxWidth: .infinity, alignment: .leading)
                #if os(iOS)
                Image(systemName: "chevron.forward")
                    .font(.maktabaBody(13, weight: .semibold, relativeTo: .footnote))
                    .foregroundStyle(Color.mktMuted)
                    .accessibilityHidden(true)
                #endif
            }
            .contentShape(Rectangle())
        }
        #if os(macOS)
        // a Mac's button style would draw a push button around the row
        .buttonStyle(.plain)
        #endif
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

/// A result's poster, the brand's colour with the title when it has none: 16:9 at a width that
/// follows the text size. Both sides are set: a frame of the width alone let a loaded poster
/// (`scaledToFill`) take its own picture's height, 214 pt on an iPad at every text size, so the
/// row was the picture's height and not its texts'.
private struct Thumb: View {
    let url: URL
    let title: String
    @ScaledMetric(relativeTo: .body) private var width: CGFloat = 112

    var body: some View {
        let w = min(width, 168)
        PosterArt(url: url, title: title)
            .frame(width: w, height: w * 9 / 16)
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

// MARK: Search help (story 49.11)

/// A suggestion: an icon and a word for its kind (an earlier search, a word, a video, a
/// speaker), none from a home before story 49.8.
private struct SuggestionRow: View {
    let suggestion: SearchSuggestion

    var body: some View {
        Label {
            HStack(spacing: MaktabaTokens.Spacing.s2) {
                Text(verbatim: suggestion.text)
                Spacer(minLength: MaktabaTokens.Spacing.s2)
                if let kind = suggestion.kind {
                    Text(verbatim: SearchWords.kind(kind))
                        .font(.maktabaBody(13, relativeTo: .caption))
                        .foregroundStyle(Color.mktMuted)
                }
            }
        } icon: {
            Image(systemName: Self.icon(suggestion.kind))
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

/// "Did you mean المدرسة?": the corrected words marked where the sentence puts them; the row
/// runs them with the order and the filters kept.
private struct DidYouMeanRow: View {
    let words: String
    let run: () -> Void

    var body: some View {
        let parts = SearchWords.didYouMeanParts()
        Button(action: run) {
            (Text(verbatim: parts.before)
                + Text(verbatim: "\u{2068}\(words)\u{2069}").fontWeight(.semibold).foregroundStyle(Color.mktBrassText).underline()
                + Text(verbatim: parts.after))
                .font(.maktabaBody(15, relativeTo: .body))
                .foregroundStyle(Color.mktText)
                .frame(maxWidth: .infinity, minHeight: 44, alignment: .leading)
                .contentShape(Rectangle())
        }
        .buttonStyle(.borderless)
        .accessibilityLabel(Text(verbatim: SearchWords.didYouMean(words)))
        .accessibilityIdentifier("search.didYouMean")
    }
}

/// Filters (with how many are in use) and the order: Relevance, Newest, Most matches.
private struct RefineBar: View {
    let sort: SearchSort
    let inUse: Int
    /// There are facets to narrow by.
    let canFilter: Bool
    let refining: Bool
    let sortBy: (SearchSort) -> Void
    let showFilters: () -> Void

    var body: some View {
        // one row; a label too long for its share wraps rather than being cut (no
        // ViewThatFits: on an iPhone in Arabic it went back and forth between its two layouts
        // and the app never went idle)
        HStack(spacing: MaktabaTokens.Spacing.s3) {
            filters
            Spacer(minLength: 0)
            if refining { progress }
            order
        }
        .font(.maktabaBody(15, weight: .medium, relativeTo: .body))
        .foregroundStyle(Color.mktBrassText)
    }

    private var progress: some View {
        ProgressView().controlSize(.small)
            .accessibilityIdentifier("search.refining")
    }

    @ViewBuilder private var filters: some View {
        if canFilter || inUse > 0 {
            Button(action: showFilters) {
                Label {
                    Text(verbatim: inUse > 0 ? "\(SearchWords.filters()) (\(SearchWords.number(inUse)))" : SearchWords.filters())
                } icon: {
                    Image(systemName: inUse > 0 ? "line.3.horizontal.decrease.circle.fill" : "line.3.horizontal.decrease.circle")
                }
                .frame(minHeight: 44)
                .contentShape(Rectangle())
            }
            .buttonStyle(.borderless)
            .accessibilityIdentifier("search.filters")
        }
    }

    private var order: some View {
        Menu {
            Picker(selection: Binding(get: { sort }, set: sortBy)) {
                ForEach(SearchSort.allCases, id: \.self) { s in
                    Text(verbatim: SearchWords.sort(s)).tag(s)
                }
            } label: {
                Text(verbatim: SearchWords.sortBy())
            }
        } label: {
            Label { Text(verbatim: SearchWords.sortedBy(sort)) } icon: { Image(systemName: "arrow.up.arrow.down") }
                .frame(minHeight: 44)
                .contentShape(Rectangle())
        }
        #if os(macOS)
        .menuStyle(.borderlessButton)
        .fixedSize()
        #else
        // its own button in the row: a tap on Filters does not open it
        .menuStyle(.button)
        .buttonStyle(.borderless)
        #endif
        .accessibilityIdentifier("search.sort")
    }
}

/// The filters in use, each a chip that takes it off, and Clear all filters.
private struct FiltersInUse: View {
    let model: SearchModel
    let refine: (SearchModel.Ask) -> Void

    var body: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: MaktabaTokens.Spacing.s2) {
                ForEach(Array(model.filters.inUse.enumerated()), id: \.offset) { _, f in
                    let name = SearchWords.inUse(f.facet, f.value, label: model.label(f.facet, f.value))
                    Button {
                        model.ask(removing: f.facet, f.value).map(refine)
                    } label: {
                        HStack(spacing: 6) {
                            Text(verbatim: name)
                            Image(systemName: "xmark")
                                .font(.maktabaBody(11, weight: .bold, relativeTo: .caption2))
                                .accessibilityHidden(true)
                        }
                        .font(.maktabaBody(15, weight: .medium, relativeTo: .body))
                        .foregroundStyle(Color.mktText)
                        .padding(.horizontal, MaktabaTokens.Spacing.s3)
                        .padding(.vertical, 6)
                        .background(Color.mktRaised, in: Capsule())
                        .frame(minHeight: 44)
                        .contentShape(Rectangle())
                    }
                    .buttonStyle(.borderless)
                    .accessibilityLabel(Text(verbatim: SearchWords.remove(name)))
                    .accessibilityIdentifier("search.filter.\(f.facet.rawValue).\(f.value)")
                }
                Button {
                    model.askClearingFilters().map(refine)
                } label: {
                    Text(verbatim: SearchWords.clearFilters())
                        .font(.maktabaBody(15, weight: .medium, relativeTo: .body))
                        .foregroundStyle(Color.mktBrassText)
                        .frame(minHeight: 44)
                        .contentShape(Rectangle())
                }
                .buttonStyle(.borderless)
                .accessibilityIdentifier("search.filters.clear")
            }
        }
        .accessibilityElement(children: .contain)
        .accessibilityLabel(Text(verbatim: SearchWords.filtersInUse()))
        .accessibilityIdentifier("search.filters.inUse")
    }
}

/// Narrow the results: each facet's buckets with their counts, a chosen one ticked; a press
/// takes it on or off and searches again at once, the sheet staying open on the new counts.
private struct FilterSheet: View {
    let model: SearchModel
    let run: (SearchModel.Ask) -> Void
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            List {
                ForEach(model.shownFacets) { group in
                    Section {
                        ForEach(group.buckets) { b in
                            bucketRow(b)
                        }
                    } header: {
                        SearchHeading(text: Text(verbatim: SearchWords.facet(group.facet)))
                    }
                }
            }
            .accessibilityIdentifier("search.facets")
            .navigationTitle(Text(verbatim: SearchWords.narrow()))
            #if os(iOS)
            .navigationBarTitleDisplayMode(.inline)
            #endif
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button { dismiss() } label: { Text(verbatim: SearchWords.done()) }
                        .accessibilityIdentifier("search.facets.done")
                }
                if !model.filters.isEmpty {
                    ToolbarItem(placement: .cancellationAction) {
                        Button { model.askClearingFilters().map(run) } label: { Text(verbatim: SearchWords.clearFilters()) }
                            .accessibilityIdentifier("search.facets.clear")
                    }
                }
            }
            .maktabaSurface()
        }
        #if os(iOS)
        .presentationDetents([.medium, .large])
        #else
        .frame(minWidth: 420, minHeight: 480)
        #endif
    }

    private func bucketRow(_ b: ShownBucket) -> some View {
        let name = SearchWords.bucket(b.facet, b.value, label: b.label)
        return Button {
            model.ask(toggling: b.facet, b.value).map(run)
        } label: {
            HStack(spacing: MaktabaTokens.Spacing.s3) {
                Image(systemName: b.chosen ? "checkmark.circle.fill" : "circle")
                    .foregroundStyle(b.chosen ? Color.mktBrassText : Color.mktMuted)
                Text(verbatim: name)
                    .font(.maktabaBody(17, relativeTo: .body))
                    .foregroundStyle(Color.mktText)
                    .fixedSize(horizontal: false, vertical: true)
                Spacer(minLength: MaktabaTokens.Spacing.s2)
                if let n = b.count {
                    Text(verbatim: SearchWords.number(n))
                        .font(.maktabaBody(15, relativeTo: .subheadline))
                        .foregroundStyle(Color.mktMuted)
                }
            }
            .frame(maxWidth: .infinity, minHeight: 44, alignment: .leading)
            .contentShape(Rectangle())
        }
        .buttonStyle(.borderless)
        .listRowBackground(Color.clear)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(Text(verbatim: b.count.map { "\(name), \(SearchWords.videos($0))" } ?? name))
        .accessibilityAddTraits(b.chosen ? [.isButton, .isSelected] : .isButton)
        .accessibilityIdentifier("search.facet.\(b.facet.rawValue).\(b.value)")
    }
}
