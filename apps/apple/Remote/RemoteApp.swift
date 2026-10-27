import MaktabaKit
import SwiftUI

/// Maktaba Remote: run a home from your phone or iPad (its admins only; the home refuses
/// anyone else, whatever the app shows). It opens on what needs attention; the processing
/// queue, the libraries, the machines and the home's state are a tap away (tabs on iPhone,
/// a sidebar on iPad).
@main
struct RemoteApp: App {
    @State private var session = AppSession(app: Bundle.main.bundleIdentifier ?? "com.hamzalabs.maktaba.remote")
    @AppStorage(ThemeMode.storageKey) private var theme = ThemeMode.system

    init() {
        // a crash at an earlier launch is logged and kept on this device
        CrashReports.shared.start()
    }

    var body: some Scene {
        WindowGroup {
            SessionGate(session: session, title: "Maktaba Remote") { user, client in
                if user.isAdmin {
                    RemoteRoot(session: session, user: user, client: client)
                } else {
                    NotAnAdmin(session: session, user: user)
                }
            }
            .maktabaTheme(theme)
            .permissionsWalkthrough()
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
