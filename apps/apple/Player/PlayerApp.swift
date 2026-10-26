import MaktabaKit
import SwiftUI

/// Maktaba Player: browse a home's libraries and watch. Nothing to manage the home (that is
/// Maktaba Remote). One code base for iPhone (iPhone Duo included), iPad and the Mac.
@main
struct PlayerApp: App {
    @State private var session = AppSession(app: Bundle.main.bundleIdentifier ?? "com.hamzalabs.maktaba.player")
    @AppStorage(ThemeMode.storageKey) private var theme = ThemeMode.system
    /// A video plays: the whole app goes dark, whatever the theme.
    @State private var playing = false

    var body: some Scene {
        WindowGroup {
            SessionGate(session: session, title: "Maktaba Player") { user, client in
                LibraryBrowser(session: session, user: user, client: client, playing: $playing)
            }
            .maktabaTheme(theme, forceDark: playing)
            .permissionsWalkthrough()
        }
    }
}
