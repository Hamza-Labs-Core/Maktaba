import MaktabaKit
import SwiftUI

/// Who is signed in where, the appearance, another home, signing out.
struct SettingsView: View {
    let session: AppSession
    let user: User
    @AppStorage(ThemeMode.storageKey) private var theme = ThemeMode.dark
    @AppStorage("autoplayNext") private var autoplay = true
    @Environment(\.reportProblem) private var reportProblem

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
                // Epic 56: every playlist of the user, and the Playback setting (story 56.11)
                Section("Playlists") {
                    NavigationLink("Playlists") { PlaylistsGrid(library: nil).padding(80) }
                }
                Section("Playback") {
                    Toggle("Play the next video automatically", isOn: $autoplay)
                }
                Section("Appearance") {
                    Picker("Theme", selection: $theme) {
                        ForEach(ThemeMode.allCases) { Text($0.label).tag($0) }
                    }
                }
                // Epic 53: crash reports to the home (off until turned on), Report a problem
                Section("Privacy") {
                    CrashReportsToggle(session: session)
                }
                Section("Help") {
                    Button {
                        reportProblem?()
                    } label: {
                        Label { Text(verbatim: ReportWords.title) } icon: { Image(systemName: "exclamationmark.bubble") }
                    }
                    .accessibilityIdentifier("settings.reportProblem")
                    if let client = session.client {
                        NavigationLink {
                            MyReportsView(client: client).padding(80)
                        } label: {
                            Label { Text(verbatim: ReportWords.mine) } icon: { Image(systemName: "person.crop.rectangle.stack") }
                        }
                        .accessibilityIdentifier("settings.myReports")
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
