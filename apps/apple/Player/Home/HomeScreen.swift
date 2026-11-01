import MaktabaKit
import SwiftUI

/// Home (FR-10 to FR-13): the hero, then the rows in FR-10's order (an empty one is absent).
/// A card opens its video's page (a series group its series); its context menu (a long press,
/// a secondary click on the Mac) has Resume on a Continue Watching card (the player at the
/// person's position) and Not interested on a recommendation. Pull to refresh (⌘R on the
/// Mac) reads Home again; what was read last shows at once, on a new launch too (FR-12). A
/// home with nothing the person may watch says so, with Switch home and Sign out (FR-13).
struct HomeScreen: View {
    let context: PlayerContext

    @State private var model: HomeModel
    /// A route opened by a button rather than a link: the player from the hero's Play or a
    /// card's Resume, a pinned library from its row's See all.
    @State private var pushed: PlayerRoute?
    /// Not interested could not reach the home: a line says so for a moment.
    @State private var dismissFailed = false
    @Environment(\.horizontalSizeClass) private var sizeClass

    init(context: PlayerContext) {
        self.context = context
        let session = context.session
        _model = State(initialValue: HomeModel(
            client: context.client,
            cache: HomeCache.of(home: context.client.home, user: context.user.id),
            signedOut: { session.signedOut() }))
    }

    var body: some View {
        content
            .maktabaSurface()
            .navigationTitle(Text(PlayerSection.home.title))
            #if os(iOS)
            .toolbarVisibility(.hidden, for: .navigationBar)
            #endif
            .toolbar {
                #if os(macOS)
                ToolbarItem {
                    Button {
                        Task { await model.load() }
                    } label: {
                        Label { Text("Refresh", tableName: "Home", comment: "Reads Home again (⌘R on the Mac)") }
                            icon: { Image(systemName: "arrow.clockwise") }
                    }
                    .keyboardShortcut("r", modifiers: .command)
                    .accessibilityIdentifier("home.refresh")
                }
                #endif
            }
            .navigationDestination(item: $pushed) { route in
                switch route {
                case let .video(id): VideoScreen(context: context, id: id)
                case let .series(id): SeriesScreen(context: context, id: id)
                case let .library(id): LibraryScreen(context: context, id: id)
                case let .channel(id): ChannelScreen(context: context, id: id)
                case let .watch(id, at): WatchScreen(context: context, videoID: id, at: at)
                }
            }
            .overlay(alignment: .bottom) { if dismissFailed { banner } }
            .task { await model.open() }
            .accessibilityElement(children: .contain)
            .accessibilityIdentifier("screen.home")
    }

    @ViewBuilder private var content: some View {
        if let problem = model.problem, model.content == nil {
            HomeErrorView(problem, video: false, actions: [.tryAgain, .chooseAnotherHome]) { action in
                switch action {
                case .chooseAnotherHome: context.session.switchHome()
                case .signInAgain: context.session.signOut()
                default: Task { await model.load() }
                }
            }
            .padding(MaktabaTokens.Spacing.s5)
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        } else if let content = model.content {
            if model.isEmpty {
                EmptyHome(admin: context.user.isAdmin, switchHome: context.session.switchHome,
                          signOut: context.session.signOut)
            } else {
                rows(content)
            }
        } else {
            loading
        }
    }

    private func rows(_ content: HomeContent) -> some View {
        ScrollView {
            LazyVStack(alignment: .leading, spacing: MaktabaTokens.Spacing.s6) {
                if model.stale {
                    Text("Couldn't refresh — showing the last rows.", tableName: "Home",
                         comment: "Home's refresh failed; the rows shown are the last ones read")
                        .font(.maktabaBody(14, relativeTo: .footnote))
                        .foregroundStyle(Color.mktMuted)
                        .padding(.horizontal, MaktabaTokens.Spacing.s4)
                        .padding(.top, MaktabaTokens.Spacing.s4)
                        .accessibilityIdentifier("home.stale")
                }
                if let hero = content.hero { heroView(hero) }
                ForEach(model.sections) { section in
                    switch section {
                    case let .cards(row): cardsRow(row)
                    case let .live(channels): liveRow(channels)
                    }
                }
            }
            .padding(.bottom, MaktabaTokens.Spacing.s8)
        }
        .refreshable { await model.load() }
    }

    // MARK: the hero

    private func heroView(_ hero: HomeHero) -> some View {
        VStack(alignment: .leading, spacing: MaktabaTokens.Spacing.s3) {
            Hero(title: heroTitle(hero), eyebrow: heroEyebrow(hero), detail: heroDetail(hero),
                 backdrop: poster(hero.videoID, small: false), progress: hero.progress) {
                pushed = .watch(videoID: hero.videoID)
            }
            NavigationLink(value: hero.details) {
                Label { Text("Details", tableName: "Home", comment: "The hero's button: open the video's or series' page") }
                    icon: { Image(systemName: "info.circle") }
                    .font(.maktabaBody(16, weight: .semibold, relativeTo: .body))
                    .foregroundStyle(Color.mktText)
                    .padding(.horizontal, MaktabaTokens.Spacing.s5)
                    .padding(.vertical, MaktabaTokens.Spacing.s3)
                    .frame(minHeight: 44)
                    .background(Capsule().strokeBorder(Color.mktBorder))
                    .contentShape(Capsule())
            }
            .buttonStyle(.plain)
            .padding(.horizontal, MaktabaTokens.Spacing.s4)
            .accessibilityIdentifier("hero.details")
        }
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("home.hero")
    }

    private func heroTitle(_ hero: HomeHero) -> String {
        guard hero.isSeries else { return hero.videoTitle }
        return hero.seriesName ?? String(localized: "Untitled series", table: "Home", comment: "A series with no name")
    }

    private func heroEyebrow(_ hero: HomeHero) -> Text {
        hero.picked
            ? Text("Featured", tableName: "Home", comment: "The hero's line above the title: an admin picked it")
            : Text("New series", tableName: "Home", comment: "The hero's line above the title: the newest series")
    }

    private func heroDetail(_ hero: HomeHero) -> String? {
        if hero.isSeries, let n = hero.episodes, n > 0 {
            return String(localized: "\(n) lessons", table: "Home", comment: "The hero's detail: how many lessons the series has")
        }
        let duration = hero.durationSec.flatMap { $0 > 0 ? $0 : nil }.map {
            Duration.seconds($0).formatted(.units(allowed: [.hours, .minutes], width: .abbreviated))
        }
        let parts = [hero.seriesName, duration].compactMap { $0 }
        return parts.isEmpty ? nil : parts.joined(separator: " · ")
    }

    // MARK: rows

    private var cardWidth: CGFloat { sizeClass == .regular ? 224 : 168 }

    private func cardsRow(_ row: HomeRow) -> some View {
        Rail(rowTitle(row.title), items: row.cards, seeAll: row.seeAll.map { route in { pushed = route } }) { card in
            cardView(card)
        }
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("home.row.\(row.key)")
    }

    private func rowTitle(_ title: HomeRowTitle) -> Text {
        switch title {
        case let .named(name): Text(verbatim: name)
        case .continueWatching: Text("Continue Watching", tableName: "Home", comment: "Home's row of videos the person stopped part way")
        case .onDeck: Text("On Deck", tableName: "Home", comment: "Home's row of each series' next lesson")
        case .favorites: Text("Favorites", tableName: "Home", comment: "Home's row of the person's favorite videos")
        case .recentlyAdded: Text("Recently Added", tableName: "Home", comment: "Home's row of the newest videos")
        case .recentlyWatched: Text("Recently Watched", tableName: "Home", comment: "Home's row of the videos watched last")
        case .forYou: Text("For You", tableName: "Home", comment: "Home's row of recommendations")
        case .fromLibrary: Text("From your library", tableName: "Home", comment: "Home's row of recommendations from the person's libraries")
        }
    }

    private func cardView(_ card: HomeCard) -> some View {
        NavigationLink(value: card.open) {
            PosterCard(title: cardTitle(card), detail: cardDetail(card), poster: poster(card.videoID, small: true),
                       progress: card.progress, width: cardWidth)
        }
        .buttonStyle(.plain)
        .contextMenu {
            if let at = card.resumeAt {
                Button {
                    pushed = .watch(videoID: card.videoID, at: at)
                } label: {
                    Label { Text("Resume", tableName: "Components", comment: "The hero's button: go on from where it stopped") }
                        icon: { Image(systemName: "play.fill") }
                }
                .accessibilityIdentifier("home.menu.resume")
            }
            if card.recommended {
                Button(role: .destructive) {
                    Task {
                        if !(await model.dismiss(card.videoID)) { showDismissFailed() }
                    }
                } label: {
                    Label { Text("Not interested", tableName: "Home", comment: "A recommendation's menu: show it no more") }
                        icon: { Image(systemName: "hand.thumbsdown") }
                }
                .accessibilityIdentifier("home.menu.notInterested")
            }
        }
        .accessibilityIdentifier("home.card.\(card.id)")
        .task(id: card.title == nil && card.newLessons == nil) {
            if card.title == nil && card.newLessons == nil { await model.resolve(card.videoID) }
        }
    }

    private func cardTitle(_ card: HomeCard) -> String {
        if let title = card.title { return title }
        return card.newLessons != nil
            ? String(localized: "Untitled series", table: "Home", comment: "A series with no name")
            : String(localized: "Loading…", table: "Components", comment: "A placeholder while a picture or a row loads")
    }

    private func cardDetail(_ card: HomeCard) -> String? {
        if let n = card.newLessons {
            return String(localized: "\(n) new lessons", table: "Home", comment: "A Recently Added series: how many lessons are new")
        }
        return card.subtitle
    }

    /// Live TV on now: each channel's programme, how far through it is, and the channel; a tap
    /// opens the channel.
    private func liveRow(_ channels: [ChannelNow]) -> some View {
        Rail(Text("What's On Now", tableName: "Home", comment: "Home's row of what each Live TV channel shows now"),
             items: channels) { entry in
            let current = entry.current.flatMap { $0.kind == "filler" ? nil : $0 }
            let title = current.flatMap { $0.title.trimmingCharacters(in: .whitespaces).isEmpty ? nil : $0.title }
                ?? String(localized: "No content right now", table: "Home", comment: "A channel with nothing scheduled now")
            NavigationLink(value: PlayerRoute.channel(id: entry.channel.id)) {
                PosterCard(title: title, detail: entry.channel.name,
                           poster: picture(current?.poster ?? entry.channel.logoPath ?? entry.channel.logo),
                           progress: current.flatMap { $0.progress > 0 ? $0.progress : nil }, width: cardWidth)
            }
            .buttonStyle(.plain)
            .accessibilityIdentifier("home.channel.\(entry.channel.id)")
        }
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("home.row.\(HomeRow.live)")
    }

    // MARK: pictures

    /// A video's poster on the home; `small` asks for the pipeline's small copy.
    private func poster(_ video: String, small: Bool) -> URL {
        let url = context.client.home.appending(path: "api/videos/\(video)/poster")
        return small ? url.appending(queryItems: [URLQueryItem(name: "size", value: "small")]) : url
    }

    /// A picture's address: a path on the home (`/api/…`), or a URL as given.
    private func picture(_ path: String?) -> URL? {
        guard let path, !path.isEmpty else { return nil }
        if path.hasPrefix("http://") || path.hasPrefix("https://") { return URL(string: path) }
        return context.client.home.appending(path: String(path.drop { $0 == "/" }))
    }

    // MARK: loading, not interested

    /// The first read, nothing kept: the hero's and two rows' shapes (after 0.4 s).
    private var loading: some View {
        DelayedPlaceholder {
            VStack(alignment: .leading, spacing: MaktabaTokens.Spacing.s6) {
                Shimmer(cornerRadius: 0).aspectRatio(16 / 9, contentMode: .fit).frame(maxHeight: 420)
                ForEach(0..<2, id: \.self) { _ in
                    HStack(spacing: MaktabaTokens.Spacing.s3) {
                        ForEach(0..<3, id: \.self) { _ in
                            Shimmer().frame(width: cardWidth).aspectRatio(16 / 9, contentMode: .fit)
                        }
                    }
                    .padding(.horizontal, MaktabaTokens.Spacing.s4)
                }
                Spacer(minLength: 0)
            }
            .accessibilityElement(children: .combine)
        }
        .accessibilityIdentifier("home.loading")
    }

    private var banner: some View {
        Text("Could not tell the home. Try again.", tableName: "Home",
             comment: "Not interested did not reach the home; the card is back")
            .font(.maktabaBody(15, relativeTo: .subheadline))
            .foregroundStyle(Color.mktText)
            .padding(.horizontal, MaktabaTokens.Spacing.s4)
            .padding(.vertical, MaktabaTokens.Spacing.s3)
            .background(Capsule().fill(Color.mktRaised))
            .overlay(Capsule().strokeBorder(Color.mktBorder, lineWidth: 0.5))
            .padding(MaktabaTokens.Spacing.s4)
            .accessibilityIdentifier("home.dismissFailed")
    }

    private func showDismissFailed() {
        dismissFailed = true
        Task {
            try? await Task.sleep(for: .seconds(4))
            dismissFailed = false
        }
    }
}

/// FR-13: nothing the person may watch on this home: says so, with Switch home and Sign out.
private struct EmptyHome: View {
    let admin: Bool
    let switchHome: () -> Void
    let signOut: () -> Void

    var body: some View {
        ScrollView {
            VStack(spacing: MaktabaTokens.Spacing.s4) {
                Image(systemName: "rectangle.stack")
                    .font(.system(size: 44))
                    .foregroundStyle(Color.mktMuted)
                    .accessibilityHidden(true)
                Text("Nothing to watch yet", tableName: "Home", comment: "Home with no video the person may watch: the title")
                    .font(.maktabaHeading(MaktabaTokens.TypeSize.h2, relativeTo: .title2))
                    .foregroundStyle(Color.mktText)
                    .accessibilityAddTraits(.isHeader)
                    .accessibilityIdentifier("home.empty.title")
                Group {
                    if admin {
                        Text("Add a library under Manage libraries on the home's web page and scan it; its videos then show here.",
                             tableName: "Home", comment: "Home with no video, said to an admin")
                    } else {
                        Text("Open Libraries to see the videos you may watch. Pin the libraries you watch most to see them here.",
                             tableName: "Home", comment: "Home with no video, said to a viewer")
                    }
                }
                .font(.maktabaBody(16, relativeTo: .body))
                .foregroundStyle(Color.mktMuted)
                .frame(maxWidth: 480)
                ViewThatFits(in: .horizontal) {
                    HStack(spacing: MaktabaTokens.Spacing.s3) { buttons }
                    VStack(spacing: MaktabaTokens.Spacing.s3) { buttons }
                }
                .padding(.top, MaktabaTokens.Spacing.s2)
            }
            .multilineTextAlignment(.center)
            .fixedSize(horizontal: false, vertical: true)
            .padding(MaktabaTokens.Spacing.s6)
            .frame(maxWidth: .infinity)
        }
        .defaultScrollAnchor(.center)
    }

    @ViewBuilder private var buttons: some View {
        Button(action: switchHome) {
            Text("Switch home", tableName: "Home", comment: "Choose another home")
        }
        .buttonStyle(BrassButtonStyle())
        .accessibilityIdentifier("home.switchHome")
        Button(action: signOut) {
            Text("Sign out", tableName: "Home", comment: "Sign out of this home")
                .font(.maktabaBody(16, weight: .semibold, relativeTo: .body))
                .foregroundStyle(Color.mktText)
                .padding(.horizontal, MaktabaTokens.Spacing.s5)
                .padding(.vertical, MaktabaTokens.Spacing.s3)
                .frame(minHeight: 44)
                .background(Capsule().strokeBorder(Color.mktBorder))
                .contentShape(Capsule())
        }
        .buttonStyle(.plain)
        .accessibilityIdentifier("home.signOut")
    }
}
