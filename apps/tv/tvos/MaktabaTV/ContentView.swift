import MaktabaKit
import SwiftUI

/// The top tab bar (tvOS draws it as the focusable bar along the top): Home, Libraries,
/// Live TV, Search, Settings. Each tab has its own navigation stack. A viewer has no Live TV
/// tab while it is off on the home; an admin keeps it, and it says so (story 51.11).
struct ContentView: View {
    let session: AppSession
    let user: User

    var body: some View {
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
