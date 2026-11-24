import MaktabaKit
import SwiftUI

/// Maktaba Player: browse a home's libraries and watch, in the sections of `PlayerShell`. Nothing to manage the home (that is
/// Maktaba Remote). One code base for iPhone (iPhone Duo included), iPad and the Mac.
@main
struct PlayerApp: App {
    @State private var session: AppSession
    @AppStorage(ThemeMode.storageKey) private var theme = ThemeMode.system
    /// A video plays: the whole app goes dark, whatever the theme.
    @State private var playing = false
    /// A deep link's page (`maktaba://video/{id}`, FR-9), kept until the shell opens it (a link
    /// that arrives before sign-in waits for it).
    @State private var link: PlayerRoute?
    /// Story 50.27: Watch a broadcast is open (with the invite a link brought, if any).
    @State private var guest: GuestLaunch?
    /// An invite link that came before the parental limit was known (`GuestGate.checking`).
    @State private var held = HeldInvite()
    /// Why an invite link did not open, or why Watch a broadcast closed (an alert).
    @State private var guestNotice: String?

    init() {
        #if DEBUG && os(iOS)
        // the UI tests' app writes nothing to standard error (`ConsoleHook`; only when launched
        // with its flag): first, before the app logs anything
        ConsoleHook.start()
        #endif
        // Epic 57: the Player asks the home for the account's parental limit and tells it
        // what the device says about its user's age (the source is set before any sign-in
        // is restored, so the first sign-in already sends it)
        let session = AppSession(app: Bundle.main.bundleIdentifier ?? "com.hamzalabs.maktaba.player", parental: true,
                                 compat: .current) // story 58.01
        session.ageSignal.use(DeviceAgeSignal.source())
        // when the account's limit changes, what the Player kept for it on the device goes;
        // set here, before a sign-in is restored, so the first answer's change is heard
        session.parentalChanged = { home, user in ParentalCaches.drop(home: home, user: user) }
        _session = State(initialValue: session)
        // the launch's first log lines: the app and its version
        Log.launched()
        // a crash at an earlier launch is logged and kept on this device
        CrashReports.shared.start()
        // pictures from a home reached by its LAN address: checked against its name (50.15 §3)
        ImageCache.trust = PinnedTrust.shared
        #if DEBUG && os(iOS)
        // the UI tests' Dynamic Type sizes, changed while the app runs (`TextSizeHook`; only
        // when launched with its flag)
        TextSizeHook.start()
        // a touch the system cancels, counted for a UI test that fails after it (`TouchHook`,
        // #639; only when launched with its flag)
        TouchHook.start()
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
        // a video's link, or a broadcast's invite (story 50.27); a home's connect link stays SessionGate's
        .onOpenURL { url in
            if let route = VideoLink.route(url) {
                link = route
            } else if GuestBroadcast.enabled, let invite = GuestInput.invite(in: url) {
                // while broadcasting is off (`GuestBroadcast`) an invite link opens nothing
                arrived(invite)
            }
        }
        // the home picker's Watch a broadcast: a guest needs no home and no account, and the
        // entry shows only once the parental limit is known and is none (fails closed), and
        // never while the build has broadcasting off (`GuestBroadcast`)
        .environment(\.watchBroadcast, session.showsWatchBroadcast ? { guest = GuestLaunch(invite: nil) } : nil)
        // the gate moved: a held invite opens or goes; under a limit an open guest space closes
        .onChange(of: session.guestGate) { _, gate in gateMoved(gate) }
        .alert(Text(verbatim: GuestWords.message("guest.watchBroadcast")),
               isPresented: Binding(get: { guestNotice != nil }, set: { if !$0 { guestNotice = nil } })) {
            Button(GuestWords.message("guest.close")) { guestNotice = nil }
        } message: {
            Text(verbatim: guestNotice ?? "")
        }
        // full screen on iPhone and iPad (a player opened inside an iPad's form sheet never
        // showed); a sheet on the Mac
        #if os(iOS)
        .fullScreenCover(item: $guest) { launch in
            GuestSpaceView(app: Bundle.main.bundleIdentifier ?? "com.hamzalabs.maktaba.player", invite: launch.invite)
        }
        #else
        .sheet(item: $guest) { launch in
            GuestSpaceView(app: Bundle.main.bundleIdentifier ?? "com.hamzalabs.maktaba.player", invite: launch.invite)
                .frame(minWidth: 560, minHeight: 640)
        }
        #endif
        .maktabaTheme(theme, forceDark: playing)
        .permissionsWalkthrough()
    }
}

/// Watch a broadcast, opened from the home picker or by an invite's link.
struct GuestLaunch: Identifiable {
    let id = UUID()
    let invite: String?
}

extension PlayerApp {
    /// An invite link was opened: it opens only when the gate allows (`GuestGate`); while the
    /// limit is not known it waits, under a limit it is refused, and either way it says so.
    func arrived(_ invite: String) {
        let gate = session.guestGate
        if let open = held.arrive(invite, gate: gate) {
            guest = GuestLaunch(invite: open)
        } else {
            guestNotice = gate.inviteMessage
        }
    }

    /// The gate moved: a held invite opens once allowed; under a limit, or while the limit is
    /// not known again (another sign-in), an open guest space closes.
    func gateMoved(_ gate: GuestGate) {
        if let open = held.gateChanged(gate) {
            guestNotice = nil
            guest = GuestLaunch(invite: open)
            return
        }
        guard !gate.allowed else { return }
        if guest != nil {
            guest = nil
            guestNotice = GuestWords.message(gate == .limited ? "guest.gate.closed" : "guest.gate.checking")
        } else if gate == .limited, guestNotice == GuestGate.checking.inviteMessage {
            guestNotice = GuestGate.limited.inviteMessage
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
