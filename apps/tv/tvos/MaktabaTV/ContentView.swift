import MaktabaKit
import SwiftUI

/// The top tab bar (tvOS draws it as the focusable bar along the top). Each tab has its
/// own navigation stack.
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
            Tab("Search", systemImage: "magnifyingglass") {
                NavigationStack { SearchView() }
            }
            Tab("Settings", systemImage: "gearshape.fill") {
                SettingsView(session: session, user: user)
            }
        }
    }
}
