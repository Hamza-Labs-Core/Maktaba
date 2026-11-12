import MaktabaKit
import SwiftUI

/// The top tab bar (tvOS draws it as the focusable bar along the top): Home, Libraries,
/// Live TV, Search, Settings. Each tab has its own navigation stack. A viewer has no Live TV
/// tab while it is off on the home; an admin keeps it, and it says so (story 51.11).
///
/// Epic 57: each tab's first screen (and each page pushed on it, `parentalFresh`) is built
/// again when the account's parental limit changes, so nothing hidden stays on screen. What
/// plays keeps playing: the home stops it.
struct ContentView: View {
    let session: AppSession
    let user: User
    @Environment(\.client) private var client
    @State private var queue: QueuePlayer?

    var body: some View {
        tabs
            .environment(\.queuePlayer, queue)
            .task(id: client.map { ObjectIdentifier($0) }) {
                guard let client else { return }
                let q = QueuePlayer(client: client)
                // Epic 57: under a parental limit a "not found" may be a block
                let parental = session.parental
                q.limited = { parental.current.limited }
                queue = q
            }
            // Epic 56: the queue's player over every tab
            .fullScreenCover(isPresented: Binding(get: { queue?.showing == true }, set: { if !$0 { queue?.close() } })) {
                if let queue {
                    QueuePlayerScreen(queue: queue)
                        .environment(\.appSession, session)
                        .environment(\.client, client)
                }
            }
    }

    private var tabs: some View {
        TabView {
            Tab("Home", systemImage: "house.fill") {
                NavigationStack { HomeView().parentalFresh() }
            }
            Tab("Libraries", systemImage: "books.vertical.fill") {
                NavigationStack { LibraryView().parentalFresh() }
            }
            if session.capabilities.current.shown(.liveTV) != .hidden {
                Tab("Live TV", systemImage: "dot.radiowaves.left.and.right") {
                    NavigationStack { LiveView().parentalFresh() }
                }
            }
            Tab("Search", systemImage: "magnifyingglass") {
                NavigationStack { SearchView().parentalFresh() }
            }
            Tab("Settings", systemImage: "gearshape.fill") {
                SettingsView(session: session, user: user)
            }
        }
    }
}
