import MaktabaKit
import SwiftUI

/// The top tab bar (tvOS draws it as the focusable bar along the top): Home, Libraries,
/// Live TV, Search, Settings. Each tab has its own navigation stack. A viewer has no Live TV
/// tab while it is off on the home; an admin keeps it, and it says so (story 51.11).
struct ContentView: View {
    let session: AppSession
    let user: User
    @Environment(\.client) private var client
    @State private var queue: QueuePlayer?

    var body: some View {
        tabs
            .environment(\.queuePlayer, queue)
            .task(id: client.map { ObjectIdentifier($0) }) { if let client { queue = QueuePlayer(client: client) } }
            // Epic 56: the queue's player over every tab
            .fullScreenCover(isPresented: Binding(get: { queue?.playback != nil }, set: { if !$0 { queue?.close() } })) {
                if let queue { QueuePlayerScreen(queue: queue) }
            }
    }

    private var tabs: some View {
        TabView {
            Tab("Home", systemImage: "house.fill") {
                NavigationStack { HomeView() }
            }
            Tab("Libraries", systemImage: "books.vertical.fill") {
                NavigationStack { LibraryView() }
            }
            if session.capabilities.current.shown(.liveTV) != .hidden {
                Tab("Live TV", systemImage: "dot.radiowaves.left.and.right") {
                    NavigationStack { LiveView() }
                }
            }
            Tab("Search", systemImage: "magnifyingglass") {
                NavigationStack { SearchView() }
            }
            Tab("Settings", systemImage: "gearshape.fill") {
                SettingsView(session: session, user: user)
            }
        }
    }
}
