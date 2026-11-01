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
    @State private var model: LibrariesModel
    @State private var path: [PlayerRoute]
    @State private var choice: LibrariesChoice?
    @Environment(\.horizontalSizeClass) private var width

    init(session: AppSession, user: User, client: APIClient, playing: Binding<Bool>) {
        self.session = session
        self.user = user
        self.client = client
        _playing = playing
        _playback = State(initialValue: Playback(client: client))
        _model = State(initialValue: LibrariesModel(client: client, signedOut: { session.signedOut() }))
        // a Debug build's `-maktaba-open library/lib-docs` (the UI tests): opened at launch
        let start = ShellLaunch.section()
        _path = State(initialValue: (start == nil || start == .libraries) ? ShellLaunch.route().map { [$0] } ?? [] : [])
    }

    private var context: PlayerContext { PlayerContext(session: session, user: user, client: client) }

    var body: some View {
        layout
            .environment(playback)
            .focusedSceneValue(\.playback, playback)
            .onChange(of: playback.video != nil) { playing = $1 }
            .onDisappear { playback.stop(); playing = false }
    }

    @ViewBuilder private var layout: some View {
        if width == .compact {
            NavigationStack(path: $path) {
                LibrariesScreen(context: context, model: model)
                    .toolbar { LibrariesAccountMenu(context: context) }
                    .librariesDestinations(context)
            }
            .player(playback, session: session)
        } else {
            NavigationSplitView(columnVisibility: .constant(.all)) {
                LibrariesScreen(context: context, model: model, choice: $choice)
                    .toolbar { LibrariesAccountMenu(context: context) }
                    #if os(macOS)
                    .navigationSplitViewColumnWidth(min: 300, ideal: 360, max: 480)
                    #else
                    .navigationSplitViewColumnWidth(min: 320, ideal: 360, max: 480)
                    #endif
            } detail: {
                NavigationStack(path: $path) {
                    page.librariesDestinations(context)
                }
            }
            .navigationSplitViewStyle(.balanced)
            .onChange(of: choice) { old, _ in if old != nil { path = [] } }
            .onChange(of: model.libraries.first?.id, initial: true) { _, first in
                // a wide window shows a page, not an empty column
                if choice == nil, let first { choice = .library(first) }
            }
            .player(playback, session: session)
            #if os(macOS)
            .frame(minWidth: 760, minHeight: 480)
            #endif
        }
    }

    /// The page beside the list.
    @ViewBuilder private var page: some View {
        switch choice {
        case let .library(id): LibraryScreen(context: context, id: id).id(id)
        case .series: SeriesPage(context: context, model: model)
        case nil:
            ContentUnavailableView {
                Label { Text("Choose a library", tableName: "Libraries", comment: "A wide window's page before a library is chosen") } icon: {
                    Image(systemName: "books.vertical")
                }
            }
        }
    }
}

private extension View {
    /// The section's routes. `video` (and `watch`) open today's video page and player until
    /// 48.19's page and 48.23's player land; the rest open as in every section.
    func librariesDestinations(_ context: PlayerContext) -> some View {
        navigationDestination(for: PlayerRoute.self) { route in
            switch route {
            case let .video(id), let .watch(id, _): VideoBridge(context: context, id: id)
            case let .series(id): SeriesScreen(context: context, id: id)
            case let .library(id): LibraryScreen(context: context, id: id)
            case let .channel(id): ChannelScreen(context: context, id: id)
            }
        }
    }

    /// Today's player over the section while something plays: full screen on iOS, the
    /// window on the Mac (the green button or Control-Command-F makes it full screen).
    func player(_ playback: Playback, session: AppSession) -> some View {
        modifier(PlayerOverlay(playback: playback, session: session))
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
