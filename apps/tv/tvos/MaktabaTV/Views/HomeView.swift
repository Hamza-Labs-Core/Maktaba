import MaktabaKit
import SwiftUI

/// The landing screen over the library scene: the same rows as the iPhone/iPad Player's Home,
/// from the same answers (`HomeShelves`, MaktabaKit): Continue watching, Up next, On now (Live
/// TV), Favorites, Recently added, Recently watched, each pinned library, For you, From your
/// libraries. Each is a horizontal shelf of focusable cards with their posters; a row that
/// could not be read stays, as a tile that reads it again.
struct HomeView: View {
    @Environment(\.client) private var client
    @State private var shelves: [HomeShelf] = []
    @State private var loading = true
    @State private var live = LiveModel()
    @State private var tuned: LiveView.Tuned?

    var body: some View {
        ZStack {
            LibraryBackdrop()
            ScrollView {
                VStack(alignment: .leading, spacing: 40) {
                    SectionTitle(String(localized: "Your library"), size: 52)
                        .readingPanel(padding: 28)
                        .padding(.top, 160)

                    Group {
                        if loading && shelves.isEmpty {
                            ProgressView().frame(maxWidth: .infinity).padding(.vertical, 60)
                        } else if shelves.isEmpty {
                            ContentUnavailableView("Nothing to watch yet", systemImage: "books.vertical",
                                                   description: Text("Videos appear here once this home shares a library with you."))
                                .accessibilityIdentifier("home.empty")
                        } else {
                            VStack(alignment: .leading, spacing: 48) {
                                ForEach(shelves) { shelf in
                                    HomeShelfView(shelf: shelf, retry: { Task { await load() } },
                                                  tune: { tuned = LiveView.Tuned(channel: $0) })
                                }
                            }
                        }
                    }
                    .readingPanel(padding: 40)
                }
                .padding(.vertical, 60)
                .padding(.horizontal, 80) // 10-foot safe-area gutter
            }
        }
        .ignoresSafeArea(edges: .top)
        .task { await load() }
        .refreshable { await load() }
        .fullScreenCover(item: $tuned) { t in
            if let client {
                LivePlayerScreen(client: client, model: live, start: t.channel)
                    .environment(\.client, client)
            }
        }
    }

    /// A shelf's title is the app's to say (the home sends rows, not words); a library is
    /// called by its name.
    static func title(of kind: HomeShelf.Kind) -> String {
        switch kind {
        case .continueWatching: String(localized: "Continue watching")
        case .upNext: String(localized: "Up next")
        case .liveNow: String(localized: "On now")
        case .favorites: String(localized: "Favorites")
        case .recentlyAdded: String(localized: "Recently added")
        case .recentlyWatched: String(localized: "Recently watched")
        case let .library(_, name): name
        case .forYou, .recommendations: String(localized: "For you")
        case .fromLibrary: String(localized: "From your libraries")
        case .home: String(localized: "Your library")
        }
    }

    private func load() async {
        guard let client else { return }
        let read = await HomeShelves.load(
            home: { try await client.home() },
            rails: { try await client.homeRails(surface: "tv-home") },
            now: { try await client.channelsNow() },
            // a row that cannot be read again keeps what it showed (as the Player's Home)
            keeping: shelves)
        // the screen went away (or a newer read started): nothing to say
        guard !Task.isCancelled else { return }
        for shelf in read {
            switch shelf.content {
            case let .failed(f): Log.failure("load home row", HomeFailureError(f))
            case let .channels(channels): live.show(channels)
            case .cards: break
            }
        }
        shelves = read
        loading = false
    }
}

/// One shelf: its title, then its cards in a row that scrolls sideways (or the tile that
/// reads it again).
struct HomeShelfView: View {
    let shelf: HomeShelf
    let retry: () -> Void
    let tune: (String) -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 20) {
            SectionTitle(HomeView.title(of: shelf.kind), size: 40)
            ScrollView(.horizontal) {
                content
                    .padding(.vertical, 32) // room for the focus scale to grow
            }
            .scrollClipDisabled()
        }
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("home.row.\(shelf.key)")
    }

    @ViewBuilder private var content: some View {
        switch shelf.content {
        case let .cards(cards):
            LazyHStack(alignment: .top, spacing: 48) {
                ForEach(cards) { card in
                    NavigationLink {
                        VideoPage(id: card.videoID)
                    } label: {
                        VideoCard(id: card.videoID, title: card.title, progress: card.progress)
                    }
                    .buttonStyle(.borderless)
                }
            }
        case let .channels(channels):
            // the progress bars move on their own, a little every half minute
            TimelineView(.periodic(from: .now, by: 30)) { context in
                LazyHStack(alignment: .top, spacing: 48) {
                    ForEach(channels) { c in
                        Button { tune(c.channel.id) } label: {
                            LiveChannelCard(channel: c.channel, now: c, at: context.date)
                                .frame(width: LiveNowGrid.cardWidth)
                        }
                        .buttonStyle(.borderless)
                        .accessibilityIdentifier("home.live.\(c.channel.id)")
                    }
                }
            }
        case let .failed(failure):
            RetryTile(failure: failure, retry: retry)
        }
    }
}

/// A row that could not be read: what went wrong, and Select to read it again (or to sign
/// in again, or choose another home, when that is what the failure needs).
struct RetryTile: View {
    let failure: HomeFailure
    let retry: () -> Void

    @Environment(\.appSession) private var session

    var body: some View {
        Button(action: act) {
            RetryTileFace(failure: failure)
        }
        .buttonStyle(.borderless)
        .accessibilityIdentifier("home.row.retry")
    }

    private func act() {
        switch ErrorBanner.actions(failure).first {
        case .signInAgain: session?.signedOut()
        case .chooseAnotherHome: session?.switchHome()
        default: retry()
        }
    }
}

private struct RetryTileFace: View {
    let failure: HomeFailure
    @Environment(\.isFocused) private var focused

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Image(systemName: "arrow.clockwise.circle")
                .font(.system(size: 56))
                .foregroundStyle(Theme.goldText)
            Text("This row could not be loaded.")
                .font(Theme.body(28, weight: .semibold))
                .foregroundStyle(Theme.text)
            Text(failure.message(video: false))
                .font(Theme.body(24))
                .foregroundStyle(Theme.muted)
                .lineLimit(2)
            Text("Select to try again.")
                .font(Theme.body(24, weight: .medium))
                .foregroundStyle(Theme.goldText)
        }
        .padding(28)
        .frame(width: VideoCard.width * 1.5, height: VideoCard.width * 9 / 16, alignment: .leading)
        .background(focused ? Theme.raised : Theme.surface, in: .rect(cornerRadius: Theme.cornerRadius))
        .overlay(RoundedRectangle(cornerRadius: Theme.cornerRadius).strokeBorder(Theme.border, lineWidth: 1))
        .goldFocus(focused)
    }
}

/// Inline error with a retry (focused first): what went wrong in a sentence, never what
/// the home answered (that is behind Details).
struct ErrorBanner: View {
    let failure: HomeFailure
    let retry: () -> Void

    @Environment(\.appSession) private var session

    var body: some View {
        HomeErrorView(failure, video: false, actions: Self.actions(failure)) { action in
            switch action {
            case .signInAgain: session?.signedOut()
            case .chooseAnotherHome: session?.switchHome()
            default: retry()
            }
        }
        .buttonStyle(.gold)
        .foregroundStyle(Theme.text)
        .frame(maxWidth: .infinity)
        .padding(.vertical, 60)
    }

    /// A list's actions: the failure's own (Sign in again after a 401, another home when
    /// this one does not answer), Try again where waiting may help; never Back (a tab has
    /// nowhere to go back to).
    static func actions(_ f: HomeFailure) -> [ErrorAction] {
        let own = f.actions.filter { $0 != .back }
        return own.isEmpty ? [.tryAgain] : own
    }
}

extension HomeFailure {
    /// What a TV screen shows for a failed load: nil when there is nothing to say (the
    /// screen moved on). Logged.
    static func shown(_ what: StaticString, _ error: Error) -> HomeFailure? {
        guard !Log.isCancellation(error) else { return nil }
        Log.failure(what, error)
        return HomeFailure(error)
    }
}
