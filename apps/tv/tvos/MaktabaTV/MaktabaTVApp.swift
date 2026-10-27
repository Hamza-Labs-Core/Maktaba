import MaktabaKit
import Security
import SwiftUI

/// Maktaba for Apple TV: a home's libraries on the television, watched from the sofa.
/// The session (homes, tokens, sign-in) and the API client are MaktabaKit's, as in the
/// Player apps; the screens are the TV's own (focus engine, big type, overscan gutters).
@main
struct MaktabaTVApp: App {
    @State private var session: AppSession
    /// Dark by default on a television; Settings can follow the system or go light.
    @AppStorage(ThemeMode.storageKey) private var theme = ThemeMode.dark

    init() {
        Theme.registerFonts()
        // the UI tests start from a TV that knows no home and holds no tokens
        if CommandLine.arguments.contains("-uitest-reset") {
            UserDefaults.standard.removePersistentDomain(forName: Bundle.main.bundleIdentifier ?? "")
            SecItemDelete([kSecClass as String: kSecClassGenericPassword] as CFDictionary)
        }
        _session = State(initialValue: AppSession(app: Bundle.main.bundleIdentifier ?? "com.hamzalabs.maktaba.tv"))
    }

    var body: some Scene {
        WindowGroup {
            RootView(session: session)
                .font(Theme.body(29))
                .maktabaTheme(theme)
                .permissionsWalkthrough()
                .tint(Theme.gold)
        }
    }
}

/// Choose a home, sign in, then the tabs. A home's connect link (`maktaba://connect?…`)
/// opened from outside is asked about first: in the picker, which shows it inline, or here.
struct RootView: View {
    @Bindable var session: AppSession
    private let words = ConnectStrings.forPreferredLanguages()

    var body: some View {
        ZStack {
            switch session.phase {
            case .choosingHome:
                TVHomePicker(session: session)
            case .signingIn:
                TVSignInView(session: session)
            case let .signedIn(user):
                if let client = session.client {
                    ContentView(session: session, user: user)
                        .environment(\.client, client)
                        .environment(\.appSession, session)
                }
            }
        }
        .onOpenURL { session.open($0) }
        .alert(words.text(session.pendingLink != nil ? .linkTitle : .connect), isPresented: Binding(
            get: { session.phase != .choosingHome && (session.pendingLink != nil || session.connectProblem != nil) },
            set: { if !$0 { session.dismissLink(); session.connectProblem = nil } })
        ) {
            if session.pendingLink != nil {
                // on success the home is in use (signing in there); else the alert says why
                Button(words.text(.connect)) { session.confirmLink() }
                Button(words.text(.cancel), role: .cancel) { session.dismissLink() }
            } else {
                Button(words.text(.done)) { session.connectProblem = nil }
            }
        } message: {
            if let link = session.pendingLink {
                Text(words.text(.linkMessage, link.name ?? link.urls.first?.host() ?? "",
                                link.urls.map { Home(url: $0, name: "").displayAddress }.joined(separator: ", ")))
            } else {
                Text(session.connectProblem.map(words.reach) ?? "")
            }
        }
    }
}

extension EnvironmentValues {
    /// The signed-in home's client (set under `RootView` once signed in).
    @Entry var client: APIClient?
    /// The session, for a screen that sends the person back to signing in or to the homes.
    @Entry var appSession: AppSession?
}
