import MaktabaKit
import SwiftUI

/// Who is signed in where, the appearance, another home, signing out.
struct SettingsView: View {
    let session: AppSession
    let user: User
    @AppStorage(ThemeMode.storageKey) private var theme = ThemeMode.dark

    var body: some View {
        NavigationStack {
            Form {
                Section("Account") {
                    LabeledContent("Signed in as", value: user.username)
                    if let home = session.current {
                        LabeledContent(String(localized: "settings.home", defaultValue: "Home"), value: home.name)
                        LabeledContent("Address", value: home.url.absoluteString)
                    }
                }
                Section("Appearance") {
                    Picker("Theme", selection: $theme) {
                        ForEach(ThemeMode.allCases) { Text($0.label).tag($0) }
                    }
                }
                Section {
                    Button("Choose another home", systemImage: "house") { session.switchHome() }
                    Button("Sign out", systemImage: "rectangle.portrait.and.arrow.right", role: .destructive) {
                        session.signOut()
                    }
                }
            }
            .frame(maxWidth: 1200)
            .frame(maxWidth: .infinity)
            .background(Theme.background.ignoresSafeArea())
        }
    }
}
