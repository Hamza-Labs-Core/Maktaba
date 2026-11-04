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
    /// A deep link's page (`maktaba://video/{id}`, FR-9), kept until the shell opens it (a link
    /// that arrives before sign-in waits for it).
    @State private var link: PlayerRoute?

    init() {
        // the launch's first log lines: the app and its version
        Log.launched()
        // a crash at an earlier launch is logged and kept on this device
        CrashReports.shared.start()
        #if DEBUG && os(iOS)
        // the UI tests' Dynamic Type sizes, changed while the app runs (`TextSizeHook`; only
        // when launched with its flag)
        TextSizeHook.start()
        #endif
        // once per process, before any view: the UI tests' first-run reset of the walkthrough
        PermissionsModel.applyLaunchArguments()
        // and of the playback settings and the theme (`-maktaba-settings-reset`), before any
        // screen reads them: a setting one test changed never reaches the next test's player
        PlaybackPreferences.resetIfAsked()
        #if os(macOS)
        // the View menu's full-screen item is the Player's own (`PlayerViewCommands`: in the
        // app's language, on ⌃⌘F, Exit while full screen); without this AppKit adds its own
        // beside it and the menu shows two
        UserDefaults.standard.register(defaults: ["NSFullScreenMenuItemEverywhere": false])
        // one window, no window tabs: with tabbing on, AppKit puts Show Tab Bar and Show All
        // Tabs at the top of the View menu as it opens, then takes them out while it is open, so
        // every section's item moves up two rows under the pointer (a click aimed at Live TV
        // chose Profile)
        NSWindow.allowsAutomaticWindowTabbing = false
        #endif
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
            PlayerViewCommands()
            PlayerPlaybackCommands()
            AccountCommands(session: session, theme: $theme)
        }
    }

    private var signedIn: some View {
        SessionGate(session: session, title: "Maktaba Player") { user, client in
            // the sections (PlayerSection.enabled), Live TV only with a channel to watch (48.21)
            // a viewer has no Live TV while it is off on the home (51.11)
            LiveSectionGate(client: client, capabilities: session.capabilities) { sections, liveTVOff in
                PlayerShell(context: PlayerContext(session: session, user: user, client: client), playing: $playing, link: $link,
                            sections: sections, liveTVHidden: liveTVOff)
            }
        }
        // a video's link; a home's connect link stays SessionGate's
        .onOpenURL { url in if let route = VideoLink.route(url) { link = route } }
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
