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

/// Choose a home, sign in, then the tabs.
struct RootView: View {
    let session: AppSession

    var body: some View {
        switch session.phase {
        case .choosingHome:
            TVHomePicker(session: session)
        case .signingIn:
            TVSignInView(session: session)
        case let .signedIn(user):
            if let client = session.client {
                ContentView(session: session, user: user)
                    .environment(\.client, client)
            }
        }
    }
}

extension EnvironmentValues {
    /// The signed-in home's client (set under `RootView` once signed in).
    @Entry var client: APIClient?
}
