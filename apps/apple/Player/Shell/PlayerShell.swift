import MaktabaKit
import SwiftUI

/// What every screen of the signed-in Player gets: the session (switch home, sign out), who
/// is signed in, and the home's client. One value, so a screen's story never changes the
/// shell's calls to it.
struct PlayerContext {
    let session: AppSession
    let user: User
    let client: APIClient
}

/// The signed-in Player (Epic 48, FR-5 to FR-7): its sections in a `TabView` that adapts,
/// a bottom tab bar on an iPhone (a folded iPhone Duo too), top tabs that become a sidebar
/// on an iPad and an open Duo, a sidebar on the Mac. Each section keeps its own stack of
/// `PlayerRoute`s; choosing the section already chosen takes it back to its first screen.
///
/// Only `PlayerSection.enabled` shows. With one section (Libraries, until the other screens
/// merge) the app is that section's stack alone with no tab bar, so Libraries looks exactly
/// as it did before the shell, and any other single section still opens routes. The half-folded Duo's
/// hinge split stays the Libraries screen's own (`LibraryBrowser`).
struct PlayerShell: View {
    let context: PlayerContext
    @Binding var playing: Bool
    let sections: [PlayerSection]
    /// A deep link's page (`maktaba://video/{id}`, `PlayerApp`), opened on the section in view
    /// and then cleared.
    @Binding var link: PlayerRoute?

    @State private var selection: PlayerSection
    /// A linked page over a section with no route stack (Libraries' split view), in a sheet.
    @State private var linked: PlayerRoute?
    @State private var paths: [PlayerSection: [PlayerRoute]]
    /// The tabs' order and the sidebar's sections as the person arranged them (iPad, Mac).
    @AppStorage("player.tabs") private var customization = TabViewCustomization()
    #if os(macOS)
    /// The player over the whole window, sidebar and all (`WindowPlayer`).
    @State private var windowPlayer = WindowPlayer()
    #endif

    init(context: PlayerContext, playing: Binding<Bool>, link: Binding<PlayerRoute?> = .constant(nil),
         sections: [PlayerSection] = PlayerSection.visible(),
         start: PlayerSection? = ShellLaunch.section(), route: PlayerRoute? = ShellLaunch.route()) {
        self.context = context
        _playing = playing
        _link = link
        self.sections = sections
        let first = start.flatMap { sections.contains($0) ? $0 : nil } ?? sections.first ?? .libraries
        _selection = State(initialValue: first)
        _paths = State(initialValue: route.map { [first: [$0]] } ?? [:])
    }

    var body: some View {
        Group {
            if sections.count == 1, let only = sections.first {
                // its stack (and the routes) without a tab bar; Libraries stays its split view
                stack(only)
            } else {
                tabs
            }
        }
        .onChange(of: link, initial: true) { _, route in
            guard let route else { return }
            link = nil
            open(route)
        }
        .sheet(isPresented: Binding { linked != nil } set: { if !$0 { linked = nil } }) {
            if let linked { LinkedRouteSheet(context: context, route: linked) }
        }
        // the View menu (⌘1–⌘5, ⌘F) acts on this window's sections (Player/Mac)
        .playerMenuTarget(sections: sections, selection: chosen)
        // the components' posters and logos: the image cache, with this home's Bearer token
        .environment(\.imageSource, .home(context.client))
        #if os(macOS)
        .environment(windowPlayer)
        .overlay {
            if let playback = windowPlayer.playback {
                WatchView(playback: playback, session: context.session) { windowPlayer.close?() }
                    .ignoresSafeArea()
                    .accessibilityIdentifier("player.window")
            }
        }
        #endif
    }

    private var tabs: some View {
        TabView(selection: chosen) {
            ForEach(sections) { section in
                Tab(value: section, role: section == .search ? .search : nil) {
                    stack(section)
                } label: {
                    Label { Text(section.title) } icon: { Image(systemName: section.systemImage) }
                }
                .customizationID("player.section.\(section.rawValue)")
            }
        }
        .tabViewStyle(.sidebarAdaptable)
        .tabViewCustomization($customization)
        .accessibilityIdentifier("player.shell")
    }

    /// The chosen section; choosing it again pops its stack to its first screen (FR-6).
    private var chosen: Binding<PlayerSection> {
        Binding {
            selection
        } set: { new in
            if new == selection { paths[new] = [] }
            selection = new
        }
    }

    /// A deep link's page: pushed on the chosen section's stack, or in a sheet over a section
    /// with none.
    private func open(_ route: PlayerRoute) {
        if hasStack(selection) {
            paths[selection, default: []].append(route)
        } else {
            linked = route
        }
    }

    /// Libraries (`LibraryBrowser`) keeps its own stack, out of the shell's reach; every other
    /// section is a stack of `PlayerRoute`s the shell holds.
    private func hasStack(_ section: PlayerSection) -> Bool { section != .libraries }

    private func path(_ section: PlayerSection) -> Binding<[PlayerRoute]> {
        Binding { paths[section] ?? [] } set: { paths[section] = $0 }
    }

    /// A section's stack. Libraries is today's split view, which navigates on its own until
    /// its new screens (story 48.18) replace it.
    @ViewBuilder private func stack(_ section: PlayerSection) -> some View {
        if !hasStack(section) {
            root(section)
        } else {
            NavigationStack(path: path(section)) {
                root(section).playerDestinations(context)
            }
        }
    }

    /// A section's first screen.
    @ViewBuilder private func root(_ section: PlayerSection) -> some View {
        switch section {
        case .home: HomeScreen(context: context)
        case .libraries: LibraryBrowser(session: context.session, user: context.user, client: context.client, playing: $playing)
        case .liveTV: LiveTVScreen(context: context)
        case .search: SearchScreen(context: context)
        case .profile: ProfileScreen(context: context)
        }
    }
}

extension View {
    /// Where each `PlayerRoute` goes: one place, so every section's stack opens a route the
    /// same way.
    func playerDestinations(_ context: PlayerContext) -> some View {
        navigationDestination(for: PlayerRoute.self) { PlayerDestination(context: context, route: $0) }
    }
}

/// A route's screen.
struct PlayerDestination: View {
    let context: PlayerContext
    let route: PlayerRoute

    var body: some View {
        switch route {
        case let .video(id): VideoScreen(context: context, id: id)
        case let .series(id): SeriesScreen(context: context, id: id)
        case let .library(id): LibraryScreen(context: context, id: id)
        case let .channel(id): ChannelScreen(context: context, id: id)
        case let .watch(id, at): WatchScreen(context: context, videoID: id, at: at)
        }
    }
}

/// A screen not built yet: its name and a line saying it is on its way. Reachable only in a
/// Debug build (`PlayerSection.visible`); a screen's story replaces it.
struct ShellPlaceholder: View {
    let title: LocalizedStringResource
    let systemImage: String
    /// The screen's accessibility identifier (`screen.home`, `screen.video` …), for the UI tests.
    let id: String
    var detail: String?

    var body: some View {
        ContentUnavailableView {
            Label { Text(title) } icon: { Image(systemName: systemImage) }
        } description: {
            VStack(spacing: 4) {
                Text("This screen is on its way.", tableName: "Shell", comment: "A section or page not built yet")
                if let detail { Text(verbatim: detail).font(.caption.monospaced()) }
            }
        }
        .maktabaSurface()
        .navigationTitle(Text(title))
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier(id)
    }
}
