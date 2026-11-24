import MaktabaKit
import SwiftUI

/// The Libraries section (Epic 48, story 48.18): Libraries and Series, a library, a series,
/// and today's video page and player.
/// - A phone (and a folded iPhone Duo, an iPad in Slide Over): one stack, the Libraries root
///   first, a card pushing its page.
/// - A wide window (iPad, the Mac, an open Duo): the Libraries root as the list beside the
///   chosen library's (or the Series grid's) page, which keeps its own stack (FR-15).
///
/// A video opens its page through `PlayerRoute.video`. Until the new video page (48.19) and
/// player (48.23) land, this section maps that route to today's page (`VideoDetail`) and plays
/// with today's `Playback`, full screen (on the Mac, over the window), so a build never loses
/// playback. The shell keeps calling this view by its old name; the section owns its stack.
struct LibraryBrowser: View {
    let session: AppSession
    let user: User
    let client: APIClient
    @Binding var playing: Bool

    @State private var playback: Playback
    /// The section's lists, its chosen library and its stack: the shell's on the Mac, kept
    /// while another section is in view (story 13.16); this view's own elsewhere.
    @State private var state: LibrariesState
    @Environment(\.horizontalSizeClass) private var width

    init(session: AppSession, user: User, client: APIClient, playing: Binding<Bool>, state: LibrariesState? = nil) {
        self.session = session
        self.user = user
        self.client = client
        _playing = playing
        let playback = Playback(client: client)
        // Epic 57: under a parental limit a video the home stops giving is the blocked page
        let parental = session.parental
        playback.limited = { parental.current.limited }
        _playback = State(initialValue: playback)
        _state = State(initialValue: state ?? LibrariesState(session: session, client: client))
    }

    private var context: PlayerContext { PlayerContext(session: session, user: user, client: client) }

    var body: some View {
        layout
            .environment(playback)
            .focusedSceneValue(\.playback, playback)
            .onChange(of: playback.video != nil) { playing = $1 }
            .onDisappear { playback.stop(); playing = false }
            // Epic 57: the limit changed (`ParentalStore.epoch`). The lists and the pages are
            // read again, so nothing hidden stays on them; what plays keeps playing (the home
            // stops it if it must) and the stack stays where it is
            // (also when it changed while another section was in view, on the Mac)
            .onChange(of: session.parental.epoch, initial: true) { state.follow(epoch: session.parental.epoch) }
            .onChange(of: state.model.libraries.map(\.id)) { _, ids in
                // wide: the library beside the list is no longer one the home gives
                if case let .library(id)? = state.choice, !ids.isEmpty, !ids.contains(id) { state.choice = ids.first.map { .library($0) } }
            }
    }

    @ViewBuilder private var layout: some View {
        @Bindable var state = state
        if width == .compact {
            NavigationStack(path: $state.path) {
                LibrariesScreen(context: context, model: state.model)
                    .id(state.fresh)
                    .toolbar { LibrariesAccountMenu(context: context) }
                    .librariesDestinations(context)
            }
            .environment(\.playerPush, PlayerPush { state.path.append($0) })
            .player(playback, session: session)
        } else {
            wide
            .onChange(of: state.choice) { old, _ in if old != nil { state.path = [] } }
            .onChange(of: state.model.libraries.first?.id, initial: true) { _, first in
                // a wide window shows a page, not an empty column
                if state.choice == nil, let first { state.choice = .library(first) }
            }
            .player(playback, session: session)
            #if os(macOS)
            .frame(minWidth: 760, minHeight: 480)
            #endif
        }
    }

    /// The list and the page beside it. On the Mac the two are set side by side: the section is
    /// already the detail of the window's split view (`MacShellSplit`), and a split view inside
    /// it insets its page by the sidebar's width again (the page was 160 points of a 1024-point
    /// window).
    @ViewBuilder private var wide: some View {
        @Bindable var state = state
        let list = LibrariesScreen(context: context, model: state.model, choice: $state.choice)
            .id(state.fresh)
            .toolbar { LibrariesAccountMenu(context: context) }
        let pages = NavigationStack(path: $state.path) {
            FreshOnLimit(session: session) { page }.librariesDestinations(context)
        }
        .environment(\.playerPush, PlayerPush { state.path.append($0) })
        #if os(macOS)
        HStack(spacing: 0) {
            list.frame(width: 344)
            Divider()
            pages.frame(maxWidth: .infinity, maxHeight: .infinity)
        }
        #else
        NavigationSplitView(columnVisibility: .constant(.all)) {
            list.navigationSplitViewColumnWidth(min: 320, ideal: 360, max: 480)
        } detail: {
            pages
        }
        .navigationSplitViewStyle(.balanced)
        #endif
    }

    /// The page beside the list.
    @ViewBuilder private var page: some View {
        switch state.choice {
        case let .library(id): LibraryScreen(context: context, id: id).id(id)
        case .series: SeriesPage(context: context, model: state.model)
        case nil:
            ContentUnavailableView {
                Label { Text("Choose a library", tableName: "Libraries", comment: "A wide window's page before a library is chosen") } icon: {
                    Image(systemName: "books.vertical")
                }
            }
        }
    }
}

/// The Libraries section's state (story 13.16): its lists (`model`), the library chosen beside
/// them, and its stack. The Mac's shell keeps one while another section is in view, so coming
/// back finds the same library and the same pages, without reading the lists again.
@MainActor @Observable
final class LibrariesState {
    var path: [PlayerRoute]
    var choice: LibrariesChoice?
    private(set) var model: LibrariesModel
    /// Moves when the account's parental limit changes: the lists are read again.
    private(set) var fresh = 0
    /// The parental epoch the lists were read under (nil: not yet followed).
    @ObservationIgnored private var epoch: Int?
    @ObservationIgnored private let remake: () -> LibrariesModel

    init(session: AppSession, client: APIClient) {
        let remake = { LibrariesModel(client: client, signedOut: { session.signedOut() }) }
        self.remake = remake
        model = remake()
        // a Debug build's `-maktaba-open library/lib-docs` (the UI tests): opened at launch
        let start = ShellLaunch.section()
        path = (start == nil || start == .libraries) ? ShellLaunch.route().map { [$0] } ?? [] : []
    }

    /// Epic 57: the limit changed (`ParentalStore.epoch`). The lists and the pages are read
    /// again, so nothing hidden stays on them; the stack stays where it is.
    func follow(epoch now: Int) {
        defer { epoch = now }
        guard let epoch, epoch != now else { return }
        model = remake()
        fresh += 1
    }
}

private extension View {
    /// The section's routes. `video` (and `watch`) open today's video page and player until
    /// 48.19's page and 48.23's player land; the rest open as in every section.
    func librariesDestinations(_ context: PlayerContext) -> some View {
        navigationDestination(for: PlayerRoute.self) { route in
            FreshOnLimit(session: context.session) {
                switch route {
                case let .video(id), let .watch(id, _): VideoBridge(context: context, id: id)
                case let .series(id): SeriesScreen(context: context, id: id)
                case let .library(id): LibraryScreen(context: context, id: id)
                case let .channel(id): ChannelScreen(context: context, id: id)
                case let .search(q): SearchScreen(context: context, query: q)
                }
            }
        }
    }

    /// Today's player over the section while something plays: full screen on iOS, the
    /// window on the Mac (the green button or Control-Command-F makes it full screen).
    func player(_ playback: Playback, session: AppSession) -> some View {
        modifier(PlayerOverlay(playback: playback, session: session))
    }
}

/// A page built again when the account's parental limit changes: it reads the home afresh,
/// so a page of something now hidden says so and nothing hidden stays on it.
private struct FreshOnLimit<Content: View>: View {
    let session: AppSession
    @ViewBuilder let content: Content

    var body: some View {
        content.id(session.parental.epoch)
    }
}

private struct PlayerOverlay: ViewModifier {
    let playback: Playback
    let session: AppSession

    func body(content: Content) -> some View {
        #if os(iOS)
        content.fullScreenCover(isPresented: Binding(get: { playback.video != nil }, set: { if !$0 { playback.stop() } })) {
            WatchView(playback: playback, session: session)
        }
        #else
        content
            .overlay {
                if playback.video != nil {
                    WatchView(playback: playback, session: session).transition(.opacity)
                }
            }
            .toolbar(playback.video != nil ? .hidden : .automatic, for: .windowToolbar)
            .animation(.easeInOut(duration: 0.2), value: playback.video != nil)
        #endif
    }
}

/// A video's page, as today: the video read from the home, then `VideoDetail` (play there
/// hands it to the section's `Playback`). Replaced by 48.19's `VideoScreen` once it lands.
private struct VideoBridge: View {
    let context: PlayerContext
    let id: String

    @State private var video: Video?
    @State private var problem: LoadProblem?

    var body: some View {
        Group {
            if let video {
                VideoDetail(client: context.client, video: video)
            } else if let problem {
                ProblemView(problem: problem) { Task { await load() } }
                    .maktabaSurface()
            } else {
                ProgressView().frame(maxWidth: .infinity, maxHeight: .infinity).maktabaSurface()
            }
        }
        .accessibilityIdentifier("screen.videoDetail")
        .task { if video == nil { await load() } }
    }

    private func load() async {
        problem = nil
        await loading("player video page", signedOut: { context.session.signedOut() }, problem: { problem = $0 }) {
            do {
                video = try await context.client.video(id)
            } catch let APIError.status(code, _, _) where code == 404 || code == 403 {
                throw BrowseError.notAvailable
            }
        }
    }
}
