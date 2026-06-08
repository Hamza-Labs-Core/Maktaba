import SwiftUI

/// The main navigation surface: a top tab bar (the tvOS-native
/// `TabView` renders this as the focusable bar along the top of the
/// screen). Each tab is an independent navigation stack.
struct ContentView: View {
    @EnvironmentObject private var auth: AuthManager

    var body: some View {
        TabView {
            HomeView()
                .tabItem { Label("Home", systemImage: "house.fill") }

            LibraryView()
                .tabItem { Label("Libraries", systemImage: "books.vertical.fill") }

            SearchView()
                .tabItem { Label("Search", systemImage: "magnifyingglass") }

            SettingsView()
                .tabItem { Label("Settings", systemImage: "gearshape.fill") }
        }
    }
}
