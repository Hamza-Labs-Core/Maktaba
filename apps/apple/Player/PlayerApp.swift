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
        #if os(macOS)
        .defaultSize(width: 1200, height: 780)
        #endif
        .commands {
            PlaybackCommands()
            AccountCommands(session: session, theme: $theme)
        }
    }
}

/// The Account menu (the Mac's menu bar, an iPad's with a keyboard): the theme, switching
/// home, signing out.
struct AccountCommands: Commands {
    let session: AppSession
    @Binding var theme: ThemeMode

    var body: some Commands {
        CommandMenu("Account") {
            Picker("Theme", selection: $theme) {
                ForEach(ThemeMode.allCases) { Text($0.label).tag($0) }
            }
            Divider()
            Button("Switch Home…") { session.switchHome() }
                .keyboardShortcut("h", modifiers: [.command, .shift])
                .disabled(session.phase == .choosingHome)
            Button("Sign Out") { session.signOut() }
                .disabled(!session.signedIn)
        }
    }
}
