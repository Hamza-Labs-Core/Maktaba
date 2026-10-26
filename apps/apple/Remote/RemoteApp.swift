import MaktabaKit
import SwiftUI

/// Maktaba Remote: run a home from your phone or iPad (its admins only; the home refuses
/// anyone else). It opens on what needs attention. Deeper admin pages come in later stories;
/// until then the home's web UI has them.
@main
struct RemoteApp: App {
    @State private var session = AppSession(app: Bundle.main.bundleIdentifier ?? "com.hamzalabs.maktaba.remote")
    @AppStorage(ThemeMode.storageKey) private var theme = ThemeMode.system

    var body: some Scene {
        WindowGroup {
            SessionGate(session: session, title: "Maktaba Remote") { user, client in
                if user.isAdmin {
                    StatusView(session: session, client: client)
                } else {
                    NotAnAdmin(session: session, user: user)
                }
            }
            .maktabaTheme(theme)
        }
    }
}

/// A home's user who is not its admin: this app is not for them.
struct NotAnAdmin: View {
    let session: AppSession
    let user: User

    var body: some View {
        ContentUnavailableView {
            Label("For the home's admins", systemImage: "lock")
        } description: {
            Text("\(user.username) can watch with Maktaba Player. Maktaba Remote manages the home, and only its admins can use it.")
        } actions: {
            Button("Sign out") { session.signOut() }
        }
    }
}
