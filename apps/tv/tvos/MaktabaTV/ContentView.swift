import MaktabaKit
import SwiftUI

/// The top tab bar (tvOS draws it as the focusable bar along the top). Each tab has its
/// own navigation stack, and every stack opens a video's page the same way.
struct ContentView: View {
    let session: AppSession
    let user: User

    var body: some View {
        TabView {
            Tab("Home", systemImage: "house.fill") {
                NavigationStack { HomeView().videoDestination() }
            }
            Tab("Libraries", systemImage: "books.vertical.fill") {
                NavigationStack { LibraryView().videoDestination() }
            }
            Tab("Search", systemImage: "magnifyingglass") {
                NavigationStack { SearchView().videoDestination() }
            }
            Tab("Settings", systemImage: "gearshape.fill") {
                SettingsView(session: session, user: user)
            }
        }
    }
}

/// A video to open, by its id (a rail knows only the id until its card loads).
struct VideoRoute: Hashable {
    let id: String
}

extension View {
    func videoDestination() -> some View {
        navigationDestination(for: VideoRoute.self) { VideoPage(id: $0.id) }
    }
}
