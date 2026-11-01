import MaktabaKit
import SwiftUI

/// What the Libraries root shows beside the page on a wide window (iPad, Mac, an open iPhone
/// Duo): a library, or the Series grid.
enum LibrariesChoice: Hashable {
    case library(String)
    case series
}

/// The Libraries section's first screen (FR-14, FR-16): two tabs, **Libraries** (a grid of
/// the library cards, art, name and count, the pinned first, each with Pin / Unpin) and
/// **Series** (every series the person may see, with progress).
///
/// On a phone a card pushes its page on the section's stack. On a wide window this screen is
/// the list beside the page: a card (or the Series tab) chooses what the page shows.
struct LibrariesScreen: View {
    let context: PlayerContext
    let model: LibrariesModel
    /// Wide: what the page beside shows (nil on a phone: cards push their pages).
    var choice: Binding<LibrariesChoice?>?

    @State private var tab = LibrariesTab.libraries

    private var client: APIClient { context.client }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: MaktabaTokens.Spacing.s4) {
                Picker(selection: $tab) {
                    Text("Libraries", tableName: "Libraries", comment: "The Libraries root's tab of library cards").tag(LibrariesTab.libraries)
                    Text("Series", tableName: "Libraries", comment: "The Libraries root's tab of every series").tag(LibrariesTab.series)
                } label: {
                    Text("Show", tableName: "Libraries", comment: "The Libraries root's tabs, spoken")
                }
                .pickerStyle(.segmented)
                .accessibilityIdentifier("libraries.tabs")
                switch tab {
                case .libraries: libraries
                case .series: series
                }
            }
            .padding(.horizontal, MaktabaTokens.Spacing.s4)
            .padding(.vertical, MaktabaTokens.Spacing.s3)
        }
        .maktabaSurface()
        .refreshable {
            if tab == .libraries { await model.loadLibraries() } else { await model.loadSeries() }
        }
        .task { if !model.librariesLoaded { await model.loadLibraries() } }
        .task(id: tab) { if tab == .series, !model.seriesLoaded { await model.loadSeries() } }
        .onChange(of: tab) { _, now in
            // wide: the page beside follows the tab
            guard let choice else { return }
            if now == .series {
                choice.wrappedValue = .series
            } else if choice.wrappedValue == .series {
                choice.wrappedValue = model.libraries.first.map { .library($0.id) }
            }
        }
        .navigationTitle(Text("Libraries", tableName: "Libraries", comment: "The Libraries section's title"))
        .accessibilityIdentifier("screen.libraries")
    }

    private var columns: [GridItem] {
        [GridItem(.adaptive(minimum: 150), spacing: MaktabaTokens.Spacing.s3, alignment: .top)]
    }

    @ViewBuilder private var libraries: some View {
        if let problem = model.problem, model.libraries.isEmpty {
            ProblemView(problem: problem) { Task { await model.loadLibraries() } }
        } else if !model.librariesLoaded {
            ProgressView().frame(maxWidth: .infinity, minHeight: 200)
        } else if model.libraries.isEmpty {
            ContentUnavailableView {
                Label { Text("No libraries yet", tableName: "Libraries", comment: "The Libraries grid with no library") } icon: {
                    Image(systemName: "books.vertical")
                }
            }
        } else {
            LazyVGrid(columns: columns, alignment: .leading, spacing: MaktabaTokens.Spacing.s4) {
                ForEach(model.libraries) { lib in
                    LibraryCard(library: lib, pinned: model.isPinned(lib.id),
                                poster: LibraryArt.poster(client, video: lib.posterVideoIDs.first),
                                chosen: choice?.wrappedValue == .library(lib.id),
                                choose: choice.map { c in { c.wrappedValue = .library(lib.id) } },
                                pin: { Task { await model.togglePin(lib.id) } })
                }
            }
            .animation(.default, value: model.libraries.map(\.id))
        }
    }

    @ViewBuilder private var series: some View {
        if let problem = model.seriesProblem, model.series.isEmpty {
            ProblemView(problem: problem) { Task { await model.loadSeries() } }
        } else if !model.seriesLoaded {
            ProgressView().frame(maxWidth: .infinity, minHeight: 200)
        } else if model.series.isEmpty {
            ContentUnavailableView {
                Label { Text("No series yet", tableName: "Libraries", comment: "The Series grid with no series") } icon: {
                    Image(systemName: "rectangle.stack")
                }
            }
        } else if choice != nil {
            // wide: the grid is the page beside; the list says where it is
            ContentUnavailableView {
                Label { Text("Series", tableName: "Libraries", comment: "The Libraries root's tab of every series") } icon: {
                    Image(systemName: "rectangle.stack")
                }
            } description: {
                if !model.moreSeries { Text(SeriesGrid.count(model.series.count)) }
            }
        } else {
            SeriesGrid(context: context, series: model.series) { await model.loadMoreSeries() }
        }
    }
}

/// A library's card: its newest video's poster, its name and how many videos, and a pin.
private struct LibraryCard: View {
    let library: LibrarySummary
    let pinned: Bool
    let poster: URL?
    let chosen: Bool
    /// Wide: chooses the page beside; nil pushes the library's page.
    let choose: (() -> Void)?
    let pin: () -> Void

    var body: some View {
        ZStack(alignment: .topTrailing) {
            Group {
                if let choose {
                    Button(action: choose) { card }
                } else {
                    NavigationLink(value: PlayerRoute.library(id: library.id)) { card }
                }
            }
            .buttonStyle(.plain)
            .accessibilityIdentifier("library.\(library.id)")
            .accessibilityAddTraits(chosen ? .isSelected : [])
            Button(action: pin) {
                Image(systemName: pinned ? "pin.fill" : "pin")
                    .font(.system(size: 15, weight: .semibold))
                    .foregroundStyle(pinned ? Color.brass : Color.white)
                    .frame(width: 32, height: 32)
                    .background(Circle().fill(Color.black.opacity(0.55)))
                    .frame(width: 44, height: 44)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .accessibilityLabel(pinned
                ? Text("Unpin \(library.name)", tableName: "Libraries", comment: "Unpins a library, spoken with its name")
                : Text("Pin \(library.name)", tableName: "Libraries", comment: "Pins a library first, spoken with its name"))
            .accessibilityIdentifier("library.pin.\(library.id)")
        }
        .padding(MaktabaTokens.Spacing.s1)
        .background {
            RoundedRectangle(cornerRadius: MaktabaTokens.Radius.md + 4, style: .continuous)
                .fill(chosen ? Color.mktChosen : Color.clear)
        }
        .contextMenu {
            Button(action: pin) {
                pinned
                    ? Label { Text("Unpin", tableName: "Libraries", comment: "Unpins a library (its menu)") } icon: { Image(systemName: "pin.slash") }
                    : Label { Text("Pin", tableName: "Libraries", comment: "Pins a library first (its menu)") } icon: { Image(systemName: "pin") }
            }
        }
    }

    private var card: some View {
        PosterCard(title: library.name, detail: detail, poster: poster, width: 150)
    }

    private var detail: String {
        let count = String(localized: LocalizedStringResource("\(library.videos) videos", table: "Libraries",
                                                              comment: "A library card's count of videos"))
        guard pinned else { return count }
        return count + " · " + String(localized: LocalizedStringResource("Pinned", table: "Libraries",
                                                                         comment: "A library card's mark: pinned first"))
    }
}

/// Every series in a grid of covers (FR-16), each with how much was watched.
struct SeriesGrid: View {
    let context: PlayerContext
    let series: [SeriesSummary]
    /// Asks for the next page once a cover near the end shows (nil: all are here).
    var more: (() async -> Void)?

    /// How many covers before the end the next page is asked for: about a screen ahead,
    /// so scrolling doesn't stop at the edge of a page.
    static let ahead = 18

    static func count(_ n: Int) -> LocalizedStringResource {
        LocalizedStringResource("\(n) series", table: "Libraries", comment: "How many series there are")
    }

    var body: some View {
        LazyVGrid(columns: [GridItem(.adaptive(minimum: 120), spacing: MaktabaTokens.Spacing.s3, alignment: .top)],
                  alignment: .leading, spacing: MaktabaTokens.Spacing.s4) {
            ForEach(series) { s in
                NavigationLink(value: PlayerRoute.series(id: s.id)) {
                    PosterCard(title: s.name.isEmpty ? String(localized: Self.untitled) : s.name,
                               detail: String(localized: Self.episodes(s.episodeCount)),
                               poster: LibraryArt.poster(context.client, video: s.posterVideoID),
                               progress: s.progress, shape: .tall, width: 120)
                }
                .buttonStyle(.plain)
                .accessibilityIdentifier("series.\(s.id)")
                .onAppear { if let more, Self.nearEnd(s.id, of: series) { Task { await more() } } }
            }
        }
    }

    /// Whether `id` is one of the last `ahead` covers.
    static func nearEnd(_ id: String, of series: [SeriesSummary]) -> Bool {
        series.suffix(ahead).contains { $0.id == id }
    }

    static let untitled = LocalizedStringResource("Untitled series", table: "Libraries", comment: "A series with no name")

    static func episodes(_ n: Int) -> LocalizedStringResource {
        LocalizedStringResource("\(n) episodes", table: "Libraries", comment: "How many episodes a series has")
    }
}

/// The Series grid as the page beside the list (a wide window).
struct SeriesPage: View {
    let context: PlayerContext
    let model: LibrariesModel

    var body: some View {
        ScrollView {
            SeriesGrid(context: context, series: model.series) { await model.loadMoreSeries() }
                .padding(MaktabaTokens.Spacing.s4)
        }
        .maktabaSurface()
        .task { if !model.seriesLoaded { await model.loadSeries() } }
        .navigationTitle(Text("Series", tableName: "Libraries", comment: "The Libraries root's tab of every series"))
        .accessibilityIdentifier("screen.seriesGrid")
    }
}

/// A page with nothing to show: "Not available" (404), or the list's error with Try again.
struct ProblemView: View {
    let problem: LoadProblem
    let retry: () -> Void

    var body: some View {
        switch problem {
        case .notAvailable:
            ContentUnavailableView {
                Label { Text("Not available", tableName: "Libraries", comment: "A library or series that is gone or not shared") } icon: {
                    Image(systemName: "eye.slash")
                }
            } description: {
                Text("It may have been removed, or it is not shared with you.", tableName: "Libraries",
                     comment: "Why a library or series is not available")
            }
            .accessibilityIdentifier("libraries.notAvailable")
        case let .failed(failure):
            HomeErrorView(failure, video: false) { _ in retry() }
                .frame(maxWidth: .infinity, minHeight: 240)
        }
    }
}

/// The account menu (who is signed in, approve a sign-in, the theme, switch home, sign out):
/// on the Libraries root until the Profile section (48.22) is turned on, as it is the only way
/// to sign out there.
struct LibrariesAccountMenu: ToolbarContent {
    let context: PlayerContext
    @AppStorage(ThemeMode.storageKey) private var theme = ThemeMode.system

    var body: some ToolbarContent {
        ToolbarItem(placement: .primaryAction) {
            Menu {
                Text(verbatim: context.user.username)
                if let home = context.session.current { Text(verbatim: home.name) }
                // a TV, phone or computer showing a sign-in code (Epic 42 story 42.6)
                Button(ConnectStrings.forPreferredLanguages().text(.approveMenu), systemImage: "checkmark.shield") {
                    context.session.approvingSignIn = true
                }
                .accessibilityIdentifier("account.approve")
                ThemePicker(mode: $theme)
                Button("Switch home", systemImage: "house") { context.session.switchHome() }
                Button("Sign out", systemImage: "rectangle.portrait.and.arrow.right") { context.session.signOut() }
            } label: {
                Label("Account", systemImage: "person.crop.circle")
            }
            .accessibilityIdentifier("account.menu")
        }
    }
}
