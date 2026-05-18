#if os(tvOS)
import SwiftUI

@main
public struct MaktabaApp: App {
    @StateObject private var session = AppSession()

    public init() {}

    public var body: some Scene {
        WindowGroup {
            RootView()
                .environmentObject(session)
        }
    }
}

public struct RootView: View {
    @EnvironmentObject var session: AppSession

    public init() {}

    public var body: some View {
        if session.isPaired {
            MainTabView()
        } else {
            PairingView()
        }
    }
}

public struct MainTabView: View {
    public init() {}

    public var body: some View {
        TabView {
            HomeView().tabItem { Text("Home") }
            LibraryView().tabItem { Text("Library") }
            SearchView().tabItem { Text("Search") }
            SettingsView().tabItem { Text("Settings") }
        }
    }
}
#endif
