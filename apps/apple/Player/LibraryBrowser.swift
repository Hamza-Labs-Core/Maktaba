import MaktabaKit
import SwiftUI

/// Libraries, their videos, a video, and what is playing. The layout follows the device:
/// - a NavigationSplitView (Apple's guidance for iPhone Duo: one column folded, columns
///   open flat, as on iPad and the Mac; on an iPad in Split View or Slide Over it folds to
///   one column like a phone), with the player full screen over it (on the Mac, over the
///   window);
/// - a half-folded iPhone Duo: an ArrangementView split at the hinge, the navigation (a
///   library menu and the videos) on one half and the page on the other, the player taking
///   the page's half while it plays, so browsing goes on beside it.
struct LibraryBrowser: View {
    let session: AppSession
    let user: User
    let client: APIClient
    @Binding var playing: Bool

    @AppStorage(ThemeMode.storageKey) private var theme = ThemeMode.system
    @State private var playback: Playback
    @State private var halfFolded = false
    @Environment(\.horizontalSizeClass) private var width
    @State private var libraries: [Library] = []
    @State private var library: String? = Self.all
    @State private var videos: [Video] = []
    @State private var next: String?
    @State private var video: Video?
    @State private var query = ""
    @State private var problem: String?
    /// Pages opened from the list while the player holds the other half.
    @State private var browsing: [Video] = []

    static let all = "all"

    init(session: AppSession, user: User, client: APIClient, playing: Binding<Bool>) {
        self.session = session
        self.user = user
        self.client = client
        _playing = playing
        _playback = State(initialValue: Playback(client: client))
    }

    var body: some View {
        layout
            .environment(playback)
            .task { await loadLibraries() }
            .task(id: "\(library ?? "")|\(query)") { await reload() }
            .focusedSceneValue(\.playback, playback)
            .onChange(of: playback.video != nil) { playing = $1 }
            .onDisappear { playback.stop(); playing = false }
    }

    @ViewBuilder private var layout: some View {
        #if os(iOS)
        if #available(iOS 27.1, *) {
            Group {
                if halfFolded { split } else { columns }
            }
            .onHingeChange { _, now in halfFolded = now.hinge?.status == .partiallyOpen }
        } else {
            columns
        }
        #else
        columns
        #endif
    }

    /// Flat, folded, iPad, Mac: three columns, the player full screen.
    private var columns: some View {
        NavigationSplitView {
            List(selection: $library) {
                Label("All videos", systemImage: "square.grid.2x2").tag(Self.all)
                Section("Libraries") {
                    ForEach(libraries) { lib in
                        Label(lib.name, systemImage: "books.vertical").tag(lib.id)
                    }
                }
            }
            .navigationTitle("Libraries")
            .toolbar { accountMenu }
        } content: {
            List(selection: $video) {
                ForEach(videos) { v in
                    VideoRow(client: client, video: v).tag(v)
                        .onAppear { if v == videos.last { Task { await loadMore() } } }
                }
            }
            .maktabaSurface()
            .overlay { listState }
            .searchable(text: $query, prompt: "Search titles")
            .navigationTitle(title)
            .refreshable { await reload() }
            #if os(macOS)
            // room for a poster and a title beside it
            .navigationSplitViewColumnWidth(min: 280, ideal: 340, max: 460)
            #endif
        } detail: {
            page
        }
        #if os(iOS)
        .fullScreenCover(isPresented: watching) { WatchView(playback: playback) }
        #else
        // the Mac: the video fills the window (it resizes with it; the green button or
        // Control-Command-F makes it full screen), the toolbar out of the way
        .overlay {
            if playback.video != nil {
                WatchView(playback: playback).transition(.opacity)
            }
        }
        .toolbar(playback.video != nil ? .hidden : .automatic, for: .windowToolbar)
        .animation(.easeInOut(duration: 0.2), value: playback.video != nil)
        .frame(minWidth: 760, minHeight: 480)
        #endif
    }

    #if os(iOS)
    /// Half folded: every half used. The first half (the upright one when the Duo stands
    /// like a laptop) holds what is looked at, the second what is touched:
    /// - nothing chosen: the libraries | their videos;
    /// - a video chosen: its page | the videos;
    /// - playing: the player | the videos, where a tap opens that video's page (play there
    ///   switches what plays), so browsing goes on beside the video.
    @available(iOS 27.1, *)
    private var split: some View {
        ArrangementView {
            if playback.video != nil {
                WatchView(playback: playback)
            } else if video != nil {
                NavigationStack {
                    page.toolbar {
                        ToolbarItem(placement: .topBarLeading) {
                            Button("Libraries", systemImage: "books.vertical") { video = nil }
                        }
                    }
                }
            } else {
                NavigationStack {
                    List {
                        ForEach([LibraryChoice(id: Self.all, name: "All videos")] + libraries.map { LibraryChoice(id: $0.id, name: $0.name) }) { lib in
                            Button { library = lib.id } label: {
                                Label {
                                    Text(lib.name).foregroundStyle(Color.primary)
                                } icon: {
                                    Image(systemName: lib.id == Self.all ? "square.grid.2x2" : "books.vertical")
                                        .foregroundStyle(Color.brass)
                                }
                            }
                            .listRowBackground(lib.id == library ? Color.mktChosen : Color.mktSurface)
                        }
                    }
                    .maktabaSurface()
                    .navigationTitle("Libraries")
                }
            }
        } secondary: {
            NavigationStack(path: $browsing) {
                List {
                    ForEach(videos) { v in
                        Button {
                            if playback.video != nil { browsing = [v] } else { video = v }
                        } label: { VideoRow(client: client, video: v).foregroundStyle(Color.primary) }
                        .listRowBackground(v.id == (playback.video ?? video)?.id ? Color.mktChosen : Color.mktSurface)
                        .onAppear { if v == videos.last { Task { await loadMore() } } }
                    }
                }
                .maktabaSurface()
                .overlay { listState }
                .searchable(text: $query, prompt: "Search titles")
                .navigationTitle(title)
                .refreshable { await reload() }
                .navigationDestination(for: Video.self) { VideoDetail(client: client, video: $0) }
                .toolbar {
                    if playback.video != nil || video != nil {
                        ToolbarItem(placement: .topBarLeading) {
                            Picker("Library", systemImage: "books.vertical", selection: $library) {
                                Text("All videos").tag(Optional(Self.all))
                                ForEach(libraries) { lib in Text(lib.name).tag(Optional(lib.id)) }
                            }
                            .pickerStyle(.menu)
                        }
                    }
                    accountMenu
                }
            }
        }
        .arrangementViewStyle(.split)
        // a video that starts playing is the page to come back to when it stops
        .onChange(of: playback.video?.id) { browsing = []; if let v = playback.video { video = v } }
    }
    #endif

    @ViewBuilder private var page: some View {
        if let video {
            VideoDetail(client: client, video: video)
        } else {
            ContentUnavailableView("Choose a video", systemImage: "play.rectangle")
        }
    }

    @ViewBuilder private var listState: some View {
        if videos.isEmpty, problem == nil { ContentUnavailableView("No videos", systemImage: "film") }
        if let problem { ContentUnavailableView("Can't load", systemImage: "wifi.exclamationmark", description: Text(problem)) }
    }

    /// The full-screen player is up while something plays (and the screen is not split).
    private var watching: Binding<Bool> {
        Binding(get: { playback.video != nil && !halfFolded },
                set: { if !$0, !halfFolded { playback.stop() } })
    }

    private var title: String {
        libraries.first { $0.id == library }?.name ?? "All videos"
    }

    private var accountMenu: some ToolbarContent {
        ToolbarItem(placement: .primaryAction) {
            Menu {
                Text(user.username)
                if let home = session.current { Text(home.name) }
                ThemePicker(mode: $theme)
                Button("Switch home", systemImage: "house") { session.switchHome() }
                Button("Sign out", systemImage: "rectangle.portrait.and.arrow.right") { session.signOut() }
            } label: {
                Label("Account", systemImage: "person.crop.circle")
            }
        }
    }

    private func loadLibraries() async {
        do {
            libraries = try await client.libraries()
        } catch APIError.signedOut {
            session.signedOut()
        } catch {
            problem = error.localizedDescription
        }
    }

    private func reload() async {
        do {
            let page = try await client.videos(library: library == Self.all ? nil : library, query: query)
            videos = page.items
            next = page.next
            problem = nil
            // a wide screen (open flat, iPad, Mac) shows a page, not an empty column; folded
            // (one column) and half folded (the libraries beside the videos) keep the list
            if width != .compact, !halfFolded, video == nil || !videos.contains(where: { $0.id == video?.id }) {
                video = videos.first
            }
        } catch APIError.signedOut {
            session.signedOut()
        } catch {
            problem = error.localizedDescription
        }
    }

    private func loadMore() async {
        guard let next else { return }
        self.next = nil
        if let page = try? await client.videos(library: library == Self.all ? nil : library, query: query, cursor: next) {
            videos += page.items
            self.next = page.next
        }
    }
}

struct VideoRow: View {
    let client: APIClient
    let video: Video

    var body: some View {
        HStack(spacing: 12) {
            AuthorizedImage(client: client, video: video.id)
                .frame(width: 120)
                .clipShape(.rect(cornerRadius: 6))
            VStack(alignment: .leading, spacing: 4) {
                Text(video.displayTitle).font(.headline).lineLimit(2)
                if let d = video.durationSec {
                    Text(Duration.seconds(d).formatted(.time(pattern: d >= 3600 ? .hourMinuteSecond : .minuteSecond)))
                        .font(.caption).foregroundStyle(.secondary).monospacedDigit()
                }
            }
        }
        .padding(.vertical, 4)
    }
}

/// A row of the half-folded libraries list ("All videos" or a library).
private struct LibraryChoice: Identifiable {
    let id: String
    let name: String
}
