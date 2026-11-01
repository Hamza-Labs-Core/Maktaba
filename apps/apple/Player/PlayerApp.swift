import MaktabaKit
import SwiftUI

/// Maktaba Player: browse a home's libraries and watch, in the sections of `PlayerShell`. Nothing to manage the home (that is
/// Maktaba Remote). One code base for iPhone (iPhone Duo included), iPad and the Mac.
@main
struct PlayerApp: App {
    @State private var session = AppSession(app: Bundle.main.bundleIdentifier ?? "com.hamzalabs.maktaba.player")
    @AppStorage(ThemeMode.storageKey) private var theme = ThemeMode.system
    /// A video plays: the whole app goes dark, whatever the theme.
    @State private var playing = false

    init() {
        // a crash at an earlier launch is logged and kept on this device
        CrashReports.shared.start()
    }

    var body: some Scene {
        WindowGroup {
            #if DEBUG
            // the UI tests' component pages (`-maktaba-gallery poster`); never in a release build
            if let gallery = ComponentGallery(arguments: CommandLine.arguments) {
                gallery.maktabaTheme(theme)
            } else {
                signedIn
            }
            #else
            signedIn
            #endif
        }
        #if os(macOS)
        .defaultSize(width: 1200, height: 780)
        #endif
        .commands {
            PlaybackCommands()
            AccountCommands(session: session, theme: $theme)
        }
    }

    private var signedIn: some View {
        SessionGate(session: session, title: "Maktaba Player") { user, client in
            // the sections (PlayerSection.enabled); today Libraries alone
            PlayerShell(context: PlayerContext(session: session, user: user, client: client), playing: $playing)
        }
        .maktabaTheme(theme, forceDark: playing)
        .permissionsWalkthrough()
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
            Button(ConnectStrings.forPreferredLanguages().text(.approveMenu)) { session.approvingSignIn = true }
                .disabled(!session.signedIn)
            Button("Switch Home…") { session.switchHome() }
                .keyboardShortcut("h", modifiers: [.command, .shift])
                .disabled(session.phase == .choosingHome)
            Button("Sign Out") { session.signOut() }
                .disabled(!session.signedIn)
        }
    }
}
