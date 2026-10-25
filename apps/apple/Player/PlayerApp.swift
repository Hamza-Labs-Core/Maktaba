import MaktabaKit
import SwiftUI

/// Maktaba Player: browse a home's libraries and watch. Nothing to manage the home (that is
/// Maktaba Remote). One code base for iPhone (iPhone Duo included), iPad and the Mac.
@main
struct PlayerApp: App {
    @State private var session = AppSession(app: Bundle.main.bundleIdentifier ?? "com.hamzalabs.maktaba.player")

    var body: some Scene {
        WindowGroup {
            SessionGate(session: session, title: "Maktaba Player") { user, client in
                LibraryBrowser(session: session, user: user, client: client)
            }
            .tint(.brass)
        }
    }
}

extension Color {
    /// The brand's gold ("Ink & Brass", as on the TV apps).
    static let brass = Color(red: 0xE3 / 255, green: 0xB2 / 255, blue: 0x52 / 255)
}
