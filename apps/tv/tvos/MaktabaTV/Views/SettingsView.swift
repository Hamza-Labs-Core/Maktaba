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
                parental
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

    /// Epic 57 story 57.11: for a sign-in the home limits, that a parent manages the account
    /// and its limit, and the limit this Apple TV's Screen Time set (57.04). No one else
    /// sees any of it; Family is on a parent's iPhone or iPad.
    @ViewBuilder private var parental: some View {
        let limit = session.parental.current
        if limit.limited || limit.devicePending {
            Section {
                if limit.restricted {
                    Label { Text(verbatim: ParentalWords.text("parental.profile.managed")) } icon: { Image(systemName: "person.2.badge.gearshape") }
                        .accessibilityIdentifier("settings.parental.managed")
                    if let tier = limit.maxTier {
                        LabeledContent {
                            Text(verbatim: AgeTier.label(tier))
                        } label: {
                            Text(verbatim: ParentalWords.text("parental.profile.limit"))
                        }
                        .accessibilityIdentifier("settings.parental.limit")
                    }
                }
                if let device = limit.deviceLimit {
                    Label { Text(verbatim: ParentalWords.text("ageSignal.limit.device", AgeTier.label(device))) } icon: { Image(systemName: "appletv") }
                        .accessibilityIdentifier("settings.parental.device")
                }
                if limit.devicePending || session.ageSignal.answer?.pendingLooser == true {
                    Label { Text(verbatim: ParentalWords.text("ageSignal.limit.pending")) } icon: { Image(systemName: "hourglass") }
                        .accessibilityIdentifier("settings.parental.pending")
                }
            }
        }
    }
}
